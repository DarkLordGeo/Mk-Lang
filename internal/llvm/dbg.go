// Why: a debugger needs variables and types, not only line tables, to show
// a Mk program's state.
//
// What: the DI nodes of LLVM's "Source Level Debugging with LLVM" that
// codegen states: compile unit with FullDebug, basic, derived and composite
// types, subranges, enumerators, namespaces, typed subprograms, local
// variables and lexical blocks, plus forward nodes for recursive types.
//
// How: llvm.dbg.declare is emitted in its intrinsic form, which LLVM 18
// parses and LLVM 19 to 21 upgrade to debug records. Local variables are
// distinct nodes, so two locals of one name and line stay two.

package llvm

import "strconv"

// Reserve is a new node whose text Fill sets later: its number is fixed now,
// so a recursive type can name itself before its members exist. A reserved
// node never filled prints as an empty tuple !{}.
func (mt *Meta) Reserve() MD {
	mt.nodes = append(mt.nodes, mdNode{text: "!{}", open: true})
	return MD{uint32(len(mt.nodes)), mt}
}

// Fill sets the text of a reserved node to !kind(fields...), distinct when
// asked. It panics on a node that Reserve did not make or that was filled.
func (mt *Meta) Fill(md MD, kind string, distinct bool, fields ...Field) {
	mt.check(md, "Fill")
	if md.IsZero() || !mt.nodes[md.id-1].open {
		panic("llvm: Fill of a node that is not an unfilled reserved node")
	}
	mt.checkFields(fields, "Fill")
	mt.nodeText(kind, fields)
	text := string(mt.sc)
	if distinct {
		text = "distinct " + text
	}
	mt.nodes[md.id-1] = mdNode{text: text}
}

// FillTuple sets the text of a reserved node to the tuple !{ops...}.
func (mt *Meta) FillTuple(md MD, ops ...MDOp) {
	mt.check(md, "FillTuple")
	if md.IsZero() || !mt.nodes[md.id-1].open {
		panic("llvm: FillTuple of a node that is not an unfilled reserved node")
	}
	mt.checkOps(ops, "FillTuple")
	mt.tupleText(ops)
	mt.nodes[md.id-1] = mdNode{text: string(mt.sc)}
}

// DICompileUnitFull is the pack's distinct compile unit with full debug
// info (variables and types), added to !llvm.dbg.cu. nameTableKind None
// keeps .debug_pubnames out of ELF objects (lld builds no index from it
// without --gdb-index) and is what clang writes for CodeView.
func (mt *Meta) DICompileUnitFull(file MD, optimized bool) MD {
	cu := mt.DistinctNode("DICompileUnit", FEnum("language", "DW_LANG_C99"), FRef("file", file),
		FStr("producer", "mk"), FBool("isOptimized", optimized), FInt("runtimeVersion", 0),
		FEnum("emissionKind", "FullDebug"), FEnum("nameTableKind", "None"))
	mt.Named("llvm.dbg.cu", cu)
	mt.cu = cu
	return cu
}

// DIBasicType is a scalar type: enc is a DW_ATE_ name (DW_ATE_signed,
// DW_ATE_unsigned, DW_ATE_float, DW_ATE_boolean, DW_ATE_UTF).
func (mt *Meta) DIBasicType(name string, bits uint64, enc string) MD {
	return mt.Node("DIBasicType", FStr("name", name), FInt("size", int64(bits)), FEnum("encoding", enc))
}

// DerivedType is the content of a DIDerivedType: a pointer
// (DW_TAG_pointer_type, Size 64), a member (DW_TAG_member, with Scope,
// Offset and Size in bits), a typedef. Zero fields are left out.
type DerivedType struct {
	Tag    string // DW_TAG_pointer_type, DW_TAG_member, DW_TAG_typedef
	Name   string
	Scope  MD
	File   MD
	Line   int
	Base   MD
	Size   uint64 // bits
	Align  uint64 // bits
	Offset uint64 // bits
	Flags  string // e.g. "DIFlagArtificial"; "" for none
}

