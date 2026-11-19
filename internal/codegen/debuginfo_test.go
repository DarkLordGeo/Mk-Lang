// Why: debug info has edge cases each debugger rejects or misreads in its
// own way.
// What: tests of unsigned enumerators of 2^63 and above, the member names of
// ?T and payload enums, lexical scopes across a for {} loop left by break,
// two same-line locals of one name, the COFF LTO link of native objects with
// Debug, the Debug link flags, and the subprograms of spawn thunks and
// system-stack adapters.
// How: the emitted metadata is parsed into nodes (mdNodes) and checked.

package codegen

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// mdNodes maps every numbered metadata node of a module to its text.
func mdNodes(ll string) map[string]string {
	nodes := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^(!\d+) = (.*)$`).FindAllStringSubmatch(ll, -1) {
		nodes[m[1]] = m[2]
	}
	return nodes
}

// mdField is the value of field key in a node's text ("" when absent).
func mdField(txt, key string) string {
	m := regexp.MustCompile(`[(, ]` + key + `: ("[^"]*"|[^,)]+)`).FindStringSubmatch(txt)
	if m == nil {
		return ""
	}
	return m[1]
}

// mdTuple is the operands of a tuple node !{!a, null, !b}, null included.
func mdTuple(nodes map[string]string, ref string) []string {
	body := strings.TrimSuffix(strings.TrimPrefix(nodes[ref], "!{"), "}")
	if body == "" {
		return nil
	}
	return strings.Split(body, ", ")
}

// varType is the type node of the first local variable named v.
func varType(t *testing.T, ll string, nodes map[string]string, v string) string {
	t.Helper()
	m := regexp.MustCompile(`!DILocalVariable\(name: "` + v + `",.* type: (!\d+)\)`).FindStringSubmatch(ll)
	if m == nil {
		t.Fatalf("no variable %s", v)
	}
	return m[1]
}

// members is the members of a composite type, by name: each member's node
// text, in order.
func members(nodes map[string]string, comp string) ([]string, map[string][]string) {
	var names []string
	by := map[string][]string{}
	for _, r := range mdTuple(nodes, mdField(nodes[comp], "elements")) {
		txt := nodes[r]
		if !strings.Contains(txt, "tag: DW_TAG_member") {
			continue
		}
		n, _ := strconv.Unquote(mdField(txt, "name"))
		names = append(names, n)
		by[n] = append(by[n], txt)
	}
	return names, by
}

// TestDebugEnumeratorUnsigned: a payload-free enum over u64 with
// discriminants 2^63 and 2^64-1 prints them in unsigned decimal next to
// isUnsigned: true, a signed i64 enum prints -1 without it, and clang
// accepts the pack for both formats (it refused "unsigned enumerator with
// negative value" before).
func TestDebugEnumeratorUnsigned(t *testing.T) {
	build := func(e *testEnv) []*mir.Body {
		m := e.enumT("M", types.U64, []uint64{0, 1 << 63, ^uint64(0)})
		s := e.enumT("SI", types.I64, []uint64{0, ^uint64(0)})
		f := e.body(e.fn("Enums", nil, types.Void, 0))
		f.userLocal("m", m, f.at(2, 5))
		f.userLocal("si", s, f.at(3, 5))
		f.ret(f.at(4, 1))
		return []*mir.Body{f.b}
	}
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
		e := newTestEnv()
		ll := emitDebug(t, e, unitOf(e, build(e)), target, types.Dev)
		for _, want := range []string{
			`!DIEnumerator(name: "VA", value: 0, isUnsigned: true)`,
			`!DIEnumerator(name: "VB", value: 9223372036854775808, isUnsigned: true)`,
			`!DIEnumerator(name: "VC", value: 18446744073709551615, isUnsigned: true)`,
			`!DIEnumerator(name: "VB", value: -1)`,
		} {
			if !strings.Contains(ll, want) {
				t.Errorf("%s: no %s", target.Name, want)
			}
		}
		for _, opt := range []string{"-O0", "-O2"} {
			clangC(t, target, t.TempDir(), "enums", ll, opt)
		}
	}
}

