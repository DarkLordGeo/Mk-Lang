// Why: `mk profile merge` combines the profiles of several runs into one,
// and the result must not depend on the order of its inputs.
//
// What: Merge and addSat.
//
// How: records are summed by (Key, Checksum), counter by counter, saturating
// at 2^64-1, and the result is sorted and unique like any canonical profile.

package pgo

import (
	"fmt"
	"math"
	"slices"

	"mk/internal/source"
)

type mergeKey struct {
	key string
	sum source.Hash128
}

// Merge sums records by (Key, Checksum), counter by counter, saturating at
// 2^64-1; the result is canonical (sorted, unique) and shares no slice with
// any input. Records of one (Key, Checksum) with different len(Counts), within
// one input or across inputs, are an error naming the key and two lengths
// (the first such pair in key order, with the shortest and the longest length
// seen for it, so the message does not depend on argument order); on an error
// no profile is returned. A result that would break a limit of Check (more
// than MaxCount records, a key over MaxKeyLen or not UTF-8) is an error too,
// so a merged profile is always writable.
// Inputs need not be canonical, nil inputs are empty, and Merge never panics
// and never indexes one record by another's length. The result does not depend
// on the order of the inputs or of their records.
func Merge(ps ...*Profile) (*Profile, error) {
	index := map[mergeKey]int{}
	var out []Record
	type mismatch struct{ a, b int }
	bad := map[int]mismatch{}
	for _, p := range ps {
		if p == nil {
			continue
		}
		for i := range p.Records {
			r := &p.Records[i]
			k := mergeKey{r.Key, r.Checksum}
			j, ok := index[k]
			if !ok {
				index[k] = len(out)
				out = append(out, Record{Key: r.Key, Checksum: r.Checksum, Counts: slices.Clone(r.Counts)})
				continue
			}
			dst := &out[j]
			if len(dst.Counts) != len(r.Counts) {
				m, seen := bad[j]
				if !seen {
					m = mismatch{len(dst.Counts), len(dst.Counts)}
				}
				bad[j] = mismatch{min(m.a, len(r.Counts)), max(m.b, len(r.Counts))}
				continue
			}
			for c := range dst.Counts {
				dst.Counts[c] = addSat(dst.Counts[c], r.Counts[c])
			}
		}
	}
	if len(bad) > 0 {
		// Report the first offending record in key order with the shortest
		// and longest lengths seen for it, so any argument order gives one
		// message.
		first := -1
		for j := range bad {
			if first < 0 || less(&out[j], &out[first]) {
				first = j
			}
		}
		m := bad[first]
		r := &out[first]
		return nil, fmt.Errorf("pgo: merge: record %q checksum %s has %d counters in one record and %d in another",
			r.Key, r.Checksum, m.a, m.b)
	}
	slices.SortFunc(out, func(a, b Record) int { return cmpKey(&a, b.Key, b.Checksum) })
	m := &Profile{Records: out}
	// Sorted and unique by construction; Check refuses what Write would
	// refuse (an input built by hand with an oversized or non-UTF-8 key).
	if err := m.Check(); err != nil {
		return nil, fmt.Errorf("pgo: merge: %w", err)
	}
	return m, nil
}

// addSat is a + b, saturating at 2^64-1.
func addSat(a, b uint64) uint64 {
	if s := a + b; s >= a {
		return s
	}
	return math.MaxUint64
}
