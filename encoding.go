package lsmtree

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// Low-level encoding for SSTable files. Keys, values and index keys are all
// stored as length-prefixed byte strings: a big-endian uint32 length followed
// by that many bytes.

// appendBytes appends s to b as a uint32 length followed by the bytes of s.
func appendBytes(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

// sectionReader reads records from the bytes between two offsets of an
// SSTable file. It tracks how many bytes are left, so a corrupt length is
// rejected before anything is allocated for it.
type sectionReader struct {
	r    *bufio.Reader
	left int64
}

func newSectionReader(f io.ReaderAt, start, end int64) *sectionReader {
	return &sectionReader{
		r:    bufio.NewReader(io.NewSectionReader(f, start, end-start)),
		left: end - start,
	}
}

// read reads exactly n bytes. If fewer than n bytes are left, the file is
// truncated or corrupt, and it returns io.ErrUnexpectedEOF.
func (r *sectionReader) read(n int64) ([]byte, error) {
	if n > r.left {
		return nil, fmt.Errorf("need %d bytes, only %d left: %w", n, r.left, io.ErrUnexpectedEOF)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r.r, b); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	r.left -= n
	return b, nil
}

func (r *sectionReader) readUint32() (uint32, error) {
	b, err := r.read(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

// readBytes reads a uint32 length followed by that many bytes.
func (r *sectionReader) readBytes() ([]byte, error) {
	n, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	return r.read(int64(n))
}

// readEntry reads one data entry: a length-prefixed key and value.
func (r *sectionReader) readEntry() (Entry, error) {
	key, err := r.readBytes()
	if err != nil {
		return Entry{}, err
	}
	value, err := r.readBytes()
	if err != nil {
		return Entry{}, err
	}
	return Entry{Key: string(key), Value: string(value)}, nil
}