// debugTagged is fn Tagged() with locals o: ?int, p: P (VA(fa: int),
// VB(fb: bool, fc: i32), VC) and eu: Bad!int.
func debugTagged(e *testEnv) []*mir.Body {
	pay := e.enumT("P", types.NoType, []uint64{0, 1, 2}, []types.TypeID{types.Int}, []types.TypeID{types.Bool, types.I32})
	d := e.items.Get(e.tt.Get(pay).Item).Decl.(*types.EnumDecl)
	d.Variants[0].Fields[0].Name = e.in.Intern("fa")
	d.Variants[1].Fields[0].Name = e.in.Intern("fb")
	d.Variants[1].Fields[1].Name = e.in.Intern("fc")
	bad := e.errItem("Bad", types.Int)
	eu := e.tt.ErrorUnion(e.tt.ErrorSet([]types.ErrorMember{{Item: bad}}), types.Int)
	f := e.body(e.fn("Tagged", nil, types.Void, 0))
	f.userLocal("o", e.tt.Optional(types.Int), f.at(2, 5))
	f.userLocal("p", pay, f.at(3, 5))
	f.userLocal("eu", eu, f.at(4, 5))
	f.ret(f.at(5, 1))
	return []*mir.Body{f.b}
}

// TestDebugTaggedMembers: ?int is {some: bool, value: int} with one member
// named some (a DW_ATE_boolean byte at offset 0) and value at offset 64;
// every variant of a payload enum with fields, a one-field one included,
// is a member structure named by the variant holding the named fields
// (VA: struct {fa: int}); E!T stays {code, ok: T}. debug/dwarf reads the
// same shapes back from an ELF object.
func TestDebugTaggedMembers(t *testing.T) {
	e := newTestEnv()
	ll := emitDebug(t, e, unitOf(e, debugTagged(e)), types.X86_64Linux, types.Dev)
	nodes := mdNodes(ll)
	names, by := members(nodes, varType(t, ll, nodes, "o"))
	if !slices.Equal(names, []string{"some", "value"}) {
		t.Errorf("?int members %v, want [some value]", names)
	} else {
		if bt := nodes[mdField(by["some"][0], "baseType")]; !strings.Contains(bt, "DW_ATE_boolean") || mdField(by["some"][0], "offset") != "" {
			t.Errorf("some: %s of %s, want a bool at offset 0", by["some"][0], bt)
		}
		if mdField(by["value"][0], "offset") != "64" {
			t.Errorf("value: %s, want offset 64", by["value"][0])
		}
	}
	names, by = members(nodes, varType(t, ll, nodes, "p"))
	if !slices.Equal(names, []string{"tag", "VA", "VB"}) {
		t.Fatalf("P members %v, want [tag VA VB]", names)
	}
	for v, want := range map[string][]string{"VA": {"fa"}, "VB": {"fb", "fc"}} {
		st := mdField(by[v][0], "baseType")
		if !strings.Contains(nodes[st], "DW_TAG_structure_type") || mdField(nodes[st], "name") != strconv.Quote(v) {
			t.Errorf("%s: member type %s, want a structure named %s", v, nodes[st], v)
			continue
		}
		if got, _ := members(nodes, st); !slices.Equal(got, want) {
			t.Errorf("%s: fields %v, want %v", v, got, want)
		}
	}
	if names, _ = members(nodes, varType(t, ll, nodes, "eu")); len(names) < 2 || names[0] != "code" || names[1] != "ok" {
		t.Errorf("Bad!int members %v, want code, ok first", names)
	}
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
		e := newTestEnv()
		clangC(t, target, t.TempDir(), "tagged", emitDebug(t, e, unitOf(e, debugTagged(e)), target, types.Dev), "-O0")
	}
	obj := clangC(t, types.X86_64Linux, t.TempDir(), "tagged", ll, "-O0")
	f, err := elf.Open(obj)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*dwarf.StructType{}
	for r := d.Reader(); ; {
		ent, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if ent == nil {
			break
		}
		if ent.Tag != dwarf.TagVariable {
			continue
		}
		name, _ := ent.Val(dwarf.AttrName).(string)
		if off, ok := ent.Val(dwarf.AttrType).(dwarf.Offset); ok {
			if ty, err := d.Type(off); err == nil {
				if st, ok := ty.(*dwarf.StructType); ok {
					got[name] = st
				}
			}
		}
	}
	o := got["o"]
	if o == nil || len(o.Field) != 2 || o.Field[0].Name != "some" || o.Field[1].Name != "value" || o.Field[1].ByteOffset != 8 {
		t.Fatalf("DWARF ?int: %+v", o)
	}
	if _, ok := o.Field[0].Type.(*dwarf.BoolType); !ok {
		t.Errorf("DWARF ?int some: %T, want a bool", o.Field[0].Type)
	}
	p := got["p"]
	if p == nil || len(p.Field) != 3 || p.Field[1].Name != "VA" {
		t.Fatalf("DWARF P: %+v", p)
	}
	if va, ok := p.Field[1].Type.(*dwarf.StructType); !ok || len(va.Field) != 1 || va.Field[0].Name != "fa" {
		t.Errorf("DWARF P.VA: %v, want struct VA {fa}", p.Field[1].Type)
	}
}

