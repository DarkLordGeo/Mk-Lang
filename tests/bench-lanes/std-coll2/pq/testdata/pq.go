// pq, Go, container/heap over a []uint64 (a min-heap): the workload of
// ../README.md.
package main

import (
	"container/heap"
	"fmt"
	"os"
	"strconv"
)

type u64Heap []uint64

func (h u64Heap) Len() int           { return len(h) }
func (h u64Heap) Less(i, j int) bool { return h[i] < h[j] }
func (h u64Heap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *u64Heap) Push(x any)        { *h = append(*h, x.(uint64)) }
func (h *u64Heap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	seed, _ := strconv.ParseUint(os.Args[2], 10, 64)
	h := &u64Heap{}
	s := seed
	for i := 0; i < n; i++ {
		s += 0x9E3779B97F4A7C15
		heap.Push(h, mix(s))
	}
	var sum uint64
	for h.Len() > 0 {
		sum = sum*31 + heap.Pop(h).(uint64)
	}
	fmt.Println(sum)
}
