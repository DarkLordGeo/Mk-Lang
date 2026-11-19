// Why: a compiler change needs a quick answer to "did this make the Mk
// programs faster or slower", without a full published run.
//
// What: `tools/bench compare --base <old mk> --head <new mk> <rows>`: the Mk
// columns of each row built with both compilers, the checksums checked
// equal, and the paired ratio head/base reported with its bootstrap
// interval.
//
// How: A and B run alternately in one invocation (ABBA order, so neither
// side always runs first). The result is marked provisional: it holds no
// lock and the machine is shared, so it is a relative number for work in
// progress, never a published one.

package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

type compareOptions struct {
	Root  string
	Work  string
	Base  string
	Head  string
	Rows  []string
	Pairs int    // A/B pairs per cell
	Cols  string // comma-separated Mk columns; default "mk,mk-wrap"
	Quick bool   // use each row's quick arguments
	Log   func(string)
	Out   io.Writer
}

// compareCell is one row and column of a comparison.
type compareCell struct {
	Row, Col   string
	Base, Head []float64
	Ratios     []float64 // head / base, pair by pair
}

func compare(o compareOptions) ([]compareCell, error) {
	all, err := discover(o.Root)
	if err != nil {
		return nil, err
	}
	logBroken(all, o.Log)
	rows, err := selectRows(all, "", o.Rows)
	if err == nil {
		err = prepareRows(o.Root, rows)
	}
	if err != nil {
		return nil, err
	}
	if s := strayProfiles(o.Root); len(s) > 0 {
		return nil, fmt.Errorf("refusing to run: a PGO profile is under the benchmark trees: %s", strings.Join(s, ", "))
	}
	want := strings.Split(o.Cols, ",")
	only := func(c column) bool { return slices.Contains(want, c.ID) }
	tb, err := findToolchain(o.Work, o.Base)
	if err != nil {
		return nil, err
	}
	th := *tb
	th.Mk = o.Head
	bb := buildAll(tb, "base", rows, only, o.Log)
	hb := buildAll(&th, "head", rows, only, o.Log)
	var out []compareCell
	for i := range bb {
		b, h := bb[i], hb[i]
		if b.Err != nil || h.Err != nil {
			return nil, fmt.Errorf("%s %s: build failed: base %v, head %v", b.Row.id(), b.Col.ID, b.Err, h.Err)
		}
		args, want := b.Row.runArgs, b.Row.Checksum
		if o.Quick {
			args, want = b.Row.runQuick, b.Row.QuickSum
		}
		checks, agreed, ok := verify([]*built{b, h}, args, want)
		if !ok {
			return nil, fmt.Errorf("%s %s: checksums base %q head %q, expected %q (%v %v)", b.Row.id(), b.Col.ID,
				checks[0].Sum, checks[1].Sum, agreed, checks[0].Err, checks[1].Err)
		}
		cc := compareCell{Row: b.Row.id(), Col: b.Col.ID}
		for p := range o.Pairs {
			order := []*built{b, h}
			if p%2 == 1 {
				order = []*built{h, b}
			}
			var t [2]float64
			for k, x := range order {
				// The row's environment (a row's threads, an app row's env) and
				// an app row's fresh output file, as publish runs it.
				run, err := timedRun(x, args) // app.go: deletes the output first
				if err != nil {
					return nil, err
				}
				_, d, err := runOnceEnv(x.Exe, run.Args, run.Env)
				if err != nil {
					return nil, err
				}
				t[k] = d.Seconds()
			}
			if p%2 == 1 {
				t[0], t[1] = t[1], t[0]
			}
			cc.Base, cc.Head = append(cc.Base, t[0]), append(cc.Head, t[1])
			cc.Ratios = append(cc.Ratios, t[1]/t[0])
		}
		out = append(out, cc)
	}
	return out, nil
}

// renderCompare is the provisional report.
func renderCompare(w io.Writer, cs []compareCell, base, head string) {
	fmt.Fprintf(w, "PROVISIONAL (tools/bench compare: no machine lock, shared machine; relative only, never published)\n")
	fmt.Fprintf(w, "base %s\nhead %s\n\n", base, head)
	fmt.Fprintf(w, "%-24s %-8s %10s %10s %8s %18s %s\n", "row", "column", "base med", "head med", "ratio", "95% interval", "pairs")
	for _, c := range cs {
		ci := pairedCI(c.Ratios)
		verdict := "no change shown"
		switch {
		case ci.Hi < 1:
			verdict = "head faster"
		case ci.Lo > 1:
			verdict = "head slower"
		}
		fmt.Fprintf(w, "%-24s %-8s %10s %10s %8.3f   [%.3f, %.3f] %d  %s\n", c.Row, c.Col, fmtSec(median(c.Base)), fmtSec(median(c.Head)),
			median(c.Ratios), ci.Lo, ci.Hi, len(c.Ratios), verdict)
	}
}
