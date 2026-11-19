// Why: gen's tests "with std" need generators written in Mk, checked and
// lowered as the driver would, without importing the driver.
// What: mkWorld, a harness that builds modules from Mk source (add,
// addLoose, addStd), runs their gen lines (stage4, genLine, genArg) and
// serves eval's world (Decl, Body, Layout, Const).
// How: resolve, check and lower run in the driver's order, stage 4 between
// resolve's Finish and Settle; std.core is a small prelude plus the real
// std/core/core_gen.mk. Generators that need std's library code (derive's enum
// forms) run only in std's TestDeriveTargetsRun.

package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/ast"
	"mk/internal/check"
	"mk/internal/diag"
	"mk/internal/eval"
	"mk/internal/lower"
	"mk/internal/mir"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// mkPrelude is std.core's text besides core_gen.mk: the names derive.mk and
// the sketches mention outside their quotes, with std's signatures, and the
// interfaces and primitive impls the generated code calls (Eq, Hash, Format,
// Hasher, Writer), so stage 5 checks the output of every gen line. System is
// here because check counts the prelude among a module's dependencies, whose
// impls it sees, through Known.Slice, Cell or System (computeDeps).
const mkPrelude = `export struct System {
	p: *u8
}
export struct Arena {
	p: *u8
}
export struct Text {
	p: *u8
	n: int
}
export error OutOfMemory
export error WriteError
export Text.New(a: &Arena): Text
export Text.Push(mut self, s: str): OutOfMemory!void
export Text.Str(&self): str from self
export Arena.Dup(&self, s: str): OutOfMemory!str from self
export struct Hasher {
	v: u64
}
export Hasher.U64(mut self, x: u64)
export struct Writer {
	p: *u8
}
export Writer.Str(mut self, s: str): WriteError!void
export interface Eq {
	Eq(&self, other: &Self): bool
}
export interface Hash {
	Hash(&self, h: mut Hasher)
}
export interface Format {
	Format(&self, w: mut Writer): WriteError!void
}
impl Eq for str {
	Eq(&self, other: &str): bool {
		return true
	}
}
impl Eq for int {
	Eq(&self, other: &int): bool {
		return true
	}
}
impl Hash for str {
	Hash(&self, h: mut Hasher) {
	}
}
impl Hash for int {
	Hash(&self, h: mut Hasher) {
	}
}
impl Format for str {
	Format(&self, w: mut Writer): WriteError!void {
	}
}
impl Format for int {
	Format(&self, w: mut Writer): WriteError!void {
	}
}
`