// DIDerived is the uniqued DIDerivedType of d.
func (mt *Meta) DIDerived(d DerivedType) MD {
	fs := []Field{FEnum("tag", d.Tag)}
	if d.Name != "" {
		fs = append(fs, FStr("name", d.Name))
	}
	if !d.Scope.IsZero() {
		fs = append(fs, FRef("scope", d.Scope))
	}
	if !d.File.IsZero() {
		fs = append(fs, FRef("file", d.File))
	}
	if d.Line > 0 {
		fs = append(fs, FInt("line", int64(d.Line)))
	}
	fs = append(fs, FRef("baseType", d.Base))
	if d.Size > 0 {
		fs = append(fs, FInt("size", int64(d.Size)))
	}
	if d.Align > 0 {
		fs = append(fs, FInt("align", int64(d.Align)))
	}
	if d.Offset > 0 {
		fs = append(fs, FInt("offset", int64(d.Offset)))
	}
	if d.Flags != "" {
		fs = append(fs, FEnum("flags", d.Flags))
	}
	return mt.Node("DIDerivedType", fs...)
}

// CompositeType is the content of a DICompositeType: a structure
// (DW_TAG_structure_type, Elements its members), a union, an array
// (DW_TAG_array_type, Base the element, Elements its subranges) or an
// enumeration (DW_TAG_enumeration_type, Base the tag type, Elements its
// enumerators). Identifier, when set, lets LTO merge the copies of one type
// from several modules (ODR uniquing by name).
type CompositeType struct {
	Tag        string
	Name       string
	Scope      MD
	File       MD
	Line       int
	Base       MD
	Size       uint64 // bits
	Align      uint64 // bits
	Elements   MD
	Identifier string
	Flags      string
}

// FillComposite fills a reserved node with the distinct DICompositeType c.
func (mt *Meta) FillComposite(md MD, c CompositeType) {
	fs := []Field{FEnum("tag", c.Tag)}
	if c.Name != "" {
		fs = append(fs, FStr("name", c.Name))
	}
	if !c.Scope.IsZero() {
		fs = append(fs, FRef("scope", c.Scope))
	}
	if !c.File.IsZero() {
		fs = append(fs, FRef("file", c.File))
	}
	if c.Line > 0 {
		fs = append(fs, FInt("line", int64(c.Line)))
	}
	if !c.Base.IsZero() {
		fs = append(fs, FRef("baseType", c.Base))
	}
	fs = append(fs, FInt("size", int64(c.Size)))
	if c.Align > 0 {
		fs = append(fs, FInt("align", int64(c.Align)))
	}
	if c.Flags != "" {
		fs = append(fs, FEnum("flags", c.Flags))
	}
	fs = append(fs, FRef("elements", c.Elements))
	if c.Identifier != "" {
		fs = append(fs, FStr("identifier", c.Identifier))
	}
	mt.Fill(md, "DICompositeType", true, fs...)
}

// DISubrange is !DISubrange(count: n).
func (mt *Meta) DISubrange(count int64) MD {
	return mt.Node("DISubrange", FInt("count", count))
}

// DIEnumerator is one enumerator of an enumeration type: value is the bit
// pattern, printed in unsigned decimal when unsigned (LLVM refuses a
// negative value with isUnsigned: true, so a u64 tag of 2^63 or more must
// not print as int64) and as its int64 otherwise.
func (mt *Meta) DIEnumerator(name string, value uint64, unsigned bool) MD {
	if unsigned {
		return mt.Node("DIEnumerator", FStr("name", name), FUint("value", value), FBool("isUnsigned", true))
	}
	return mt.Node("DIEnumerator", FStr("name", name), FInt("value", int64(value)))
}

// DISubroutineTypeOf is the subroutine type whose types tuple is the result
// (the zero MD for void, printed null) then the parameters.
func (mt *Meta) DISubroutineTypeOf(result MD, params ...MD) MD {
	ops := make([]MDOp, 0, len(params)+1)
	ops = append(ops, MDRef(result))
	for _, p := range params {
		ops = append(ops, MDRef(p))
	}
	return mt.Node("DISubroutineType", FRef("types", mt.Tuple(ops...)))
}

// DINamespace is the namespace name inside scope (the zero MD for the top).
func (mt *Meta) DINamespace(name string, scope MD) MD {
	return mt.Node("DINamespace", FStr("name", name), FRef("scope", scope))
}

