package lsmtree

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestPutAndGet(t *testing.T) {
	m := NewMemTable()

	m.Add("banana", "yellow")
	m.Add("apple", "red")
	m.Add("cherry", "dark red")

	tests := []struct {
		key     string
		wantVal string
		wantOk  bool
	}{
		{"apple", "red", true},
		{"banana", "yellow", true},
		{"cherry", "dark red", true},
		{"durian", "", false},
	}

	for _, tt := range tests {
		val, ok := m.Get(tt.key)
		if ok != tt.wantOk || val != tt.wantVal {
			t.Errorf("Get(%q) = (%q, %v), want (%q, %v)", tt.key, val, ok, tt.wantVal, tt.wantOk)
		}
	}
}

func TestUpdate(t *testing.T) {
	m := NewMemTable()

	m.Add("key", "v1")
	m.Add("key", "v2")

	val, ok := m.Get("key")
	if !ok || val != "v2" {
		t.Errorf("Get after update = (%q, %v), want (\"v2\", true)", val, ok)
	}
}

func TestDelete(t *testing.T) {
	m := NewMemTable()

	m.Add("key", "value")
	m.Delete("key")

	val, ok := m.Get("key")
	if !ok || val != Tombstone {
		t.Errorf("Get after delete = (%q, %v), want (%q, true)", val, ok, Tombstone)
	}
}

func TestDeleteNonExistentKey(t *testing.T) {
	m := NewMemTable()

	m.Delete("ghost")

	val, ok := m.Get("ghost")
	if !ok || val != Tombstone {
		t.Errorf("Get after deleting non-existent key = (%q, %v), want (%q, true)", val, ok, Tombstone)
	}
}

func TestEntriesSorted(t *testing.T) {
	m := NewMemTable()

	keys := []string{"delta", "alpha", "charlie", "echo", "bravo"}
	for _, k := range keys {
		m.Add(k, "val-"+k)
	}

	entries := m.Entries()
	expected := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	if len(entries) != len(expected) {
		t.Fatalf("Entries() returned %d entries, want %d", len(entries), len(expected))
	}
	for i, e := range entries {
		if e.Key != expected[i] {
			t.Errorf("entries[%d].Key = %q, want %q", i, e.Key, expected[i])
		}
	}
}

func TestRandomInsertStaysSorted(t *testing.T) {
	m := NewMemTable()

	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%06d", rng.Intn(10000))
		m.Add(key, fmt.Sprintf("val-%d", i))
	}

	entries := m.Entries()
	for i := 1; i < len(entries); i++ {
		if entries[i].Key <= entries[i-1].Key {
			t.Fatalf("entries not sorted at index %d: %q <= %q", i, entries[i].Key, entries[i-1].Key)
		}
	}
}

func TestSizeTracking(t *testing.T) {
	m := NewMemTable()

	if m.Size() != 0 {
		t.Errorf("empty memtable Size = %d, want 0", m.Size())
	}

	m.Add("ab", "cd")   // 2+2 = 4
	m.Add("ef", "ghij") // 2+4 = 6, total = 10
	if m.Size() != 10 {
		t.Errorf("Size = %d, want 10", m.Size())
	}

	// Update: old "cd" (2) replaced by "x" (1), total = 9
	m.Add("ab", "x")
	if m.Size() != 9 {
		t.Errorf("Size after update = %d, want 9", m.Size())
	}

	// Delete: old "x" (1) replaced by tombstone (13), total = 9 - 2 - 1 + 2 + 13 = 21
	m.Delete("ab")
	expected := len("ef") + len("ghij") + len("ab") + len(Tombstone)
	if m.Size() != expected {
		t.Errorf("Size after delete = %d, want %d", m.Size(), expected)
	}
}
