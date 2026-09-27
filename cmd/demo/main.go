package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	lsmtree "github.com/akarshippili/lsm-tree"
)

func main() {
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	path := flag.String("path", "data/sstable", "SSTable file to write, relative to the current directory")
	flag.Parse()

	switch *logLevel {
	case "debug":
		lsmtree.SetLogLevel(lsmtree.DEBUG)
	case "info":
		lsmtree.SetLogLevel(lsmtree.INFO)
	case "warn":
		lsmtree.SetLogLevel(lsmtree.WARN)
	case "error":
		lsmtree.SetLogLevel(lsmtree.ERROR)
	}

	entries := []lsmtree.Entry{}
	for i := range 100 {
		entries = append(entries, lsmtree.Entry{Key: fmt.Sprintf("key-%d", i), Value: fmt.Sprintf("value-%d", i)})
	}

	if err := os.MkdirAll(filepath.Dir(*path), 0o755); err != nil {
		log.Fatal(err)
	}

	if err := lsmtree.WriteSSTable(*path, entries); err != nil {
		log.Fatal(err)
	}

	sstable, err := lsmtree.OpenSSTable(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer sstable.Close()

	entries, err = sstable.Read()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("entries: %+v\n", entries)

	index := sstable.Index()
	fmt.Printf("index: %+v\n", index)
}
