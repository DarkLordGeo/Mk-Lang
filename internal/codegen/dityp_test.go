// Why: full debug info describes every local's type, and each kind of type
// has its own node shape that debuggers must read.
// What: TestDebugTypes over one function whose named locals cover every kind
// of type dityp.go describes.
// How: the node text per kind is checked, clang's verifier runs on the pack
// for both formats, and debug/dwarf reads the types back.

package codegen

import (
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// debugTypes is fn Types() with one named local per kind of type.
func debugTypes(e *testEnv) []*mir.Body {
	s := e.structT("S", types.Int, types.Bool)
	rec := e.tt.Record([]types.RecordField{{Name: e.in.Intern("a"), Type: types.Int}, {Name: e.in.Intern("b"), Type: types.U8}})
	ab := e.enumT("AB", types.U8, []uint64{0, 1})
	pay := e.enumT("P", types.NoType, []uint64{0, 1}, []types.TypeID{types.Int}, []types.TypeID{types.Bool, types.I32})
	bad := e.errItem("Bad", types.Int)
	eu := e.tt.ErrorUnion(e.tt.ErrorSet([]types.ErrorMember{{Item: bad}}), types.Int)
	nd := &types.StructDecl{}
	node := e.tt.Named(e.item("Node", types.KindStruct, nd), nil)
	nd.Fields = []types.FieldDecl{{Name: e.in.Intern("next"), Type: e.tt.RawPtr(node)}, {Name: e.in.Intern("val"), Type: types.Int}}
	fnT := e.tt.Fn(types.FnType{Params: []types.FnParam{{Type: types.Int, Into: types.NoParam}}, Result: types.Bool})
	arr := e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 4)}, types.I32)

	f := e.body(e.fn("Types", nil, types.Void, 0))
	for i, l := range []struct {
		name string
		t    types.TypeID
	}{
		{"s", s}, {"r", rec}, {"ab", ab}, {"p", pay}, {"o1", e.tt.Optional(e.tt.Ref(types.Int))},
		{"o2", e.tt.Optional(types.Int)}, {"eu", eu}, {"sl", e.tt.Slice(types.U8)}, {"st", types.Str},
		{"ar", arr}, {"rp", e.tt.RawPtr(types.U8)}, {"fv", fnT}, {"n", node}, {"ch", types.Char},
		{"fl", types.F64}, {"u", types.U16}, {"ms", e.tt.MutSlice(types.I64)}, {"rf", e.tt.Mut(s)},
		{"of", e.tt.Optional(fnT)}, {"om", e.tt.Optional(e.tt.Mut(s))}, {"vp", e.tt.RawPtr(types.Void)},
	} {
		f.userLocal(l.name, l.t, f.at(uint32(i+2), 5))
	}
	f.ret(f.at(30, 1))
	return []*mir.Body{f.b}
}

