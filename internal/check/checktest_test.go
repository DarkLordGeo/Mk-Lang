// Why: check's tests need modules from real Mk source without the driver,
// the real std or an evaluator.
// What: the test harness: world, built, opt, fakePrelude, preludeNames and
// fakeEval, with helpers to add modules and read diagnostics.
// How: parses Mk source (a test-only import of syntax), resolves it in
// driver order (Begin, Finish, Settle) against a fake prelude, then runs
// Signatures and Check with a stub evaluator (fakeEval).

package check

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// preludeNames is the prelude's list of names, in its order. The generator
// API's names (Gen, Type, Code, ...) and HashKey are left out, as
// internal/resolve's helpers_test.go leaves them.
var preludeNames = []string{"Alloc", "Arch", "Arena", "Box", "Canceled", "Cell", "Chan", "Chunks", "ChunksMut", "Closed", "Eq",
	"Format", "Free", "Guard", "Hash", "Hasher", "Heap", "HeapList", "HeapMap", "HeapText", "Io", "Limits", "Lines",
	"List", "Map", "MapIter", "Mutex", "Ord", "Os", "OutOfMemory", "ParseError", "Path", "Print", "ReadError",
	"Reader", "Replace", "Scalars", "Scope", "Set", "Slice", "Split", "StackSize", "Swap", "System", "TargetInfo", "Text",
	"TextMap", "TextMapIter", "Utf8Error", "Words", "WriteError", "Writer"}

