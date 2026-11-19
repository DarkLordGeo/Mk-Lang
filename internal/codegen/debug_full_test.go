// Why: with Config.Debug the pack carries full debug info, and debuggers
// read it only if every record is well formed and in the right place.
// What: tests of the printed text (FullDebug unit, typed subprograms,
// variables, scopes, types), that Debug=false prints what it printed
// before, determinism, and the variables and inlining records in objects.
// How: hand-built bodies are emitted, clang compiles every pack in both
// formats at -O0 and -O2, and DWARF and CodeView objects are read back.

package codegen

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// emitDebug is emit with Config.Debug.
func emitDebug(t *testing.T, e *testEnv, u *Unit, target types.Target, profile types.Profile) string {
	t.Helper()
	for _, f := range u.Funcs {
		if f.Body != nil {
			if err := f.Body.Validate(&mir.Env{Types: e.tt}); err != nil {
				t.Fatalf("%s: %v", f.Symbol, err)
			}
		}
	}
	var b bytes.Buffer
	if _, err := EmitPack(u, 0, e, Config{Target: target, Profile: profile, Debug: true}, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// userLocal adds a named user local of type t declared at span sp.
func (f *fnb) userLocal(name string, t types.TypeID, sp mir.SpanID) mir.LocalID {
	return f.b.AddLocal(mir.Local{Type: t, Kind: mir.LocalUser, Name: f.e.in.Intern(name), Loop: mir.NoLoop, Span: sp})
}

// debugWork is
//
//	fn Work(pa: int): int {      // line 1
//	    x := pa * 3              // line 2
//	    if x > 10 {              // line 3
//	        y := x + 1           // line 4
//	        return Probe(y)      // line 5
//	    } else {
//	        y := x - 1           // line 7
//	        return y             // line 8
//	    }
//	}
//
// and fn Probe(pa: int): int { return pa * pa } (line 11, a checked
// multiply, so its inlined code survives -O2), so the two y are siblings
// and x is homed in the entry block.
func debugWork(e *testEnv) []*mir.Body {
	probe := e.body(e.fn("Probe", []types.TypeID{types.Int}, types.Int, 0))
	probe.assign(place(0), bin(mir.Mul, mir.Copy(place(1)), mir.Copy(place(1))), probe.at(11, 5))
	probe.ret(probe.at(11, 20))
	probe.b.Spans[0] = source.Span{File: 1, Start: 10 * 100, End: 10*100 + 1}

	f := e.body(e.fn("Work", []types.TypeID{types.Int}, types.Int, 0))
	x := f.userLocal("x", types.Int, f.at(2, 5))
	f.assign(place(x), bin(mir.MulWrap, mir.Copy(place(1)), mir.Imm(types.Int, 3)), f.at(2, 5))
	c := f.local(types.Bool)
	f.assign(place(c), bin(mir.Gt, mir.Copy(place(x)), mir.Imm(types.Int, 10)), f.at(3, 8))
	then, els := f.newBlock(), f.newBlock()
	f.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c)),
		Cases: f.b.AddCases(mir.SwitchCase{Value: 1, Target: then}), Default: els, Span: f.at(3, 5)})
	f.cur = then
	y1 := f.userLocal("y", types.Int, f.at(4, 9))
	f.assign(place(y1), bin(mir.AddWrap, mir.Copy(place(x)), mir.Imm(types.Int, 1)), f.at(4, 9))
	ret := f.newBlock()
	f.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: probe.item}},
		Args: f.b.AddArgs(mir.Arg{Operand: mir.Copy(place(y1))}), Dest: place(0), Target: ret, Spawn: mir.NoLocal, Span: f.at(5, 16)})
	f.cur = ret
	f.ret(f.at(5, 9))
	f.cur = els
	y2 := f.userLocal("y", types.Int, f.at(7, 9))
	f.assign(place(y2), bin(mir.SubWrap, mir.Copy(place(x)), mir.Imm(types.Int, 1)), f.at(7, 9))
	f.assign(place(0), use(mir.Copy(place(y2))), f.at(8, 9))
	f.ret(f.at(8, 9))
	return []*mir.Body{f.b, probe.b}
}

func TestDebugOffUnchanged(t *testing.T) {
	for _, c := range emitCases {
		e := newTestEnv()
		bodies, bodiless := c.build(e)
		ll := emit(t, e, unitOf(e, bodies, bodiless...), types.X86_64Linux, types.Dev)
		for _, bad := range []string{"FullDebug", "llvm.dbg.declare", "DILocalVariable", "DIBasicType", "DINamespace"} {
			if strings.Contains(ll, bad) {
				t.Errorf("%s: Debug=false prints %s", c.name, bad)
			}
		}
	}
}