// SubprogramInfo is the content of a full-debug-info DISubprogram.
type SubprogramInfo struct {
	Name, LinkageName string
	Scope, File       MD
	Line              int
	Type              MD
	Unit              MD
	Optimized         bool
}

// DISubprogramFull is a function definition's distinct subprogram with a
// scope (a namespace) and a real subroutine type.
func (mt *Meta) DISubprogramFull(s SubprogramInfo) MD {
	mt.check(s.Scope, "DISubprogramFull")
	mt.check(s.File, "DISubprogramFull")
	mt.check(s.Type, "DISubprogramFull")
	mt.check(s.Unit, "DISubprogramFull")
	b := append(mt.sc[:0], "!DISubprogram(name: "...)
	b = appendQuoted(b, s.Name)
	if s.LinkageName != "" && s.LinkageName != s.Name {
		b = append(b, ", linkageName: "...)
		b = appendQuoted(b, s.LinkageName)
	}
	scope := s.Scope
	if scope.IsZero() {
		scope = s.File
	}
	b = append(b, ", scope: "...)
	b = appendMDRef(b, scope)
	b = append(b, ", file: "...)
	b = appendMDRef(b, s.File)
	b = append(b, ", line: "...)
	b = strconv.AppendInt(b, int64(s.Line), 10)
	b = append(b, ", type: "...)
	b = appendMDRef(b, s.Type)
	b = append(b, ", scopeLine: "...)
	b = strconv.AppendInt(b, int64(s.Line), 10)
	b = append(b, ", spFlags: DISPFlagDefinition"...)
	if s.Optimized {
		b = append(b, " | DISPFlagOptimized"...)
	}
	b = append(b, ", unit: "...)
	b = appendMDRef(b, s.Unit)
	mt.sc = append(b, ')')
	return mt.add(true)
}

// DILocalVariable is a new distinct local variable of scope (a subprogram
// or lexical block); arg is the 1-based parameter number, 0 for a local.
// Distinct, not uniqued: two locals of one name, scope, line and type (two
// bare blocks on one line) are two variables, each with its own
// llvm.dbg.declare; a uniqued node would make them one, and LLVM would keep
// only the first location.
func (mt *Meta) DILocalVariable(name string, arg int, scope, file MD, line int, ty MD) MD {
	fs := []Field{FStr("name", name)}
	if arg > 0 {
		fs = append(fs, FInt("arg", int64(arg)))
	}
	fs = append(fs, FRef("scope", scope), FRef("file", file), FInt("line", int64(max(line, 0))), FRef("type", ty))
	return mt.DistinctNode("DILocalVariable", fs...)
}

// DILexicalBlock is a new distinct lexical block in scope.
func (mt *Meta) DILexicalBlock(scope, file MD, line, col int) MD {
	if col < 0 || col > MaxColumn {
		col = 0
	}
	return mt.DistinctNode("DILexicalBlock", FRef("scope", scope), FRef("file", file),
		FInt("line", int64(max(line, 0))), FInt("column", int64(col)))
}

// dbgDeclareName is the intrinsic DbgDeclare calls.
const dbgDeclareName = "llvm.dbg.declare"

// DbgDeclare emits call void @llvm.dbg.declare(metadata ptr addr, metadata
// !variable, metadata !DIExpression()) with location loc, and declares the
// intrinsic in the module on first use. addr is the variable's storage for
// the whole function (an alloca, or a caller's copy).
func (b *Block) DbgDeclare(addr Value, variable, loc MD) {
	b.ptr(addr, "DbgDeclare")
	mt := b.f.m.meta
	mt.check(variable, "DbgDeclare")
	m := b.f.m
	if !m.dbgDeclare {
		m.claim(dbgDeclareName, "DbgDeclare")
		m.dbgDeclare = true
	}
	md := []Attach{Dbg(loc)}
	b.begin(md)
	b.str("call void @llvm.dbg.declare(metadata ")
	b.typed(addr)
	b.str(", metadata ")
	b.text = appendMDRef(b.text, variable)
	b.str(", metadata !DIExpression())")
	b.end(md)
}
