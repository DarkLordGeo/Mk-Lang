// Package probe is a development tool for internal/borrow, not a test of the
// build: it lives under testdata, so go build ./..., go test ./... and
// internal/archtest never see it (its imports of syntax, check and lower are
// outside borrow's DAG row). It runs design blocks and conformance units
// through parse, resolve, check and lower with lower's own test harness
// (harness_test.go and corpushelp_test.go are copies of internal/lower's),
// then borrow.Check on every lowered body, and compares the (line, code)
// set with the unit's markers. The real run is the corpus's, through the
// driver's borrow stage.
//
//	go test ./internal/borrow/testdata/probe -run TestProbe -v
package probe

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"testing"
	"time"

	"mk/internal/borrow"
	"mk/internal/check"
	"mk/internal/diag"
	"mk/internal/lower"
	"mk/internal/mir"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
	stdfs "mk/std"
)

type unit struct {
	key, src  string
	noPrelude bool
	// bin is a program of tests/programs: a bin root, no test prelude,
	// verdict ok.
	bin bool
}

func programUnits(t testing.TB) []unit {
	var out []unit
	for _, d := range []string{"", "panics", "tasks", "testblocks", "pgo"} {
		root := filepath.Join("..", "..", "..", "..", "tests", "programs", d)
		ents, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".mk") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, unit{key: "programs/" + d + "/" + e.Name(), src: strings.ReplaceAll(string(data), "\r\n", "\n"), noPrelude: true, bin: true})
		}
	}
	return out
}

var markerRE = regexp.MustCompile(`// <- ((?:E\d{4}[ ,]*)+)`)
var codeRE = regexp.MustCompile(`E\d{4}`)

// markers is the unit's (line, code) set and its verdict ("ok", "parse",
// "reject").
func markers(src string) (map[string]bool, string) {
	out := map[string]bool{}
	lines := strings.Split(src, "\n")
	verdict := ""
	for i, l := range lines {
		if i == 0 && strings.HasPrefix(l, "// verdict: ") {
			f := strings.Fields(strings.TrimPrefix(l, "// verdict: "))
			if len(f) > 0 {
				verdict = f[0]
			}
		}
		if m := markerRE.FindStringSubmatch(l); m != nil {
			for _, c := range codeRE.FindAllString(m[1], -1) {
				out[fmt.Sprintf("%s@%d", c, i+1)] = true
			}
		}
	}
	return out, verdict
}

func designUnits(t testing.TB) []unit {
	keys, texts := docBlocks(t)
	var out []unit
	for _, k := range keys {
		out = append(out, unit{key: k, src: texts[k]})
	}
	return out
}

func confUnits(t testing.TB, dirs ...string) []unit {
	var out []unit
	for _, d := range dirs {
		root := filepath.Join("..", "..", "..", "..", "tests", "conformance", d)
		ents, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".mk") {
				continue
			}
			if d == "codes" && !borrowCodeFile(e.Name()) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			src := strings.ReplaceAll(string(data), "\r\n", "\n")
			out = append(out, unit{key: d + "/" + e.Name(), src: src, noPrelude: strings.Contains(src, "\n// prelude: no")})
		}
	}
	return out
}

func borrowCodeFile(name string) bool {
	return strings.HasPrefix(name, "E04") || strings.HasPrefix(name, "E05") || strings.HasPrefix(name, "E06") || strings.HasPrefix(name, "E1102")
}

// borrowCode reports whether c is emitted by borrow.
func borrowCode(c string) bool {
	switch c {
	case "E0601", "E0607", "E0608", "E0609", "E0610", "E0408", "E0410", "E0412":
		return false
	}
	return strings.HasPrefix(c, "E04") || strings.HasPrefix(c, "E05") || strings.HasPrefix(c, "E06") || c == "E1102"
}

type outcome struct {
	key, bucket, detail string
}

