// Why: check's cost per stage must be visible apart from the rest of the
// compiler.
// What: Go benchmarks of plain and parallel checking, signatures, facts and
// exhaustiveness, and TestBenchInputsClean for their inputs.
// How: generates inputs of chosen (not measured) sizes; a recorded number
// names its command and machine.

package check

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"mk/internal/types"
)

// plainModule is n copies of a 20-line block of plain code: a struct, a
// function with a loop, a construction, field reads, a branch and a call.
func plainModule(n int) (string, int) {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `struct P%d {
	x: int
	y: int
}

F%d(a: int, b: int): int {
	var s := 0
	for i in 0..a {
		s += i * b
	}
	p := P%d(x: a, y: b)
	if p.x > p.y {
		return s + G%d(p.x)
	}
	return s - p.y
}

G%d(x: int): int {
	return x * 2 + 1
}
`, i, i, i, i, i)
	}
	src := b.String()
	return src, strings.Count(src, "\n")
}

// resolved parses and resolves a module without running stage 5.
func resolved(b *testing.B, w *world, path, src string) *built {
	b.Helper()
	return w.add(path, opt{stopBefore: true}, src)
}

// checkModule runs Signatures and then every unit with procs workers.
func checkModule(w *world, m *built, procs int) {
	m.m = w.c.Signatures(&ModuleInput{Module: m.id, Files: m.files, Info: m.info})
	units := m.m.Units()
	var wg sync.WaitGroup
	sem := make(chan struct{}, procs)
	for _, id := range units {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.c.Check(m.m, id)
		}()
	}
	wg.Wait()
}

// BenchmarkCheckPlain: 20,000 lines of plain functions, parsed and resolved
// outside the timer; reports lines per second on one core.
func BenchmarkCheckPlain(b *testing.B) {
	src, lines := plainModule(1000)
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	var total time.Duration
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		w := newWorld(b, true)
		m := resolved(b, w, "m", src)
		b.StartTimer()
		start := time.Now()
		checkModule(w, m, 1)
		total += time.Since(start)
	}
	b.ReportMetric(float64(lines)*float64(b.N)/total.Seconds(), "lines/s")
}

// BenchmarkCheckParallel: 8 such modules (2,000 lines each, so a run stays
// short), with GOMAXPROCS 1 and 8; the scaling factor is the ratio of the
// two lines/s figures.
func BenchmarkCheckParallel(b *testing.B) {
	src, lines := plainModule(100)
	for _, procs := range []int{1, 8} {
		b.Run(fmt.Sprintf("procs=%d", procs), func(b *testing.B) {
			old := runtime.GOMAXPROCS(procs)
			defer runtime.GOMAXPROCS(old)
			var total time.Duration
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				w := newWorld(b, true)
				var ms []*built
				for k := 0; k < 8; k++ {
					ms = append(ms, resolved(b, w, fmt.Sprintf("m%d", k), src))
				}
				b.StartTimer()
				start := time.Now()
				var wg sync.WaitGroup
				for _, m := range ms {
					wg.Add(1)
					go func() {
						defer wg.Done()
						checkModule(w, m, procs)
					}()
				}
				wg.Wait()
				total += time.Since(start)
			}
			b.ReportMetric(float64(8*lines)*float64(b.N)/total.Seconds(), "lines/s")
		})
	}
}

// BenchmarkSignatures: 2,000 structs and 5,000 signatures with flows;
// reports items per second.
func BenchmarkSignatures(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&sb, "struct S%d {\n\ta: int\n\tb: str\n}\n\n", i)
	}
	for i := 0; i < 5000; i++ {
		k := i % 2000
		fmt.Fprintf(&sb, "H%d(s: &S%d, t: &S%d): str from s {\n\treturn s.b\n}\n\n", i, k, k)
	}
	src := sb.String()
	var total time.Duration
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		w := newWorld(b, true)
		m := resolved(b, w, "m", src)
		b.StartTimer()
		start := time.Now()
		m.m = w.c.Signatures(&ModuleInput{Module: m.id, Files: m.files, Info: m.info})
		total += time.Since(start)
		if len(m.m.Diags) != 0 {
			b.Fatalf("diagnostics: %s", dump(w, m.m.Diags))
		}
	}
	b.ReportMetric(7000*float64(b.N)/total.Seconds(), "items/s")
}

// BenchmarkFacts: Facts over 100,000 distinct instantiated types, cold
// (first ask) and warm (memoized); reports ns per type.
func BenchmarkFacts(b *testing.B) {
	const n = 100000
	for _, warm := range []bool{false, true} {
		name := "cold"
		if warm {
			name = "warm"
		}
		b.Run(name, func(b *testing.B) {
			var total time.Duration
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				w := newWorld(b, true)
				m := w.add("m", opt{}, "struct Box2[T] {\n\tv: T\n\tw: ?T\n}\n")
				box := m.item("Box2")
				ts := make([]types.TypeID, n)
				for k := range ts {
					arr := w.tab.Array(types.ConstArg{Kind: types.CAValue, Value: w.c.intConst(int64(k)), Type: types.Int}, types.U8)
					ts[k] = w.tab.Named(box, []types.GenericArg{types.TypeArg(arr)})
				}
				if warm {
					for _, t := range ts {
						w.c.Facts(t)
					}
				}
				b.StartTimer()
				start := time.Now()
				for _, t := range ts {
					w.c.Facts(t)
				}
				total += time.Since(start)
			}
			b.ReportMetric(float64(total.Nanoseconds())/float64(n*b.N), "ns/type")
		})
	}
}

// BenchmarkExhaustive: a match over a record of three 16-variant enum
// fields whose arms cover the first field; reports ns per match checked
// (the whole unit, typing included).
func BenchmarkExhaustive(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("enum E {\n")
	for v := 0; v < 16; v++ {
		fmt.Fprintf(&sb, "\tV%d\n", v)
	}
	sb.WriteString("}\n\nstruct S3 {\n\tf0: E\n\tf1: E\n\tf2: E\n}\n\nF(s: S3): int {\n\tmatch s {\n")
	for v := 0; v < 16; v++ {
		fmt.Fprintf(&sb, "\t\tS3(f0: .V%d, f1: _, f2: _) {\n\t\t\treturn %d\n\t\t}\n", v, v)
	}
	sb.WriteString("\t}\n\treturn 0\n}\n")
	w := newWorld(b, true)
	m := w.add("m", opt{}, sb.String())
	f := m.item("F")
	if body := w.c.Check(m.m, f); len(body.Diags) != 0 {
		b.Fatalf("diagnostics: %s", dump(w, body.Diags))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.c.Check(m.m, f)
	}
}

// TestBenchInputsClean: the benchmark inputs draw no diagnostic, so the
// benchmarks measure the accepting path.
func TestBenchInputsClean(t *testing.T) {
	src, _ := plainModule(3)
	checkBody(t, newWorld(t, true), "", src)
}
