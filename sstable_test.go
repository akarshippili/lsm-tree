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

	s := NewSSTable(filepath.Join(t.TempDir(), "sstable"), m.Entries())
	if err := s.Write(); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

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

func TestSSTableGetMissingFile(t *testing.T) {
	s := NewSSTable(filepath.Join(t.TempDir(), "missing"), nil)

	if _, _, err := s.Get("a"); err == nil {
		t.Error("Get() on a missing file returned nil error")
	}
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
