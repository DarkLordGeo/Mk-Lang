// Why: a warm build is mostly cache lookups, and the stat hint only pays if
// it beats reading and hashing the file.
// What: BenchmarkCacheGetHit, BenchmarkCachePut (4 KiB and 1 MiB),
// BenchmarkHintLookup and BenchmarkReadAndHash.
// How: times each operation on a temporary store; the hint must be the
// cheaper one on each system or it is switched off there.

package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mk/internal/source"
)

var benchSizes = []int{4 << 10, 1 << 20}

func BenchmarkCacheGetHit(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("%dKiB", n>>10), func(b *testing.B) {
			s, _ := Open(b.TempDir(), Options{})
			key := testKey(1)
			s.Put(KindFunc, key, testPayload(1, n))
			b.SetBytes(int64(n))
			b.ReportAllocs()
			for b.Loop() {
				if _, ok, err := s.Get(KindFunc, key); !ok || err != nil {
					b.Fatal("miss")
				}
			}
		})
	}
}

func BenchmarkCachePut(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("new/%dKiB", n>>10), func(b *testing.B) {
			s, _ := Open(b.TempDir(), Options{})
			payload := testPayload(1, n)
			b.SetBytes(int64(n))
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				i++
				if err := s.Put(KindFunc, testKey(i), payload); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("existing/%dKiB", n>>10), func(b *testing.B) {
			s, _ := Open(b.TempDir(), Options{})
			payload := testPayload(1, n)
			s.Put(KindFunc, testKey(1), payload)
			b.SetBytes(int64(n))
			b.ReportAllocs()
			for b.Loop() {
				if err := s.Put(KindFunc, testKey(1), payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// benchTree writes 1,000 source files of about 3 KB in 10 directories, aged
// an hour so their hint records are not racy.
func benchTree(b *testing.B) (root string, dirs []string) {
	root = b.TempDir()
	old := time.Now().Add(-time.Hour)
	line := "    let total = items.fold(0, fn(a: Int, x: Int) -> Int { a + x })\n"
	var text []byte
	for len(text) < 3000 {
		text = append(text, line...)
	}
	for d := 0; d < 10; d++ {
		dir := filepath.Join(root, fmt.Sprintf("m%d", d))
		os.MkdirAll(dir, 0o755)
		dirs = append(dirs, dir)
		for f := 0; f < 100; f++ {
			p := filepath.Join(dir, fmt.Sprintf("f%d.mk", f))
			os.WriteFile(p, append([]byte(fmt.Sprintf("// %d %d\n", d, f)), text...), 0o644)
			os.Chtimes(p, old, old)
		}
	}
	return root, dirs
}

func BenchmarkHintLookup(b *testing.B) {
	root, dirs := benchTree(b)
	s, _ := Open(b.TempDir(), Options{})
	h := s.LoadHints(root)
	for _, dir := range dirs {
		statAt := s.Now()
		ds, _ := os.ReadDir(dir)
		for _, d := range ds {
			fi, _ := d.Info()
			h.Record(filepath.Join(filepath.Base(dir), d.Name()), fi, statAt, testKey(1))
		}
	}
	h.Save()
	b.ReportAllocs()
	for b.Loop() {
		hh := s.LoadHints(root)
		n := 0
		for _, dir := range dirs {
			ds, err := os.ReadDir(dir)
			if err != nil {
				b.Fatal(err)
			}
			for _, d := range ds {
				fi, err := d.Info()
				if err != nil {
					b.Fatal(err)
				}
				if _, ok := hh.Lookup(filepath.Join(filepath.Base(dir), d.Name()), fi); ok {
					n++
				}
			}
		}
		if n != 1000 {
			b.Fatalf("%d hint hits, want 1000", n)
		}
	}
}

func BenchmarkReadAndHash(b *testing.B) {
	_, dirs := benchTree(b)
	b.ReportAllocs()
	for b.Loop() {
		for _, dir := range dirs {
			ds, err := os.ReadDir(dir)
			if err != nil {
				b.Fatal(err)
			}
			for _, d := range ds {
				raw, err := os.ReadFile(filepath.Join(dir, d.Name()))
				if err != nil {
					b.Fatal(err)
				}
				source.HashString("mk.file.v1", source.Normalize(raw))
			}
		}
	}
}
