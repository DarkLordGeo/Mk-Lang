// Why: LLVM's own passes decide hotness from a "ProfileSummary" module flag;
// codegen must emit one that agrees with the counts it attaches, or LLVM's
// view of the profile would differ from Mk's.
//
// What: Cutoffs, CutoffEntry, LLVMSummary and Profile.LLVM, the numbers of
// that flag (format InstrProf).
//
// How: computed as LLVM's InstrProfSummaryBuilder and
// ProfileSummaryBuilder::computeDetailedSummary compute them, over every
// record of the profile, never a subset, with saturating sums.

package pgo

import (
	"math/bits"
	"slices"
)

// Cutoffs are LLVM's default detailed-summary cutoffs, in parts per million
// (ProfileSummaryBuilder::DefaultCutoffs).
var Cutoffs = []uint32{10000, 100000, 200000, 300000, 400000, 500000, 600000, 700000, 800000,
	900000, 950000, 990000, 999000, 999900, 999990, 999999}

// CutoffEntry is one detailed-summary row: the smallest count among the
// largest counts that together reach Cutoff of the total, and how many
// counts that is.
type CutoffEntry struct {
	Cutoff   uint32
	MinCount uint64
	NumCount uint64
}

// LLVMSummary is the content of LLVM's ProfileSummary for this profile.
// Counts[0] of each record is a function entry count, the others internal
// counts; sums saturate at 2^64-1.
type LLVMSummary struct {
	TotalCount, MaxCount, MaxInternalCount, MaxFunctionCount uint64
	NumCounts, NumFunctions                                  uint64
	Detailed                                                 []CutoffEntry
}

// LLVM computes p's LLVMSummary. Deterministic: it depends only on the
// multiset of counts. A profile with no counts gives a zero summary.
func (p *Profile) LLVM() LLVMSummary {
	var s LLVMSummary
	var all []uint64
	for _, r := range p.records() {
		s.NumFunctions++
		for i, c := range r.Counts {
			if i == 0 {
				s.MaxFunctionCount = max(s.MaxFunctionCount, c)
			} else {
				s.MaxInternalCount = max(s.MaxInternalCount, c)
			}
			s.MaxCount = max(s.MaxCount, c)
			s.TotalCount = addSat(s.TotalCount, c)
			s.NumCounts++
			all = append(all, c)
		}
	}
	slices.Sort(all)
	slices.Reverse(all)
	var sum, seen uint64
	var count uint64
	i := 0
	for _, cut := range Cutoffs {
		hi, lo := bits.Mul64(s.TotalCount, uint64(cut))
		desired, _ := bits.Div64(hi, lo, 1000000)
		for sum < desired && i < len(all) {
			count = all[i]
			// LLVM steps over equal counts as one frequency bucket.
			j := i
			for j < len(all) && all[j] == count {
				j++
			}
			sum = addSat(sum, mulSat(count, uint64(j-i)))
			seen += uint64(j - i)
			i = j
		}
		s.Detailed = append(s.Detailed, CutoffEntry{Cutoff: cut, MinCount: count, NumCount: seen})
	}
	return s
}

func mulSat(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return ^uint64(0)
	}
	return lo
}
