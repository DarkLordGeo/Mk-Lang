// Why: the front end has a speed budget (check of plain code at least 150
// KLOC/s per core, and at least 5x on 8 cores), and only a large program
// shows whether it holds.
// What: BenchmarkFrontEnd100K over the generated program of progen_test.go,
// and TestBenchProgramClean, which checks that program draws no diagnostic.
// How: each stage is timed on its own, in the driver's structure: parse over
// files, resolve and stage 5 per module with the modules of one import depth
// at once, stage 6 over every unit on one worker pool. The "check" row is
// stage 5 plus stage 6, with parse and resolve beside it.
//
// Command: go test -run '^$' -bench FrontEnd100K -benchtime 5x ./internal/check

package check

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// frontMod is one generated module through the stages.
type frontMod struct {
	gm    *genModule
	id    types.ModuleID
	srcs  []*source.File
	files []*ast.File
	info  *resolve.Info
	m     *Module
	diags []*diag.Diagnostic
	mu    sync.Mutex
}

// front is one run of the front end over a generated program.
type front struct {
	w     *world
	mods  []*frontMod
	byP   map[string]*frontMod
	depth [][]*frontMod
}

// newFront registers the program's files and modules in a fresh world (the
// fake prelude parsed, resolved and checked), outside any timer.
func newFront(tb testing.TB, p *genProgram) *front {
	f := &front{w: newWorld(tb, true), byP: map[string]*frontMod{}}
	for _, gm := range p.mods {
		fm := &frontMod{gm: gm}
		mod := &types.Module{Path: gm.path}
		for i, text := range gm.files {
			src := f.w.sm.Add(fmt.Sprintf("%s/%02d.mk", gm.path, i), []byte(text))
			fm.srcs = append(fm.srcs, src)
			mod.Files = append(mod.Files, src.ID)
		}
		fm.files = make([]*ast.File, len(fm.srcs))
		fm.id = f.w.items.AddModule(mod)
		f.mods = append(f.mods, fm)
		f.byP[gm.path] = fm
		for len(f.depth) <= gm.depth {
			f.depth = append(f.depth, nil)
		}
		f.depth[gm.depth] = append(f.depth[gm.depth], fm)
	}
	return f
}