func TestProbe(t *testing.T) {
	core, pre := corpusPrelude(t), testPrelude(t)
	var units []unit
	only := os.Getenv("PROBE_ONLY")
	all := append(designUnits(t), confUnits(t, "model", "memory", "rules", "codes")...)
	if os.Getenv("PROBE_STD") != "" {
		all = append(all, programUnits(t)...)
	}
	for _, u := range all {
		if only != "" && !strings.Contains(u.key, only) {
			continue
		}
		units = append(units, u)
	}
	var outs []outcome
	tally := map[string]int{}
	for _, u := range units {
		o := runUnit(t, core, pre, u)
		outs = append(outs, o)
		tally[o.bucket]++
	}
	sort.Slice(outs, func(i, j int) bool {
		if outs[i].bucket != outs[j].bucket {
			return outs[i].bucket < outs[j].bucket
		}
		return outs[i].key < outs[j].key
	})
	for _, o := range outs {
		if o.bucket == "match" || (o.bucket == "skip" && os.Getenv("PROBE_ALL") == "") {
			continue
		}
		t.Logf("%-14s %s %s", o.bucket, o.key, o.detail)
	}
	var tk []string
	for k, v := range tally {
		tk = append(tk, fmt.Sprintf("%s %d", k, v))
	}
	sort.Strings(tk)
	t.Logf("%d units: %s", len(units), strings.Join(tk, ", "))
}

// stdFiles reads the .mk files of std/<dir>, in byte order of names.
func stdFiles(t testing.TB, dir string) []string {
	ents, err := fs.ReadDir(stdfs.FS, dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mk") {
			continue
		}
		data, err := fs.ReadFile(stdfs.FS, dir+"/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.ReplaceAll(string(data), "\r\n", "\n"))
	}
	return out
}

// syncStub is std.sync's Shared as reference §18 declares it, bodiless
// (std has no std.sync yet): a linear view on a raw pointer, its allocator
// &Heap, Clone `from self.ptr`. It lets the probe judge the units
// that clone a Shared.
const syncStub = `export linear view struct Shared[T] {
	ptr: *T
}
export Shared[T].New(h: &Heap, x: T): OutOfMemory!Shared[T] from h, x
export Shared[T].Clone(&self): Shared[T] from self.ptr
export Shared[T].Ref(&self): &T
export Shared[T].Free(self, h: &Heap)
`

// newStdWorld is a world over the real std (std.raw, std.core, std.fs), as
// the driver loads it, instead of lower's fake prelude (PROBE_STD=1).
func newStdWorld(t *testing.T) *world {
	w := &world{t: t, sm: source.NewSourceMap(), in: source.NewInterner(), items: types.NewItems(), mods: map[string]*built{}, deferStd: true}
	w.consts = types.NewConstTable()
	w.tab = types.NewTable(w.in, w.items, w.consts)
	rcfg := &resolve.Config{Interner: w.in, Sources: w.sm, Items: w.items, PreludePath: "std.core", PreludeNames: stdfs.PreludeNames[:]}
	w.rs = resolve.NewSession(rcfg)
	w.cfg = &check.Config{Interner: w.in, Types: w.tab, Items: w.items, Consts: w.consts, Target: types.X86_64Windows}
	w.add("std.raw", opt{std: true, noPrelude: true, unsafe: true}, stdFiles(t, "raw")...)
	core := w.add("std.core", opt{std: true, noPrelude: true, unsafe: true}, stdFiles(t, "core")...)
	w.core = core.id
	w.add("std.fs", opt{std: true, unsafe: true}, stdFiles(t, "fs")...)
	w.add("std.sync", opt{std: true, unsafe: true}, syncStub)
	if os.Getenv("PROBE_DUMP") != "" {
		for _, p := range []string{"std.raw", "std.core", "std.fs"} {
			if ds := w.mods[p].diags(); len(ds) > 0 {
				t.Logf("%s: %d diagnostics, first %v %s", p, len(ds), ds[0].Code, ds[0].Message)
			}
		}
	}
	return w
}

