package lsmtree

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
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
// MemTable.Entries returns them.
func WriteSSTable(path string, entries []Entry) (err error) {
	file, err := os.Create(path)

	if err != nil {
		return err
	}

	defer func() {
		if cerr := file.Close(); err == nil {
			err = cerr
		}
	}()

	w := &countingWriter{w: bufio.NewWriter(file)}
	index := []IndexEntry{}

	for i, entry := range entries {
		if i%indexInterval == 0 {
			index = append(index, IndexEntry{Key: entry.Key, Offset: w.n})
		}
		w.writeUint32(uint32(len(entry.Key)))
		w.writeString(entry.Key)
		w.writeUint32(uint32(len(entry.Value)))
		w.writeString(entry.Value)
	}

	indexOffset := w.n
	defaultLogger.Debug("index offset: %d", indexOffset)
	defaultLogger.Debug("index: %v", index)

	for _, e := range index {
		w.writeUint32(uint32(len(e.Key)))
		w.writeString(e.Key)
		w.writeUint32(uint32(e.Offset))
	}

	w.writeUint64(uint64(len(index)))
	w.writeUint64(uint64(indexOffset))

	if w.err != nil {
		return w.err
	}
	return w.w.Flush()
}

// countingWriter tracks how many bytes have been written, so WriteSSTable
// knows each entry's offset, and keeps the first error so callers can check
// once at the end.
type countingWriter struct {
	w   *bufio.Writer
	n   int64
	err error
}

func (c *countingWriter) write(p []byte) {
	if c.err != nil {
		return
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.err = err
}

func (c *countingWriter) writeString(s string) { c.write([]byte(s)) }

func (c *countingWriter) writeUint32(v uint32) {
	c.write(binary.BigEndian.AppendUint32(nil, v))
}

func (c *countingWriter) writeUint64(v uint64) {
	c.write(binary.BigEndian.AppendUint64(nil, v))
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

	r := bufio.NewReader(io.NewSectionReader(file, int64(indexOffset), int64(indexEnd-indexOffset)))
	index := []IndexEntry{}
	for i := uint64(0); i < indexLen; i++ {
		key, err := readBytes(r)
		if err != nil {
			return nil, fmt.Errorf("index entry %d: %w", i, unexpectedEOF(err))
		}
		offset, err := readUint32(r)
		if err != nil {
			return nil, fmt.Errorf("index entry %d: %w", i, unexpectedEOF(err))
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

// Read returns every entry in the data section.
func (s *SSTable) Read() ([]Entry, error) {
	r := bufio.NewReader(io.NewSectionReader(s.file, 0, s.dataEnd))
	result := []Entry{}

	for {
		key, err := readBytes(r)
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}

		value, err := readBytes(r)
		if err != nil {
			return nil, unexpectedEOF(err)
		}
		result = append(result, Entry{Key: string(key), Value: string(value)})
	}
}

// Get looks up key using the sparse index. It finds the index entry with the
// largest key <= key and scans that block, which ends at the next index entry
// or at the end of the data section. A deleted key is found with the value
// Tombstone.
func (s *SSTable) Get(key string) (string, bool, error) {
	i, ok := floorIndex(s.index, key)
	if !ok {
		return "", false, nil
	}

	blockStart := s.index[i].Offset
	blockEnd := s.dataEnd
	if i+1 < len(s.index) {
		blockEnd = s.index[i+1].Offset
	}

	r := bufio.NewReader(io.NewSectionReader(s.file, blockStart, blockEnd-blockStart))
	for {
		k, err := readBytes(r)
		if err == io.EOF {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}

		valueLen, err := readUint32(r)
		if err != nil {
			return "", false, unexpectedEOF(err)
		}

		// Keys are sorted, so once we pass key it is not in the table.
		if string(k) > key {
			return "", false, nil
		}

		if string(k) == key {
			value := make([]byte, valueLen)
			if _, err := io.ReadFull(r, value); err != nil {
				return "", false, unexpectedEOF(err)
			}
			return string(value), true, nil
		}

		// Skip the value of a non-matching entry without reading it.
		if _, err := r.Discard(int(valueLen)); err != nil {
			return "", false, unexpectedEOF(err)
		}
	}
}

// readBytes reads a uint32 length followed by that many bytes. It returns
// io.EOF only if r was already at its end, and io.ErrUnexpectedEOF if the
// record is cut short.
func readBytes(r *bufio.Reader) ([]byte, error) {
	n, err := readUint32(r)
	if err != nil {
		return nil, err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, unexpectedEOF(err)
	}
	return b, nil
}

// readUint32 reads a big-endian uint32. Like io.ReadFull, it returns io.EOF
// if no bytes were read and io.ErrUnexpectedEOF if only some were.
func readUint32(r *bufio.Reader) (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

// unexpectedEOF turns io.EOF into io.ErrUnexpectedEOF, for reads in the middle
// of a record where hitting the end means the file is truncated.
func unexpectedEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
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
