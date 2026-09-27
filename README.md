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

- `WriteSSTable(path, entries)` — write sorted entries to disk with a sparse index (every 16th key)
- `OpenSSTable(path)` — open an SSTable and load its index into memory; `Close()` when done
- `Get(key)` — look up one key by binary searching the index and scanning a single block
- `Iterator()` — iterate over every entry in key order, one at a time
- `Scan(from)` — iterate over entries with keys >= `from`, starting at the block that could contain it
- `Index()` — the sparse index mapping keys to file offsets

#### File Format

```
┌──────────────────────────────────────┐
│ Data Section                         │
│  [keyLen | key | valueLen | value]   │
│  ...                                 │
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

import (
    "fmt"
    "log"

    lsm "github.com/akarshippili/lsm-tree"
)

func main() {
    // Write to memtable
    mt := lsm.NewMemTable()
    mt.Add("name", "alice")
    mt.Add("city", "seattle")

    // Flush to SSTable
    if err := lsm.WriteSSTable("data/sstable", mt.Entries()); err != nil {
        log.Fatal(err)
    }

    // Read back
    st, err := lsm.OpenSSTable("data/sstable")
    if err != nil {
        log.Fatal(err)
    }
    defer st.Close()

    city, found, err := st.Get("city")
    fmt.Println(city, found, err)

    it := st.Iterator()
    for it.Next() {
        fmt.Println(it.Entry())
    }
    if err := it.Err(); err != nil {
        log.Fatal(err)
    }
}
```

## Running Tests

```sh
go test ./...
```
