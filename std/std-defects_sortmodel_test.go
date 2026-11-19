// Why: the sort tests compare Mk's Slice.Sort with a model that makes the same
// less calls in the same order, so a count or permutation change shows exactly.
// What: mkSortModel and mkPdqsort, Go's pdqsort_func with Mk's Lomuto
// partition, small-element networks (mkSort9, mkSort13) and mkMed3Rec.
// How: Go's source made generic, taking Mk's paths for the same element
// sizes and pivot samples as Mk does.

package std

// The Go model of std.core's Slice.Sort (std/core/core_sort.mk): Go's
// pdqsort_func, copied from the Go 1.27 source (sort/zsortfunc.go, BSD-style
// licence, the Go Authors) with lessSwap made generic, plus what the Mk sort
// takes from Rust's ipnsort (core::slice::sort::unstable and shared): the
// branch-free cyclic Lomuto partition for an element of at most
// PdqLomutoBytes (32) bytes, for a range whose pivot sample did not read
// increasing and for the partition of the elements equal to the pivot;
// and, for an element of 1 to 8 bytes, median3_rec's pseudo-median, its
// samples centred (mkMed3Rec), for a range of 64 or more whose ninther
// sample read neither increasing nor decreasing, and leaves of up to
// PdqSmallMax (16) elements sorted by the sort9 or sort13 network and an
// insertion sort of the rest, a whole slice that short skipping a network
// its sorted prefix spans or leaves two or fewer after. The model makes
// the same calls of less in the same order and leaves the same
// permutation, so a test compares Mk's permutation and comparison count
// with it exactly. Its insertion sorts swap where Mk's shift a held
// element: the same calls of less on the same values, and the same result.

import (
	"math/bits"
)

// mkSortSlice is the data the model sorts: s ordered by less; lomuto when
// the Mk element is at most 32 bytes, small when it is 1 to 8.
type mkSortSlice[T any] struct {
	s      []T
	less   func(a, b *T) bool
	lomuto bool
	small  bool
}

func (d mkSortSlice[T]) Less(i, j int) bool { return d.less(&d.s[i], &d.s[j]) }
func (d mkSortSlice[T]) Swap(i, j int)      { d.s[i], d.s[j] = d.s[j], d.s[i] }

// mkSortModel sorts s as Mk's Slice.Sort does for an element of elemBytes
// bytes.
func mkSortModel[T any](s []T, elemBytes int, less func(a, b *T) bool) {
	d := mkSortSlice[T]{s: s, less: less, lomuto: elemBytes <= 32, small: elemBytes > 0 && elemBytes <= 8}
	if d.small && len(s) <= mkSmallMax {
		// Mk's Slice.Sort: a whole short slice goes to PdqSmall with the
		// length of its sorted prefix.
		k := 1
		for k < len(s) && !d.Less(k, k-1) {
			k++
		}
		mkSmall(d, 0, len(s), k)
		return
	}
	mkPdqsort(d, 0, len(s), bits.Len(uint(len(s))))
}

// mkSmallMax is core_sort.mk's PdqSmallMax: the longest leaf of a small
// element, sorted by a network and insertion.
const mkSmallMax = 16

// mkSort9 and mkSort13 are core_sort.mk's PdqSort9 and PdqSort13, the
// comparator pairs of ipnsort's sort9_optimal and sort13_optimal in order
// (TestSortNetworksMatchMk reads the Mk bodies against them).
var mkSort9 = [][2]int{
	{0, 3}, {1, 7}, {2, 5}, {4, 8}, {0, 7}, {2, 4}, {3, 8}, {5, 6}, {0, 2},
	{1, 3}, {4, 5}, {7, 8}, {1, 4}, {3, 6}, {5, 7}, {0, 1}, {2, 4}, {3, 5},
	{6, 8}, {2, 3}, {4, 5}, {6, 7}, {1, 2}, {3, 4}, {5, 6},
}

