package main

import (
	"fmt"

	lsmtree "github.com/akarshippili/lsm-tree"
)

func main() {

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
	fmt.Printf("%+v\n", entries)

	index := sstable.GetIndex()
	fmt.Printf("%+v\n", index)
}