// readRepo reads a file of the repository relative to its root.
func readRepo(t testing.TB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// stdPreludeNames is std.PreludeNames, read from std/embed.go (gen's tests
// cannot import mk/std).
func stdPreludeNames(t testing.TB) []string {
	t.Helper()
	text := readRepo(t, "std/embed.go")
	i := strings.Index(text, "var PreludeNames = ")
	if i < 0 {
		t.Fatal("std/embed.go has no PreludeNames")
	}
	j := strings.Index(text[i:], "}")
	var out []string
	for _, m := range regexp.MustCompile(`"([A-Za-z0-9]+)"`).FindAllStringSubmatch(text[i:i+j], -1) {
		out = append(out, m[1])
	}
	if len(out) < 50 {
		t.Fatalf("std/embed.go: %d prelude names", len(out))
	}
	return out
}

// mkWorld is one build of Mk test modules.
type mkWorld struct {
	t      testing.TB
	sm     *source.SourceMap
	in     *source.Interner
	items  *types.Items
	tab    *types.Table
	consts *types.ConstTable
	rs     *resolve.Session
	cfg    *check.Config
	c      *check.Checker
	lcfg   *lower.Config
	ev     *eval.Evaluator
	known  types.KnownItems
	core   types.ModuleID
	mods   map[string]*mkMod

	mu     sync.Mutex
	bodies map[types.ItemID]*mir.Body
}

// mkMod is one module through stage 5 and lowering.
type mkMod struct {
	id    types.ModuleID
	path  string
	info  *resolve.Info
	files []*ast.File
	m     *check.Module
	// lines are the module's gen lines as given to gen, and gen its output
	// (nil when the module has no gen line).
	lines []Line
	ctx   Context
	gen   *Output
	// diags are every diagnostic of the module, Wrap applied.
	diags []*diag.Diagnostic
}

// newMkWorld makes a world whose std.core is mkPrelude plus the real
// core_gen.mk.
func newMkWorld(t testing.TB) *mkWorld {
	t.Helper()
	return newMkWorldCore(t, readRepo(t, "std/core/core_gen.mk"))
}

// newMkWorldCore is newMkWorld with coreGen as core_gen.mk's text (a test
// of an API method gen implements before std declares it).
func newMkWorldCore(t testing.TB, coreGen string) *mkWorld {
	t.Helper()
	w := &mkWorld{t: t, sm: source.NewSourceMap(), in: source.NewInterner(), items: types.NewItems(),
		consts: types.NewConstTable(), mods: map[string]*mkMod{}, bodies: map[types.ItemID]*mir.Body{}}
	w.tab = types.NewTable(w.in, w.items, w.consts)
	w.rs = resolve.NewSession(&resolve.Config{Interner: w.in, Sources: w.sm, Items: w.items,
		PreludePath: "std.core", PreludeNames: stdPreludeNames(t)})
	w.cfg = &check.Config{Interner: w.in, Types: w.tab, Items: w.items, Consts: w.consts, Target: types.X86_64Windows}
	m := w.add("std.core", true, map[string]string{"prelude.mk": mkPrelude, "core_gen.mk": coreGen})
	w.core = m.id
	return w
}

// addStd adds a std module from the repository's std/ tree, one file.
func (w *mkWorld) addStd(path, rel string) *mkMod {
	return w.add(path, true, map[string]string{filepath.Base(rel): readRepo(w.t, rel)})
}

// add parses, resolves (running its gen lines between Finish and Settle),
// checks and lowers module path. files maps a file name to its text; files
// are taken in name order. Any diagnostic fails the test unless the
// caller reads mkMod.diags (addLoose).
func (w *mkWorld) add(path string, std bool, files map[string]string) *mkMod {
	w.t.Helper()
	m := w.addLoose(path, std, files)
	if len(m.diags) > 0 {
		w.t.Fatalf("%s: want no diagnostics, got:\n%s", path, w.dump(m.diags))
	}
	return m
}

// addLoose is add, keeping the module's diagnostics in mkMod.diags.
func (w *mkWorld) addLoose(path string, std bool, files map[string]string) *mkMod {
	w.t.Helper()
	m := &mkMod{path: path}
	mod := &types.Module{Path: path, Std: std}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		src := w.sm.Add(strings.ReplaceAll(path, ".", "/")+"/"+n, []byte(files[n]))
		f, ds := syntax.ParseFile(src, w.in, syntax.Options{AllowBodiless: std})
		if len(ds) > 0 {
			w.t.Fatalf("%s: parse:\n%s", src.Path, w.dump(ds))
		}
		mod.Files = append(mod.Files, src.ID)
		m.files = append(m.files, f)
	}
	m.id = w.items.AddModule(mod)
	in := &resolve.ModuleInput{Module: m.id, Files: m.files, Imports: map[string]types.ModuleID{}}
	if path != "std.core" {
		in.Prelude = w.core
	}
	for _, f := range m.files {
		for _, il := range resolve.ImportsOf(f, w.in) {
			dep, ok := w.mods[il.Path]
			if !ok {
				w.t.Fatalf("%s imports %s, which the test did not add", path, il.Path)
			}
			in.Imports[il.Path] = dep.id
		}
	}
	rm := w.rs.Begin(in)
	m.info = rm.Finish(1)
	m.diags = append(m.diags, m.info.Diags...)
	w.stage4(m, rm)
	ds, gs := rm.Settle()
	m.diags = append(m.diags, ds...)
	for _, d := range gs {
		m.diags = append(m.diags, w.wrap(m, d))
	}
	if m.gen != nil {
		m.diags = append(m.diags, m.ctx.Finish(m.gen, func(n source.Symbol) types.ItemID {
			if id, ok := rm.Lookup(n); ok {
				return id
			}
			return types.NoItem
		})...)
	}
	w.mods[path] = m
	if w.c == nil {
		w.startSemantics()
	}
	m.m = w.c.Signatures(&check.ModuleInput{Module: m.id, Files: m.files, Info: m.info})
	for _, d := range m.m.Diags {
		m.diags = append(m.diags, w.wrap(m, d))
	}
	for _, id := range m.m.Units() {
		it := w.items.Get(id)
		var body *check.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { body = w.c.Check(m.m, id) }); ie != nil {
			m.diags = append(m.diags, diag.FromInternal(ie))
			continue
		}
		if body == nil {
			continue
		}
		if len(body.Diags) > 0 {
			for _, d := range body.Diags {
				m.diags = append(m.diags, w.wrap(m, d))
			}
			continue
		}
		var mb *mir.Body
		if ie := source.Guard("lower "+it.Key.String(), it.Span, func() { mb = lower.Lower(w.lcfg, lower.FromCheck(w.lcfg, w.c, body)) }); ie != nil {
			m.diags = append(m.diags, diag.FromInternal(ie))
			continue
		}
		w.mu.Lock()
		w.bodies[id] = mb
		w.mu.Unlock()
	}
	return m
}

