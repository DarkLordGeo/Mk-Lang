// Why: a std function's result sources are public facts callers borrow by, so
// they must match the reference and never be ambiguous.
// What: TestComputedSources and its plant, no-loan and generic variants.
// How: compares check's computed sources with each written clause and with
// doc/reference.md's "Library surface" lines; with no clause, one parameter.

package stdtest

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/syntax"
	"mk/internal/types"
)

// sourceMods are the std modules whose sources are judged on x86_64-windows
// (sourceModsFor).
var sourceMods = sourceModsFor(types.X86_64Windows)

// sourceModsFor is every std module that the library surface declares
// lines for, with what they import, in import order: std.sys is the
// target's directory (std/pkg.mk's targets), under which std.rt and
// std.task sit, and std.fs, std.net and std.proc import std.rt.
func sourceModsFor(target types.Target) []modDir {
	return slices.Concat(layerMods, []modDir{sysMod(target), {"std.rt", "rt"}, {"std.task", "task"},
		{"std.sync", "sync"}, {"std.time", "time"}, {"std.fs", "fs"}, {"std.net", "net"}, {"std.proc", "proc"}})
}

// sysMod is std.sys for target, as std/pkg.mk's targets select it.
func sysMod(target types.Target) modDir {
	switch target.OS {
	case types.OSWindows:
		return modDir{"std.sys", "sys/windows"}
	case types.OSNutshell:
		return modDir{"std.sys", "sys/nutshell"}
	}
	return modDir{"std.sys", "sys/linux"}
}

// sourceTargets are the targets TestComputedSources judges: one per
// std.sys directory std ships (std/sys/nutshell is a placeholder).
var sourceTargets = []types.Target{types.X86_64Windows, types.X86_64Linux, types.X86_64Nutshell}

// srcString spells a computed source as a `from` clause names it: the
// parameter, then each field of its path.
func (cs *checkedStd) srcString(sig *types.Signature, s types.Source) string {
	p := sig.Param(s.Param)
	out := cs.in.Str(p.Name)
	cur := p.Type
	for _, st := range s.Path {
		for k := cs.tab.Kind(cur); k == types.KRef || k == types.KMut; k = cs.tab.Kind(cur) {
			cur = cs.tab.Get(cur).Elem
		}
		name := fmt.Sprintf("#%d", st.Field)
		if ty := cs.tab.Get(cur); ty.Kind == types.KNamed {
			if d, ok := cs.items.Get(ty.Item).Decl.(*types.StructDecl); ok && int(st.Field) < len(d.Fields) {
				name = cs.in.Str(d.Fields[st.Field].Name)
			}
		}
		out += "." + name
		cur = st.Type
	}
	return out
}

// computedSources is check's answer for fn as from-clause strings, sorted.
func (cs *checkedStd) computedSources(sig *types.Signature) []string {
	var out []string
	for _, s := range sig.Flows.Sources {
		out = append(out, cs.srcString(sig, s))
	}
	slices.Sort(out)
	return out
}

// refLine is one function line of the library surface (doc/reference.md's
// "Library surface" section).
type refLine struct {
	module string // the section's module, "" for the prelude part
	owner  string // the receiver or namespace type, "" for a free function
	name   string
	from   []string // the written from clause, sorted; nil when none
	line   int
}