// pool runs n jobs on procs workers and waits.
func pool(procs, n int, job func(i int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for k := 0; k < procs; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				job(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

// note keeps a module's diagnostics (safe for concurrent use).
func (fm *frontMod) note(ds []*diag.Diagnostic) {
	fm.mu.Lock()
	fm.diags = append(fm.diags, ds...)
	fm.mu.Unlock()
}

// parse parses every file of the program on procs workers.
func (f *front) parse(procs int) {
	type job struct {
		fm *frontMod
		i  int
	}
	var jobs []job
	for _, fm := range f.mods {
		for i := range fm.srcs {
			jobs = append(jobs, job{fm, i})
		}
	}
	pool(procs, len(jobs), func(k int) {
		j := jobs[k]
		file, ds := syntax.ParseFile(j.fm.srcs[j.i], f.w.in, syntax.Options{})
		j.fm.files[j.i] = file
		j.fm.note(ds)
	})
}

// resolve runs stage 3 depth by depth, the modules of a depth at once.
func (f *front) resolve(procs int) {
	for _, level := range f.depth {
		pool(procs, len(level), func(k int) {
			fm := level[k]
			in := &resolve.ModuleInput{Module: fm.id, Files: fm.files, Imports: map[string]types.ModuleID{}, Prelude: f.w.core}
			for _, imp := range fm.gm.imports {
				in.Imports[imp] = f.byP[imp].id
			}
			rm := f.w.rs.Begin(in)
			fm.info = rm.Finish(1)
			ds, gs := rm.Settle()
			fm.note(fm.info.Diags)
			fm.note(ds)
			fm.note(gs)
		})
	}
}

// signatures runs stage 5 depth by depth, the modules of a depth at once.
func (f *front) signatures(procs int) {
	for _, level := range f.depth {
		pool(procs, len(level), func(k int) {
			fm := level[k]
			fm.m = f.w.c.Signatures(&ModuleInput{Module: fm.id, Files: fm.files, Info: fm.info})
			fm.note(fm.m.Diags)
		})
	}
}

// bodies runs stage 6 over every unit of the program on one pool.
func (f *front) bodies(procs int) {
	type unit struct {
		fm *frontMod
		id types.ItemID
	}
	var units []unit
	for _, fm := range f.mods {
		for _, id := range fm.m.Units() {
			units = append(units, unit{fm, id})
		}
	}
	pool(procs, len(units), func(k int) {
		u := units[k]
		if body := f.w.c.Check(u.fm.m, u.id); len(body.Diags) > 0 {
			u.fm.note(body.Diags)
		}
	})
}

// clean fails when any stage left a diagnostic.
func (f *front) clean(tb testing.TB) {
	tb.Helper()
	for _, fm := range f.mods {
		if len(fm.diags) > 0 {
			n := min(len(fm.diags), 10)
			tb.Fatalf("%s: %d diagnostics, first %d:\n%s", fm.gm.path, len(fm.diags), n, dump(f.w, fm.diags[:n]))
		}
	}
}

// check runs stages 5 and 6 as the driver schedules them (no depth
// barrier): a module's stage 5 starts once its imports' stage
// 5 is done, its units join the pool once its own is, and a ready stage 5
// goes before any unit. sig and body sum the time of each kind of task.
func (f *front) check(procs int, sig, body *time.Duration) {
	type unit struct {
		fm *frontMod
		id types.ItemID
	}
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	waiting := map[*frontMod]int{}
	users := map[*frontMod][]*frontMod{}
	var sigQ []*frontMod
	var bodyQ []unit
	for _, fm := range f.mods {
		waiting[fm] = len(fm.gm.imports)
		for _, imp := range fm.gm.imports {
			users[f.byP[imp]] = append(users[f.byP[imp]], fm)
		}
		if len(fm.gm.imports) == 0 {
			sigQ = append(sigQ, fm)
		}
	}
	left := len(f.mods) // stage-5 tasks not done, plus queued or running units
	var wg sync.WaitGroup
	for k := 0; k < procs; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			for {
				for left > 0 && len(sigQ) == 0 && len(bodyQ) == 0 {
					cond.Wait()
				}
				if left == 0 {
					cond.Broadcast()
					return
				}
				if len(sigQ) > 0 {
					fm := sigQ[0]
					sigQ = sigQ[1:]
					mu.Unlock()
					t := time.Now()
					fm.m = f.w.c.Signatures(&ModuleInput{Module: fm.id, Files: fm.files, Info: fm.info})
					d := time.Since(t)
					fm.note(fm.m.Diags)
					units := fm.m.Units()
					mu.Lock()
					*sig += d
					for _, id := range units {
						bodyQ = append(bodyQ, unit{fm, id})
					}
					left += len(units) - 1
					for _, u := range users[fm] {
						if waiting[u]--; waiting[u] == 0 {
							sigQ = append(sigQ, u)
						}
					}
				} else {
					u := bodyQ[0]
					bodyQ = bodyQ[1:]
					mu.Unlock()
					t := time.Now()
					b := f.w.c.Check(u.fm.m, u.id)
					d := time.Since(t)
					if len(b.Diags) > 0 {
						u.fm.note(b.Diags)
					}
					mu.Lock()
					*body += d
					left--
					if left == 0 {
						cond.Broadcast()
					}
					continue // no new task: wake nobody
				}
				cond.Broadcast()
			}
		}()
	}
	wg.Wait()
}

// stageTimes is one run's wall time per stage, check as one stage, and the
// summed task time of its two kinds.
type stageTimes struct{ parse, resolve, check, sigs, bodies time.Duration }

// runFront runs every stage on procs workers and times each.
func runFront(tb testing.TB, p *genProgram, procs int) stageTimes {
	f := newFront(tb, p)
	var st stageTimes
	t0 := time.Now()
	f.parse(procs)
	t1 := time.Now()
	f.resolve(procs)
	t2 := time.Now()
	f.check(procs, &st.sigs, &st.bodies)
	t3 := time.Now()
	st.parse, st.resolve, st.check = t1.Sub(t0), t2.Sub(t1), t3.Sub(t2)
	f.clean(tb)
	return st
}

// TestBenchProgramClean: the generated program, at a fifth of its size,
// draws no diagnostic from any stage, with one worker and with eight, so
// the benchmark measures the accepting path.
func TestBenchProgramClean(t *testing.T) {
	p := generateProgram(20_000)
	if p.lines < 18_000 || p.lines > 22_000 {
		t.Fatalf("%d lines, want about 20,000", p.lines)
	}
	runFront(t, p, 1)
	runFront(t, p, 8)
	// The harness sees the bodies: a type error in one is reported.
	bad := generateProgram(20_000)
	lib := bad.mods[1]
	lib.files[0] = strings.Replace(lib.files[0], "return n * 2 + 1", "return n * 2 + true", 1)
	f := newFront(t, bad)
	f.parse(1)
	f.resolve(1)
	f.signatures(1)
	f.bodies(1)
	if got := codes(f.w, f.byP["lib0"].diags); got == "" {
		t.Fatalf("an injected body error drew no diagnostic")
	}
	t.Logf("%s", genSummary(p))
}

// BenchmarkFrontEnd100K: the generated program of about 100,000 lines with
// GOMAXPROCS and workers 1 and 8. Reports lines per second of each stage's
// wall time (parse, resolve, check: stages 5 and 6 together, the budget's
// row) and of the whole front end, and of the summed task time of stage 5
// and of stage 6 (with one worker, their wall time). The scaling is the
// ratio of the procs=8 and procs=1 figures.
func BenchmarkFrontEnd100K(b *testing.B) {
	p := generateProgram(100_000)
	for _, procs := range []int{1, 8} {
		b.Run(fmt.Sprintf("procs=%d", procs), func(b *testing.B) {
			old := runtime.GOMAXPROCS(procs)
			defer runtime.GOMAXPROCS(old)
			var sum stageTimes
			for i := 0; i < b.N; i++ {
				st := runFront(b, p, procs)
				sum.parse += st.parse
				sum.resolve += st.resolve
				sum.check += st.check
				sum.sigs += st.sigs
				sum.bodies += st.bodies
			}
			rate := func(d time.Duration) float64 { return float64(p.lines) * float64(b.N) / d.Seconds() }
			b.ReportMetric(0, "ns/op")
			b.ReportMetric(rate(sum.parse), "parse-lines/s")
			b.ReportMetric(rate(sum.resolve), "resolve-lines/s")
			b.ReportMetric(rate(sum.sigs), "sig-task-lines/s")
			b.ReportMetric(rate(sum.bodies), "body-task-lines/s")
			b.ReportMetric(rate(sum.check), "check-lines/s")
			b.ReportMetric(rate(sum.parse+sum.resolve+sum.check), "front-lines/s")
		})
	}
	b.Logf("%d lines, %d modules", p.lines, len(p.mods))
}

// genSummary is a one-line description of a generated program, for logs.
func genSummary(p *genProgram) string {
	var parts []string
	for _, m := range p.mods {
		n := 0
		for _, f := range m.files {
			n += strings.Count(f, "\n")
		}
		parts = append(parts, fmt.Sprintf("%s:%d", m.path, n))
	}
	return strings.Join(parts, " ")
}