// debugLoop is, as lowering builds it (StScopeEnd on every edge out of the
// body's scope: the break and the fall-through to the header),
//
//	fn Loop(c: bool): int {        // line 1
//	    for {                      // line 2
//	        y := 1                 // line 3
//	        if c { break }         // line 4
//	    }
//	    y := 2                     // line 6
//	    return y                   // line 7
//	}
//
// b0 -> header -> body (y1, switch c) -> brk (end y1) -> exit (y2), or
// -> latch (end y1) -> header. exit is dominated by body.
func debugLoop(e *testEnv) []*mir.Body {
	f := e.body(e.fn("Loop", []types.TypeID{types.Bool}, types.Int, 0))
	header, body, brk, latch, exit := f.newBlock(), f.newBlock(), f.newBlock(), f.newBlock(), f.newBlock()
	f.term(mir.Terminator{Kind: mir.TermGoto, Target: header, Span: f.at(2, 5)})
	f.cur = header
	f.term(mir.Terminator{Kind: mir.TermGoto, Target: body, Span: f.at(2, 5)})
	f.cur = body
	y1 := f.userLocal("y", types.Int, f.at(3, 9))
	f.assign(place(y1), use(mir.Imm(types.Int, 1)), f.at(3, 9))
	f.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(1)),
		Cases: f.b.AddCases(mir.SwitchCase{Value: 1, Target: brk}), Default: latch, Span: f.at(4, 9)})
	scopeEnd := func(l mir.LocalID, sp mir.SpanID) {
		blk := &f.b.Blocks[f.cur]
		blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StScopeEnd, Local: l, Span: sp})
	}
	f.cur = brk
	scopeEnd(y1, f.at(4, 16))
	f.term(mir.Terminator{Kind: mir.TermGoto, Target: exit, Span: f.at(4, 16)})
	f.cur = latch
	scopeEnd(y1, f.at(5, 5))
	f.term(mir.Terminator{Kind: mir.TermGoto, Target: header, Span: f.at(5, 5)})
	f.cur = exit
	y2 := f.userLocal("y", types.Int, f.at(6, 5))
	f.assign(place(y2), use(mir.Imm(types.Int, 2)), f.at(6, 5))
	f.assign(place(0), use(mir.Copy(place(y2))), f.at(7, 5))
	scopeEnd(y2, f.at(7, 5))
	f.ret(f.at(7, 5))
	return []*mir.Body{f.b}
}

