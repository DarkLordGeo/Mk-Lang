// Why: a .mkprof file comes from disk and may be corrupt or hostile.
// What: FuzzPGORead.
// How: Read, Merge and Lookup must never panic or hang on any input.

package pgo

import (
	"bytes"
	"testing"
)

func FuzzPGORead(f *testing.F) {
	good := file(2, rec("a", 1, 5, 6), rec("b", 0, 7))
	f.Add(good, good, 2)
	f.Add(file(1, rec("a", 1, 5)), good, 1)
	f.Add(file(0), []byte(Magic), 0)
	f.Add([]byte("MKPROF"), file(1, rec("a", 1)), -1)
	f.Add(file(1, rec("a", 1, 1, 2, 3)), file(1, rec("a", 1, 1, 2)), 3)
	f.Fuzz(func(t *testing.T, a, b []byte, n int) {
		p, errP := Read(bytes.NewReader(a))
		q, errQ := Read(bytes.NewReader(b))
		if errP == nil {
			if err := p.Check(); err != nil {
				t.Fatalf("Read returned a non-canonical profile: %v", err)
			}
			out, err := p.Bytes()
			if err != nil || !bytes.Equal(out, a) {
				t.Fatalf("accepted input is not canonical: %v", err)
			}
			m, err := Merge(p, p)
			if err != nil {
				t.Fatalf("Merge(p, p): %v", err)
			}
			if err := m.Check(); err != nil {
				t.Fatalf("Merge(p, p) is not canonical: %v", err)
			}
			for _, r := range p.Records {
				if c, ok := p.Lookup(r.Key, r.Checksum, n); ok && len(c) != n {
					t.Fatalf("Lookup returned %d counts for n = %d", len(c), n)
				}
				if _, ok := p.Lookup(r.Key, r.Checksum, len(r.Counts)); !ok {
					t.Fatalf("Lookup misses its own record %q", r.Key)
				}
			}
			p.Summarize().Hot(uint64(n))
		}
		if errQ == nil {
			if m, err := Merge(p, q); err == nil {
				if err := m.Check(); err != nil {
					t.Fatalf("Merge(p, q) is not canonical: %v", err)
				}
				for _, r := range m.Records {
					Weights(r.Counts)
				}
			}
		}
	})
}
