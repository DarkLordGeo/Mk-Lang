// dispatchalt, Go, same algorithm: ../README.md's loop of n dynamic calls
// through a two-element array of {context pointer, func value} pairs, the
// C column's layout, over an Add and a Xor, the target alternating (i & 1),
// so the indirect branch is predicted. Safe Go cannot erase the context's type without
// "unsafe" (banned in this table), so both objects are one Acc type and the
// func value is what differs. A Go func value points to a closure record,
// so the call loads the code pointer from it: one dependent load more than
// C, which Go cannot avoid. uint64, wrapping. Built with GOAMD64=v2
// -pgo=off. Usage: dispatchalt <n> <seed>. Prints the checksum.
package main

import (
	"fmt"
	"os"
	"strconv"
)

type Acc struct{ acc uint64 }

type Step struct {
	ctx  *Acc
	step func(*Acc, uint64) uint64
}

func addStep(a *Acc, x uint64) uint64 {
	a.acc += x
	return a.acc
}

func xorStep(o *Acc, x uint64) uint64 {
	o.acc = (o.acc ^ x) * 0x100000001b3
	return o.acc
}

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func run(n int, seed uint64) uint64 {
	a := &Acc{acc: 1}
	x := &Acc{acc: 0xcbf29ce484222325}
	objs := [2]Step{{a, addStep}, {x, xorStep}}
	st := seed
	var sum uint64
	for i := 0; i < n; i++ {
		st += 0x9e3779b97f4a7c15
		z := mix(st)
		s := &objs[i&1]
		sum += s.step(s.ctx, z>>1)
	}
	return sum
}

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	seed, _ := strconv.ParseUint(os.Args[2], 10, 64)
	fmt.Println(run(n, seed))
}