// TestDebugLoopSiblingScopes: the y of a for {} body and the sibling y
// after the loop get two lexical blocks of the subprogram, neither inside
// the other, and the code after the loop (y2's return) is not in the
// first y's block. clang accepts the pack for both formats.
func TestDebugLoopSiblingScopes(t *testing.T) {
	e := newTestEnv()
	ll := emitDebug(t, e, unitOf(e, debugLoop(e)), types.X86_64Linux, types.Dev)
	nodes := mdNodes(ll)
	sp := regexp.MustCompile(`define .*@mk\.main\.Loop\(.*!dbg (!\d+) \{`).FindStringSubmatch(ll)
	if sp == nil {
		t.Fatal("no subprogram on Loop")
	}
	var ys []string
	for _, m := range regexp.MustCompile(`!DILocalVariable\(name: "y", scope: (!\d+), file: !\d+, line: (\d+)`).FindAllStringSubmatch(ll, -1) {
		ys = append(ys, m[1])
	}
	if len(ys) != 2 || ys[0] == ys[1] {
		t.Fatalf("y scopes %v, want two distinct", ys)
	}
	ancestors := func(ref string) []string {
		var out []string
		for r := mdField(nodes[ref], "scope"); strings.Contains(nodes[r], "DILexicalBlock"); r = mdField(nodes[r], "scope") {
			out = append(out, r)
		}
		return out
	}
	for i, y := range ys {
		if !strings.HasPrefix(nodes[y], "distinct !DILexicalBlock(scope: "+sp[1]+",") {
			t.Errorf("y%d scope %s = %s, want a lexical block of the subprogram %s", i+1, y, nodes[y], sp[1])
		}
		if a := ancestors(y); slices.Contains(a, ys[1-i]) {
			t.Errorf("y%d's block %s is inside the other y's block %s", i+1, y, ys[1-i])
		}
	}
	// The return on line 7 is in y2's block, not y1's.
	ret := regexp.MustCompile(`(?m)^!\d+ = !DILocation\(line: 7, column: 5, scope: (!\d+)\)$`).FindStringSubmatch(ll)
	if ret == nil || ret[1] != ys[1] {
		t.Errorf("line 7 location %v, want scope %s (y2)", ret, ys[1])
	}
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
		e := newTestEnv()
		ll := emitDebug(t, e, unitOf(e, debugLoop(e)), target, types.Dev)
		for _, opt := range []string{"-O0", "-O2"} {
			clangC(t, target, t.TempDir(), "loop", ll, opt)
		}
	}
}

