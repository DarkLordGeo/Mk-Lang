// fs, Go: the tree operations of tests/bench-lanes/std-fs2/README.md through
// Go's os and path/filepath. The tree is made under os.TempDir in
// mkbench-fs2-go: 10 directories d<i>, each with 10 directories s<j> of 10
// one-byte files f<k>, 1,110 entries.
// Usage: fs walk <reps> | fs mkrm <reps> | fs copy <file> <reps>.
//
//	walk: makes the tree, lists it reps times with filepath.WalkDir (the
//	      root not counted), removes it; prints reps, directories, files
//	      and name bytes, summed over the walks.
//	mkrm: reps times makes the tree (os.MkdirAll and os.WriteFile) and
//	      removes it with os.RemoveAll; prints reps, entries made and the
//	      removals that left the root behind.
//	copy: copies file reps times onto one destination (os.Create and
//	      io.Copy, which uses copy_file_range on Linux); prints reps and
//	      the bytes copied.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func makeTree(root string) int {
	n := 0
	for i := 0; i < 10; i++ {
		for j := 0; j < 10; j++ {
			d := filepath.Join(root, "d"+strconv.Itoa(i), "s"+strconv.Itoa(j))
			check(os.MkdirAll(d, 0o777))
			for k := 0; k < 10; k++ {
				check(os.WriteFile(filepath.Join(d, "f"+strconv.Itoa(k)), []byte("x"), 0o666))
			}
			n += 11
		}
		n++
	}
	return n
}

func copyFile(src, dst string) int64 {
	in, err := os.Open(src)
	check(err)
	defer in.Close()
	out, err := os.Create(dst)
	check(err)
	n, err := io.Copy(out, in)
	check(err)
	check(out.Close())
	return n
}

func main() {
	root := filepath.Join(os.TempDir(), "mkbench-fs2-go")
	if os.Args[1] == "copy" {
		n, err := strconv.Atoi(os.Args[3])
		check(err)
		dst := root + "-copy"
		var bytes int64
		for i := 0; i < n; i++ {
			bytes += copyFile(os.Args[2], dst)
		}
		check(os.Remove(dst))
		fmt.Printf("%d %d\n", n, bytes)
		return
	}
	n, err := strconv.Atoi(os.Args[2])
	check(err)
	check(os.RemoveAll(root))
	if os.Args[1] == "walk" {
		makeTree(root)
		var dirs, files, names int
		for i := 0; i < n; i++ {
			check(filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if p == root {
					return nil
				}
				if d.IsDir() {
					dirs++
				} else {
					files++
				}
				names += len(d.Name())
				return nil
			}))
		}
		check(os.RemoveAll(root))
		fmt.Printf("%d %d %d %d\n", n, dirs, files, names)
		return
	}
	made, fails := 0, 0
	for i := 0; i < n; i++ {
		made += makeTree(root)
		check(os.RemoveAll(root))
		if _, err := os.Lstat(root); err == nil {
			fails++
		}
	}
	fmt.Printf("%d %d %d\n", n, made, fails)
}
