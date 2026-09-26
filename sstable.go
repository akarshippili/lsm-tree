package lsmtree

import (
	"encoding/binary"
	"io"
	"os"
	"sort"
)

// IndexEntry maps a key in the sparse index to the byte offset of its entry
// in the data section.
type IndexEntry struct {
	Key    string
	Offset int64
}

type SSTable struct {
	entries []Entry
	path    string
}

func NewSSTable(path string, entries []Entry) *SSTable {
	return &SSTable{path: path, entries: entries}
}

func (s *SSTable) Write() error {
	file, err := os.Create(s.path)
	index := []IndexEntry{}
	indexOffset := int64(0)

	if err != nil {
		return err
	}

	defer file.Close()

	for entryIndex, entry := range s.entries {
		entryStart, _ := file.Seek(0, io.SeekCurrent)
		if entryIndex%16 == 0 {
			index = append(index, IndexEntry{Key: entry.Key, Offset: entryStart})
		}

		keyLen := uint32(len(entry.Key))
		valueLen := uint32(len(entry.Value))

		binary.Write(file, binary.BigEndian, keyLen)
		file.WriteString(entry.Key)
		binary.Write(file, binary.BigEndian, valueLen)
		file.WriteString(entry.Value)
	}

	indexOffset, _ = file.Seek(0, io.SeekCurrent)
	defaultLogger.Debug("index offset: %d", indexOffset)
	defaultLogger.Debug("index: %v", index)

	for _, e := range index {
		keyLen := uint32(len(e.Key))
		binary.Write(file, binary.BigEndian, keyLen)
		file.WriteString(e.Key)
		binary.Write(file, binary.BigEndian, uint32(e.Offset))
	}

	indexLen := uint64(len(index))
	binary.Write(file, binary.BigEndian, indexLen)
	binary.Write(file, binary.BigEndian, uint64(indexOffset))
	return nil
}

func (s *SSTable) Read() ([]Entry, error) {
	file, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}

	defer file.Close()
	result := []Entry{}

	// The data section ends where the index section begins.
	_, dataEnd := s.getIndexLenAndOffset()

	for {
		pos, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		if pos >= dataEnd {
			break
		}

		var keyLen uint32
		if err := binary.Read(file, binary.BigEndian, &keyLen); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}

		key := make([]byte, keyLen)
		file.Read(key)

		var valueLen uint32
		if err := binary.Read(file, binary.BigEndian, &valueLen); err != nil {
			return nil, err
		}

		value := make([]byte, valueLen)
		file.Read(value)

		entry := Entry{Key: string(key), Value: string(value)}
		result = append(result, entry)
	}

	return result, nil
}

// GetIndex returns the sparse index in on-disk order, which is key order when
// the entries passed to NewSSTable were sorted.
func (s *SSTable) GetIndex() []IndexEntry {
	result := []IndexEntry{}
	file, err := os.Open(s.path)

	if err != nil {
		return nil
	}

	defer file.Close()
	indexLen, indexOffset := s.getIndexLenAndOffset()
	file.Seek(indexOffset, 0)

	for i := 0; i < int(indexLen); i++ {
		var keyLen uint32
		binary.Read(file, binary.BigEndian, &keyLen)

		key := make([]byte, keyLen)
		file.Read(key)

		var offset uint32
		binary.Read(file, binary.BigEndian, &offset)
		result = append(result, IndexEntry{Key: string(key), Offset: int64(offset)})

		defaultLogger.Debug("index entry %s: %d", string(key), offset)
	}

	return result
}

// Get looks up key using the sparse index. It seeks to the index entry with
// the largest key <= key and scans that block, which ends at the next index
// entry or at the end of the data section. A deleted key is found with the
// value Tombstone.
func (s *SSTable) Get(key string) (string, bool, error) {
	file, err := os.Open(s.path)
	if err != nil {
		return "", false, err
	}

	defer file.Close()

	index := s.GetIndex()
	i, ok := floorIndex(index, key)
	if !ok {
		return "", false, nil
	}

	// The block runs up to the next index entry, or to the end of the data
	// section for the last block.
	_, blockEnd := s.getIndexLenAndOffset()
	if i+1 < len(index) {
		blockEnd = index[i+1].Offset
	}

	pos := index[i].Offset
	if _, err := file.Seek(pos, io.SeekStart); err != nil {
		return "", false, err
	}

	for pos < blockEnd {
		var keyLen uint32
		if err := binary.Read(file, binary.BigEndian, &keyLen); err != nil {
			return "", false, err
		}

		k := make([]byte, keyLen)
		if _, err := io.ReadFull(file, k); err != nil {
			return "", false, err
		}

		var valueLen uint32
		if err := binary.Read(file, binary.BigEndian, &valueLen); err != nil {
			return "", false, err
		}

		// Keys are sorted, so once we pass key it is not in the table.
		if string(k) > key {
			return "", false, nil
		}

		if string(k) == key {
			value := make([]byte, valueLen)
			if _, err := io.ReadFull(file, value); err != nil {
				return "", false, err
			}
			return string(value), true, nil
		}

		// Skip the value of a non-matching entry without reading it.
		if _, err := file.Seek(int64(valueLen), io.SeekCurrent); err != nil {
			return "", false, err
		}
		pos += 4 + int64(keyLen) + 4 + int64(valueLen)
	}

	return "", false, nil
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

func (s *SSTable) getIndexLenAndOffset() (int64, int64) {
	file, err := os.Open(s.path)
	if err != nil {
		return 0, 0
	}

	defer file.Close()
	file.Seek(-8-8, 2)
	var indexOffset uint64
	var indexLen uint64
	binary.Read(file, binary.BigEndian, &indexLen)
	binary.Read(file, binary.BigEndian, &indexOffset)
	return int64(indexLen), int64(indexOffset)
}
