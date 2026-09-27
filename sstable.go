package lsmtree

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

// SSTable file layout:
//
//	data:   [keyLen uint32][key][valueLen uint32][value] ...
//	index:  [keyLen uint32][key][offset uint32] ...   (every 16th data entry)
//	footer: [indexLen uint64][indexOffset uint64]
//
// All integers are big endian. indexOffset is where the data section ends.
const footerSize = 8 + 8

// indexInterval is how many data entries each sparse index entry covers.
const indexInterval = 16

// IndexEntry maps a key in the sparse index to the byte offset of its entry
// in the data section.
type IndexEntry struct {
	Key    string
	Offset int64
}

// WriteSSTable writes entries to a new SSTable file at path, replacing any
// existing file. entries must be sorted by key with no duplicates, as
// MemTable.Entries returns them. The file is built in memory and written in
// one call, so offsets are just positions in the buffer.
func WriteSSTable(path string, entries []Entry) error {
	var b []byte
	index := []IndexEntry{}

	for i, e := range entries {
		if i%indexInterval == 0 {
			index = append(index, IndexEntry{Key: e.Key, Offset: int64(len(b))})
		}
		b = appendBytes(b, e.Key)
		b = appendBytes(b, e.Value)
	}

	indexOffset := len(b)
	for _, e := range index {
		b = appendBytes(b, e.Key)
		b = binary.BigEndian.AppendUint32(b, uint32(e.Offset))
	}

	b = binary.BigEndian.AppendUint64(b, uint64(len(index)))
	b = binary.BigEndian.AppendUint64(b, uint64(indexOffset))

	defaultLogger.Debug("index offset: %d, index: %v", indexOffset, index)
	return os.WriteFile(path, b, 0o644)
}

// SSTable is an open, read-only SSTable file. Its sparse index is loaded into
// memory when it is opened. It is safe for concurrent use and must be closed
// with Close.
type SSTable struct {
	file    *os.File
	index   []IndexEntry
	dataEnd int64
}

// OpenSSTable opens the SSTable at path and loads its footer and index.
func OpenSSTable(path string) (*SSTable, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	s, err := loadSSTable(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("open sstable %s: %w", path, err)
	}
	return s, nil
}

func loadSSTable(file *os.File) (*SSTable, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size < footerSize {
		return nil, fmt.Errorf("file is %d bytes, too small for footer", size)
	}

	footer := make([]byte, footerSize)
	if _, err := file.ReadAt(footer, size-footerSize); err != nil {
		return nil, err
	}
	indexLen := binary.BigEndian.Uint64(footer[0:8])
	indexOffset := binary.BigEndian.Uint64(footer[8:16])
	indexEnd := uint64(size - footerSize)
	if indexOffset > indexEnd {
		return nil, fmt.Errorf("index offset %d is past index end %d", indexOffset, indexEnd)
	}

	r := newSectionReader(file, int64(indexOffset), int64(indexEnd))
	index := []IndexEntry{}
	for i := uint64(0); i < indexLen; i++ {
		key, err := r.readBytes()
		if err != nil {
			return nil, fmt.Errorf("index entry %d: %w", i, err)
		}
		offset, err := r.readUint32()
		if err != nil {
			return nil, fmt.Errorf("index entry %d: %w", i, err)
		}
		index = append(index, IndexEntry{Key: string(key), Offset: int64(offset)})
		defaultLogger.Debug("index entry %s: %d", key, offset)
	}

	return &SSTable{file: file, index: index, dataEnd: int64(indexOffset)}, nil
}

// Close closes the underlying file.
func (s *SSTable) Close() error {
	return s.file.Close()
}

// Index returns the sparse index in on-disk order, which is key order when the
// entries passed to WriteSSTable were sorted. Callers must not modify it.
func (s *SSTable) Index() []IndexEntry {
	return s.index
}

// Iterator reads SSTable entries in key order, one at a time. Use it like
// bufio.Scanner: call Next until it returns false, then check Err.
type Iterator struct {
	r     *sectionReader
	from  string // entries with keys < from are skipped
	entry Entry
	err   error
}

// Iterator returns an iterator over every entry in the table.
func (s *SSTable) Iterator() *Iterator {
	return s.iterator(0, s.dataEnd, "")
}

// Scan returns an iterator over the entries with keys >= from. It starts at
// the block that could contain from, so it skips at most one block's worth of
// smaller keys.
func (s *SSTable) Scan(from string) *Iterator {
	start := int64(0)
	if i, ok := floorIndex(s.index, from); ok {
		start = s.index[i].Offset
	}
	return s.iterator(start, s.dataEnd, from)
}

// iterator returns an iterator over the data between byte offsets start and
// end, skipping keys < from.
func (s *SSTable) iterator(start, end int64, from string) *Iterator {
	return &Iterator{r: newSectionReader(s.file, start, end), from: from}
}

// Next advances to the next entry and reports whether there is one. It
// returns false at the end of the table or on an error.
func (it *Iterator) Next() bool {
	for it.err == nil && it.r.left > 0 {
		e, err := it.r.readEntry()
		if err != nil {
			it.err = err
			return false
		}
		if e.Key >= it.from {
			it.entry = e
			return true
		}
	}
	return false
}

// Entry returns the entry Next just advanced to.
func (it *Iterator) Entry() Entry {
	return it.entry
}

// Err returns the error that stopped the iterator, or nil if it reached the
// end of the table.
func (it *Iterator) Err() error {
	return it.err
}

// Get looks up key using the sparse index. It finds the block that could
// contain key, which runs from the floor index entry to the next index entry
// (or the end of the data), and returns the first entry there with key >= key
// if it matches. A deleted key is found with the value Tombstone.
func (s *SSTable) Get(key string) (string, bool, error) {
	i, ok := floorIndex(s.index, key)
	if !ok {
		return "", false, nil
	}

	end := s.dataEnd
	if i+1 < len(s.index) {
		end = s.index[i+1].Offset
	}

	it := s.iterator(s.index[i].Offset, end, key)
	if it.Next() && it.Entry().Key == key {
		return it.Entry().Value, true, nil
	}
	return "", false, it.Err()
}

// floorIndex returns the position of the index entry with the largest key <=
// key. Since the index is sparse, that entry marks where a scan for key should
// start, and the entry after it marks where the scan can stop. It
// returns false if every index key is greater than key, meaning key is not in
// the SSTable. index must be sorted by key.
func floorIndex(index []IndexEntry, key string) (int, bool) {
	// i is the first entry whose key is > key, so i-1 is the floor.
	i := sort.Search(len(index), func(i int) bool {
		return index[i].Key > key
	})
	if i == 0 {
		return 0, false
	}
	return i - 1, true
}