// fakePrelude declares every prelude name but Path, as resolve's harness does,
// with field shapes close enough to std for the kind rules: Cell[T] holds a
// T, Mutex[T] a lock word and a T, Arena a raw pointer.
func fakePrelude() string {
	var b strings.Builder
	bodies := map[string]string{
		"Cell":        "[T] { v: T }",
		"Mutex":       "[T] { lock: u32; v: T }",
		"Chan":        "[T] { p: *T }",
		"Box":         "[T] { p: *T }",
		"HeapList":    "[T] { p: *T; n: int }",
		"List":        "[T] { p: *T; n: int }",
		"Guard":       "[T] { p: *T }",
		"HeapMap":     "[K, V] { k: *K; v: *V }",
		"Map":         "[K, V] { k: *K; v: *V }",
		"MapIter":     "[K, V] { k: *K; v: *V }",
		"Set":         "[T] { p: *T }",
		"TextMap":     "[V] { p: *V }",
		"TextMapIter": "[V] { p: *V }",
		"Scope":       " { words: [16]u64 }",
		"Arena":       " { p: *u8; n: int }",
		// std/core/mem_views.mk's fields, so the surface's `from self.xs`
		// and `from self.ptr` resolve.
		"Chunks":    "[T] { xs: []T; pos: int; n: int }",
		"ChunksMut": "[T] { ptr: *T; left: int; n: int; marker: [0]&T }",
		// The reference's fields; Target is a value of this type.
		"TargetInfo": " { export arch: Arch; export os: Os; export pointer_bytes: int }",
	}
	views := map[string]bool{"Arena": true, "Box": true, "HeapList": true, "List": true, "HeapMap": true,
		"Map": true, "MapIter": true, "Set": true, "TextMap": true, "TextMapIter": true, "HeapText": true,
		"Guard": true, "Scope": true, "Chunks": true, "ChunksMut": true}
	for _, n := range preludeNames {
		switch n {
		case "Path":
		case "Canceled", "Closed", "OutOfMemory", "ParseError", "ReadError", "Utf8Error", "WriteError":
			fmt.Fprintf(&b, "export error %s\n", n)
		case "Format":
			b.WriteString("export interface Format {\n\tFormat(&self, w: mut Writer): WriteError!void\n}\n")
		case "Eq":
			b.WriteString("export interface Eq {\n\tEq(&self, other: &Self): bool\n}\n")
		case "Ord":
			b.WriteString("export interface Ord {\n\tLess(&self, other: &Self): bool\n}\n")
		case "Hash":
			b.WriteString("export interface Hash {}\n")
		case "Free":
			// The reference's release protocol, in the shape check keys the
			// release bounds on.
			b.WriteString("export interface Free {\n\tFree(self, a: &Alloc)\n}\n")
		case "Swap":
			b.WriteString("export Swap[T](a: mut T, b: mut T)\n\t*a into b, *b into a\n")
		case "Replace":
			b.WriteString("export Replace[T](dst: mut T, x: T into dst): T\n")
		case "Slice":
			b.WriteString("export interface Slice[T] {}\nimpl[T] Slice[T] for List[T] {}\nimpl[T] Slice[T] for HeapList[T] {}\n")
		case "Print":
			b.WriteString("export Print[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void\n")
		case "Arch", "Os", "StackSize":
			fmt.Fprintf(&b, "export enum %s { A }\n", n)
		default:
			body, ok := bodies[n]
			if !ok {
				body = " {}"
			}
			kw := ""
			if views[n] {
				kw = "view "
			}
			// One field per line (the parser's E0101 otherwise).
			body = strings.NewReplacer("{ ", "{\n\t", "; ", "\n\t", " }", "\n}").Replace(body)
			fmt.Fprintf(&b, "export %sstruct %s%s\n", kw, n, body)
		}
	}
	b.WriteString("export struct Duration {}\nexport struct IoTable {}\n")
	b.WriteString("export Arena.Fmt[A: Fields[Format] + Plain](&self, const f: str, args: A): OutOfMemory!str from self\n")
	for _, t := range []string{"int", "i64", "str", "f64"} {
		fmt.Fprintf(&b, "impl Format for %s {\n\tFormat(&self, w: mut Writer): WriteError!void {\n\t}\n}\n", t)
	}
	for _, t := range []string{"int", "u8", "i32", "str"} {
		fmt.Fprintf(&b, "impl Eq for %s {\n\tEq(&self, other: &%s): bool {\n\t\treturn true\n\t}\n}\n", t, t)
		fmt.Fprintf(&b, "impl Ord for %s {\n\tLess(&self, other: &%s): bool {\n\t\treturn true\n\t}\n}\n", t, t)
	}
	b.WriteString("export Writer.Str(mut self, s: str): WriteError!void\n")
	b.WriteString("export Cell[T].New(x: T): Cell[T]\nexport Cell[T].Load(&self): T\nexport Cell[T].Store(&self, x: T)\n")
	// The generator API: not prelude names, found
	// by Known in the prelude module.
	b.WriteString("export struct Gen {\n\tp: *u8\n}\nexport struct Type {\n\tp: *u8\n}\nexport struct Code {\n\tp: *u8\n}\n")
	b.WriteString("export Gen.Emit(mut self, c: Code)\nexport Type.Name(&self): str\n")
	b.WriteString("export Io.Scope(self): Scope\nexport Io.ScopeOf[E](self): Scope[E]\nexport Scope.Cancel(&self)\n")
	return b.String()
}

// fakeEval answers evaluations from a table keyed by item key text (an
// EvalResult, or a function of the request); a missing entry is poison
// (NoConst, no error). It records every request. Safe for concurrent use.
type fakeEval struct {
	items   *types.Items
	answers map[string]types.EvalResult
	fn      func(req *types.EvalRequest) (types.EvalResult, bool)
	mu      sync.Mutex
	asked   []*types.EvalRequest
}

func (f *fakeEval) Eval(req *types.EvalRequest) types.EvalResult {
	f.mu.Lock()
	cp := *req
	f.asked = append(f.asked, &cp)
	f.mu.Unlock()
	if f.fn != nil {
		if r, ok := f.fn(req); ok {
			return r
		}
	}
	return f.answers[f.items.Get(req.Item).Key.String()]
}

// requests renders the requests asked so far as "What Stage key".
func (f *fakeEval) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.asked {
		out = append(out, fmt.Sprintf("%d/%d %s", r.What, r.Stage, f.items.Get(r.Item).Key.String()))
	}
	return out
}

// withEval installs a fakeEval on the world's Config.
func (w *world) withEval() *fakeEval {
	f := &fakeEval{items: w.items, answers: map[string]types.EvalResult{}}
	w.cfg.Eval = f
	return f
}

