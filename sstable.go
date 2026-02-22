package lsmtree

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

type SSTable struct {
	entries []Entry
	path    string
}

func NewSSTable(path string, entries []Entry) *SSTable {
	return &SSTable{path: path, entries: entries}
}

func (s *SSTable) Write() error {
	file, err := os.Create(s.path)
	index := make(map[string]int64)
	prevOffset := int64(0)
	indexOffset := int64(0)

	if err != nil {
		return err
	}

	defer file.Close()

	for entryIndex, entry := range s.entries {
		keyLen := uint32(len(entry.Key))
		valueLen := uint32(len(entry.Value))

		binary.Write(file, binary.BigEndian, keyLen)
		file.WriteString(entry.Key)
		binary.Write(file, binary.BigEndian, valueLen)
		file.WriteString(entry.Value)
		newOffset, _ := file.Seek(0, io.SeekCurrent)

		if entryIndex%16 == 0 {
			index[entry.Key] = prevOffset
		}

		prevOffset = newOffset
	}

	tombstoneEntry := Entry{Key: "END", Value: ""}
	keyLen := uint32(len(tombstoneEntry.Key))
	valueLen := uint32(len(tombstoneEntry.Value))

	binary.Write(file, binary.BigEndian, keyLen)
	file.WriteString(tombstoneEntry.Key)
	binary.Write(file, binary.BigEndian, valueLen)
	file.WriteString(tombstoneEntry.Value)
	newOffset, _ := file.Seek(0, io.SeekCurrent)

	indexOffset = newOffset
	fmt.Println("index offset: ", indexOffset)
	fmt.Println("index: ", index)

	for key, offset := range index {
		keyLen := uint32(len(key))
		binary.Write(file, binary.BigEndian, keyLen)
		file.WriteString(key)
		binary.Write(file, binary.BigEndian, uint32(offset))
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

	for {
		var keyLen uint32
		if err := binary.Read(file, binary.BigEndian, &keyLen); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}

		key := make([]byte, keyLen)
		file.Read(key)

		if string(key) == "END" {
			break
		}

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

func (s *SSTable) GetIndex() map[string]int64 {
	result := make(map[string]int64)
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
		result[string(key)] = int64(offset)

		fmt.Printf("%s: %d\n", string(key), offset)
	}

	return result
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
