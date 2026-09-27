package lsmtree

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// writeAndOpen writes entries to a temp SSTable file and opens it. The table
// is closed when the test ends.
func writeAndOpen(t *testing.T, entries []Entry) *SSTable {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sstable")
	if err := WriteSSTable(path, entries); err != nil {
		t.Fatalf("WriteSSTable() error = %v", err)
	}
	s, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// writeSSTable builds a memtable from kv pairs, writes and opens it as an
// SSTable, and returns it along with the sorted entries that were written.
func writeSSTable(t *testing.T, kv map[string]string) (*SSTable, []Entry) {
	t.Helper()
	m := NewMemTable()
	for k, v := range kv {
		m.Add(k, v)
	}
	entries := m.Entries()
	return writeAndOpen(t, entries), entries
}

// collect drains it into a slice and returns it with the iterator's error.
func collect(it *Iterator) ([]Entry, error) {
	result := []Entry{}
	for it.Next() {
		result = append(result, it.Entry())
	}
	return result, it.Err()
}

func TestSSTableRoundTrip(t *testing.T) {
	kv := map[string]string{}
	for i := 0; i < 50; i++ {
		kv[fmt.Sprintf("key-%03d", i)] = fmt.Sprintf("value-%d", i)
	}
	s, want := writeSSTable(t, kv)

	got, err := collect(s.Iterator())
	if err != nil {
		t.Fatalf("Iterator() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Iterator() returned %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entries[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSSTableEmpty(t *testing.T) {
	s, _ := writeSSTable(t, map[string]string{})

	got, err := collect(s.Iterator())
	if err != nil {
		t.Fatalf("Iterator() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Iterator() returned %d entries, want 0", len(got))
	}
	if index := s.Index(); len(index) != 0 {
		t.Errorf("Index() returned %d entries, want 0", len(index))
	}
}

func TestSSTableKeyNamedEND(t *testing.T) {
	s, want := writeSSTable(t, map[string]string{"A": "a", "END": "e", "Z": "z"})

	got, err := collect(s.Iterator())
	if err != nil {
		t.Fatalf("Iterator() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Iterator() returned %+v, want %+v", got, want)
	}
}

func TestSSTableTombstoneValue(t *testing.T) {
	m := NewMemTable()
	m.Add("keep", "v")
	m.Add("gone", "v")
	m.Delete("gone")
	s := writeAndOpen(t, m.Entries())

	got, err := collect(s.Iterator())
	if err != nil {
		t.Fatalf("Iterator() error = %v", err)
	}
	found := false
	for _, e := range got {
		if e.Key == "gone" {
			found = true
			if e.Value != Tombstone {
				t.Errorf("value for deleted key = %q, want %q", e.Value, Tombstone)
			}
		}
	}
	if !found {
		t.Errorf("deleted key missing from Iterator() result %+v", got)
	}
}

func TestSSTableIndex(t *testing.T) {
	kv := map[string]string{}
	for i := 0; i < 50; i++ {
		kv[fmt.Sprintf("key-%03d", i)] = fmt.Sprintf("value-%d", i)
	}
	s, entries := writeSSTable(t, kv)

	// Each entry is keyLen(4) + key + valueLen(4) + value bytes.
	var want []IndexEntry
	offset := int64(0)
	for i, e := range entries {
		if i%16 == 0 {
			want = append(want, IndexEntry{Key: e.Key, Offset: offset})
		}
		offset += int64(8 + len(e.Key) + len(e.Value))
	}

	got := s.Index()
	if len(got) != len(want) {
		t.Fatalf("Index() returned %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].Key <= got[i-1].Key {
			t.Errorf("index not sorted at %d: %q <= %q", i, got[i].Key, got[i-1].Key)
		}
	}
}

func TestSSTableWriteBadPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "sstable")
	if err := WriteSSTable(path, nil); err == nil {
		t.Error("WriteSSTable() to a missing directory returned nil error")
	}
}

func TestFloorIndex(t *testing.T) {
	index := []IndexEntry{
		{Key: "b", Offset: 0},
		{Key: "d", Offset: 100},
		{Key: "f", Offset: 200},
	}

	tests := []struct {
		key    string
		want   int
		wantOk bool
	}{
		{"a", 0, false}, // before the first key
		{"b", 0, true},  // exact match on the first key
		{"c", 0, true},  // between keys
		{"d", 1, true},  // exact match in the middle
		{"e", 1, true},  // between keys
		{"f", 2, true},  // exact match on the last key
		{"z", 2, true},  // after the last key
		{"b0", 0, true}, // prefix of the next key's range
	}

	for _, tt := range tests {
		got, ok := floorIndex(index, tt.key)
		if ok != tt.wantOk || (ok && got != tt.want) {
			t.Errorf("floorIndex(%q) = (%d, %v), want (%d, %v)", tt.key, got, ok, tt.want, tt.wantOk)
		}
	}
}

func TestFloorIndexEmpty(t *testing.T) {
	if got, ok := floorIndex(nil, "a"); ok {
		t.Errorf("floorIndex(nil, \"a\") = (%d, true), want not found", got)
	}
}

func TestSSTableGet(t *testing.T) {
	// 50 even keys give index entries at key-000, key-032, key-064 and key-096,
	// and the odd keys are gaps to look up.
	kv := map[string]string{}
	for i := 0; i < 100; i += 2 {
		kv[fmt.Sprintf("key-%03d", i)] = fmt.Sprintf("value-%d", i)
	}
	s, _ := writeSSTable(t, kv)

	tests := []struct {
		key     string
		wantVal string
		wantOk  bool
	}{
		{"key-000", "value-0", true},  // first key, first index entry
		{"key-030", "value-30", true}, // last key in the first block
		{"key-032", "value-32", true}, // index entry key
		{"key-034", "value-34", true}, // first key after an index entry
		{"key-098", "value-98", true}, // last key in the table
		{"a", "", false},              // before the first key
		{"key-001", "", false},        // gap in the first block
		{"key-031", "", false},        // gap just before an index entry
		{"key-099", "", false},        // after the last key
		{"key-0", "", false},          // prefix of real keys
	}

	for _, tt := range tests {
		val, ok, err := s.Get(tt.key)
		if err != nil {
			t.Errorf("Get(%q) error = %v", tt.key, err)
			continue
		}
		if ok != tt.wantOk || val != tt.wantVal {
			t.Errorf("Get(%q) = (%q, %v), want (%q, %v)", tt.key, val, ok, tt.wantVal, tt.wantOk)
		}
	}
}

func TestSSTableGetTombstone(t *testing.T) {
	m := NewMemTable()
	m.Add("gone", "v")
	m.Delete("gone")
	s := writeAndOpen(t, m.Entries())

	val, ok, err := s.Get("gone")
	if err != nil || !ok || val != Tombstone {
		t.Errorf("Get(\"gone\") = (%q, %v, %v), want (%q, true, nil)", val, ok, err, Tombstone)
	}
}

func TestSSTableGetEmpty(t *testing.T) {
	s, _ := writeSSTable(t, map[string]string{})

	val, ok, err := s.Get("a")
	if err != nil || ok {
		t.Errorf("Get(\"a\") on empty table = (%q, %v, %v), want (\"\", false, nil)", val, ok, err)
	}
}

func TestOpenSSTableMissingFile(t *testing.T) {
	if _, err := OpenSSTable(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("OpenSSTable() on a missing file returned nil error")
	}
}

func TestOpenSSTableCorrupt(t *testing.T) {
	footer := func(indexLen, indexOffset uint64) []byte {
		b := binary.BigEndian.AppendUint64(nil, indexLen)
		return binary.BigEndian.AppendUint64(b, indexOffset)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{"empty file", nil},
		{"shorter than footer", []byte{1, 2, 3}},
		{"index offset past end", footer(0, 100)},
		{"index shorter than indexLen", footer(3, 0)},
	}

	for _, tt := range tests {
		path := filepath.Join(t.TempDir(), "sstable")
		if err := os.WriteFile(path, tt.data, 0o644); err != nil {
			t.Fatal(err)
		}
		if s, err := OpenSSTable(path); err == nil {
			s.Close()
			t.Errorf("%s: OpenSSTable() returned nil error", tt.name)
		}
	}
}

func TestSSTableConcurrentGet(t *testing.T) {
	kv := map[string]string{}
	for i := 0; i < 200; i++ {
		kv[fmt.Sprintf("key-%03d", i)] = fmt.Sprintf("value-%d", i)
	}
	s, _ := writeSSTable(t, kv)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k, want := range kv {
				if val, ok, err := s.Get(k); err != nil || !ok || val != want {
					t.Errorf("Get(%q) = (%q, %v, %v), want (%q, true, nil)", k, val, ok, err, want)
				}
			}
		}()
	}
	wg.Wait()
}