func TestDebugTypes(t *testing.T) {
	e := newTestEnv()
	ll := emitDebug(t, e, unitOf(e, debugTypes(e)), types.X86_64Linux, types.Dev)
	nodes := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^(!\d+) = (.*)$`).FindAllStringSubmatch(ll, -1) {
		nodes[m[1]] = m[2]
	}
	typeOf := func(v string) string {
		m := regexp.MustCompile(`!DILocalVariable\(name: "` + v + `", scope: !\d+, file: !\d+, line: \d+, type: (!\d+)\)`).FindStringSubmatch(ll)
		if m == nil {
			t.Fatalf("no variable %s", v)
		}
		return nodes[m[1]]
	}
	want := map[string][]string{
		"s":  {"DW_TAG_structure_type", `name: "S"`, "size: 128", `identifier: "mk.`},
		"r":  {"DW_TAG_structure_type", "size: 128"},
		"ab": {"DW_TAG_enumeration_type", `name: "AB"`, "size: 8"},
		"p":  {"DW_TAG_structure_type", `name: "P"`},
		"o1": {"DW_TAG_pointer_type", "size: 64"},
		"o2": {"DW_TAG_structure_type", "size: 128"},
		"eu": {"DW_TAG_structure_type"},
		"sl": {"DW_TAG_structure_type", `name: "[]u8"`, "size: 128"},
		"st": {"DW_TAG_structure_type", `name: "str"`, "size: 128"},
		"ar": {"DW_TAG_array_type", "size: 128"},
		"rp": {"DW_TAG_pointer_type", "size: 64"},
		"fv": {"DW_TAG_pointer_type", "size: 64"},
		"n":  {"DW_TAG_structure_type", `name: "Node"`, "size: 128"},
		"ch": {"DIBasicType", `name: "char"`, "size: 32", "DW_ATE_UTF"},
		"fl": {"DIBasicType", `name: "f64"`, "size: 64", "DW_ATE_float"},
		"u":  {"DIBasicType", `name: "u16"`, "size: 16", "DW_ATE_unsigned"},
		"ms": {"DW_TAG_structure_type", "size: 128"},
		"rf": {"DW_TAG_pointer_type", "size: 64"},
		"of": {"DW_TAG_pointer_type", "size: 64", "baseType: !"},
		"om": {"DW_TAG_pointer_type", "size: 64", "baseType: !"},
		"vp": {"DW_TAG_pointer_type", "size: 64", "baseType: null"},
	}
	for v, subs := range want {
		txt := typeOf(v)
		for _, s := range subs {
			if !strings.Contains(txt, s) {
				t.Errorf("%s: type %s lacks %q", v, txt, s)
			}
		}
	}
	for _, s := range []string{`!DIEnumerator(name: "VA", value: 0, isUnsigned: true)`, `!DIEnumerator(name: "VB", value: 1, isUnsigned: true)`,
		`!DISubrange(count: 4)`, `tag: DW_TAG_member, name: "fa"`, `tag: DW_TAG_member, name: "fb"`,
		`tag: DW_TAG_member, name: "a"`, `tag: DW_TAG_member, name: "b"`, `tag: DW_TAG_member, name: "ptr"`,
		`tag: DW_TAG_member, name: "len"`, `tag: DW_TAG_member, name: "next"`, `tag: DW_TAG_member, name: "some"`,
		`tag: DW_TAG_member, name: "tag"`, `tag: DW_TAG_member, name: "code"`, `tag: DW_TAG_member, name: "ok"`,
		`!DISubroutineType(types: `} {
		if !strings.Contains(ll, s) {
			t.Errorf("no %s", s)
		}
	}
	// Node's next points to Node itself (Reserve, then Fill).
	m := regexp.MustCompile(`(?m)^(!\d+) = distinct !DICompositeType\(tag: DW_TAG_structure_type, name: "Node"`).FindStringSubmatch(ll)
	if m == nil {
		t.Fatal("no Node")
	}
	if !regexp.MustCompile(`!DIDerivedType\(tag: DW_TAG_pointer_type, name: "\*Node", baseType: ` + regexp.QuoteMeta(m[1]) + `, size: 64\)`).MatchString(ll) {
		t.Error("*Node does not point to Node")
	}
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
		e := newTestEnv()
		clangC(t, target, t.TempDir(), "types", emitDebug(t, e, unitOf(e, debugTypes(e)), target, types.Dev), "-O0")
	}
	e = newTestEnv()
	got := dwarfVars(t, clangC(t, types.X86_64Linux, t.TempDir(), "types", emitDebug(t, e, unitOf(e, debugTypes(e)), types.X86_64Linux, types.Dev), "-O0"))
	// debug/dwarf names DW_ATE_UTF "?" and prints an enum with its
	// enumerators, so those two are matched by prefix.
	for _, w := range []string{"Types.s:struct S", "Types.ab:enum AB {VA=0; VB=1}", "Types.ch:", "Types.n:struct Node",
		"Types.ar:[4]i32", "Types.sl:struct []u8", "Types.o1:*int", "Types.rf:*struct S"} {
		found := false
		for k := range got {
			if k == w || strings.HasSuffix(w, ":") && strings.HasPrefix(k, w) {
				found = true
			}
		}
		if !found {
			t.Errorf("no DWARF variable %s in %v", w, got)
		}
	}
}