// startSemantics is the driver's (internal/driver/evaluator.go): the Target
// item, Known from std.core, the Evaluator over this world, the Checker.
func (w *mkWorld) startSemantics() {
	if ti, ok := w.items.ByKey(types.ItemKey("std.core", "TargetInfo")); ok {
		tiIt := w.items.Get(ti)
		w.items.AddItem(&types.Item{Module: tiIt.Module, Name: source.SymTarget, Kind: types.KindConst, Exported: true,
			Span: tiIt.Span, Key: types.ItemKey("std.core", "Target"), Decl: &types.ConstDecl{Type: w.tab.Named(ti, nil)}})
	}
	w.known = w.rs.Known()
	w.cfg.Known = &w.known
	w.ev = eval.New(eval.Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Program: w,
		Known: w.known, Target: types.X86_64Windows, TargetItem: w.known.Target})
	w.cfg.Eval = w.ev
	w.c = check.New(w.cfg)
	w.lcfg = &lower.Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Known: &w.known,
		Impls: lower.ImplsOf(w.c), Sources: w.sm, Validate: true}
}

func (w *mkWorld) wrap(m *mkMod, d *diag.Diagnostic) *diag.Diagnostic {
	if m.gen == nil || m.gen.File == nil {
		return d
	}
	return Wrap(d, m.gen)
}

// stage4 mirrors internal/driver/gen.go's stage4: one Line per gen line,
// Run, then AddGenerated.
func (w *mkWorld) stage4(m *mkMod, rm *resolve.Module) {
	if len(m.info.Gens) == 0 {
		return
	}
	if w.c == nil {
		w.t.Fatalf("%s: a gen line in the prelude", m.path)
	}
	view := w.c.GenView(&check.ModuleInput{Module: m.id, Files: m.files, Info: m.info})
	for _, g := range m.info.Gens {
		m.lines = append(m.lines, w.genLine(m.info, view, g))
	}
	slices.SortStableFunc(m.lines, func(x, y Line) int { return x.Index - y.Index })
	m.ctx = w.context(view)
	m.gen = m.ctx.Run(m.id, m.lines)
	m.diags = append(m.diags, m.gen.Diags...)
	if m.gen.AST == nil {
		return
	}
	gf := resolve.GeneratedFile{File: m.gen.AST}
	for _, it := range m.gen.AST.Items {
		if o, ok := m.gen.OriginOf(it); ok {
			gf.Origins = append(gf.Origins, resolve.NodeOrigin{Node: it.ID(), Origin: o.GenOrigin()})
		}
	}
	for _, o := range m.gen.Origins {
		gf.Names = append(gf.Names, resolve.NameOrigin{Offset: o.Offset, Kind: resolve.NameKind(o.Kind), Module: o.Module, Item: o.Item})
	}
	rm.AddGenerated([]resolve.GeneratedFile{gf})
}

