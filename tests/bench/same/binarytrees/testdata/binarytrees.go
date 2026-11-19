// binarytrees, Go: the reference algorithm of ../README.md (the Benchmarks
// Game's binary-trees, with the row's bump allocator). Nodes are node{left, right
// uint32} in one []node from make, allocated by a uint32 bump index; index 0
// is the null node. No new per node, so the GC has nothing to collect.
// nodes[idx] is bounds checked; integers wrap. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"strconv"
)

type node struct {
	left, right uint32
}

type pool struct {
	nodes []node
	top   uint32
}

func build(p *pool, depth int) uint32 {
	idx := p.top
	p.top += 1
	if depth > 0 {
		l := build(p, depth-1)
		r := build(p, depth-1)
		p.nodes[idx] = node{l, r}
	} else {
		p.nodes[idx] = node{0, 0}
	}
	return idx
}

func check(nodes []node, idx uint32) int {
	n := nodes[idx]
	if n.left == 0 {
		return 1
	}
	return 1 + check(nodes, n.left) + check(nodes, n.right)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: binarytrees <n>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	minDepth := 4
	maxDepth := n
	if minDepth+2 > maxDepth {
		maxDepth = minDepth + 2
	}
	p := &pool{nodes: make([]node, (1<<(maxDepth+2))+1), top: 1}
	total := check(p.nodes, build(p, maxDepth+1))
	p.top = 1
	longLived := build(p, maxDepth)
	mark := p.top
	for d := minDepth; d <= maxDepth; d += 2 {
		iterations := 1 << (maxDepth - d + minDepth)
		for i := 0; i < iterations; i++ {
			p.top = mark
			total += check(p.nodes, build(p, d))
		}
	}
	total += check(p.nodes, longLived)
	fmt.Println(total)
}