var mkSort13 = [][2]int{
	{0, 12}, {1, 10}, {2, 9}, {3, 7}, {5, 11}, {6, 8}, {1, 6}, {2, 3}, {4, 11},
	{7, 9}, {8, 10}, {0, 4}, {1, 2}, {3, 6}, {7, 8}, {9, 10}, {11, 12}, {4, 6},
	{5, 9}, {8, 11}, {10, 12}, {0, 5}, {3, 8}, {4, 7}, {6, 11}, {9, 10}, {0, 1},
	{2, 5}, {6, 9}, {7, 8}, {10, 11}, {1, 3}, {2, 4}, {5, 6}, {9, 10}, {1, 2},
	{3, 4}, {5, 7}, {6, 8}, {2, 3}, {4, 5}, {6, 7}, {8, 9}, {3, 4}, {5, 6},
}

// mkNetwork is PdqSort9 or PdqSort13 on data[a:]: each pair (i, j) swapped
// when data[a+j] is less than data[a+i] (PdqSwapIfLess).
func mkNetwork[T any](data mkSortSlice[T], a int, net [][2]int) {
	for _, p := range net {
		if data.Less(a+p[1], a+p[0]) {
			data.Swap(a+p[0], a+p[1])
		}
	}
}

// mkInsertFrom is PdqInsertFrom on data[a:b], whose first k are sorted.
func mkInsertFrom[T any](data mkSortSlice[T], a, k, b int) {
	for i := a + k; i < b; i++ {
		for j := i; j > a && data.Less(j, j-1); j-- {
			data.Swap(j, j-1)
		}
	}
}

// mkSmall is PdqSmall: data[a:b] of at most mkSmallMax elements whose
// first k0 are sorted, sorted by sort13 when 13 or more, sort9 when 9 or
// more, unless the sorted prefix spans the network or leaves two or fewer
// after it, then insertion.
func mkSmall[T any](data mkSortSlice[T], a, b, k0 int) {
	n := b - a
	if n < 2 {
		return
	}
	k := k0
	if n >= 13 {
		if k < 13 && n-k > 2 {
			mkNetwork(data, a, mkSort13)
			k = 13
		}
	} else if n >= 9 {
		if k < 9 && n-k > 2 {
			mkNetwork(data, a, mkSort9)
			k = 9
		}
	}
	mkInsertFrom(data, a, k, b)
}

// mkMed3, mkMed3Rec and mkMedSpread are core_sort.mk's PdqMed3, PdqMed3Rec
// and PdqMedSpread: ipnsort's median3 and median3_rec (shared/pivot.rs),
// with each block's three sub-blocks centred at 1/8, 1/2 and 7/8 of it
// (ipnsort starts them at 0, 1/2 and 7/8).
func mkMed3[T any](data mkSortSlice[T], a, b, c int) int {
	x := data.Less(a, b)
	y := data.Less(a, c)
	if x == y {
		if z := data.Less(b, c); z != x {
			return c
		}
		return b
	}
	return a
}

func mkMed3Rec[T any](data mkSortSlice[T], a, b, c, n int) int {
	if n >= 8 {
		n8 := n / 8
		h := n8 / 2
		a = mkMed3Rec(data, a+h, a+n8*3+h, a+n8*6+h, n8)
		b = mkMed3Rec(data, b+h, b+n8*3+h, b+n8*6+h, n8)
		c = mkMed3Rec(data, c+h, c+n8*3+h, c+n8*6+h, n8)
	}
	return mkMed3(data, a, b, c)
}

func mkMedSpread[T any](data mkSortSlice[T], a, length int) int {
	n8 := length / 8
	h := n8 / 2
	return mkMed3Rec(data, a+h, a+n8*3+h, a+n8*6+h, n8)
}

type mkSortedHint int

const (
	mkUnknownHint mkSortedHint = iota
	mkIncreasingHint
	mkDecreasingHint
)

type mkXorshift uint64

