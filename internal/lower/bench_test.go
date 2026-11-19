// Why: lowering runs on every body of every build, so its cost on the
// shapes that expand most (matches, defers, copy chains) is watched.
// What: BenchmarkLowerPlain, Match, Defers, CopyChain and ManyBodies.
// How: each builds and checks its input once, outside the timer, then times
// Lower alone (FromCheck included, Validate off) and reports the MIR
// statements and terminators emitted per body as stmts/op. Nothing here is a
// measurement until it is run on a named machine and recorded with the
// command.

package lower

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/check"
	"mk/internal/mir"
)

// benchUnit checks src as module m and returns the checked unit named key.
func benchUnit(b testing.TB, src, key string) (*Config, *check.Checker, *check.Body) {
	b.Helper()
	w := newWorld(b, true)
	m := w.add("m", opt{}, src)
	if ds := m.diags(); len(ds) > 0 {
		b.Fatalf("diagnostics:\n%s", dump(w, ds))
	}
	cfg := w.lowerCfg()
	cfg.Validate = false
	for _, id := range m.m.Units() {
		if m.key(id) != key {
			continue
		}
		body := w.c.Check(m.m, id)
		if len(body.Diags) > 0 {
			b.Fatalf("check diagnostics:\n%s", dump(w, body.Diags))
		}
		return cfg, w.c, body
	}
	b.Fatalf("no unit %s", key)
	return nil, nil, nil
}

func stmtCount(mb *mir.Body) int {
	n := 0
	for _, blk := range mb.Blocks {
		n += len(blk.Stmts) + 1
	}
	return n
}

func benchLower(b *testing.B, src, key string) {
	cfg, c, body := benchUnit(b, src, key)
	stmts := 0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stmts = stmtCount(Lower(cfg, FromCheck(cfg, c, body)))
	}
	b.ReportMetric(float64(stmts), "stmts/op")
}

// BenchmarkLowerPlain: a 10,000-line function of assignments, arithmetic
// and calls.
func BenchmarkLowerPlain(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("H(n: int): int {\n\treturn n\n}\nF(a: int, b: int): int {\n\tvar v := a\n")
	for i := 0; i < 10000; i++ {
		if i%10 == 9 {
			sb.WriteString("\tv = H(v)\n")
		} else {
			fmt.Fprintf(&sb, "\tv = v * %d + b\n", i%7+1)
		}
	}
	sb.WriteString("\treturn v\n}\n")
	benchLower(b, sb.String(), "F")
}

// BenchmarkLowerMatch: a 200-arm match over a nested enum (?E with
// payloads), through the decision tree.
func BenchmarkLowerMatch(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("enum E {\n")
	for k := 0; k < 20; k++ {
		fmt.Fprintf(&sb, "\tV%d(n: int)\n", k)
	}
	sb.WriteString("}\nF(o: ?E): int {\n\tmatch o {\n")
	for i := 0; i < 199; i++ {
		fmt.Fprintf(&sb, "\t\tsome(.V%d(n: %d)) {\n\t\t\treturn %d\n\t\t}\n", i%20, i/20, i)
	}
	sb.WriteString("\t\t_ {\n\t\t\treturn -1\n\t\t}\n\t}\n}\n")
	benchLower(b, sb.String(), "F")
}

// BenchmarkLowerDefers: 20 defers and 100 exits, the cleanup expansion's
// worst case (one copy per exit edge).
func BenchmarkLowerDefers(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("Log(n: int) {\n}\nF(a: int): int {\n")
	for k := 0; k < 20; k++ {
		fmt.Fprintf(&sb, "\tdefer Log(%d)\n", k)
	}
	for k := 0; k < 100; k++ {
		fmt.Fprintf(&sb, "\tif a == %d {\n\t\treturn %d\n\t}\n", k, k)
	}
	sb.WriteString("\treturn a\n}\n")
	benchLower(b, sb.String(), "F")
}

// BenchmarkLowerCopyChain: a copy chain in a loop, sized
// to about 64,000 MIR operations; its output feeds borrow's benchmark.
func BenchmarkLowerCopyChain(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("F(xs: []int): int {\n\tvar s := 0\n\tfor x in xs {\n\t\tv0 := x\n")
	const n = 16000
	for i := 1; i < n; i++ {
		fmt.Fprintf(&sb, "\t\tv%d := v%d\n", i, i-1)
	}
	fmt.Fprintf(&sb, "\t\ts += v%d\n\t}\n\treturn s\n}\n", n-1)
	benchLower(b, sb.String(), "F")
}

// BenchmarkLowerManyBodies: one small body of a file holding 2,000 others.
// The per-body cost should not grow with the file: it shows whether a side
// table is sized by the file's NodeCount rather than by the body.
func BenchmarkLowerManyBodies(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&sb, "F%d(a: int, b: int): int {\n\tv := a * %d + b\n\treturn v\n}\n", i, i%7+1)
	}
	benchLower(b, sb.String(), "F1000")
}