// TestDebugSameLineLocals: { x := 1 } { x := 2 } on one line, both homed in
// the entry block, are two distinct variables, each declared once, and the
// ELF object has two DW_TAG_variable x at two locations.
func TestDebugSameLineLocals(t *testing.T) {
	build := func(e *testEnv) []*mir.Body {
		f := e.body(e.fn("Same", nil, types.Void, 0))
		x1 := f.userLocal("x", types.Int, f.at(2, 7))
		f.assign(place(x1), use(mir.Imm(types.Int, 1)), f.at(2, 7))
		blk := &f.b.Blocks[f.cur]
		blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StScopeEnd, Local: x1, Span: f.at(2, 14)})
		x2 := f.userLocal("x", types.Int, f.at(2, 19))
		f.assign(place(x2), use(mir.Imm(types.Int, 2)), f.at(2, 19))
		blk = &f.b.Blocks[f.cur]
		blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StScopeEnd, Local: x2, Span: f.at(2, 26)})
		f.ret(f.at(3, 1))
		return []*mir.Body{f.b}
	}
	e := newTestEnv()
	ll := emitDebug(t, e, unitOf(e, build(e)), types.X86_64Linux, types.Dev)
	vars := regexp.MustCompile(`(?m)^(!\d+) = distinct !DILocalVariable\(name: "x",`).FindAllStringSubmatch(ll, -1)
	if len(vars) != 2 {
		t.Fatalf("%d variables x, want 2 distinct", len(vars))
	}
	for _, v := range vars {
		if n := strings.Count(ll, ", metadata "+v[1]+", metadata !DIExpression()"); n != 1 {
			t.Errorf("variable %s declared %d times, want 1", v[1], n)
		}
	}
	f, err := elf.Open(clangC(t, types.X86_64Linux, t.TempDir(), "same", ll, "-O0"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	var locs []string
	for r := d.Reader(); ; {
		ent, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if ent == nil {
			break
		}
		if name, _ := ent.Val(dwarf.AttrName).(string); ent.Tag == dwarf.TagVariable && name == "x" {
			loc, _ := ent.Val(dwarf.AttrLocation).([]byte)
			locs = append(locs, string(loc))
		}
	}
	if len(locs) != 2 || locs[0] == locs[1] {
		t.Errorf("DWARF variables x at %q, want two at two locations", locs)
	}
	for _, opt := range []string{"-O0", "-O2"} {
		e := newTestEnv()
		clangC(t, types.X86_64Windows, t.TempDir(), "same", emitDebug(t, e, unitOf(e, build(e)), types.X86_64Windows, types.Dev), opt)
	}
}

// TestDebugLTONativeDebugLink: a COFF link with LTO and Debug whose objects
// are all native (no bitcode, so thinLink has no module) goes through
// pdbLink like every other COFF Debug link: two links in two directories
// give identical executables that do not name their directory, and the PDB
// is beside each.
func TestDebugLTONativeDebugLink(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("lld-link's PDB on a Windows host only")
	}
	a := debugExeWith(t, types.X86_64Windows, types.Release, t.TempDir(), true)
	b := debugExeWith(t, types.X86_64Windows, types.Release, t.TempDir(), true)
	ab, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ab, bb) {
		t.Errorf("two native LTO Debug links in two directories differ (%d and %d bytes)", len(ab), len(bb))
	}
	if bytes.Contains(ab, []byte(filepath.Dir(a))) {
		t.Error("the executable names its directory")
	}
	if _, err := os.Stat(strings.TrimSuffix(a, ".exe") + ".pdb"); err != nil {
		t.Errorf("no PDB beside the executable: %v", err)
	}
}

// TestLinkArgsDebug: Debug adds exactly /debug:full and
// /pdbaltpath:%_PDB% at the end of a COFF command line, dev and release,
// LTO or not; an ELF command line is the same with and without it, but
// for a release link's --strip-all, which Debug drops; and a
// COFF ThinLTO link's index link carries no /debug even when the job has
// Debug.
func TestLinkArgsDebug(t *testing.T) {
	dir := t.TempDir()
	out, o1, o2 := filepath.Join(dir, "t.exe"), filepath.Join(dir, "a.o"), filepath.Join(dir, "b.o")
	lib := filepath.Join(dir, "kernel32.lib")
	for _, rel := range []bool{false, true} {
		for _, lto := range []bool{false, true} {
			coff := toolchain.LinkJob{Format: toolchain.COFF, Output: out, Objects: []string{o1, o2}, Libs: []string{lib},
				Release: rel, LTO: lto}
			plain, err := toolchain.LinkArgs(coff)
			if err != nil {
				t.Fatal(err)
			}
			coff.Debug = true
			dbg, err := toolchain.LinkArgs(coff)
			if err != nil {
				t.Fatal(err)
			}
			if want := append(slices.Clone(plain), "/debug:full", "/pdbaltpath:%_PDB%"); !slices.Equal(dbg, want) {
				t.Errorf("COFF release=%v lto=%v Debug args %q, want %q", rel, lto, dbg, want)
			}
			if slices.ContainsFunc(plain, func(a string) bool { return strings.HasPrefix(strings.ToLower(a), "/debug") }) {
				t.Errorf("COFF release=%v lto=%v without Debug has /debug: %q", rel, lto, plain)
			}
			elfJob := toolchain.LinkJob{Format: toolchain.ELF, Output: filepath.Join(dir, "t"), Objects: []string{o1, o2},
				Release: rel, LTO: lto}
			ep, err := toolchain.LinkArgs(elfJob)
			if err != nil {
				t.Fatal(err)
			}
			elfJob.Debug = true
			ed, err := toolchain.LinkArgs(elfJob)
			if err != nil {
				t.Fatal(err)
			}
			// A release ELF link without Debug strips; Debug drops
			// exactly that --strip-all, and changes nothing else.
			if want := slices.DeleteFunc(slices.Clone(ep), func(a string) bool { return a == "--strip-all" }); !slices.Equal(ed, want) {
				t.Errorf("ELF release=%v lto=%v: Debug args %q, want %q", rel, lto, ed, want)
			}
			if strip := slices.Contains(ep, "--strip-all"); strip != rel {
				t.Errorf("ELF release=%v lto=%v without Debug: --strip-all present %v: %q", rel, lto, strip, ep)
			}
		}
	}
	work := filepath.Join(dir, "mkdt-x")
	ij := toolchain.LinkJob{Format: toolchain.COFF, Output: out, Objects: []string{o1, o2}, Libs: []string{lib},
		Release: true, LTO: true, Debug: true}
	iargs, err := toolchain.ThinIndexArgs(ij, work, []string{"m0000.o", ""})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(iargs, func(a string) bool {
		return strings.HasPrefix(strings.ToLower(a), "/debug") || strings.HasPrefix(a, "/pdb")
	}) {
		t.Errorf("the index link of a Debug job has /debug: %q", iargs)
	}
	for _, want := range []string{"/thinlto-index-only", "m0000.o", o2} {
		if !slices.Contains(iargs, want) {
			t.Errorf("index link args lack %q: %q", want, iargs)
		}
	}
}