func TestSSTableGetEveryKey(t *testing.T) {
	kv := map[string]string{}
	for i := 0; i < 1000; i++ {
		kv[fmt.Sprintf("key-%04d", i)] = fmt.Sprintf("value-%d", i)
	}
	s, _ := writeSSTable(t, kv)

	for k, want := range kv {
		val, ok, err := s.Get(k)
		if err != nil || !ok || val != want {
			t.Errorf("Get(%q) = (%q, %v, %v), want (%q, true, nil)", k, val, ok, err, want)
		}
	}
}

func TestSSTableScan(t *testing.T) {
	// 50 even keys, so index entries are at key-000, key-032, key-064 and
	// key-096, and the odd keys are gaps.
	kv := map[string]string{}
	for i := 0; i < 100; i += 2 {
		kv[fmt.Sprintf("key-%03d", i)] = fmt.Sprintf("value-%d", i)
	}
	s, entries := writeSSTable(t, kv)

	tests := []struct {
		from      string
		wantFirst string // "" means no entries
	}{
		{"", "key-000"},        // everything
		{"a", "key-000"},       // before the first key
		{"key-000", "key-000"}, // first key
		{"key-001", "key-002"}, // gap in the first block
		{"key-031", "key-032"}, // gap just before an index entry
		{"key-032", "key-032"}, // index entry key
		{"key-050", "key-050"}, // middle of a block
		{"key-098", "key-098"}, // last key
		{"key-099", ""},        // after the last key
		{"z", ""},              // far after the last key
	}

	for _, tt := range tests {
		got, err := collect(s.Scan(tt.from))
		if err != nil {
			t.Errorf("Scan(%q) error = %v", tt.from, err)
			continue
		}

		// Scan must return exactly the entries with key >= from, in order.
		var want []Entry
		for _, e := range entries {
			if e.Key >= tt.from {
				want = append(want, e)
			}
		}
		if len(got) != len(want) {
			t.Errorf("Scan(%q) returned %d entries, want %d", tt.from, len(got), len(want))
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Scan(%q)[%d] = %+v, want %+v", tt.from, i, got[i], want[i])
			}
		}
		if tt.wantFirst != "" && got[0].Key != tt.wantFirst {
			t.Errorf("Scan(%q) first key = %q, want %q", tt.from, got[0].Key, tt.wantFirst)
		}
	}
}

