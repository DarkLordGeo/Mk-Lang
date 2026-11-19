// sortints, Go: the reference algorithm of ../README.md.
// Go 1.27's pdqsortOrdered (slices/zsortordered.go) written out for []int64
// and <, not a call of slices.Sort, so every column runs the same text. Every
// s[i] is bounds checked (gc removes the checks it proves). Integers wrap in
// Go; overflow class "never". Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"strconv"
)

// tools/bench flips this line in a copy for the counting build.
const benchCount = false

var (
	nPdq    uint64
	nIns    uint64
	nShift  uint64
	nHeap   uint64
	nRev    uint64
	nPart   uint64
	nParteq uint64
	nPis    uint64
	nBrk    uint64
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func bitlen(n int) int {
	l := 0
	for x := uint64(n); x > 0; x >>= 1 {
		l++
	}
	return l
}

func insertionSort(s []int64, a, b int) {
	if benchCount {
		nIns++
	}
	for i := a + 1; i < b; i++ {
		for j := i; j > a && s[j] < s[j-1]; j-- {
			t := s[j]
			s[j] = s[j-1]
			s[j-1] = t
			if benchCount {
				nShift++
			}
		}
	}
}

func siftDown(s []int64, lo, hi, first int) {
	root := lo
	for {
		child := 2*root + 1
		if child >= hi {
			return
		}
		if child+1 < hi && s[first+child] < s[first+child+1] {
			child++
		}
		if !(s[first+root] < s[first+child]) {
			return
		}
		t := s[first+root]
		s[first+root] = s[first+child]
		s[first+child] = t
		root = child
	}
}

func heapSort(s []int64, a, b int) {
	if benchCount {
		nHeap++
	}
	first := a
	hi := b - a
	for i := (hi - 1) / 2; i >= 0; i-- {
		siftDown(s, i, hi, first)
	}
	for i := hi - 1; i >= 0; i-- {
		t := s[first]
		s[first] = s[first+i]
		s[first+i] = t
		siftDown(s, 0, i, first)
	}
}

func median(s []int64, a, b, c int, swaps *int) int {
	x, y, z := a, b, c
	if s[y] < s[x] {
		*swaps++
		x, y = y, x
	}
	if s[z] < s[y] {
		*swaps++
		y, z = z, y
	}
	if s[y] < s[x] {
		*swaps++
		x, y = y, x
	}
	_ = z
	return y
}

// choosePivot returns the pivot and the hint: 0 unknown, 1 increasing, 2 decreasing.
func choosePivot(s []int64, a, b int) (int, int) {
	l := b - a
	swaps := 0
	i := a + l/4*1
	j := a + l/4*2
	k := a + l/4*3
	if l >= 8 {
		if l >= 50 {
			i = median(s, i-1, i, i+1, &swaps)
			j = median(s, j-1, j, j+1, &swaps)
			k = median(s, k-1, k, k+1, &swaps)
		}
		j = median(s, i, j, k, &swaps)
	}
	switch swaps {
	case 0:
		return j, 1
	case 12:
		return j, 2
	}
	return j, 0
}

func reverseRange(s []int64, a, b int) {
	if benchCount {
		nRev++
	}
	i, j := a, b-1
	for i < j {
		t := s[i]
		s[i] = s[j]
		s[j] = t
		i++
		j--
	}
}

func partialInsertionSort(s []int64, a, b int) bool {
	if benchCount {
		nPis++
	}
	i := a + 1
	for step := 0; step < 5; step++ {
		for i < b && !(s[i] < s[i-1]) {
			i++
		}
		if i == b {
			return true
		}
		if b-a < 50 {
			return false
		}
		t := s[i]
		s[i] = s[i-1]
		s[i-1] = t
		if i-a >= 2 {
			for j := i - 1; j >= 1; j-- {
				if !(s[j] < s[j-1]) {
					break
				}
				t := s[j]
				s[j] = s[j-1]
				s[j-1] = t
			}
		}
		if b-i >= 2 {
			for j := i + 1; j < b; j++ {
				if !(s[j] < s[j-1]) {
					break
				}
				t := s[j]
				s[j] = s[j-1]
				s[j-1] = t
			}
		}
	}
	return false
}

func breakPatterns(s []int64, a, b int) {
	if benchCount {
		nBrk++
	}
	length := b - a
	if length < 8 {
		return
	}
	r := uint64(length)
	modulus := uint64(1) << bitlen(length)
	idx := a + (length/4)*2 - 1
	for i := 0; i < 3; i++ {
		r ^= r << 13
		r ^= r >> 7
		r ^= r << 17
		other := int(r & (modulus - 1))
		if other >= length {
			other -= length
		}
		t := s[idx+i]
		s[idx+i] = s[a+other]
		s[a+other] = t
	}
}

func partition(s []int64, a, b, pivot int) (int, bool) {
	if benchCount {
		nPart++
	}
	t := s[a]
	s[a] = s[pivot]
	s[pivot] = t
	i, j := a+1, b-1
	for i <= j && s[i] < s[a] {
		i++
	}
	for i <= j && !(s[j] < s[a]) {
		j--
	}
	if i > j {
		t := s[j]
		s[j] = s[a]
		s[a] = t
		return j, true
	}
	t = s[i]
	s[i] = s[j]
	s[j] = t
	i++
	j--
	for {
		for i <= j && s[i] < s[a] {
			i++
		}
		for i <= j && !(s[j] < s[a]) {
			j--
		}
		if i > j {
			break
		}
		t := s[i]
		s[i] = s[j]
		s[j] = t
		i++
		j--
	}
	t = s[j]
	s[j] = s[a]
	s[a] = t
	return j, false
}

func partitionEqual(s []int64, a, b, pivot int) int {
	if benchCount {
		nParteq++
	}
	t := s[a]
	s[a] = s[pivot]
	s[pivot] = t
	i, j := a+1, b-1
	for {
		for i <= j && !(s[a] < s[i]) {
			i++
		}
		for i <= j && s[a] < s[j] {
			j--
		}
		if i > j {
			break
		}
		t := s[i]
		s[i] = s[j]
		s[j] = t
		i++
		j--
	}
	return i
}

func pdqsort(s []int64, a, b, limit int) {
	balanced := true
	partitioned := true
	for {
		length := b - a
		if benchCount {
			nPdq++
		}
		if length <= 12 {
			insertionSort(s, a, b)
			return
		}
		if limit == 0 {
			heapSort(s, a, b)
			return
		}
		if !balanced {
			breakPatterns(s, a, b)
			limit--
		}
		pivot, hint := choosePivot(s, a, b)
		if hint == 2 {
			reverseRange(s, a, b)
			pivot = (b - 1) - (pivot - a)
			hint = 1
		}
		if balanced && partitioned && hint == 1 {
			if partialInsertionSort(s, a, b) {
				return
			}
		}
		if a > 0 && !(s[a-1] < s[pivot]) {
			a = partitionEqual(s, a, b, pivot)
			continue
		}
		mid, already := partition(s, a, b, pivot)
		partitioned = already
		left, right := mid-a, b-mid
		threshold := length / 8
		if left < right {
			balanced = left >= threshold
			pdqsort(s, a, mid, limit)
			a = mid + 1
		} else {
			balanced = right >= threshold
			pdqsort(s, mid+1, b, limit)
			b = mid
		}
	}
}

func check(s []int64) int64 {
	var sum int64
	for i := 0; i < len(s); i++ {
		if i > 0 && s[i-1] > s[i] {
			return -1
		}
		sum += s[i] * int64(i%1000)
	}
	return sum
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sortints <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	xs := make([]int64, n)
	var total int64
	for r := 0; r < rounds; r++ {
		for p := 0; p < 4; p++ {
			for i := 0; i < n; i++ {
				switch p {
				case 0:
					st += 0x9e3779b97f4a7c15
					xs[i] = int64(mix(st) & 0xFFFFF)
				case 1:
					xs[i] = int64(n - i)
				case 2:
					xs[i] = int64(i % 1000)
				default:
					st += 0x9e3779b97f4a7c15
					xs[i] = int64(mix(st) >> 60)
				}
			}
			pdqsort(xs, 0, n, bitlen(n))
			total += check(xs)
		}
	}
	fmt.Println(total)
	if benchCount {
		fmt.Printf("counts pdq=%d ins=%d shift=%d heap=%d rev=%d part=%d parteq=%d pis=%d brk=%d\n", nPdq, nIns, nShift, nHeap, nRev, nPart, nParteq, nPis, nBrk)
	}
}
