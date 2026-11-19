// Why: resolve runs on every module of every build, so its cost per name and
// its scaling with workers are watched.
// What: benchmarks of plain modules, many locals, lookup misses, parallel
// Finish, pending generated names and qualified lookups.
// How: each module is parsed once and resolved anew per iteration.

package resolve

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// plainModule is a generated module of plain functions, about lines long.
func plainModule(lines int) string {
	var b strings.Builder
	b.WriteString("struct Point {\n    x: int\n    y: int\n}\n\n")
	for i := 0; b.Len() < lines*24; i++ {
		fmt.Fprintf(&b, "F%d(p: Point, n: int): int {\n    var total := 0\n    for i in 0..n {\n        if i > p.x {\n            total = total + i * p.y\n        }\n    }\n    q := Point(x: total, y: n)\n    return q.x + G%d(q)\n}\n\nG%d(p: Point): int { return p.x - p.y }\n\n", i, i, i)
	}
	return b.String()
}

// benchModule parses text once and returns a function resolving it anew.
func benchModule(b *testing.B, text string) (func(workers int), int) {
	b.Helper()
	sm := source.NewSourceMap()
	in := source.NewInterner()
	src := sm.Add("bench.mk", []byte(text))
	f, ds := syntax.ParseFile(src, in, syntax.Options{})
	if len(ds) > 0 {
		b.Fatalf("parse: %v %s", ds[0].Code, ds[0].Message)
	}
	lines := strings.Count(text, "\n")
	return func(workers int) {
		items := types.NewItems()
		s := NewSession(&Config{Interner: in, Sources: sm, Items: items})
		id := items.AddModule(&types.Module{Path: "main", Files: []source.FileID{src.ID}})
		m := s.Begin(&ModuleInput{Module: id, Files: []*ast.File{f}})
		info := m.Finish(workers)
		m.Settle()
		if len(info.Diags) > 0 {
			b.Fatalf("diagnostics: %v %s", info.Diags[0].Code, info.Diags[0].Message)
		}
	}, lines
}

func BenchmarkResolve_PlainModule(b *testing.B) {
	run, lines := benchModule(b, plainModule(10000))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		run(1)
	}
	b.ReportMetric(float64(lines)*float64(b.N)/b.Elapsed().Seconds(), "lines/s")
}

func BenchmarkResolve_ManyLocals(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("F(n: int): int {\n    var acc := 0\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&sb, "    v%d := n + %d\n    if v%d > acc {\n        acc = v%d\n    }\n", i, i, i, i)
	}
	sb.WriteString("    return acc\n}\n")
	run, _ := benchModule(b, sb.String())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		run(1)
	}
}

func BenchmarkLookup_Miss(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("struct MaxUsers {}\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "F%d(x: maxUsers) { }\n", i)
	}
	text := sb.String()
	sm := source.NewSourceMap()
	in := source.NewInterner()
	src := sm.Add("miss.mk", []byte(text))
	f, _ := syntax.ParseFile(src, in, syntax.Options{})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		items := types.NewItems()
		s := NewSession(&Config{Interner: in, Sources: sm, Items: items})
		id := items.AddModule(&types.Module{Path: "main", Files: []source.FileID{src.ID}})
		m := s.Begin(&ModuleInput{Module: id, Files: []*ast.File{f}})
		if info := m.Finish(1); len(info.Diags) != 200 {
			b.Fatalf("%d diagnostics", len(info.Diags))
		}
	}
}

func BenchmarkFinish_Parallel(b *testing.B) {
	run, lines := benchModule(b, plainModule(10000))
	for _, workers := range []int{1, 8} {
		b.Run(fmt.Sprint("workers=", workers), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				run(workers)
			}
			b.ReportMetric(float64(lines)*float64(b.N)/b.Elapsed().Seconds(), "lines/s")
		})
	}
}