func (r *mkXorshift) Next() uint64 {
	*r ^= *r << 13
	*r ^= *r >> 7
	*r ^= *r << 17
	return uint64(*r)
}

func mkNextPowerOfTwo(length int) uint {
	return uint(1 << uint(bits.Len(uint(length))))
}

// mkPartitionLomuto is core_sort.mk's PdqPartitionLomuto: the pivot to a,
// s[a+1] held out, each later element compared with the pivot, the first
// element not less moved into the gap and the compared one into its place,
// the held element put back last, and the pivot swapped to its place. With
// neg an element goes left when the pivot is not less than it, so the left
// part holds the elements not greater than the pivot (the equal partition).
func mkPartitionLomuto[T any](data mkSortSlice[T], a, b, pivot int, neg bool) int {
	data.Swap(a, pivot)
	s := data.s
	lt := a + 1
	held := s[a+1]
	gap := a + 1
	for r := a + 2; r < b; r++ {
		var x bool
		if neg {
			x = !data.less(&s[a], &s[r])
		} else {
			x = data.less(&s[r], &s[a])
		}
		s[gap] = s[lt]
		s[lt] = s[r]
		gap = r
		if x {
			lt++
		}
	}
	s[gap] = s[lt]
	var x bool
	if neg {
		x = !data.less(&s[a], &held)
	} else {
		x = data.less(&held, &s[a])
	}
	s[lt] = held
	if x {
		lt++
	}
	data.Swap(a, lt-1)
	return lt - 1
}

func mkInsertionSort[T any](data mkSortSlice[T], a, b int) {
	for i := a + 1; i < b; i++ {
		for j := i; j > a && data.Less(j, j-1); j-- {
			data.Swap(j, j-1)
		}
	}
}

// siftDown_func implements the heap property on data[lo:hi].
// first is an offset into the array where the root of the heap lies.
func mkSiftDown[T any](data mkSortSlice[T], lo, hi, first int) {
	root := lo
	for {
		child := 2*root + 1
		if child >= hi {
			break
		}
		if child+1 < hi && data.Less(first+child, first+child+1) {
			child++
		}
		if !data.Less(first+root, first+child) {
			return
		}
		data.Swap(first+root, first+child)
		root = child
	}
}

func mkHeapSort[T any](data mkSortSlice[T], a, b int) {
	first := a
	lo := 0
	hi := b - a

	// Build heap with greatest element at top.
	for i := (hi - 1) / 2; i >= 0; i-- {
		mkSiftDown(data, i, hi, first)
	}

	// Pop elements, largest first, into end of data.
	for i := hi - 1; i >= 0; i-- {
		data.Swap(first, first+i)
		mkSiftDown(data, lo, i, first)
	}
}