// sectionModule is the module a library-surface comment line opens
// ("// std.fs. File and Error below ...", "// std.x509, Experimental. ...",
// "// std.coll"), or "" for any other line. Every such comment opens a
// section, so the lines of a module no fixed list names (std.simd's Vec and
// Mask among them) are never read as the previous section's.
func sectionModule(l string) string {
	rest, ok := strings.CutPrefix(l, "// std.")
	if !ok {
		return ""
	}
	word, _, _ := strings.Cut(rest, " ")
	word = strings.TrimRight(word, ".,")
	if word == "" || strings.ContainsFunc(word, func(r rune) bool { return !(r == '.' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') }) {
		return ""
	}
	return "std." + word
}

// refLines parses the library surface's block with the compiler's parser
// and returns its function lines with their section.
func (cs *checkedStd) refLines(t *testing.T) []refLine {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "doc", "reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(text), "\r\n", "\n")
	at := strings.Index(doc, "\n## 18. ")
	if at < 0 {
		t.Fatal("doc/reference.md: no standard library section")
	}
	// The surface is the section's first `verdict: parse` block: the section
	// also holds ok and reject blocks, and ok blocks come first, so take the
	// block internal/lsp's surface generator takes.
	sec := doc[at+1:]
	if next := strings.Index(sec, "\n## "); next >= 0 {
		sec = sec[:next]
	}
	start, end := -1, -1
	for from := 0; ; {
		open := strings.Index(sec[from:], "\n```mk\n")
		if open < 0 {
			break
		}
		s := from + open + len("\n```mk\n")
		e := strings.Index(sec[s:], "\n```")
		if e < 0 {
			break
		}
		if strings.HasPrefix(sec[s:], "// verdict: parse") {
			start, end = at+1+s, e
			break
		}
		from = s + e + len("\n```")
	}
	if start < 0 {
		t.Fatal("the standard library section: no verdict: parse block")
	}
	block := doc[start : start+end+1]
	lineBase := strings.Count(doc[:start], "\n") + 1
	src := cs.sm.Add("doc/reference.md#18", []byte(block))
	f, ds := syntax.ParseFile(src, cs.in, syntax.Options{AllowBodiless: true})
	for _, d := range ds {
		t.Fatalf("the standard library section: parse %v: %s", d.Code, d.Message)
	}
	// The module of each block line: a `// std.x` comment opens a section
	// that lasts to the next one (as in TestAPIMatchesReference).
	lines := strings.Split(block, "\n")
	modAt := make([]string, len(lines)+1)
	cur := ""
	for i, l := range lines {
		if m := sectionModule(l); m != "" {
			cur = m
		}
		modAt[i+1] = cur
	}
	var out []refLine
	for _, d := range f.Items {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Sig == nil || fd.Sig.Name == nil {
			continue
		}
		ln := int(src.LineCol(fd.Span().Start).Line)
		r := refLine{module: modAt[ln], name: cs.in.Str(fd.Sig.Name.Name.Name), line: lineBase + ln - 1}
		if fd.Sig.Name.Owner != nil {
			r.owner = cs.in.Str(fd.Sig.Name.Owner.Name)
		}
		for _, fl := range fd.Sig.Flows {
			ff, ok := fl.(*ast.FromFlow)
			if !ok {
				continue
			}
			for _, s := range ff.Sources {
				var parts []string
				for _, n := range s.Names {
					parts = append(parts, cs.in.Str(n.Name))
				}
				r.from = append(r.from, strings.Join(parts, "."))
			}
		}
		slices.Sort(r.from)
		out = append(out, r)
	}
	return out
}

// lookup is the std function a library-surface line names: in its section's
// module when that module declares it, else in the prelude (prelude routing,
// reduced to what this test's modules need).
func (cs *checkedStd) lookup(r refLine) (types.ItemID, bool) {
	var mods []string
	if r.module != "" {
		mods = append(mods, r.module)
	}
	mods = append(mods, "std.core")
	for _, m := range mods {
		k := types.ItemKey(m, r.name)
		// `time.Now` under `// std.time` is a free function of std.time;
		// `Duration.Nanos` is a member.
		if r.owner != "" && r.owner != m[strings.LastIndex(m, ".")+1:] {
			k = types.MemberKey(m, r.owner, r.name)
		}
		if id, ok := cs.items.ByKey(k); ok {
			return id, true
		}
	}
	return types.NoItem, false
}

// typeParams adds to out the generic indices t mentions (KSelf as
// selfIndex): written here from the generic source rule, not from check's
// walk.
func (cs *checkedStd) typeParams(t types.TypeID, out map[uint32]bool) {
	ty := cs.tab.Get(t)
	switch ty.Kind {
	case types.KSelf:
		out[selfIndex] = true
	case types.KParam:
		out[ty.Index] = true
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional, types.KArray, types.KErrorUnion:
		cs.typeParams(ty.Elem, out)
	case types.KRecord:
		for _, f := range ty.Fields {
			cs.typeParams(f.Type, out)
		}
	case types.KNamed:
		for _, a := range ty.Args {
			if !a.IsConst {
				cs.typeParams(a.Type, out)
			}
		}
	case types.KFn:
		for _, fp := range ty.Fn.Params {
			cs.typeParams(fp.Type, out)
		}
		cs.typeParams(ty.Fn.Result, out)
	}
}

const selfIndex = ^uint32(0)

// genericSuppliers is the generic source rule's answer for a result whose type
// mentions a type parameter: every parameter whose type mentions one of them
// (a T result may be any argument of type T), as from-clause names, sorted;
// nil when the result mentions none.
func (cs *checkedStd) genericSuppliers(sig *types.Signature) []string {
	rp := map[uint32]bool{}
	cs.typeParams(sig.Result, rp)
	if len(rp) == 0 {
		return nil
	}
	var out []string
	for i := range sig.Params {
		pp := map[uint32]bool{}
		cs.typeParams(sig.Params[i].Type, pp)
		for k := range pp {
			if rp[k] || rp[selfIndex] || k == selfIndex {
				out = append(out, cs.in.Str(sig.Params[i].Name))
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

// sourceFinding judges one function's computed sources: a written
// clause that check took (no E0607); or else, with no clause, at most one
// parameter, or exactly the generic source rule's answer when the result's type
// mentions a type parameter (math.Min[T](a: T, b: T): T is from a, b), or
// any answer when the result's type holds no loan (resultHoldsNoLoan);
// never an ambiguous answer.
func (cs *checkedStd) sourceFinding(sig *types.Signature) string {
	fl := sig.Flows
	got := cs.computedSources(sig)
	switch {
	case fl.ClauseError:
		return "its from clause got E0607"
	case fl.Ambiguous:
		return fmt.Sprintf("the automatic answer is ambiguous (E0601): %v", got)
	case !fl.Declared && len(got) > 1:
		if g := cs.genericSuppliers(sig); g != nil && slices.Equal(g, got) {
			return ""
		}
		if cs.resultHoldsNoLoan(sig) {
			return ""
		}
		return fmt.Sprintf("no written clause, and %d parameters supply the result: %v (R10's generic answer %v); T2 wants the clause written", len(got), got, cs.genericSuppliers(sig))
	}
	return ""
}

// resultHoldsNoLoan reports whether sig's result type can hold no loan, so
// that the result needs no source and is accepted with no written clause: a
// number, bool or char, an array or record of such values, a struct (not a
// view) whose fields are such values once its generic arguments are put in, or
// a type parameter bounded by std.simd's Lane (the integers, int, uint, f32
// and f64, computed by the compiler and never implemented, E0704). So
// std.simd's Vec and Mask over a Lane hold no loan. Anything else (a
// reference, slice, str, optional, function, an enum, a type parameter with
// another bound) may hold one.
func (cs *checkedStd) resultHoldsNoLoan(sig *types.Signature) bool {
	lane := func(i uint32) bool {
		if cs.known.Lane == types.NoItem || int(i) >= len(sig.Generics) || sig.Generics[i].Const {
			return false
		}
		for _, b := range sig.Generics[i].Bounds {
			if b.Kind == types.BoundInterface && b.Item == cs.known.Lane {
				return true
			}
		}
		return false
	}
	return cs.holdsNoLoan(sig.Result, lane, 0)
}

// holdsNoLoan is resultHoldsNoLoan's walk over t, where param answers for
// a type parameter of the scope t is written in (the signature's, or a
// struct's own, whose arguments are walked in the outer scope).
func (cs *checkedStd) holdsNoLoan(t types.TypeID, param func(uint32) bool, depth int) bool {
	if depth > 16 {
		return false
	}
	ty := cs.tab.Get(t)
	switch ty.Kind {
	case types.KInt, types.KIntPtr, types.KFloat, types.KBool, types.KChar:
		return true
	case types.KArray:
		return cs.holdsNoLoan(ty.Elem, param, depth+1)
	case types.KParam:
		return param(ty.Index)
	case types.KRecord:
		for _, f := range ty.Fields {
			if !cs.holdsNoLoan(f.Type, param, depth+1) {
				return false
			}
		}
		return true
	case types.KNamed:
		sd, ok := cs.items.Get(ty.Item).Decl.(*types.StructDecl)
		if !ok || sd.View {
			return false
		}
		args := ty.Args
		inner := func(i uint32) bool {
			return int(i) < len(args) && !args[i].IsConst && cs.holdsNoLoan(args[i].Type, param, depth+1)
		}
		for _, f := range sd.Fields {
			if !cs.holdsNoLoan(f.Type, inner, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// sourceExcepted are functions whose finding is known and owned
// elsewhere, with why. It is empty. std.simd's Vec.Gather and Vec.Shuffle
// are reached because std.core imports std.simd. Shuffle's clause
// (`from self, b`) is written in vec.mk, its mirror
// internal/check/testdata/simd/vec.mk and the library surface. Gather has
// none: a written `from xs` over the by-value `[]T` makes a generic
// caller's Gather hold a loan of the place xs, and its result, a Vec over a
// Lane, holds no loan, which resultHoldsNoLoan accepts.
var sourceExcepted = map[string]string{}

// sourcesReport is what TestComputedSources saw.
type sourcesReport struct {
	findings                   []string
	fns, declared, auto, noSrc int
	generic                    int
	refMatched, refSkipped     int
	refUnfound                 []string
	skippedMods                map[string]bool
	judgedMods                 map[string]int
}

func computedSourcesReport(t *testing.T, target types.Target) *sourcesReport {
	t.Helper()
	mods := sourceModsFor(target)
	cs := checkStdModsFor(t, mods, target)
	rep := &sourcesReport{skippedMods: map[string]bool{}, judgedMods: map[string]int{}}
	for _, m := range cs.mods {
		for _, ii := range m.info.Items {
			it := cs.items.Get(ii.Item)
			fd, ok := it.Decl.(*types.FuncDecl)
			if !ok || it.Kind != types.KindFunc {
				continue
			}
			rep.fns++
			switch {
			case fd.Sig.Flows.Declared:
				rep.declared++
			case len(fd.Sig.Flows.Sources) > 1:
				rep.generic++
			case len(fd.Sig.Flows.Sources) > 0:
				rep.auto++
			default:
				rep.noSrc++
			}
			if why := cs.sourceFinding(&fd.Sig); why != "" && sourceExcepted[it.Key.String()] == "" {
				rep.findings = append(rep.findings, fmt.Sprintf("%s: %s", it.Key, why))
			}
		}
	}
	loaded := map[string]bool{}
	for _, m := range mods {
		loaded[m.path] = true
	}
	for _, r := range cs.refLines(t) {
		if r.module != "" && !loaded[r.module] {
			rep.refSkipped++
			rep.skippedMods[r.module] = true
			continue
		}
		id, ok := cs.lookup(r)
		if !ok {
			rep.refUnfound = append(rep.refUnfound, fmt.Sprintf("doc/reference.md:%d %s.%s", r.line, r.owner, r.name))
			continue
		}
		it := cs.items.Get(id)
		fd, ok := it.Decl.(*types.FuncDecl)
		if !ok {
			rep.refUnfound = append(rep.refUnfound, fmt.Sprintf("doc/reference.md:%d: %s is not a function", r.line, it.Key))
			continue
		}
		got := cs.computedSources(&fd.Sig)
		switch {
		case r.from != nil && !slices.Equal(got, r.from):
			rep.findings = append(rep.findings, fmt.Sprintf("doc/reference.md:%d %s: the reference writes from %s, check computes %v", r.line, it.Key, strings.Join(r.from, ", "), got))
		// sourceFinding's two exceptions: the generic source rule's answer,
		// and a result that holds no loan (std.simd's Vec.Gather).
		case r.from == nil && len(got) > 1 && !slices.Equal(got, cs.genericSuppliers(&fd.Sig)) && !cs.resultHoldsNoLoan(&fd.Sig):
			rep.findings = append(rep.findings, fmt.Sprintf("doc/reference.md:%d %s: the reference writes no clause, check computes %v (more than one parameter)", r.line, it.Key, got))
		}
		rep.refMatched++
		rep.judgedMods[r.module]++
	}
	return rep
}

// TestComputedSources: every function of sourceMods passes
// sourceFinding, and every library-surface line of those modules has the
// sources it writes (or one parameter). A surface line std does not declare is
// TestAPIMatchesReference's failure (package std), logged here.
func TestComputedSources(t *testing.T) {
	for _, target := range sourceTargets {
		t.Run(target.Name, func(t *testing.T) {
			rep := computedSourcesReport(t, target)
			for _, f := range rep.findings {
				t.Errorf("%s (05 section 9 T2)", f)
			}
			for _, u := range rep.refUnfound {
				t.Logf("not declared in the loaded modules: %s", u)
			}
			if rep.refMatched < 190 {
				t.Errorf("only %d standard library section lines matched a std function; the reader lost the block", rep.refMatched)
			}
			// Each loaded module that the library surface has a section for
			// is judged (so std.simd's lines are not read as std.math's).
			for _, m := range []string{"std.simd", "std.math", "std.atomic", "std.sync", "std.time", "std.fs", "std.net", "std.proc"} {
				if rep.judgedMods[m] == 0 {
					t.Errorf("no standard library section line of %s was judged: the reader lost its section", m)
				}
			}
			if rep.refSkipped != 0 {
				t.Logf("%d standard library section lines skipped, in modules this test does not load: %v", rep.refSkipped, slices.Sorted(maps.Keys(rep.skippedMods)))
			}
			t.Logf("%d functions: %d with a written clause, %d with one automatic source, %d with the generic answer, %d with none; the standard library section: %d lines judged, %d skipped",
				rep.fns, rep.declared, rep.auto, rep.generic, rep.noSrc, rep.refMatched, rep.refSkipped)
		})
	}
}

// TestComputedSourcesSeesPlants: in a std copy, str.ToLower's
// `from a` changed to `from self` (a parameter that can also supply a str,
// so check takes it) differs from the surface's clause, and str.Split
// without its `from self, sep` is an ambiguous automatic answer, and
// std.fs's ReadText `from a` changed to `from p` differs from the surface
// (a module above std.rt, so std.sys's directory is loaded); each is a
// finding, and nothing else is.
func TestComputedSourcesSeesPlants(t *testing.T) {
	plantStd(t, sourceMods, map[string]func(string) string{
		"core/text_case.mk": func(s string) string {
			return strings.Replace(s, "export str.ToLower(self, a: &Arena): OutOfMemory!str from a {", "export str.ToLower(self, a: &Arena): OutOfMemory!str from self {", 1)
		},
		"core/core_str.mk": func(s string) string {
			return strings.Replace(s, "export str.Split(self, sep: str): Split from self, sep {", "export str.Split(self, sep: str): Split {", 1)
		},
		"fs/fs.mk": func(s string) string {
			return strings.Replace(s, "export ReadText(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from a {", "export ReadText(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from p {", 1)
		},
	})
	rep := computedSourcesReport(t, types.X86_64Windows)
	seen := map[string]bool{}
	for _, f := range rep.findings {
		switch {
		case strings.Contains(f, "std.core.str:ToLower"):
			seen["ToLower"] = true
		case strings.Contains(f, "std.core.str:Split"):
			seen["Split"] = true
		case strings.Contains(f, "std.fs.ReadText"):
			seen["ReadText"] = true
		default:
			t.Errorf("finding outside the plants: %s", f)
		}
		t.Logf("planted finding: %s", f)
	}
	for _, want := range []string{"ToLower", "Split", "ReadText"} {
		if !seen[want] {
			t.Errorf("planted %s: no finding", want)
		}
	}
}

// TestComputedSourcesNoLoanResult: sourceFinding's no-loan branch.
// std.simd's Vec.Gather has no written clause and two parameters that supply
// its result, which is not the generic source rule's answer, and it passes
// only because its result, a Vec over a Lane, holds no loan; the same
// signature with the result a slice of that Lane is a finding again, and so is
// one whose type parameter loses its Lane bound.
func TestComputedSourcesNoLoanResult(t *testing.T) {
	cs := checkStdModsFor(t, sourceMods, types.X86_64Windows)
	id, ok := cs.items.ByKey(types.MemberKey("std.simd", "Vec", "Gather"))
	if !ok {
		t.Fatal("no std.simd.Vec:Gather")
	}
	sig := cs.items.Get(id).Decl.(*types.FuncDecl).Sig
	got := cs.computedSources(&sig)
	if sig.Flows.Declared || len(got) < 2 || slices.Equal(got, cs.genericSuppliers(&sig)) {
		t.Fatalf("Vec:Gather: declared %v, check computes %v, R10's answer %v; this test expects no clause and an answer beyond R10's", sig.Flows.Declared, got, cs.genericSuppliers(&sig))
	}
	if !cs.resultHoldsNoLoan(&sig) {
		t.Fatal("Vec:Gather's result (a Vec over a Lane) is said to hold a loan")
	}
	if why := cs.sourceFinding(&sig); why != "" {
		t.Errorf("Vec:Gather as checked: %s; want no finding", why)
	}
	res := cs.tab.Get(sig.Result)
	if res.Kind != types.KNamed || len(res.Args) == 0 || res.Args[0].IsConst {
		t.Fatalf("Vec:Gather's result is not Vec[T, N]: kind %v", res.Kind)
	}
	slice := sig
	slice.Result = cs.tab.Slice(res.Args[0].Type)
	unbound := sig
	unbound.Generics = slices.Clone(sig.Generics)
	for i := range unbound.Generics {
		unbound.Generics[i].Bounds = nil
	}
	for name, s := range map[string]*types.Signature{"result []T": &slice, "T unbounded": &unbound} {
		if why := cs.sourceFinding(s); why == "" {
			t.Errorf("Vec:Gather with %s and sources %v: no finding", name, got)
		} else {
			t.Logf("Vec:Gather with %s: %s", name, why)
		}
	}
}

// TestComputedSourcesJudgesGeneric: sourceFinding's generic branch,
// which no std plant reaches (check's generic answer is what the branch
// accepts, so a std edit makes check's answer change with it). math.Min[T](a:
// T, b: T): T is accepted with check's sources [a b]; the same sources are a
// finding when the result does not mention T (a []int, which may hold a loan:
// an int result holds none, which resultHoldsNoLoan accepts), and when b's
// type does not (the generic answer is then [a]).
func TestComputedSourcesJudgesGeneric(t *testing.T) {
	cs := checkStdMods(t, layerMods)
	id, ok := cs.items.ByKey(types.ItemKey("std.math", "Min"))
	if !ok {
		t.Fatal("no std.math.Min")
	}
	sig := cs.items.Get(id).Decl.(*types.FuncDecl).Sig
	if got := cs.computedSources(&sig); !slices.Equal(got, []string{"a", "b"}) || sig.Flows.Declared || sig.Flows.Ambiguous {
		t.Fatalf("math.Min: check computes %v (declared %v, ambiguous %v); this test expects R10's [a b]", got, sig.Flows.Declared, sig.Flows.Ambiguous)
	}
	if why := cs.sourceFinding(&sig); why != "" {
		t.Errorf("math.Min as checked: %s; want no finding", why)
	}
	intT := cs.tab.IntType(64, true)
	notGenericResult := sig
	notGenericResult.Result = cs.tab.Slice(intT)
	bNotGeneric := sig
	bNotGeneric.Params = slices.Clone(sig.Params)
	bNotGeneric.Params[1].Type = intT
	for name, s := range map[string]*types.Signature{"result []int": &notGenericResult, "b: int": &bNotGeneric} {
		if why := cs.sourceFinding(s); why == "" {
			t.Errorf("math.Min with %s and sources [a b]: no finding", name)
		} else {
			t.Logf("math.Min with %s: %s", name, why)
		}
	}
}