// checkFullText checks a Debug pack: one FullDebug unit, every defined
// function a typed subprogram, every dbg.declare in an entry block with a
// location, and the line-table rules of checkDebugText for instructions.
func checkFullText(t *testing.T, ll string) {
	t.Helper()
	if n := strings.Count(ll, "emissionKind: FullDebug"); n != 1 {
		t.Errorf("%d FullDebug units, want 1", n)
	}
	if strings.Contains(ll, "LineTablesOnly") {
		t.Error("a line-tables-only unit in a Debug pack")
	}
	block := ""
	for i, line := range strings.Split(ll, "\n") {
		switch {
		case strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " "):
			block = strings.TrimSuffix(line, ":")
		case strings.Contains(line, "@llvm.dbg.declare(metadata ptr"):
			if block != "entry" {
				t.Errorf("line %d: dbg.declare in block %s, not entry", i+1, block)
			}
			if !regexp.MustCompile(`metadata !DIExpression\(\)\), !dbg !\d+$`).MatchString(line) {
				t.Errorf("line %d: dbg.declare form: %s", i+1, line)
			}
		}
	}
	if strings.Contains(ll, "@llvm.dbg.declare(") &&
		!strings.Contains(ll, "declare void @llvm.dbg.declare(metadata, metadata, metadata)") {
		t.Error("dbg.declare used but not declared")
	}
	checkDeclaredOnce(t, ll)
}

// checkDeclaredOnce: every DILocalVariable has exactly one
// dbg.declare, in the function whose subprogram is its scope chain's root,
// and the arg numbers of one function's variables are distinct and within
// its subroutine type's parameters (a zero-sized parameter has no record,
// so the numbers may have gaps).
func checkDeclaredOnce(t *testing.T, ll string) {
	t.Helper()
	nodes := mdNodes(ll)
	uses := map[string]int{}
	fnOf := map[string]string{} // variable -> the subprogram of the function declaring it
	sp := ""
	declRe := regexp.MustCompile(`@llvm\.dbg\.declare\(metadata ptr [^,]+, metadata (!\d+), `)
	for _, line := range strings.Split(ll, "\n") {
		if strings.HasPrefix(line, "define ") {
			sp = ""
			if m := regexp.MustCompile(`!dbg (!\d+) \{$`).FindStringSubmatch(line); m != nil {
				sp = m[1]
			}
		}
		if m := declRe.FindStringSubmatch(line); m != nil {
			uses[m[1]]++
			fnOf[m[1]] = sp
		}
	}
	args := map[string]map[string]bool{} // subprogram -> arg numbers
	for ref, txt := range nodes {
		if !strings.Contains(txt, "!DILocalVariable(") {
			continue
		}
		if uses[ref] != 1 {
			t.Errorf("variable %s = %s has %d dbg.declare, want 1", ref, txt, uses[ref])
			continue
		}
		root := mdField(txt, "scope")
		for strings.Contains(nodes[root], "DILexicalBlock") {
			root = mdField(nodes[root], "scope")
		}
		if root != fnOf[ref] {
			t.Errorf("variable %s's scope chain ends at %s, declared in the function of %s", ref, root, fnOf[ref])
		}
		arg := mdField(txt, "arg")
		if arg == "" {
			continue
		}
		if args[root] == nil {
			args[root] = map[string]bool{}
		}
		if args[root][arg] {
			t.Errorf("two variables of %s with arg %s", root, arg)
		}
		args[root][arg] = true
		n, _ := strconv.Atoi(arg)
		params := len(mdTuple(nodes, mdField(nodes[mdField(nodes[root], "type")], "types"))) - 1
		if n < 1 || n > params {
			t.Errorf("variable %s arg %d outside the %d parameters of %s", txt, n, params, root)
		}
	}
	for ref, n := range uses {
		if !strings.Contains(nodes[ref], "!DILocalVariable(") {
			t.Errorf("dbg.declare of %s (%d times), not a variable", ref, n)
		}
	}
}

// TestDebugFullText emits every golden case with Debug for both targets
// and profiles, checks the text, and has clang compile each at -O0 and
// -O2 with -fverify-intermediate-code (LLVM's verifier checks the debug
// info: scopes, variables against their subprograms, types).
func TestDebugFullText(t *testing.T) {
	cases := append([]emitCase(nil), emitCases...)
	cases = append(cases, emitCase{"work", func(e *testEnv) ([]*mir.Body, []types.ItemID) { return debugWork(e), nil }})
	for _, c := range cases {
		for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
			for _, prof := range []types.Profile{types.Dev, types.Release} {
				t.Run(fmt.Sprintf("%s/%s/%d", c.name, target.Name, prof), func(t *testing.T) {
					t.Parallel()
					e := newTestEnv()
					bodies, bodiless := c.build(e)
					ll := emitDebug(t, e, unitOf(e, bodies, bodiless...), target, prof)
					checkFullText(t, ll)
					checkDebugText(t, strings.ReplaceAll(ll, "FullDebug", "LineTablesOnly"), target, prof)
					opt := "-O0"
					if prof == types.Release {
						opt = "-O2"
					}
					clangC(t, target, t.TempDir(), "p", ll, opt)
				})
			}
		}
	}
}

