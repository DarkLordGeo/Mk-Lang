// Why: resolve's tests need real modules without the driver.
// What: the test harness: world, built, opt, fakePrelude, fakeCatalog and
// helpers that find refs, items, reads and diagnostics.
// How: parses Mk source (a test-only import of syntax) and resolves it in
// driver order (Begin, Finish, Settle) against a fake prelude.

package resolve

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// preludeNames is the prelude's list of names, in its order.
var preludeNames = []string{"Alloc", "Arch", "Arena", "Box", "Canceled", "Cell", "Chan", "Chunks", "ChunksMut", "Closed", "Eq",
	"Format", "Free", "Guard", "Hash", "Hasher", "Heap", "HeapList", "HeapMap", "HeapText", "Io", "Limits", "Lines",
	"List", "Map", "MapIter", "Mutex", "Ord", "Os", "OutOfMemory", "ParseError", "Path", "Print", "ReadError",
	"Reader", "Replace", "Scalars", "Scope", "Set", "Slice", "Split", "StackSize", "Swap", "System", "TargetInfo", "Text",
	"TextMap", "TextMapIter", "Utf8Error", "Words", "WriteError", "Writer"}

// fakePrelude declares every prelude name but Path (a use of it is the
// ordinary E0201 here), plus two std-internal exports that must stay
// invisible.
func fakePrelude() string {
	var b strings.Builder
	generic := map[string]string{"Box": "[T]", "Cell": "[T]", "Chan": "[T]", "Guard": "[T]", "HeapList": "[T]",
		"HeapMap": "[K, V]", "List": "[T]", "Map": "[K, V]", "MapIter": "[K, V]", "Mutex": "[T]", "Set": "[T]",
		"Scope": "[E]", "TextMap": "[V]", "TextMapIter": "[V]", "Chunks": "[T]", "ChunksMut": "[T]"}
	for _, n := range preludeNames {
		switch n {
		case "Path":
		case "Canceled", "Closed", "OutOfMemory", "ParseError", "ReadError", "Utf8Error", "WriteError":
			fmt.Fprintf(&b, "export error %s\n", n)
		case "Eq", "Ord", "Hash", "Format", "Free", "Writer", "Reader":
			fmt.Fprintf(&b, "export interface %s {}\n", n)
		case "Slice":
			b.WriteString("export interface Slice[T] {}\n")
		case "Print":
			b.WriteString("export Print(x: int)\n")
		case "Arch", "Os", "StackSize":
			fmt.Fprintf(&b, "export enum %s { A }\n", n)
		default:
			fmt.Fprintf(&b, "export struct %s%s {}\n", n, generic[n])
		}
	}
	b.WriteString("export struct Duration {}\nexport struct IoTable {}\n")
	return b.String()
}

// fakeCatalog answers ExportedBy from a fixed table.
type fakeCatalog struct {
	exports  map[string][]string
	searched []string
}

func (c *fakeCatalog) ExportedBy(name string) ([]string, []string) {
	return c.exports[name], c.searched
}

// world is one build of test modules.
type world struct {
	t     testing.TB
	sm    *source.SourceMap
	in    *source.Interner
	items *types.Items
	cfg   *Config
	s     *Session
	cat   *fakeCatalog
	mu    sync.Mutex
	mods  map[string]*built
	core  types.ModuleID
}

// built is one resolved test module.
type built struct {
	w     *world
	id    types.ModuleID
	path  string
	m     *Module
	info  *Info
	srcs  []*source.File
	files []*ast.File
	// diags are Begin and Finish's, then Settle's.
	diags []*diag.Diagnostic
	// parsed are the parser's diagnostics under opt.parseErrors.
	parsed []*diag.Diagnostic
	in     *source.Interner
}

// opt configures one test module.
type opt struct {
	std, bin, noPrelude, noSettle, parseErrors bool
	testPrelude                                string
	workers                                    int
	hasGen                                     func(types.ModuleID) bool
}

func newWorld(t testing.TB, withPrelude bool) *world {
	t.Helper()
	w := &world{t: t, sm: source.NewSourceMap(), in: source.NewInterner(), items: types.NewItems(), mods: map[string]*built{}}
	w.cat = &fakeCatalog{exports: map[string][]string{}}
	w.cfg = &Config{Interner: w.in, Sources: w.sm, Items: w.items, Catalog: w.cat}
	if withPrelude {
		w.cfg.PreludePath = "std.core"
		w.cfg.PreludeNames = preludeNames
	}
	w.s = NewSession(w.cfg)
	if withPrelude {
		b := w.add("std.core", opt{std: true, noPrelude: true}, fakePrelude())
		w.core = b.id
	}
	return w
}

// begin parses and declares a module without finishing it.
func (w *world) begin(path string, o opt, texts ...string) *built {
	w.t.Helper()
	b := &built{w: w, path: path, in: w.in}
	mod := &types.Module{Path: path, Std: o.std}
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
		b.parsed = append(b.parsed, ds...)
		mod.Files = append(mod.Files, src.ID)
		b.srcs = append(b.srcs, src)
		b.files = append(b.files, f)
	}
	b.id = w.items.AddModule(mod)
	in := &ModuleInput{Module: b.id, Files: b.files, Imports: map[string]types.ModuleID{}, BinRoot: o.bin, HasGen: o.hasGen}
	if !o.noPrelude && w.core != types.NoModule {
		in.Prelude = w.core
	}
	if o.testPrelude != "" {
		in.TestPrelude = b.srcs[0].ID
	}
	for _, f := range b.files {
		for _, il := range ImportsOf(f, w.in) {
			w.mu.Lock()
			if dep, ok := w.mods[il.Path]; ok {
				in.Imports[il.Path] = dep.id
			}
			w.mu.Unlock()
		}
	}
	b.m = w.s.Begin(in)
	w.mu.Lock()
	w.mods[path] = b
	w.mu.Unlock()
	return b
}

