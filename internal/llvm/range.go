// Why: LLVM's verifier accepts a !range only in a normal form, while an
// enum's tag values come in any order and may wrap.
//
// What: RangePairs, the normalized !range of an N-bit tag.
//
// How: the pairs must be non-empty, not the full set, non-overlapping, not
// contiguous (the first with the last included) and in increasing order of
// their signed low bound; a set that cannot be stated gets no !range.

package llvm

import "sort"

// RangePairs is the normalized !range of an N-bit tag (1 <= bits <= 64) whose
// valid values are discrs (bit patterns; duplicates allowed, bits above N
// ignored), as a flat list lo0, hi0, lo1, hi1, ... of signed N-bit values,
// half-open, in increasing signed order. It is nil when no !range may be
// emitted: an empty set, the full set, or a run wrapping the signed boundary
// beside other runs.
func RangePairs(bits int, discrs []uint64) []int64 {
	if bits < 1 || bits > 64 || len(discrs) == 0 {
		return nil
	}
	sext := func(u uint64) int64 {
		if bits == 64 {
			return int64(u)
		}
		return int64(u<<(64-bits)) >> (64 - bits)
	}
	min := int64(-1) << (bits - 1)
	max := -(min + 1)
	vals := make([]int64, 0, len(discrs))
	for _, d := range discrs {
		vals = append(vals, sext(d))
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	// Step 2: maximal runs of consecutive signed values.
	type run struct{ a, b int64 }
	var runs []run
	for i, v := range vals {
		if i > 0 && v == vals[i-1] {
			continue
		}
		if n := len(runs); n > 0 && runs[n-1].b != max && runs[n-1].b+1 == v {
			runs[n-1].b = v
			continue
		}
		runs = append(runs, run{v, v})
	}
	// Step 1: the full set gives no !range.
	if len(runs) == 1 && runs[0].a == min && runs[0].b == max {
		return nil
	}
	end := func(b int64) int64 { // b+1, wrapping at the signed maximum
		if b == max {
			return min
		}
		return b + 1
	}
	// Step 3: runs contiguous across the wrap merge into one pair, which is
	// only emitted when it is the only pair.
	if len(runs) >= 2 && runs[0].a == min && runs[len(runs)-1].b == max {
		if len(runs) > 2 {
			return nil
		}
		return []int64{runs[1].a, end(runs[0].b)}
	}
	// Step 4: pairs in increasing signed order.
	out := make([]int64, 0, 2*len(runs))
	for _, r := range runs {
		out = append(out, r.a, end(r.b))
	}
	return out
}