func TestIteratorTruncatedData(t *testing.T) {
	// One entry "a" whose valueLen says 100 bytes, but the data section ends
	// after 1 byte of value.
	var b []byte
	b = binary.BigEndian.AppendUint32(b, 1)
	b = append(b, 'a')
	b = binary.BigEndian.AppendUint32(b, 100)
	b = append(b, 'x')
	dataEnd := uint64(len(b))
	b = binary.BigEndian.AppendUint32(b, 1) // index: key "a" at offset 0
	b = append(b, 'a')
	b = binary.BigEndian.AppendUint32(b, 0)
	b = binary.BigEndian.AppendUint64(b, 1) // footer
	b = binary.BigEndian.AppendUint64(b, dataEnd)

	path := filepath.Join(t.TempDir(), "sstable")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable() error = %v", err)
	}
	defer s.Close()

	it := s.Iterator()
	if it.Next() {
		t.Errorf("Next() = true on truncated entry, got %+v", it.Entry())
	}
	if err := it.Err(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Err() = %v, want io.ErrUnexpectedEOF", err)
	}
	if it.Next() {
		t.Error("Next() = true after an error")
	}

	if _, _, err := s.Get("a"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Get() error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestSSTableScanSkipsEarlierBlocks(t *testing.T) {
	// 50 even keys, so blocks start at key-000, key-032, key-064 and key-096.
	kv := map[string]string{}
	for i := 0; i < 100; i += 2 {
		kv[fmt.Sprintf("key-%03d", i)] = fmt.Sprintf("value-%d", i)
	}
	m := NewMemTable()
	for k, v := range kv {
		m.Add(k, v)
	}
	path := filepath.Join(t.TempDir(), "sstable")
	if err := WriteSSTable(path, m.Entries()); err != nil {
		t.Fatalf("WriteSSTable() error = %v", err)
	}

	// Corrupt the first entry of blocks 0 and 1 by giving it a key length
	// longer than the whole data section. Any read that starts in those
	// blocks fails with io.ErrUnexpectedEOF. The index section is untouched.
	s, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable() error = %v", err)
	}
	badKeyLen := binary.BigEndian.AppendUint32(nil, uint32(s.dataEnd)+1)
	corruptOffsets := []int64{s.index[0].Offset, s.index[1].Offset}
	s.Close()

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, off := range corruptOffsets {
		if _, err := f.WriteAt(badKeyLen, off); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	s, err = OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable() after corruption error = %v", err)
	}
	defer s.Close()

	// Sanity check: reading from the start does hit the corruption.
	if _, err := collect(s.Iterator()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Iterator() error = %v, want io.ErrUnexpectedEOF", err)
	}

	// Each of these has key-064 (block 2) as its floor, so Scan must start at
	// block 2 and never read the corrupted blocks.
	for _, from := range []string{"key-064", "key-065", "key-080", "key-095"} {
		got, err := collect(s.Scan(from))
		if err != nil {
			t.Errorf("Scan(%q) error = %v, want nil (it read a block before its floor)", from, err)
			continue
		}
		if len(got) == 0 || got[0].Key < from {
			t.Errorf("Scan(%q) returned %+v, want keys starting at %q", from, got, from)
		}
	}

	// key-063's floor is key-032 (block 1), so this scan legitimately starts
	// in a corrupted block.
	if _, err := collect(s.Scan("key-063")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Scan(\"key-063\") error = %v, want io.ErrUnexpectedEOF", err)
	}
}