func runUnit(t *testing.T, core, pre string, u unit) (o outcome) {
	o.key = u.key
	want, verdict := markers(u.src)
	if u.bin {
		verdict = "ok"
	}
	if verdict == "parse" || verdict == "" {
		o.bucket = "skip"
		return o
	}
	if strings.Contains(u.src, "\nimport ") || strings.HasPrefix(u.src, "import ") {
		ok := true
		for _, l := range strings.Split(u.src, "\n") {
			if strings.HasPrefix(l, "import ") && l != "import std.fs" && !(l == "import std.sync" && os.Getenv("PROBE_STD") != "") {
				ok = false
			}
		}
		if !ok {
			o.bucket = "skip"
			o.detail = "imports"
			return o
		}
	}
	var w *world
	if os.Getenv("PROBE_STD") != "" {
		w = newStdWorld(t)
	} else {
		w = newWorldCore(t, true, core)
		for _, path := range stubPaths() {
			if strings.Contains(u.src, "import "+path+"\n") {
				w.add(path, opt{std: true}, stdStubs[path])
			}
		}
	}
	op := opt{parseErrors: true, bin: u.bin}
	if !u.noPrelude {
		op.testPrelude = pre
	}
	var b *built
	if ie := source.Guard("module", source.Span{}, func() { b = w.add("m", op, u.src) }); ie != nil || b == nil || b.m == nil {
		o.bucket = "front-ice"
		return o
	}
	file := b.srcs[len(b.srcs)-1].ID
	got := map[string]bool{}
	gotBorrow := map[string]bool{}
	add := func(d *diag.Diagnostic, fromBorrow bool) {
		line := 0
		if d.At.File == file {
			line = int(w.sm.File(file).LineCol(d.At.Start).Line)
		}
		k := fmt.Sprintf("%v@%d", d.Code, line)
		got[k] = true
		if fromBorrow {
			gotBorrow[k] = true
		}
	}
	if len(b.pdiags) > 0 {
		o.bucket = "front-parse"
		o.detail = fmt.Sprintf("%v %s", b.pdiags[0].Code, b.pdiags[0].Message)
		return o
	}
	for _, d := range b.diags() {
		add(d, false)
	}
	cfg := &lower.Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Known: w.cfg.Known, Validate: true, Sources: w.sm,
		Impls: lower.ImplsOf(w.c)}
	frontClean := len(b.diags()) == 0
	var ices []string
	for _, id := range b.m.Units() {
		it := w.items.Get(id)
		var body *check.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { body = w.c.Check(b.m, id) }); ie != nil || body == nil {
			ices = append(ices, "check ICE "+it.Key.String())
			continue
		}
		for _, d := range body.Diags {
			add(d, false)
		}
		if len(body.Diags) > 0 || !frontClean {
			continue
		}
		var in lower.Input
		var mb *mir.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() {
			in = lower.FromCheck(cfg, w.c, body)
			mb = lower.Lower(cfg, in)
		}); ie != nil {
			ices = append(ices, fmt.Sprintf("lower ICE %s: %s", it.Key, ie.What))
			continue
		}
		env := borrowEnv(w, cfg, in, it)
		if os.Getenv("PROBE_DUMP") != "" {
			t.Logf("%s\n%s", u.key, (&mir.Printer{Types: w.tab, Items: w.items}).Print(mb))
			for bi := range mb.Blocks {
				tm := &mb.Blocks[bi].Term
				if tm.Kind != mir.TermCall || tm.Callee.Kind != mir.CallDirect {
					continue
				}
				sig := env.Callee(tm.Callee.Fn.Item)
				if sig == nil {
					continue
				}
				var ps []string
				for _, p := range sig.Params {
					ps = append(ps, w.tab.TypeString(p.Type, nil))
				}
				t.Logf("  callee %s (%s) -> %s flows %+v", w.items.Get(tm.Callee.Fn.Item).Key, strings.Join(ps, ", "), w.tab.TypeString(sig.Result, nil), sig.Flows)
			}
			src := b.srcs[len(b.srcs)-1]
			for bi, bk := range mb.Blocks {
				for si, st := range bk.Stmts {
					sp := mb.Spans[st.Span]
					if sp.File == src.ID && sp.End <= uint32(len(src.Text)) {
						t.Logf("  bb%d[%d] %v %q", bi, si, st.Rvalue.Kind, src.Text[sp.Start:sp.End])
					}
				}
			}
		}
		var res *borrow.Result
		factsFallbacks = nil
		if ie := source.Guard("borrow "+it.Key.String(), it.Span, func() { res = borrow.Check(mb, env) }); ie != nil {
			ices = append(ices, fmt.Sprintf("borrow ICE %s: %s", it.Key, ie.What))
			continue
		}
		if len(factsFallbacks) > 0 {
			ices = append(ices, fmt.Sprintf("facts fallback %s: %v", it.Key, factsFallbacks))
		}
		for _, d := range res.Diags {
			if os.Getenv("PROBE_DUMP") != "" {
				t.Logf("borrow %s: %v@%s %s %s", it.Key, d.Code, w.lc(d.At), d.Message, dump(w, []*diag.Diagnostic{d}))
			}
			add(d, true)
		}
	}
	gs, ws := keys(got), keys(want)
	if len(ices) > 0 {
		o.bucket = "ice"
		o.detail = strings.Join(ices, "; ") + " | want " + ws + " got " + gs
		return o
	}
	if gs == ws {
		o.bucket = "match"
		return o
	}
	o.detail = "want " + ws + " got " + gs
	involved := len(gotBorrow) > 0
	for k := range want {
		if borrowCode(k[:5]) && !got[k] {
			involved = true
		}
	}
	for k := range got {
		if !gotBorrow[k] && !want[k] {
			involved = false // an unexpected front-end diagnostic: the harness's prelude, not borrow
		}
	}
	if !involved {
		o.bucket = "front-diff"
		return o
	}
	o.bucket = "borrow-diff"
	return o
}