// TestDebugScopes: x (homed in the entry block) is a variable of the
// subprogram, the two sibling y are variables of two distinct lexical
// blocks, each the scope of its own branch's locations, and the parameter
// is arg 1 of the subprogram in namespace main.
func TestDebugScopes(t *testing.T) {
	e := newTestEnv()
	ll := emitDebug(t, e, unitOf(e, debugWork(e)), types.X86_64Linux, types.Dev)
	work := fnText(ll, "mk.main.Work")
	sp := regexp.MustCompile(`define .*@mk\.main\.Work\(.*!dbg (!\d+) \{`).FindStringSubmatch(work)
	if sp == nil {
		t.Fatalf("no subprogram on Work:\n%s", work)
	}
	node := func(ref string) string {
		m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(ref) + ` = (.*)$`).FindStringSubmatch(ll)
		if m == nil {
			t.Fatalf("no node %s", ref)
		}
		return m[1]
	}
	spText := node(sp[1])
	if !strings.Contains(spText, `name: "Work"`) || !strings.Contains(spText, `linkageName: "mk.main.Work"`) {
		t.Errorf("subprogram %s", spText)
	}
	ns := regexp.MustCompile(`scope: (!\d+)`).FindStringSubmatch(spText)
	if ns == nil || node(ns[1]) != `!DINamespace(name: "main", scope: null)` {
		t.Errorf("subprogram scope %v", ns)
	}
	vars := map[string][]string{} // name -> scopes
	for _, m := range regexp.MustCompile(`!DILocalVariable\(name: "(\w+)"(, arg: (\d+))?, scope: (!\d+)`).FindAllStringSubmatch(ll, -1) {
		vars[m[1]] = append(vars[m[1]], m[4]+"/"+m[3])
	}
	if got := vars["pa"]; len(got) != 2 || got[0] != sp[1]+"/1" {
		t.Errorf("pa: %v, want %s arg 1 first (then Probe's)", got, sp[1])
	}
	if got := vars["x"]; len(got) != 1 || got[0] != sp[1]+"/" {
		t.Errorf("x: %v, want the subprogram %s", got, sp[1])
	}
	ys := vars["y"]
	if len(ys) != 2 || ys[0] == ys[1] {
		t.Fatalf("y: %v, want two distinct scopes", ys)
	}
	for _, y := range ys {
		ref := strings.TrimSuffix(y, "/")
		txt := node(ref)
		if !strings.HasPrefix(txt, "distinct !DILexicalBlock(scope: "+sp[1]+",") {
			t.Errorf("y scope %s = %s, want a lexical block of the subprogram", ref, txt)
		}
		// Some location of the function is in that block.
		locs := regexp.MustCompile(`(?m)^(!\d+) = !DILocation\(line: \d+, column: \d+, scope: `+regexp.QuoteMeta(ref)+`\)$`).FindAllStringSubmatch(ll, -1)
		if len(locs) == 0 {
			t.Errorf("no location in y's block %s", ref)
		}
	}
	if n := strings.Count(work, "@llvm.dbg.declare("); n != 4 {
		t.Errorf("%d dbg.declare in Work, want 4 (pa, x, y, y)", n)
	}
}

// TestPackBytesDeterministicDebug is TestPackBytesDeterministic with
// Config.Debug: cold, then 8 goroutines under GOMAXPROCS 1 and 8, the same
// bytes every time.
func TestPackBytesDeterministicDebug(t *testing.T) {
	cases := append([]emitCase(nil), emitCases...)
	cases = append(cases, emitCase{"work", func(e *testEnv) ([]*mir.Body, []types.ItemID) { return debugWork(e), nil }},
		emitCase{"types", func(e *testEnv) ([]*mir.Body, []types.ItemID) { return debugTypes(e), nil }})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv()
			bodies, bodiless := c.build(e)
			u := unitOf(e, bodies, bodiless...)
			cfg := Config{Target: types.X86_64Windows, Profile: types.Release, CPU: "x86-64-v2", Debug: true}
			var cold bytes.Buffer
			if _, err := EmitPack(u, 0, e, cfg, &cold); err != nil {
				t.Fatal(err)
			}
			for _, procs := range []int{1, 8} {
				old := runtime.GOMAXPROCS(procs)
				outs := make([]bytes.Buffer, 8)
				errs := make([]error, 8)
				var wg sync.WaitGroup
				for i := range outs {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, errs[i] = EmitPack(u, 0, e, cfg, &outs[i])
					}()
				}
				wg.Wait()
				runtime.GOMAXPROCS(old)
				for i := range outs {
					if errs[i] != nil {
						t.Fatal(errs[i])
					}
					if !bytes.Equal(outs[i].Bytes(), cold.Bytes()) {
						t.Fatalf("GOMAXPROCS %d, emission %d differs from the cold one", procs, i)
					}
				}
			}
		})
	}
}

