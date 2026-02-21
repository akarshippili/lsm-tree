package lsmtree

import (
	"sort"
	"sync"
)

const Tombstone = "__tombstone__"

type Entry struct {
	Key   string
	Value string
}

type MemTable struct {
	entries map[string]Entry
	mu      sync.RWMutex
	size    int
}

func NewMemTable() *MemTable {
	return &MemTable{
		entries: make(map[string]Entry),
	}
}

func (m *MemTable) Add(key string, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if oldEntry, ok := m.entries[key]; ok {
		m.size -= len(key) + len(oldEntry.Value)
	}

	m.entries[key] = Entry{Key: key, Value: value}
	m.size += len(key) + len(value)
}

func (m *MemTable) Get(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[key]
	if !ok {
		return "", false
	}
	return entry.Value, true
}

func (m *MemTable) Delete(key string) {
	m.Add(key, Tombstone)
}

func (m *MemTable) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}

// Entries returns a sorted list of entries in the memtable.
// TODO: Implement a more efficient way to sort the entries.
func (m *MemTable) Entries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := make([]Entry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Key < entries[j].Key
	})
	return entries
}