// pdqsort_func sorts data[a:b].
// The algorithm based on pattern-defeating quicksort(pdqsort), but without the optimizations from BlockQuicksort.
// pdqsort paper: https://arxiv.org/pdf/2106.05123.pdf
// C++ implementation: https://github.com/orlp/pdqsort
// Rust implementation: https://docs.rs/pdqsort/latest/pdqsort/
// limit is the number of allowed bad (very unbalanced) pivots before falling back to heapsort.
func mkPdqsort[T any](data mkSortSlice[T], a, b, limit int) {
	const maxInsertion = 12

	var (
		wasBalanced    = true // whether the last partitioning was reasonably balanced
		wasPartitioned = true // whether the slice was already partitioned
	)

	for {
		length := b - a

		// Mk: a small element's leaves go up to mkSmallMax, through the
		// networks.
		if data.small {
			if length <= mkSmallMax {
				mkSmall(data, a, b, 1)
				return
			}
		} else if length <= maxInsertion {
			mkInsertionSort(data, a, b)
			return
		}

		// Fall back to heapsort if too many bad choices were made.
		if limit == 0 {
			mkHeapSort(data, a, b)
			return
		}

		// If the last partitioning was imbalanced, we need to breaking patterns.
		if !wasBalanced {
			mkBreakPatterns(data, a, b)
			limit--
		}

		pivot, hint := mkChoosePivot(data, a, b)
		if hint == mkDecreasingHint {
			mkReverseRange(data, a, b)
			// The chosen pivot was pivot-a elements after the start of the array.
			// After reversing it is pivot-a elements before the end of the array.
			// The idea came from Rust's implementation.
			pivot = (b - 1) - (pivot - a)
			hint = mkIncreasingHint
		} else if hint == mkUnknownHint && data.small && length >= 64 {
			// Mk: a pseudo-median of spread samples (median3_rec) when
			// the ninther's sample read neither way; the hint stays the
			// ninther's.
			pivot = mkMedSpread(data, a, length)
		}

		// The slice is likely already sorted.
		if wasBalanced && wasPartitioned && hint == mkIncreasingHint {
			if mkPartialInsertionSort(data, a, b) {
				return
			}
		}

		// Probably the slice contains many duplicate elements, partition the slice into
		// elements equal to and elements greater than the pivot.
		if a > 0 && !data.Less(a-1, pivot) {
			if data.lomuto {
				// Mk: the elements not greater than the pivot go left of
				// it, and the next range starts after it.
				a = mkPartitionLomuto(data, a, b, pivot, true) + 1
				continue
			}
			mid := mkPartitionEqual(data, a, b, pivot)
			a = mid
			continue
		}

		mid, alreadyPartitioned := mkPartition(data, a, b, pivot, hint == mkIncreasingHint)
		wasPartitioned = alreadyPartitioned

		leftLen, rightLen := mid-a, b-mid
		balanceThreshold := length / 8
		if leftLen < rightLen {
			wasBalanced = leftLen >= balanceThreshold
			mkPdqsort(data, a, mid, limit)
			a = mid + 1
		} else {
			wasBalanced = rightLen >= balanceThreshold
			mkPdqsort(data, mid+1, b, limit)
			b = mid
		}
	}
}

// partition_func does one quicksort partition.
// Let p = data[pivot]
// Moves elements in data[a:b] around, so that data[i]<p and data[j]>=p for i<newpivot and j>newpivot.
// On return, data[newpivot] = p
//
// The Mk sort takes the Lomuto partition only when the pivot sample did not
// read increasing: an increasing range keeps Go's partition.
func mkPartition[T any](data mkSortSlice[T], a, b, pivot int, increasing bool) (newpivot int, alreadyPartitioned bool) {
	if data.lomuto && !increasing {
		return mkPartitionLomuto(data, a, b, pivot, false), false
	}
	data.Swap(a, pivot)
	i, j := a+1, b-1 // i and j are inclusive of the elements remaining to be partitioned

	for i <= j && data.Less(i, a) {
		i++
	}
	for i <= j && !data.Less(j, a) {
		j--
	}
	if i > j {
		data.Swap(j, a)
		return j, true
	}
	data.Swap(i, j)
	i++
	j--

	for {
		for i <= j && data.Less(i, a) {
			i++
		}
		for i <= j && !data.Less(j, a) {
			j--
		}
		if i > j {
			break
		}
		data.Swap(i, j)
		i++
		j--
	}
	data.Swap(j, a)
	return j, false
}

// partitionEqual_func partitions data[a:b] into elements equal to data[pivot] followed by elements greater than data[pivot].
// It assumed that data[a:b] does not contain elements smaller than the data[pivot].
func mkPartitionEqual[T any](data mkSortSlice[T], a, b, pivot int) (newpivot int) {
	data.Swap(a, pivot)
	i, j := a+1, b-1 // i and j are inclusive of the elements remaining to be partitioned

	for {
		for i <= j && !data.Less(a, i) {
			i++
		}
		for i <= j && data.Less(a, j) {
			j--
		}
		if i > j {
			break
		}
		data.Swap(i, j)
		i++
		j--
	}
	return i
}