// add parses, declares, finishes and (unless noSettle) settles a module.
func (w *world) add(path string, o opt, texts ...string) *built {
	w.t.Helper()
	b := w.begin(path, o, texts...)
	b.finish(o)
	return b
}

func (b *built) finish(o opt) {
	b.info = b.m.Finish(o.workers)
	b.diags = append(b.diags, b.info.Diags...)
	if !o.noSettle {
		b.settle()
	}
}

func (b *built) settle() {
	ds, gs := b.m.Settle()
	b.diags = append(b.diags, ds...)
	b.diags = append(b.diags, gs...)
}

// lc renders a span as line:col of its file.
func (b *built) lc(s source.Span) string {
	f := b.w.sm.File(s.File)
	if f == nil {
		return "?"
	}
	p := f.LineCol(s.Start)
	return fmt.Sprintf("%d:%d", p.Line, p.Col)
}

// codes renders the diagnostics sorted as "E0201@3:5" (file index prefix
// when not the last file).
func (b *built) codes() []string {
	var out []string
	for _, d := range b.diags {
		out = append(out, d.Code.String()+"@"+b.lc(d.At))
	}
	sort.Strings(out)
	return out
}

// codeSet is codes joined by spaces.
func (b *built) codeSet() string { return strings.Join(b.codes(), " ") }

// only returns the diagnostics of one code.
func (b *built) only(c diag.Code) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	for _, d := range b.diags {
		if d.Code == c {
			out = append(out, d)
		}
	}
	return out
}

// dump prints every diagnostic for a failure message.
func (b *built) dump() string {
	var sb strings.Builder
	for _, d := range b.diags {
		fmt.Fprintf(&sb, "  %v@%s %s", d.Code, b.lc(d.At), d.Message)
		for _, f := range d.Facts {
			fmt.Fprintf(&sb, " %s=%s", f.Key, factString(f.Value))
		}
		for _, fx := range d.Fixes {
			fmt.Fprintf(&sb, " fix(%v:%d edits)", fx.Class, len(fx.Edits))
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

// fact returns a diagnostic's fact as text.
func fact(d *diag.Diagnostic, key string) string {
	f, ok := d.FactValue(key)
	if !ok {
		return ""
	}
	return factString(f)
}

// want checks the exact sorted code list.
func (b *built) want(t testing.TB, codes ...string) {
	t.Helper()
	sort.Strings(codes)
	if got := b.codeSet(); got != strings.Join(codes, " ") {
		t.Errorf("%s: got %q, want %q\n%s", b.path, got, strings.Join(codes, " "), b.dump())
	}
}

// ident finds the n-th (0-based) Ident spelled name in file fi and returns
// its Ref.
func (b *built) ref(name string, n int) Ref {
	for _, fi := range b.info.Files {
		var found []*ast.Ident
		ast.Inspect(fi.File, func(x ast.Node) bool {
			if id, ok := x.(*ast.Ident); ok && b.in.Str(id.Name) == name {
				found = append(found, id)
			}
			return x != nil
		})
		if n < len(found) {
			return fi.Refs[found[n].ID()]
		}
		n -= len(found)
	}
	b.w.t.Fatalf("%s: no ident %q #%d", b.path, name, n)
	return Ref{}
}

// item finds an item by key.
func (w *world) item(key string) *types.Item {
	id, ok := w.items.ByKey(types.KeyFromString(key))
	if !ok {
		w.t.Fatalf("no item %s", key)
	}
	return w.items.Get(id)
}

// parsedFile is one parse for helper tests.
type parsedFile struct {
	files []*ast.File
	in    *source.Interner
}

func parseOne(t testing.TB, text string) parsedFile {
	t.Helper()
	sm := source.NewSourceMap()
	in := source.NewInterner()
	src := sm.Add("t.mk", []byte(text))
	f, ds := syntax.ParseFile(src, in, syntax.Options{})
	for _, d := range ds {
		t.Fatalf("parse: %v %s", d.Code, d.Message)
	}
	return parsedFile{files: []*ast.File{f}, in: in}
}

// readsOf returns the reads of the item with key k.
func readsOf(b *built, k string) []types.Read {
	items := b.w.items
	for _, it := range b.info.Items {
		if items.Get(it.Item).Key.String() == k {
			return it.Reads.Sorted()
		}
	}
	return nil
}

// hasRead reports whether item k recorded a read.
func hasRead(b *built, k string, kind types.ReadKind, key, name string) bool {
	for _, r := range readsOf(b, k) {
		if r.Kind == kind && r.Key.String() == key && (name == "" || r.Name == name) {
			return true
		}
	}
	return false
}

// fieldExprRefs maps each FieldExpr's printed chain to its Ref.
func fieldExprRefs(b *built) map[string]Ref {
	out := map[string]Ref{}
	for _, fi := range b.info.Files {
		ast.Inspect(fi.File, func(n ast.Node) bool {
			if fe, ok := n.(*ast.FieldExpr); ok {
				out[chainText(b.in, fe)] = fi.Refs[fe.ID()]
			}
			return n != nil
		})
	}
	return out
}

func chainText(in *source.Interner, e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return in.Str(e.Name)
	case *ast.FieldExpr:
		return chainText(in, e.X) + "." + in.Str(e.Name.Name)
	}
	return "?"
}

func itoa(i int) string { return fmt.Sprint(i) }
