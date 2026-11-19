// Why: the orderbook row is only a fair test if its feed exercises every
// command kind and refusal.
// What: TestGenOrderfeed, a test of the "orderfeed" gen kind.
// How: generates one feed and checks its size, format, mix and stamp.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenOrderfeed checks the "orderfeed" kind: one
// spec gives one file, at least spec.Bytes long, in the dogfood's feed
// format, with every command kind, prices with no, one and two decimals,
// the refusals the generator aims at, LF line ends and no quote; and its
// stamp names the spec.
func TestGenOrderfeed(t *testing.T) {
	dir := t.TempDir()
	spec := dataSpec{Kind: "orderfeed", File: "f.csv", Bytes: 1 << 20, Seed: 11}
	p, err := genData(filepath.Join(dir, "a"), spec)
	if err != nil {
		t.Fatal(err)
	}
	q, err := genData(filepath.Join(dir, "b"), spec)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(p)
	b, _ := os.ReadFile(q)
	if !bytes.Equal(a, b) || int64(len(a)) < spec.Bytes || int64(len(a)) > spec.Bytes+200 {
		t.Fatalf("two feeds of one spec: %d and %d bytes, equal %v", len(a), len(b), bytes.Equal(a, b))
	}
	if bytes.ContainsAny(a, "\"\r") {
		t.Fatal("the feed holds a quote or a CR")
	}
	if st, _ := os.ReadFile(p + ".spec"); string(st) != "orderfeed 1048576 11\n" {
		t.Fatalf("stamp %q", st)
	}
	want := map[string]int{"N": 6, "M": 5, "S": 6, "T": 7, "C": 3, "A": 5, "H": 3, "R": 3, "X": 2}
	kinds := map[string]int{}
	var comments, zeroQty, neverIssued, sweeps, dec0, dec1, dec2 int
	lines := strings.Split(strings.TrimSuffix(string(a), "\n"), "\n")
	if lines[0] != "# orderfeed seed 11" || !strings.HasPrefix(lines[1], "X,") || !strings.HasPrefix(lines[2], "M,") {
		t.Fatalf("the feed does not open with its comment, a bust and a market order: %q", lines[:3])
	}
	for i, l := range lines {
		if strings.HasPrefix(l, "#") {
			comments++
			continue
		}
		f := strings.Split(l, ",")
		n, ok := want[f[0]]
		if !ok || len(f) != n {
			t.Fatalf("line %d: %q is not a feed command", i+1, l)
		}
		kinds[f[0]]++
		price := ""
		switch f[0] {
		case "N", "S", "T":
			price = f[5]
			if f[4] == "0" {
				zeroQty++
			}
			if f[0] == "N" && (f[5] == "0" || f[5] == "0.0" || f[5] == "0.00") {
				zeroQty++
			}
		case "M":
			if len(f[4]) >= 4 && f[4] >= "5000" {
				sweeps++
			}
		case "A":
			price = f[4]
			if f[3] == "0" {
				zeroQty++
			}
		case "C":
			if _, serial, _ := strings.Cut(f[2], "/"); len(serial) >= 7 {
				neverIssued++
			}
		}
		if price != "" {
			_, frac, dot := strings.Cut(price, ".")
			switch {
			case !dot:
				dec0++
			case len(frac) == 1:
				dec1++
			case len(frac) == 2:
				dec2++
			default:
				t.Fatalf("line %d: price %q", i+1, price)
			}
		}
	}
	for k := range want {
		if kinds[k] == 0 {
			t.Errorf("no %s command in %d lines", k, len(lines))
		}
	}
	if comments < 2 || zeroQty == 0 || neverIssued == 0 || sweeps == 0 || dec0 == 0 || dec1 == 0 || dec2 == 0 {
		t.Fatalf("comments %d, zero quantities or prices %d, never-issued cancels %d, sweeps %d, prices with 0, 1 and 2 decimals %d %d %d",
			comments, zeroQty, neverIssued, sweeps, dec0, dec1, dec2)
	}
	// Another seed gives another feed.
	spec.Seed = 12
	r, err := genData(filepath.Join(dir, "a"), spec)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := os.ReadFile(r); bytes.Equal(c, a) {
		t.Fatal("seed 12 wrote seed 11's feed")
	}
}