// partialInsertionSort_func partially sorts a slice, returns true if the slice is sorted at the end.
func mkPartialInsertionSort[T any](data mkSortSlice[T], a, b int) bool {
	const (
		maxSteps         = 5  // maximum number of adjacent out-of-order pairs that will get shifted
		shortestShifting = 50 // don't shift any elements on short arrays
	)
	i := a + 1
	for j := 0; j < maxSteps; j++ {
		for i < b && !data.Less(i, i-1) {
			i++
		}

		if i == b {
			return true
		}

		if b-a < shortestShifting {
			return false
		}

		data.Swap(i, i-1)

		// Shift the smaller one to the left.
		if i-a >= 2 {
			for j := i - 1; j >= 1; j-- {
				if !data.Less(j, j-1) {
					break
				}
				data.Swap(j, j-1)
			}
		}
		// Shift the greater one to the right.
		if b-i >= 2 {
			for j := i + 1; j < b; j++ {
				if !data.Less(j, j-1) {
					break
				}
				data.Swap(j, j-1)
			}
		}
	}
	return false
}

// breakPatterns_func scatters some elements around in an attempt to break some patterns
// that might cause imbalanced partitions in quicksort.
func mkBreakPatterns[T any](data mkSortSlice[T], a, b int) {
	length := b - a
	if length >= 8 {
		random := mkXorshift(length)
		modulus := mkNextPowerOfTwo(length)

		for idx := a + (length/4)*2 - 1; idx <= a+(length/4)*2+1; idx++ {
			other := int(uint(random.Next()) & (modulus - 1))
			if other >= length {
				other -= length
			}
			data.Swap(idx, a+other)
		}
	}
}

// choosePivot_func chooses a pivot in data[a:b].
//
// [0,8): chooses a static pivot.
// [8,shortestNinther): uses the simple median-of-three method.
// [shortestNinther,∞): uses the Tukey ninther method.
func mkChoosePivot[T any](data mkSortSlice[T], a, b int) (pivot int, hint mkSortedHint) {
	const (
		shortestNinther = 50
		maxSwaps        = 4 * 3
	)

	l := b - a

	var (
		swaps int
		i     = a + l/4*1
		j     = a + l/4*2
		k     = a + l/4*3
	)

	if l >= 8 {
		if l >= shortestNinther {
			// Tukey ninther method, the idea came from Rust's implementation.
			i = mkMedianAdjacent(data, i, &swaps)
			j = mkMedianAdjacent(data, j, &swaps)
			k = mkMedianAdjacent(data, k, &swaps)
		}
		// Find the median among i, j, k and stores it into j.
		j = mkMedian(data, i, j, k, &swaps)
	}

	switch swaps {
	case 0:
		return j, mkIncreasingHint
	case maxSwaps:
		return j, mkDecreasingHint
	default:
		return j, mkUnknownHint
	}
}

// order2_func returns x,y where data[x] <= data[y], where x,y=a,b or x,y=b,a.
func mkOrder2[T any](data mkSortSlice[T], a, b int, swaps *int) (int, int) {
	if data.Less(b, a) {
		*swaps++
		return b, a
	}
	return a, b
}

// median_func returns x where data[x] is the median of data[a],data[b],data[c], where x is a, b, or c.
func mkMedian[T any](data mkSortSlice[T], a, b, c int, swaps *int) int {
	a, b = mkOrder2(data, a, b, swaps)
	b, c = mkOrder2(data, b, c, swaps)
	a, b = mkOrder2(data, a, b, swaps)
	return b
}

// medianAdjacent_func finds the median of data[a - 1], data[a], data[a + 1] and stores the index into a.
func mkMedianAdjacent[T any](data mkSortSlice[T], a int, swaps *int) int {
	return mkMedian(data, a-1, a, a+1, swaps)
}

func mkReverseRange[T any](data mkSortSlice[T], a, b int) {
	i := a
	j := b - 1
	for i < j {
		data.Swap(i, j)
		i++
		j--
	}
}