// context is the gen.Context of a module whose stage-4 view is view.
func (w *mkWorld) context(view *check.GenView) Context {
	return Context{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Sources: w.sm, Eval: w.ev,
		World: mkGenWorld{view}, Known: w.known, Target: types.X86_64Windows, Workers: 1}
}

// genLine is the driver's genLine.
func (w *mkWorld) genLine(info *resolve.Info, view *check.GenView, g resolve.GenLine) Line {
	fi := info.Files[g.File]
	var node *ast.GenItem
	for _, it := range fi.File.Items {
		if gi, ok := it.(*ast.GenItem); ok && gi.ID() == g.Node {
			node = gi
		}
	}
	if node == nil {
		w.t.Fatalf("gen line %v has no GenItem", g.Item)
	}
	k := w.items.Get(g.Item).Key.String()
	var idx int
	if _, err := fmt.Sscanf(k[strings.LastIndex(k, "#gen(")+len("#gen("):], "%d)", &idx); err != nil {
		w.t.Fatalf("%q is not a gen key", k)
	}
	l := Line{Item: g.Item, Node: node, Index: idx, Span: node.Span(), Generator: g.Generator}
	if node.Generator != nil {
		var parts []string
		for _, id := range node.Generator.Names {
			parts = append(parts, w.in.Str(id.Name))
		}
		l.Path = strings.Join(parts, ".")
	}
	for _, a := range node.Args {
		l.Args = append(l.Args, w.genArg(fi, g.File, view, a))
	}
	switch f := node.Fuel.(type) {
	case nil:
	case *ast.BasicLit:
		l.FuelLit = f
	default:
		if r := mkRefOf(fi, f); r.Kind == resolve.RefItem && w.items.Get(types.ItemID(r.A)).Kind == types.KindConst {
			l.FuelItem = types.ItemID(r.A)
		} else {
			l.FuelOther = true
		}
	}
	if node.Fuel != nil {
		l.FuelSpan = node.Fuel.Span()
	}
	return l
}

// genArg mirrors the driver's genArg.
func (w *mkWorld) genArg(fi *resolve.FileInfo, file int, view *check.GenView, a ast.TypeOrExpr) Arg {
	sp := a.Span()
	switch x := a.(type) {
	case *ast.BasicLit:
		return Arg{Kind: ArgLiteral, Lit: x, Span: sp}
	case *ast.UnaryExpr:
		if lit, ok := x.X.(*ast.BasicLit); ok && x.Op == ast.TokMinus {
			return Arg{Kind: ArgLiteral, Lit: lit, Neg: true, Span: sp}
		}
	case *ast.Ident:
		if r := mkRefOf(fi, x); r.Kind == resolve.RefUniverse {
			if s := w.in.Str(x.Name); s == "true" || s == "false" {
				return Arg{Kind: ArgLiteral, Lit: &ast.BasicLit{Base: x.Base, Raw: s}, Span: sp}
			}
		}
	}
	if r := mkRefOf(fi, a); r.Kind == resolve.RefItem && w.items.Get(types.ItemID(r.A)).Kind == types.KindInterface {
		return Arg{Kind: ArgInterface, Item: types.ItemID(r.A), Span: sp}
	}
	t, name := view.TypeOf(file, a)
	switch {
	case t != types.NoType:
		return Arg{Kind: ArgType, Type: t, Span: sp}
	case name != 0:
		return Arg{Kind: ArgDeferred, Name: name, Span: sp}
	}
	return Arg{Kind: ArgOther, Span: sp}
}

