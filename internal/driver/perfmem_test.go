// Why: the compiler's memory on a large program is set by what one Check
// holds at its peak and what it keeps afterwards; this harness shows both
// by stage, and checks that releasing front-end output changes no bytes.
// What: TestPerfMemory_Probe, TestPerfMemory_ReleaseSameBytes and their
// helpers (memSample, liveHeap, memClasses, mirStats, releaseTree).
// How: the probe runs only when MK_MEMPROBE_DIR names a package on disk
// (the 1 MLOC program is corpus.Generate(1, 1000) written out; driver's
// tests may not import corpus), samples the heap every 5 ms and logs
// through t.Log; it asserts nothing about the numbers.

package driver

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// memSample is the heap's mapped-and-not-released bytes: what the Go
// runtime holds from the OS, the closest in-process figure to the working
// set (Go's own stacks and metadata included).
func memSample(s []metrics.Sample) uint64 {
	metrics.Read(s)
	return s[0].Value.Uint64() - s[1].Value.Uint64()
}

// liveHeap is the heap's live bytes after two collections.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// TestPerfMemory_Probe checks MK_MEMPROBE_DIR once with no cache, sampling
// the runtime's held bytes every 5 ms for the peak, then drops the
// session's tables one class at a time and logs what each freed. The order
// is the pipeline's, so a class's figure is what it holds beyond the
// classes logged before it.
func TestPerfMemory_Probe(t *testing.T) {
	dir := os.Getenv("MK_MEMPROBE_DIR")
	if dir == "" {
		t.Skip("MK_MEMPROBE_DIR is not set")
	}
	before := liveHeap()
	s, err := NewSession(Config{Timings: io.Discard, ReleaseFront: true, Root: dir, NoCache: os.Getenv("MK_MEMPROBE_CACHE") == "", CacheDir: os.Getenv("MK_MEMPROBE_CACHE")})
	if err != nil {
		t.Fatal(err)
	}
	samples := []metrics.Sample{{Name: "/memory/classes/total:bytes"}, {Name: "/memory/classes/heap/released:bytes"}, {Name: "/gc/heap/live:bytes"}}
	type point struct {
		at         time.Duration
		held, live uint64
	}
	var series []point
	peakProf := false
	t0 := time.Now()
	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		local := make([]metrics.Sample, len(samples))
		copy(local, samples)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			v := memSample(local)
			if v > peak.Load() {
				peak.Store(v)
			}
			series = append(series, point{time.Since(t0), v, local[2].Value.Uint64()})
			// MK_MEMPROBE_PEAKPROF: a heap profile (as of the last
			// collection) once the live heap first passes the given MB.
			if at, err := strconv.Atoi(os.Getenv("MK_MEMPROBE_PEAKMB")); err == nil && !peakProf && local[2].Value.Uint64()>>20 >= uint64(at) {
				peakProf = true
				if f, err := os.Create(os.Getenv("MK_MEMPROBE_PEAKPROF")); err == nil {
					pprof.WriteHeapProfile(f)
					f.Close()
				}
			}
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	start := time.Now()
	res := s.Check()
	took := time.Since(start)
	close(stop)
	<-done
	if len(res.Report.Diags) > 0 {
		t.Fatalf("%d diagnostics:\n%s", len(res.Report.Diags), res.Report.Text(res.Sources))
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	var b strings.Builder
	fmt.Fprintf(&b, "check %v, peak held %d MB, allocated in all %d MB, GCs %d\n", took.Round(time.Millisecond), peak.Load()>>20, ms.TotalAlloc>>20, ms.NumGC)
	if pf := os.Getenv("MK_MEMPROBE_PROF"); pf != "" {
		runtime.GC()
		f, err := os.Create(pf)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.WriteHeapProfile(f); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	times := res.Timings
	res = Result{}
	step := max(1, len(series)/40)
	for i := 0; i < len(series); i += step {
		var hi point
		for _, p := range series[i:min(len(series), i+step)] {
			if p.held > hi.held {
				hi = p
			}
		}
		fmt.Fprintf(&b, "  t=%5dms held %5d MB live %5d MB\n", hi.at.Milliseconds(), hi.held>>20, hi.live>>20)
	}
	for _, st := range times {
		fmt.Fprintf(&b, "  stage %-28s %v\n", st.Stage, st.Took)
	}
	mirStats(&b, s)
	total := liveHeap()
	fmt.Fprintf(&b, "retained after Check: %d MB\n", (total-before)>>20)
	last := total
	drop := func(name string, f func()) {
		f()
		now := liveHeap()
		fmt.Fprintf(&b, "  %-34s %6d MB\n", name, (int64(last)-int64(now))>>20)
		last = now
	}
	for _, c := range memClasses(s) {
		drop(c.name, c.drop)
	}
	fmt.Fprintf(&b, "  %-34s %6d MB\n", "rest", (int64(last)-int64(before))>>20)
	t.Log("\n" + b.String())
}

type memClass struct {
	name string
	drop func()
}

// memClasses are the session's retained tables, in pipeline order.
func memClasses(s *Session) []memClass {
	w := s.w
	var mods []*modNode
	for _, b := range s.p.builds {
		mods = append(mods, b.mods...)
	}
	each := func(f func(m *modNode)) func() {
		return func() {
			for _, m := range mods {
				f(m)
			}
		}
	}
	return []memClass{
		{"front cache", func() { w.front = nil }},
		{"check: modules", each(func(m *modNode) { m.cmod = nil })},
		{"syntax: ASTs", each(func(m *modNode) {
			for _, f := range m.files {
				f.ast = nil
			}
		})},
		{"syntax: parse summaries", each(func(m *modNode) {
			for _, f := range m.files {
				f.sum = nil
			}
		})},
		{"resolve: module info", each(func(m *modNode) { m.info, m.infoBy = nil, nil })},
		{"driver: unit records (reads)", each(func(m *modNode) { m.units, m.reads = nil, types.ReadSet{} })},
		{"resolve: session", func() { w.rs = nil }},
		{"mono env", func() {
			if w.sem5 != nil {
				w.sem5.menv = nil
			}
		}},
		{"lower+borrow: MIR bodies", func() {
			if w.sem5 != nil {
				w.sem5.bodies = nil
			}
		}},
		{"check: checker", func() {
			if w.sem5 != nil {
				w.sem5.chk = nil
				w.sem5.ev = nil
				w.sem5.hash = nil
			}
		}},
		{"driver: module nodes", func() {
			for _, b := range s.p.builds {
				b.mods, b.byName = nil, nil
			}
			if w.sem5 != nil {
				w.sem5.mods = nil
			}
			s.p.builds = nil
		}},
		{"types: table and items", func() { w.tab, w.items = nil, nil }},
		{"source: map and interner", func() { w.sm, w.in, w.sitePaths = nil, nil, nil }},
		{"session", func() { s.w, s.p, s.stdB, s.pkgB = nil, nil, nil, nil }},
	}
}

// mirStats logs the MIR store's bytes by list, length against capacity,
// and the statements by kind (the largest list).
func mirStats(b *strings.Builder, s *Session) {
	if s.w.sem5 == nil {
		return
	}
	var bodies, blocks, blockCap, stmts, stmtCap, locals, localCap, other int
	kinds := map[mir.StatementKind]int{}
	for _, e := range s.w.sem5.bodies {
		if e == nil || e.mir == nil {
			continue
		}
		mb := e.mir
		bodies++
		blocks += len(mb.Blocks)
		blockCap += cap(mb.Blocks)
		locals += len(mb.Locals)
		localCap += cap(mb.Locals)
		for i := range mb.Blocks {
			stmts += len(mb.Blocks[i].Stmts)
			stmtCap += cap(mb.Blocks[i].Stmts)
			for j := range mb.Blocks[i].Stmts {
				kinds[mb.Blocks[i].Stmts[j].Kind]++
			}
		}
		other += cap(mb.Operands)*int(reflect.TypeFor[mir.Operand]().Size()) + cap(mb.Spans)*int(reflect.TypeFor[source.Span]().Size()) +
			cap(mb.Args)*int(reflect.TypeFor[mir.Arg]().Size()) + cap(mb.Projections)*int(reflect.TypeFor[mir.Projection]().Size()) +
			cap(mb.Cases)*int(reflect.TypeFor[mir.SwitchCase]().Size())
	}
	ss, bs, ls := int(reflect.TypeFor[mir.Statement]().Size()), int(reflect.TypeFor[mir.Block]().Size()), int(reflect.TypeFor[mir.Local]().Size())
	fmt.Fprintf(b, "MIR: %d bodies; blocks %d (cap %d) %d MB; statements %d (cap %d) %d MB used %d MB; locals %d (cap %d) %d MB; other lists %d MB\n",
		bodies, blocks, blockCap, blockCap*bs>>20, stmts, stmtCap, stmtCap*ss>>20, stmts*ss>>20, locals, localCap, localCap*ls>>20, other>>20)
	var ks []mir.StatementKind
	for k := range kinds {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	for _, k := range ks {
		fmt.Fprintf(b, "  statements %-12v %9d\n", k, kinds[k])
	}
}

// releaseTree is a package whose modules exercise what release drops and
// keeps: a generic and its importer's instance, a same-module const a body
// folds, a compile-time requires, an impl an importer's member lookup finds,
// a gen line, and one module with a diagnostic.
func releaseTree() map[string]string {
	return map[string]string{
		"pkg.mk": libManifest(),
		"g/g.mk": "export Make(g: mut Gen) {\n    g.Emit(quote {\n        export Helper(): int {\n            return 1\n        }\n    })\n}\n",
		"lib/lib.mk": "import g\n\nconst Base: int = 4\n\nexport struct P {\n    export x: int\n}\n\n" +
			"export P.Twice(&self): int {\n    return self.x * 2\n}\n\n" +
			"export Max[T: Ord](a: T, b: T): T {\n    if a > b {\n        return a\n    }\n    return b\n}\n\n" +
			"export Scaled(const k: int, x: int): int\n    requires k > 0 {\n    if Base > 3 {\n        return x * k\n    }\n    return x\n}\n\ngen g.Make()\n",
		"app/app.mk": "import lib\n\nexport Use(p: &lib.P): int {\n    return lib.Max(p.Twice(), lib.Scaled(2, lib.Helper()))\n}\n",
		"bad/bad.mk": "import lib\n\nexport Bad(): int {\n    return lib.Nope()\n}\n",
	}
}

// TestPerfMemory_ReleaseSameBytes: a check that releases each module's
// front-end output (ReleaseFront) reports the same bytes as one that keeps
// it, with the cache off, cold and warm, and with 1 and 8 workers; and the
// released modules' ASTs and resolve Info are gone afterwards.
func TestPerfMemory_ReleaseSameBytes(t *testing.T) {
	dir := writeTree(t, releaseTree())
	want := jsonOf(t, frontCheck(t, Config{Root: dir, NoCache: true}))
	if !bytes.Contains(want, []byte("E0201")) {
		t.Fatalf("the tree's report has no E0201: %s", want)
	}
	cache := t.TempDir()
	for _, c := range []struct {
		name string
		cfg  Config
	}{
		{"no cache, 8 workers", Config{Root: dir, NoCache: true, ReleaseFront: true, Jobs: 8}},
		{"no cache, 1 worker", Config{Root: dir, NoCache: true, ReleaseFront: true, Jobs: 1}},
		{"cold", Config{Root: dir, CacheDir: cache, ReleaseFront: true}},
		{"warm", Config{Root: dir, CacheDir: cache, ReleaseFront: true}},
		{"verify", Config{Root: dir, CacheDir: cache, ReleaseFront: true, CacheVerify: true}},
	} {
		s, err := NewSession(c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		got := jsonOf(t, s.Check())
		if !bytes.Equal(got, want) {
			t.Errorf("%s: --json\n%s\nwant (no release)\n%s", c.name, got, want)
		}
		if c.name != "no cache, 8 workers" {
			continue
		}
		// The walk's contract leaves are only functions whose predicate failed: none
		// here, std's included (an extern's or bodiless declaration's
		// requires would make its callers leaves and shrink the walk).
		if lc := s.w.lostContracts(s.pkgB); len(lc) > 0 {
			for id := range lc {
				t.Errorf("lostContracts holds %s on a tree whose contracts all check", s.w.items.Get(id).Key)
			}
		}
		for _, m := range s.pkgB.mods {
			if m.path == "lib" || m.path == "app" {
				if m.info != nil || len(m.files) == 0 || m.files[0].ast != nil {
					t.Errorf("%s: module %s kept its Info or AST after release", c.name, m.path)
				}
			}
		}
	}
}