// world is one build of test modules.
type world struct {
	t      testing.TB
	sm     *source.SourceMap
	in     *source.Interner
	items  *types.Items
	tab    *types.Table
	consts *types.ConstTable
	rs     *resolve.Session
	cfg    *Config
	c      *Checker
	mods   map[string]*built
	core   types.ModuleID
}

// built is one resolved and checked test module.
type built struct {
	w     *world
	id    types.ModuleID
	path  string
	info  *resolve.Info
	srcs  []*source.File
	files []*ast.File
	m     *Module
	// rdiags are resolve's; cdiags check's stage-5 diagnostics.
	rdiags []*diag.Diagnostic
	// parsed counts the parser's diagnostics (kept only under parseErrors).
	parsed int
}

// opt configures one test module.
type opt struct {
	std, bin, noPrelude, parseErrors, unsafe bool
	// library marks a module of a package with a library bin (E1109).
	library     bool
	testPrelude string
	// stopBefore skips Signatures (the module is resolved only).
	stopBefore bool
	// core, read only by checkNoICEWith, replaces fakePrelude() as the
	// world's std.core text.
	core string
}

func newWorld(t testing.TB, withPrelude bool) *world {
	t.Helper()
	return newWorldCore(t, withPrelude, "")
}

// newWorldCore is newWorld with core as std.core's text ("" for
// fakePrelude()).
func newWorldCore(t testing.TB, withPrelude bool, core string) *world {
	t.Helper()
	custom := core != ""
	if !custom {
		core = fakePrelude()
	}
	w := &world{t: t, sm: source.NewSourceMap(), in: source.NewInterner(), items: types.NewItems(), mods: map[string]*built{}}
	w.consts = types.NewConstTable()
	w.tab = types.NewTable(w.in, w.items, w.consts)
	rcfg := &resolve.Config{Interner: w.in, Sources: w.sm, Items: w.items}
	if withPrelude {
		rcfg.PreludePath = "std.core"
		rcfg.PreludeNames = preludeNames
	}
	w.rs = resolve.NewSession(rcfg)
	w.cfg = &Config{Interner: w.in, Types: w.tab, Items: w.items, Consts: w.consts, Target: types.X86_64Windows}
	w.c = New(w.cfg)
	if withPrelude {
		b := w.add("std.core", opt{std: true, noPrelude: true, parseErrors: custom}, core)
		w.core = b.id
		w.c.known = w.rs.Known()
	}
	return w
}

// add parses, resolves and checks (stage 5) a module.
func (w *world) add(path string, o opt, texts ...string) *built {
	w.t.Helper()
	b := &built{w: w, path: path}
	mod := &types.Module{Path: path, Std: o.std, UnsafePermitted: o.unsafe}
	var srcs []string
	if o.testPrelude != "" {
		srcs = append(srcs, o.testPrelude)
	}
	srcs = append(srcs, texts...)
	for i, text := range srcs {
		name := fmt.Sprintf("%s/%02d.mk", strings.ReplaceAll(path, ".", "/"), i)
		src := w.sm.Add(name, []byte(text))
		f, ds := syntax.ParseFile(src, w.in, syntax.Options{AllowBodiless: o.std || (o.testPrelude != "" && i == 0)})
		b.parsed += len(ds)
		if len(ds) > 0 && !o.parseErrors {
			for _, d := range ds {
				w.t.Errorf("%s: parse %v at %d: %s", name, d.Code, d.At.Start, d.Message)
			}
			w.t.FailNow()
		}
		mod.Files = append(mod.Files, src.ID)
		b.srcs = append(b.srcs, src)
		b.files = append(b.files, f)
	}
	b.id = w.items.AddModule(mod)
	in := &resolve.ModuleInput{Module: b.id, Files: b.files, Imports: map[string]types.ModuleID{}, BinRoot: o.bin}
	if !o.noPrelude && w.core != types.NoModule {
		in.Prelude = w.core
	}
	if o.testPrelude != "" {
		in.TestPrelude = b.srcs[0].ID
		w.cfg.TestPrelude = true
	}
	for _, f := range b.files {
		for _, il := range resolve.ImportsOf(f, w.in) {
			if dep, ok := w.mods[il.Path]; ok {
				in.Imports[il.Path] = dep.id
			}
		}
	}
	// types.Module.Imports, as the driver sets it before any stage:
	// each direct import once, ordered by path (restate.go reads it).
	paths := make([]string, 0, len(in.Imports))
	for p := range in.Imports {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		mod.Imports = append(mod.Imports, in.Imports[p])
	}
	rm := w.rs.Begin(in)
	b.info = rm.Finish(1)
	b.rdiags = append(b.rdiags, b.info.Diags...)
	ds, gs := rm.Settle()
	b.rdiags = append(append(b.rdiags, ds...), gs...)
	w.mods[path] = b
	if path == "std.core" && o.std {
		w.c.known = w.rs.Known() // the driver's Config.Known, before std.core's stage 5
	}
	if !o.stopBefore {
		b.m = w.c.Signatures(&ModuleInput{Module: b.id, Files: b.files, Info: b.info, BinRoot: o.bin, Library: o.library})
	}
	return b
}