func mkRefOf(fi *resolve.FileInfo, n ast.Node) resolve.Ref {
	switch x := n.(type) {
	case *ast.Ident, *ast.FieldExpr, *ast.Path:
	case *ast.PathType:
		if x.Args != nil || x.Path == nil {
			return resolve.Ref{}
		}
		n = x.Path
	default:
		return resolve.Ref{}
	}
	id := int(n.ID())
	if id <= 0 || id >= len(fi.Refs) {
		return resolve.Ref{}
	}
	return fi.Refs[id]
}

// mkGenWorld is the driver's genWorld: gen.World over check's stage-4 view.
type mkGenWorld struct{ v *check.GenView }

func (g mkGenWorld) Decl(item types.ItemID, from types.ModuleID) (*DeclView, bool) {
	d, ok := g.v.Decl(item, from)
	if !ok {
		return nil, false
	}
	fields := func(fs []check.GenField) []FieldView {
		var out []FieldView
		for _, f := range fs {
			out = append(out, FieldView{Name: f.Name, Type: f.Type, Unresolved: f.Unresolved, Exported: f.Exported, Span: f.Span})
		}
		return out
	}
	out := &DeclView{Item: d.Item, Kind: d.Kind, Exported: d.Exported, Generics: d.Generics, Fields: fields(d.Fields)}
	for _, vr := range d.Variants {
		out.Variants = append(out.Variants, VariantView{Name: vr.Name, Fields: fields(vr.Fields), Span: vr.Span})
	}
	for _, mt := range d.Methods {
		out.Methods = append(out.Methods, MethodView{Sig: mt.Sig, Span: mt.Span})
	}
	return out, true
}

func (mkGenWorld) Hash(types.ItemID, types.ReadKind, string) source.Hash { return source.Hash{} }

// eval.Program over the lowered bodies: a generic instance is not served
// (no mono here), and a bodiless item is BodyMissing, which eval answers
// through the host for a generator API intrinsic.

func (w *mkWorld) Body(fn types.ItemID, args []types.GenericArg) eval.Body {
	w.mu.Lock()
	defer w.mu.Unlock()
	if b := w.bodies[fn]; b != nil && len(args) == 0 {
		return eval.Body{MIR: b, State: eval.BodyReady}
	}
	return eval.Body{State: eval.BodyMissing}
}

func (w *mkWorld) Layout(t types.TypeID) (*types.Layout, source.Hash) {
	l := w.c.Layout(t)
	return &l, source.Hash{}
}

func (w *mkWorld) LayoutName(args []types.GenericArg) string { return fmt.Sprint(args) }

func (w *mkWorld) Const(item types.ItemID) eval.Const {
	if d, ok := w.items.Get(item).Decl.(*types.ConstDecl); ok && d.HasValue {
		return eval.Const{Value: d.Value}
	}
	return eval.Const{Poisoned: true}
}

func (w *mkWorld) ConstHash(types.ConstID) source.Hash      { return source.Hash{} }
func (w *mkWorld) TargetHash(types.TargetField) source.Hash { return source.Hash{} }

// dump renders diagnostics for a failure message.
func (w *mkWorld) dump(ds []*diag.Diagnostic) string {
	var b strings.Builder
	for _, d := range ds {
		at := "?"
		if f := w.sm.File(d.At.File); f != nil {
			p := f.LineCol(d.At.Start)
			at = fmt.Sprintf("%s:%d:%d", f.Path, p.Line, p.Col)
		}
		fmt.Fprintf(&b, "  %s: %v %s", at, d.Code, d.Message)
		for _, f := range d.Facts {
			if f.Value.Kind == diag.FactText {
				fmt.Fprintf(&b, " %s=%q", f.Key, f.Value.Str)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