func keys(m map[string]bool) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return "[" + strings.Join(out, " ") + "]"
}

// factsFallbacks lists the Facts queries of the current borrow.Check that
// check's FactsIn could not answer (a panic, which the driver's Env would
// turn into an ICE, or !ok). The probe runs one check at a time.
var factsFallbacks []string

// borrowEnv is the Env the driver's borrow stage builds.
func borrowEnv(w *world, cfg *lower.Config, in lower.Input, it *types.Item) *borrow.Env {
	return &borrow.Env{
		Types:  w.tab,
		Known:  w.cfg.Known,
		Sig:    in.Sig(),
		Module: w.items.Module(it.Module),
		Callee: func(id types.ItemID) *types.Signature {
			if id == types.NoItem {
				return nil
			}
			if d, ok := w.items.Get(id).Decl.(*types.FuncDecl); ok {
				return &d.Sig
			}
			return nil
		},
		Method: func(iface types.ItemID, m uint32) *types.Signature {
			if d, ok := w.items.Get(iface).Decl.(*types.InterfaceDecl); ok && int(m) < len(d.Methods) {
				return &d.Methods[m]
			}
			return nil
		},
		Facts: func(t types.TypeID) (f types.Facts) {
			defer func() {
				if e := recover(); e != nil {
					if os.Getenv("PROBE_DUMP") != "" {
						fmt.Printf("facts panic %s: %v\n%s\n", w.tab.TypeString(t, nil), e, debug.Stack())
					}
					f = types.Facts{Kind: types.Linear, Carrier: true}
					factsFallbacks = append(factsFallbacks, "panic "+w.tab.TypeString(t, nil))
				}
			}()
			if g, ok := in.Facts(t); ok {
				return g
			}
			if os.Getenv("PROBE_DUMP") != "" {
				fmt.Printf("facts !ok %s\n", w.tab.TypeString(t, nil))
			}
			factsFallbacks = append(factsFallbacks, "!ok "+w.tab.TypeString(t, nil))
			return types.Facts{Kind: types.Linear, Carrier: true}
		},
		File: func(id source.FileID) *source.File { return w.sm.File(id) },
	}
}

