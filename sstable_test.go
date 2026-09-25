package lsmtree

import (
	"fmt"
	"path/filepath"
	"testing"
)

// writeSSTable builds a memtable from kv pairs, writes it to a temp file, and
// returns the SSTable along with the sorted entries that were written.
func writeSSTable(t *testing.T, kv map[string]string) (*SSTable, []Entry) {
	t.Helper()
	m := NewMemTable()
	for k, v := range kv {
		m.Add(k, v)
	}
	entries := m.Entries()

	s := NewSSTable(filepath.Join(t.TempDir(), "sstable"), entries)
	if err := s.Write(); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	return s, entries
}

func TestSSTableRoundTrip(t *testing.T) {
	kv := map[string]string{}
	for i := 0; i < 50; i++ {
		kv[fmt.Sprintf("key-%03d", i)] = fmt.Sprintf("value-%d", i)
	}
	s, want := writeSSTable(t, kv)

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Read() returned %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entries[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSSTableEmpty(t *testing.T) {
	s, _ := writeSSTable(t, map[string]string{})

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Read() returned %d entries, want 0", len(got))
	}
	if index := s.GetIndex(); len(index) != 0 {
		t.Errorf("GetIndex() returned %d entries, want 0", len(index))
	}
}

func TestSSTableKeyNamedEND(t *testing.T) {
	s, want := writeSSTable(t, map[string]string{"A": "a", "END": "e", "Z": "z"})

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Read() returned %+v, want %+v", got, want)
	}
}

func TestSSTableTombstoneValue(t *testing.T) {
	m := NewMemTable()
	m.Add("keep", "v")
	m.Add("gone", "v")
	m.Delete("gone")

	s := NewSSTable(filepath.Join(t.TempDir(), "sstable"), m.Entries())
	if err := s.Write(); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
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
		t.Errorf("deleted key missing from Read() result %+v", got)
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

	got := s.GetIndex()
	if len(got) != len(want) {
		t.Fatalf("GetIndex() returned %d entries, want %d", len(got), len(want))
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
	s := NewSSTable(filepath.Join(t.TempDir(), "missing-dir", "sstable"), nil)
	if err := s.Write(); err == nil {
		t.Error("Write() to a missing directory returned nil error")
	}
}