// dwarfVars reads an ELF object's DWARF: every variable and formal
// parameter as "subprogram.name:type".
func dwarfVars(t *testing.T, obj string) map[string]bool {
	t.Helper()
	f, err := elf.Open(obj)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	r := d.Reader()
	sub := ""
	for {
		ent, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if ent == nil {
			break
		}
		switch ent.Tag {
		case dwarf.TagSubprogram:
			sub, _ = ent.Val(dwarf.AttrName).(string)
		case dwarf.TagVariable, dwarf.TagFormalParameter:
			name, _ := ent.Val(dwarf.AttrName).(string)
			tn := "?"
			if off, ok := ent.Val(dwarf.AttrType).(dwarf.Offset); ok {
				if ty, err := d.Type(off); err == nil {
					tn = ty.String()
				}
			}
			out[sub+"."+name+":"+tn] = true
		case dwarf.TagInlinedSubroutine:
			out["inlined"] = true
		}
	}
	return out
}

// TestDebugDWARFVariables compiles Work for x86_64-linux at -O0 and reads
// its variables back: Work's pa, x and both y, Probe's pa, all int.
func TestDebugDWARFVariables(t *testing.T) {
	e := newTestEnv()
	ll := emitDebug(t, e, unitOf(e, debugWork(e)), types.X86_64Linux, types.Dev)
	got := dwarfVars(t, clangC(t, types.X86_64Linux, t.TempDir(), "work", ll, "-O0"))
	for _, want := range []string{"Work.pa:int", "Work.x:int", "Work.y:int", "Probe.pa:int"} {
		if !got[want] {
			t.Errorf("no DWARF variable %s in %v", want, got)
		}
	}
}

// TestDebugCodeViewVariables compiles Work for x86_64-windows at -O0 and
// reads its CodeView symbols with llvm-readobj: a procedure per function
// named in namespace main, and an S_LOCAL per variable.
func TestDebugCodeViewVariables(t *testing.T) {
	bin := needLLVM(t)
	e := newTestEnv()
	ll := emitDebug(t, e, unitOf(e, debugWork(e)), types.X86_64Windows, types.Dev)
	obj := clangC(t, types.X86_64Windows, t.TempDir(), "work", ll, "-O0")
	out := runTool(t, tool(bin, "llvm-readobj"), "--codeview", obj)
	for _, want := range []string{"DisplayName: main::Work", "DisplayName: main::Probe", "VarName: pa", "VarName: x", "VarName: y"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in the CodeView of Work", want)
		}
	}
	if n := strings.Count(out, "VarName: y"); n != 2 {
		t.Errorf("%d S_LOCAL y, want 2", n)
	}
}

// TestDebugInlinedRecords: at -O2 Probe is inlined into Work, and the
// object records the inlined call: DW_TAG_inlined_subroutine in DWARF,
// S_INLINESITE in CodeView. LLVM makes the records from Probe's
// subprogram and the call's location; codegen emits nothing else for them.
func TestDebugInlinedRecords(t *testing.T) {
	bin := needLLVM(t)
	e := newTestEnv()
	ll := emitDebug(t, e, unitOf(e, debugWork(e)), types.X86_64Linux, types.Release)
	if got := dwarfVars(t, clangC(t, types.X86_64Linux, t.TempDir(), "work", ll, "-O2")); !got["inlined"] {
		t.Errorf("no DW_TAG_inlined_subroutine at -O2: %v", got)
	}
	e = newTestEnv()
	ll = emitDebug(t, e, unitOf(e, debugWork(e)), types.X86_64Windows, types.Release)
	obj := clangC(t, types.X86_64Windows, t.TempDir(), "work", ll, "-O2")
	if out := runTool(t, tool(bin, "llvm-readobj"), "--codeview", obj); !strings.Contains(out, "S_INLINESITE") {
		t.Error("no S_INLINESITE at -O2")
	}
}
