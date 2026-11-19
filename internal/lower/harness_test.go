// Why: lower's tests need checked modules built from Mk source without the
// driver.
// What: the world type (newWorld, add), fakePrelude and the std stubs, and
// fact dumps (dump, factString).
// How: modules are parsed (the test-only edge lower -> syntax), resolved in
// driver order against a fake prelude and checked, then lowered. Copied from
// internal/check/checktest_test.go (unexported there), with the Checker
// created after std.core resolves so Config.Known is set.

package lower

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/check"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// preludeNames is the language reference's prelude list, in its order. The
// generator API's names (Gen, Type, Code, ...) and HashKey are left out, as
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
		// TargetInfo's fields, as check's harness declares them; Target
		// is a value of this type. Without them `Target.arch` is E0203 and
		// a verdict-ok block's body never reaches lower.
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
			// The release protocol, in the shape check keys the release
			// bounds on.
			b.WriteString("export interface Free {\n\tFree(self, a: &Alloc)\n}\n")
		case "Swap":
			b.WriteString("export Swap[T](a: mut T, b: mut T)\n\t*a into b, *b into a\n")
		case "Replace":
			b.WriteString("export Replace[T](dst: mut T, x: T into dst): T\n")
		case "Slice":
			// Unlike check's harness, Slice declares Items and ItemsMut, as
			// std does, so implementer loops lower.
			b.WriteString("export interface Slice[T] {\n\tItems(&self): []T from self\n\tItemsMut(mut self): mut []T from self\n}\n")
			for _, c := range []string{"List", "HeapList"} {
				fmt.Fprintf(&b, "impl[T] Slice[T] for %s[T] {\n\tItems(&self): []T from self {\n\t\tPanic(\"x\")\n\t}\n\tItemsMut(mut self): mut []T from self {\n\t\tPanic(\"x\")\n\t}\n}\n", c)
			}
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
	// Duration.Nanos as std/core declares it (the cookbook's retry doubles
	// a wait).
	b.WriteString("export struct Duration {}\nexport Duration.Nanos(self): i64\nexport struct IoTable {}\n")
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
	// The generator API: not prelude names, found by Known in the prelude
	// module.
	b.WriteString("export struct Gen {\n\tp: *u8\n}\nexport struct Type {\n\tp: *u8\n}\nexport struct Code {\n\tp: *u8\n}\n")
	b.WriteString("export Gen.Emit(mut self, c: Code)\nexport Type.Name(&self): str\n")
	b.WriteString("export Io.Scope(self): Scope\nexport Io.ScopeOf[E](self): Scope[E]\nexport Scope.Cancel(&self)\n")
	// Scope deadlines, as doc/reference.md's ReadSome example uses them.
	b.WriteString("export Scope.CancelAfter(&self, d: Duration)\nexport Scope.Expired(&self): bool\nexport Scope.Remaining(&self): ?Duration\n")
	return b.String()
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
	cfg    *check.Config
	c      *check.Checker
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
	m     *check.Module
	// rdiags are resolve's; cdiags check's stage-5 diagnostics.
	rdiags []*diag.Diagnostic
	// pdiags are the parser's (kept, not reported, under opt.parseErrors);
	// missing are import paths no module of the world supplies (the
	// driver reports those before resolve).
	pdiags  []*diag.Diagnostic
	missing []string
}

// opt configures one test module.
type opt struct {
	std, bin, noPrelude, parseErrors, unsafe bool
	testPrelude                              string
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
	w.cfg = &check.Config{Interner: w.in, Types: w.tab, Items: w.items, Consts: w.consts, Target: types.X86_64Windows}
	if withPrelude {
		b := w.add("std.core", opt{std: true, noPrelude: true, parseErrors: custom}, core)
		w.core = b.id
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
		if len(ds) > 0 && !o.parseErrors {
			for _, d := range ds {
				w.t.Errorf("%s: parse %v at %d: %s", name, d.Code, d.At.Start, d.Message)
			}
			w.t.FailNow()
		}
		b.pdiags = append(b.pdiags, ds...)
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
			} else {
				b.missing = append(b.missing, il.Path)
			}
		}
	}
	rm := w.rs.Begin(in)
	b.info = rm.Finish(1)
	b.rdiags = append(b.rdiags, b.info.Diags...)
	ds, gs := rm.Settle()
	b.rdiags = append(append(b.rdiags, ds...), gs...)
	w.mods[path] = b
	if w.c == nil {
		if path == "std.core" && o.std {
			w.addTargetItem()
			k := w.rs.Known() // the driver's Config.Known, before std.core's stage 5
			w.cfg.Known = &k
		}
		w.c = check.New(w.cfg)
	}
	if !o.stopBefore {
		b.m = w.c.Signatures(&check.ModuleInput{Module: b.id, Files: b.files, Info: b.info, BinRoot: o.bin})
	}
	return b
}

// addTargetItem is the driver's (internal/driver/target.go): the
// const item std.core.Target of type TargetInfo, with no source and no
// value, added before Known is read so Known.Target names it and the
// universe name Target lowers to RvConstRef of it. Without it every
// `Target.arch` is a pending-lowering ICE (corpusPending).
func (w *world) addTargetItem() {
	ti, ok := w.items.ByKey(types.ItemKey("std.core", "TargetInfo"))
	if !ok {
		return
	}
	key := types.ItemKey("std.core", "Target")
	if _, ok := w.items.ByKey(key); ok {
		return
	}
	tiIt := w.items.Get(ti)
	w.items.AddItem(&types.Item{Module: tiIt.Module, Name: source.SymTarget, Kind: types.KindConst,
		Exported: true, Span: tiIt.Span, Key: key, Decl: &types.ConstDecl{Type: w.tab.Named(ti, nil)}})
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