// TestDebugThunkSubprograms: with Debug, the spawn thunks and system-stack
// adapters (which keep the line-tables DISubprogram, name = symbol, empty
// subroutine type) are subprograms of the one FullDebug unit, every
// defined function has one, and clang's verifier accepts the packs at -O0
// and -O2 for both formats.
func TestDebugThunkSubprograms(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func(e *testEnv) ([]*mir.Body, []types.ItemID)
		want  *regexp.Regexp
	}{
		{"spawn", spawnCase, regexp.MustCompile(`(?m)^define internal .* @"?mk\.main\.Work\.go"?\(.*!dbg (!\d+) \{`)},
		{"sysstack", func(e *testEnv) ([]*mir.Body, []types.ItemID) { return sysBodies(e, true) },
			regexp.MustCompile(`(?m)^define internal void @mk\.cs\.BigStack\(.*!dbg (!\d+) \{`)},
	} {
		for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
			e := newTestEnv()
			bodies, bodiless := c.build(e)
			ll := emitDebug(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
			nodes := mdNodes(ll)
			cu := regexp.MustCompile(`(?m)^(!\d+) = distinct !DICompileUnit\(.*emissionKind: FullDebug`).FindStringSubmatch(ll)
			if cu == nil {
				t.Fatalf("%s/%s: no FullDebug unit", c.name, target.Name)
			}
			m := c.want.FindStringSubmatch(ll)
			if m == nil {
				t.Fatalf("%s/%s: no thunk or adapter with a subprogram", c.name, target.Name)
			}
			if sp := nodes[m[1]]; !strings.Contains(sp, "!DISubprogram(") || mdField(sp, "unit") != cu[1] {
				t.Errorf("%s/%s: thunk subprogram %s, want one of unit %s", c.name, target.Name, sp, cu[1])
			}
			for _, d := range regexp.MustCompile(`(?m)^define .*$`).FindAllString(ll, -1) {
				if !regexp.MustCompile(`!dbg !\d+ \{$`).MatchString(d) {
					t.Errorf("%s/%s: a definition without a subprogram: %s", c.name, target.Name, d)
				}
			}
			for _, opt := range []string{"-O0", "-O2"} {
				clangC(t, target, t.TempDir(), c.name, ll, opt)
			}
		}
	}
}
