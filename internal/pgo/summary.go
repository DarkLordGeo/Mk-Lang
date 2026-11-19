// Why: codegen marks functions hot or cold and attaches branch weights from a
// profile; both must be deterministic per profile, or a warm build would
// differ from a cold one.
//
// What: Summary with Summarize, Hot and Cold; Weights; and u128, the sum type
// of entry counts.
//
// How: the hot threshold is the count where the entry counts, sorted
// descending, reach 90% of their total; ties with it are hot. Weights scales
// counts to LLVM's 32-bit branch weights, keeping ratios.

package pgo

import (
	"math"
	"math/bits"
	"slices"
)

// u128 is an unsigned 128-bit integer, for sums of uint64 counts.
type u128 struct{ hi, lo uint64 }

func (a u128) add(x uint64) u128 {
	lo, c := bits.Add64(a.lo, x, 0)
	return u128{a.hi + c, lo}
}

func (a u128) sub(b u128) u128 {
	lo, br := bits.Sub64(a.lo, b.lo, 0)
	return u128{a.hi - b.hi - br, lo}
}

func (a u128) cmp(b u128) int {
	switch {
	case a.hi != b.hi:
		if a.hi < b.hi {
			return -1
		}
		return 1
	case a.lo < b.lo:
		return -1
	case a.lo > b.lo:
		return 1
	}
	return 0
}

// divSmall is a / d for d > 0.
func (a u128) divSmall(d uint64) u128 {
	qhi, r := bits.Div64(0, a.hi, d)
	qlo, _ := bits.Div64(r, a.lo, d)
	return u128{qhi, qlo}
}

func (a u128) isZero() bool { return a.hi == 0 && a.lo == 0 }

// Summary holds a profile's hot and cold thresholds, computed once by
// Summarize. The entry count of a record is Counts[0], or 0 for a record with
// no counters. With T the sum of all entry counts, the threshold is the count
// of the last member of the shortest prefix of the entry counts sorted
// descending whose sum reaches 90% of T.
//
// Tie rule: an entry count is hot when it is at least the
// threshold, so every count that ties with that last member is hot, and the
// hot set can be larger than the shortest prefix (ten records of 10: all ten
// are hot, not nine). Hot takes only the entry count, so it has no
// tie-breaker; the only other rule it could follow, "no tie is hot", would
// leave the hot set short of 90%. LLVM's own profile summary decides hotness
// by a count threshold the same way.
//
// An entry count is cold when it is 0 and T is not. When T is 0 nothing is
// hot or cold. The thresholds depend only on the multiset of entry counts, so
// they are deterministic per profile.
type Summary struct {
	total     u128
	threshold uint64 // > 0 whenever total is not 0
}

// Summarize computes p's Summary over every record of p, stale ones included.
// The profile's hash is in every release pack key, so a summary of the whole
// profile is covered by every pack's key. Callers must never summarize a
// subset of the profile, such as the records codegen matched: which records
// match depends on the checksums of functions in other packs, which is not in
// this pack's key, so a threshold computed from them could flip hot, cold and
// inlinehint in a pack whose key did not change, and a warm build would
// differ from a cold one.
//
// The cost of this rule: a stale record (another checksum, or another
// counter count) is never applied to a function, because Lookup refuses it,
// but its entry count is in the total; a profile merged across two builds
// counts a function whose checksum changed twice.
func (p *Profile) Summarize() *Summary {
	recs := p.records()
	entries := make([]uint64, len(recs))
	var s Summary
	for i := range recs {
		if len(recs[i].Counts) > 0 {
			entries[i] = recs[i].Counts[0]
		}
		s.total = s.total.add(entries[i])
	}
	if s.total.isZero() {
		return &s
	}
	slices.Sort(entries)
	// prefix reaches 90% of total when 10*(total-prefix) <= total, that is,
	// for integers, total-prefix <= floor(total/10).
	limit := s.total.divSmall(10)
	var prefix u128
	for i := len(entries) - 1; i >= 0; i-- {
		prefix = prefix.add(entries[i])
		if s.total.sub(prefix).cmp(limit) <= 0 {
			s.threshold = entries[i]
			break
		}
	}
	return &s
}

// Hot reports whether a function with this entry count is hot: the count is at
// least the threshold, ties included (see Summary).
func (s *Summary) Hot(entry uint64) bool {
	return s != nil && !s.total.isZero() && entry >= s.threshold
}

// Cold reports whether a function with this entry count is cold: its count is
// 0 and the profile's total is not.
func (s *Summary) Cold(entry uint64) bool {
	return s != nil && !s.total.isZero() && entry == 0
}

// Weights scales counts to LLVM's i32 branch weights, keeping ratios: counts
// whose largest is at most 2^32-1 are kept exactly; otherwise each count c
// becomes round(c * (2^32-1) / max), so the largest becomes exactly 2^32-1,
// and a non-zero count that would round to 0 becomes 1 (a taken edge is never
// reported as never taken). All-zero or empty counts give nil (emit no !prof).
func Weights(counts []uint64) []uint32 {
	var hi uint64
	for _, c := range counts {
		hi = max(hi, c)
	}
	if hi == 0 {
		return nil
	}
	w := make([]uint32, len(counts))
	if hi <= math.MaxUint32 {
		for i, c := range counts {
			w[i] = uint32(c)
		}
		return w
	}
	for i, c := range counts {
		// c * MaxUint32 + hi/2, divided by hi; the quotient is at most
		// MaxUint32, so the high word is below hi and Div64 cannot panic.
		ph, pl := bits.Mul64(c, math.MaxUint32)
		pl, carry := bits.Add64(pl, hi/2, 0)
		ph += carry
		q, _ := bits.Div64(ph, pl, hi)
		if q == 0 && c != 0 {
			q = 1
		}
		w[i] = uint32(q)
	}
	return w
}