// item finds an item of the module by its key text after the module path
// ("F", "P:Add", "#test(\"x\")").
func (b *built) item(suffix string) types.ItemID {
	b.w.t.Helper()
	for _, ii := range b.info.Items {
		k := b.w.items.Get(ii.Item).Key.String()
		if k == b.path+"."+suffix || k == b.path+suffix {
			return ii.Item
		}
	}
	b.w.t.Fatalf("no item %q in %s", suffix, b.path)
	return types.NoItem
}

// key is an item's key text without the module path.
func (b *built) key(id types.ItemID) string {
	k := b.w.items.Get(id).Key.String()
	k = strings.TrimPrefix(k, b.path)
	return strings.TrimPrefix(k, ".")
}

// lc renders a span as line:col of its file.
func (w *world) lc(s source.Span) string {
	f := w.sm.File(s.File)
	if f == nil {
		return "?"
	}
	p := f.LineCol(s.Start)
	return fmt.Sprintf("%d:%d", p.Line, p.Col)
}

// diags are resolve's and check's stage-5 diagnostics of the module.
func (b *built) diags() []*diag.Diagnostic {
	out := append([]*diag.Diagnostic(nil), b.rdiags...)
	if b.m != nil {
		out = append(out, b.m.Diags...)
	}
	return out
}

// codes renders the diagnostics sorted as "E0201@3:5".
func codes(w *world, ds []*diag.Diagnostic) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Code.String()+"@"+w.lc(d.At))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// dump prints diagnostics for a failure message.
func dump(w *world, ds []*diag.Diagnostic) string {
	var sb strings.Builder
	for _, d := range ds {
		fmt.Fprintf(&sb, "  %v@%s %s", d.Code, w.lc(d.At), d.Message)
		for _, f := range d.Facts {
			fmt.Fprintf(&sb, " %s=%s", f.Key, factString(f.Value))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func factString(f diag.Fact) string {
	switch f.Kind {
	case diag.FactText:
		return f.Str
	case diag.FactInt:
		return fmt.Sprint(f.Int)
	case diag.FactBool:
		return fmt.Sprint(f.Bool)
	case diag.FactList:
		var p []string
		for _, x := range f.List {
			p = append(p, factString(x))
		}
		return "[" + strings.Join(p, " ") + "]"
	case diag.FactAt:
		return fmt.Sprintf("@%d", f.At.Start)
	}
	return "?"
}

// fact returns a diagnostic's fact as text, "" when absent.
func fact(d *diag.Diagnostic, key string) string {
	f, ok := d.FactValue(key)
	if !ok {
		return ""
	}
	return factString(f)
}

// wantClean fails the test when the module has any diagnostic.
func (b *built) wantClean(t *testing.T) {
	t.Helper()
	if ds := b.diags(); len(ds) > 0 {
		t.Fatalf("%s: want no diagnostics, got:\n%s", b.path, dump(b.w, ds))
	}
}