// pendingModule is one function with 300 locals, then uses bindings of a
// type named Thing: handwritten, or generated in a module with a gen line,
// where every use misses in Finish and is pending.
func pendingModule(gen bool) string {
	var sb strings.Builder
	if gen {
		sb.WriteString("import std.derive\n\nstruct User {}\ngen derive.Eq(User)\n")
	} else {
		sb.WriteString("struct Thing {}\n")
	}
	sb.WriteString("F() {\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "    v%d := %d\n    _ = v%d\n", i, i, i)
	}
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&sb, "    t%d: Thing = Thing()\n    _ = t%d\n", i, i)
	}
	sb.WriteString("}\n")
	return sb.String()
}

// BenchmarkFinish_PendingGeneratedNames measures Finish(1) over
// pendingModule with Thing handwritten and with Thing generated.
func BenchmarkFinish_PendingGeneratedNames(b *testing.B) {
	for _, gen := range []bool{false, true} {
		b.Run(fmt.Sprint("generated=", gen), func(b *testing.B) {
			sm := source.NewSourceMap()
			in := source.NewInterner()
			dsrc := sm.Add("derive.mk", []byte("export Eq(g: int)\n"))
			df, _ := syntax.ParseFile(dsrc, in, syntax.Options{})
			src := sm.Add("main.mk", []byte(pendingModule(gen)))
			f, ds := syntax.ParseFile(src, in, syntax.Options{})
			if len(ds) > 0 {
				b.Fatalf("parse: %v %s", ds[0].Code, ds[0].Message)
			}
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				items := types.NewItems()
				s := NewSession(&Config{Interner: in, Sources: sm, Items: items})
				did := items.AddModule(&types.Module{Path: "std.derive", Std: true, Files: []source.FileID{dsrc.ID}})
				dm := s.Begin(&ModuleInput{Module: did, Files: []*ast.File{df}})
				dm.Finish(1)
				dm.Settle()
				id := items.AddModule(&types.Module{Path: "main", Files: []source.FileID{src.ID}})
				m := s.Begin(&ModuleInput{Module: id, Files: []*ast.File{f}, Imports: map[string]types.ModuleID{"std.derive": did}})
				b.StartTimer()
				info := m.Finish(1)
				if !gen && len(info.Diags) > 0 {
					b.Fatalf("diagnostics: %v %s", info.Diags[0].Code, info.Diags[0].Message)
				}
			}
		})
	}
}

// BenchmarkFinish_Qualified measures Finish over 64 functions that each
// name lib.T 200 times, at 1 and 8 workers: qualified lookups read the
// imported record without the session lock.
func BenchmarkFinish_Qualified(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("import lib\n\n")
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&sb, "F%d() {\n", i)
		for j := 0; j < 200; j++ {
			fmt.Fprintf(&sb, "    x%d: lib.T = lib.Make()\n    _ = x%d\n", j, j)
		}
		sb.WriteString("}\n")
	}
	sm := source.NewSourceMap()
	in := source.NewInterner()
	lsrc := sm.Add("lib.mk", []byte("export struct T {}\nexport Make(): T { return T() }\n"))
	lf, _ := syntax.ParseFile(lsrc, in, syntax.Options{})
	src := sm.Add("main.mk", []byte(sb.String()))
	f, ds := syntax.ParseFile(src, in, syntax.Options{})
	if len(ds) > 0 {
		b.Fatalf("parse: %v %s", ds[0].Code, ds[0].Message)
	}
	for _, workers := range []int{1, 8} {
		b.Run(fmt.Sprint("workers=", workers), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				items := types.NewItems()
				s := NewSession(&Config{Interner: in, Sources: sm, Items: items})
				lid := items.AddModule(&types.Module{Path: "lib", Files: []source.FileID{lsrc.ID}})
				lm := s.Begin(&ModuleInput{Module: lid, Files: []*ast.File{lf}})
				lm.Finish(1)
				lm.Settle()
				id := items.AddModule(&types.Module{Path: "main", Files: []source.FileID{src.ID}})
				m := s.Begin(&ModuleInput{Module: id, Files: []*ast.File{f}, Imports: map[string]types.ModuleID{"lib": lid}})
				b.StartTimer()
				if info := m.Finish(workers); len(info.Diags) > 0 {
					b.Fatalf("diagnostics: %v %s", info.Diags[0].Code, info.Diags[0].Message)
				}
			}
		})
	}
}