// TestProbeStd runs borrow over every body of std.core and std.fs that
// check accepts and lower produces: the driver runs stage 7 on std too.
func TestProbeStd(t *testing.T) {
	w := newStdWorld(t)
	cfg := &lower.Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Known: w.cfg.Known, Validate: true, Sources: w.sm,
		Impls: lower.ImplsOf(w.c)}
	tally := map[string]int{}
	// PROBE_TIME=1 logs borrow.Check's wall time over the clean-lowered std
	// bodies (the loan pass on real code, beside the synthetic budget
	// shapes). Off by default so the PROBE_ALL log stays comparable.
	timing := os.Getenv("PROBE_TIME") != ""
	var total, slowest time.Duration
	var totalOps, slowestOps int
	var slowestKey string
	for _, path := range []string{"std.core", "std.fs"} {
		b := w.mods[path]
		for _, id := range b.m.Units() {
			it := w.items.Get(id)
			var body *check.Body
			if ie := source.Guard(it.Key.String(), it.Span, func() { body = w.c.Check(b.m, id) }); ie != nil || body == nil {
				tally["check ICE"]++
				continue
			}
			if len(body.Diags) > 0 {
				tally["check rejects"]++
				t.Logf("check %s: %v %s", it.Key, body.Diags[0].Code, body.Diags[0].Message)
				continue
			}
			var in lower.Input
			var mb *mir.Body
			if ie := source.Guard(it.Key.String(), it.Span, func() {
				in = lower.FromCheck(cfg, w.c, body)
				mb = lower.Lower(cfg, in)
			}); ie != nil {
				tally["lower ICE"]++
				t.Logf("lower ICE %s: %s", it.Key, ie.What)
				continue
			}
			env := borrowEnv(w, cfg, in, it)
			if k := os.Getenv("PROBE_ITEM"); k != "" && k == it.Key.String() {
				t.Logf("%s", (&mir.Printer{Types: w.tab, Items: w.items}).Print(mb))
			}
			var res *borrow.Result
			factsFallbacks = nil
			start := time.Now()
			if ie := source.Guard("borrow "+it.Key.String(), it.Span, func() { res = borrow.Check(mb, env) }); ie != nil {
				tally["borrow ICE"]++
				t.Logf("borrow ICE %s: %s", it.Key, ie.What)
				continue
			}
			if timing {
				el := time.Since(start)
				ops := 0
				for i := range mb.Blocks {
					ops += len(mb.Blocks[i].Stmts) + 1
				}
				total += el
				totalOps += ops
				if el > slowest {
					slowest, slowestKey, slowestOps = el, it.Key.String(), ops
				}
			}
			if len(factsFallbacks) > 0 {
				tally["facts fallback"]++
				t.Logf("facts fallback %s: %v", it.Key, factsFallbacks)
			}
			if len(res.Diags) == 0 {
				tally["clean"]++
				continue
			}
			tally["borrow rejects"]++
			for _, d := range res.Diags {
				t.Logf("borrow %s: %v@%s %s", it.Key, d.Code, w.lc(d.At), d.Message)
			}
		}
	}
	t.Logf("std bodies: %v", tally)
	if timing {
		t.Logf("borrow.Check over std: %d bodies checked, %d statements and terminators, %v total; slowest %s, %d statements and terminators, %v",
			tally["clean"]+tally["borrow rejects"], totalOps, total, slowestKey, slowestOps, slowest)
	}
}
