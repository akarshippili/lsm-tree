# LSM Tree

A Log-Structured Merge Tree implementation in Go, inspired by [Designing Data-Intensive Applications](https://dataintensive.net/).

## Components

### MemTable

An in-memory sorted key-value store backed by a hashmap with thread-safe read/write operations.

- `Add(key, value)` — insert or update a key-value pair
- `Get(key)` — retrieve a value by key
- `Delete(key)` — soft-delete using a tombstone marker
- `Size()` — current memory usage in bytes
- `Entries()` — all entries sorted by key

### SSTable

Sorted String Table — an immutable on-disk format for persisting sorted key-value data with a sparse index for efficient lookups.

- `Write()` — flush sorted entries to disk with a sparse index (every 16th key)
- `Read()` — read all entries from disk
- `GetIndex()` — retrieve the sparse index mapping keys to file offsets

#### File Format

```
┌──────────────────────────────────────┐
│ Data Section                         │
│  [keyLen | key | valueLen | value]   │
│  ...                                 │
│  [END marker]                        │
├──────────────────────────────────────┤
│ Index Section (sparse, every 16th)   │
│  [keyLen | key | offset]             │
│  ...                                 │
├──────────────────────────────────────┤
│ Footer (16 bytes)                    │
│  [indexLen (8B) | indexOffset (8B)]   │
└──────────────────────────────────────┘
```

## Usage

```go
package main

import "github.com/akarshippili/lsm-tree"

func main() {
    // Write to memtable
    mt := lsm.NewMemTable()
    mt.Add("name", "alice")
    mt.Add("city", "seattle")

    // Flush to SSTable
    st := lsm.NewSSTable("data/sstable", mt.Entries())
    st.Write()

    // Read back
    entries, _ := st.Read()
    index := st.GetIndex()
}
```

## Running Tests

```sh
go test ./...
```
