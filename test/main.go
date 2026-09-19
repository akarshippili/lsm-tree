package main

import (
	"flag"
	"fmt"

	lsmtree "github.com/akarshippili/lsm-tree"
)

func main() {
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
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
	for i := 0; i < 100; i++ {
		entries = append(entries, lsmtree.Entry{Key: fmt.Sprintf("key-%d", i), Value: fmt.Sprintf("value-%d", i)})
	}

	sstable := lsmtree.NewSSTable("../data/sstable", entries)
	sstable.Write()

	entries, err := sstable.Read()
	if err != nil {
		println(err)
	}
	fmt.Printf("entries: %+v\n", entries)

	index := sstable.GetIndex()
	fmt.Printf("index: %+v\n", index)
}