// rawSSTable assembles an SSTable file from raw data and index bytes, so tests
// can write lengths that WriteSSTable never would.
func rawSSTable(t *testing.T, data, index []byte, indexLen uint64) string {
	t.Helper()
	b := append(append([]byte{}, data...), index...)
	b = binary.BigEndian.AppendUint64(b, indexLen)
	b = binary.BigEndian.AppendUint64(b, uint64(len(data)))
	path := filepath.Join(t.TempDir(), "sstable")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// allocatedDuring returns roughly how many bytes f allocated.
func allocatedDuring(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestSSTableHugeLengthDoesNotAllocate(t *testing.T) {
	const huge = 0xFFFFFFFF
	const limit = 1 << 20 // anything near huge would be ~4 GB

	u32 := func(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
	cat := func(parts ...[]byte) []byte {
		var b []byte
		for _, p := range parts {
			b = append(b, p...)
		}
		return b
	}
	// Index with one entry, key "a" at offset 0.
	indexA := cat(u32(1), []byte("a"), u32(0))

	t.Run("data key length", func(t *testing.T) {
		data := cat(u32(huge), []byte("a"), u32(1), []byte("v"))
		s, err := OpenSSTable(rawSSTable(t, data, indexA, 1))
		if err != nil {
			t.Fatalf("OpenSSTable() error = %v", err)
		}
		defer s.Close()

		checkRead(t, "Iterator", limit, func() error { _, err := collect(s.Iterator()); return err })
		checkRead(t, "Scan", limit, func() error { _, err := collect(s.Scan("a")); return err })
		checkRead(t, "Get", limit, func() error { _, _, err := s.Get("a"); return err })
	})

	t.Run("data value length", func(t *testing.T) {
		data := cat(u32(1), []byte("a"), u32(huge), []byte("v"))
		s, err := OpenSSTable(rawSSTable(t, data, indexA, 1))
		if err != nil {
			t.Fatalf("OpenSSTable() error = %v", err)
		}
		defer s.Close()

		checkRead(t, "Iterator", limit, func() error { _, err := collect(s.Iterator()); return err })
		checkRead(t, "Get", limit, func() error { _, _, err := s.Get("a"); return err })
	})

	t.Run("index key length", func(t *testing.T) {
		index := cat(u32(huge), []byte("a"), u32(0))
		path := rawSSTable(t, nil, index, 1)
		checkRead(t, "OpenSSTable", limit, func() error {
			s, err := OpenSSTable(path)
			if err == nil {
				s.Close()
			}
			return err
		})
	})
}

// checkRead runs read, which should fail on a length that runs past the end
// of its section, and checks it fails with io.ErrUnexpectedEOF without
// allocating more than limit bytes.
func checkRead(t *testing.T, name string, limit uint64, read func() error) {
	t.Helper()
	var err error
	if n := allocatedDuring(func() { err = read() }); n > limit {
		t.Errorf("%s allocated %d bytes, want <= %d", name, n, limit)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("%s error = %v, want io.ErrUnexpectedEOF", name, err)
	}
}
