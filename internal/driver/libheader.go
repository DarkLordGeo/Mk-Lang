// Why: a library built by mk is called from C, so it needs a C header, and
// only Mk types with a C form may cross its boundary.
//
// What: the header mk build writes beside every library (libHeader, with
// export, exportVar, cKeywords and docOf), and the per-function reasons an
// export extern has no C declaration.
//
// How: the C-form set is check's (check.CForms, check.HasCForm), the same
// one E1109 uses. Each export becomes one declaration after its /// comment
// copied as a C comment; structs are declared before any is defined; an
// export with no C form is left out of the .def, the version script and the
// header, and the build notes it.

package driver

import (
	"fmt"
	"sort"
	"strings"

	checker "mk/internal/check"
	"mk/internal/codegen"
	"mk/internal/types"
)

// The C-form set is check.CForms' and check.HasCForm's (internal/check
// cform.go), which the E1109 check uses too. A function also
// has no C form when a parameter is const or the function is variadic
// (neither has a C spelling). E1109 refuses a parameter or result type
// with no C form at check (internal/check/e1109.go); a const parameter, a
// variadic or a generic export extern is not E1109, so the library still
// leaves such a function out of its .def, its version script and its
// header (there as a comment), and the build notes each one and why.

// cForm is the header's walk over check.CForms: the C spelling of each
// type, the structs met (c.structs, in the order they were met) and
// whether a str was.
type cForm struct {
	w *world
	c *checker.CForms
}

// newCForm is a fresh walk for one header or one split.
func newCForm(w *world) *cForm {
	return &cForm{w: w, c: &checker.CForms{Tab: w.tab, Items: w.items, Name: w.in.Str}}
}

// typ is t's C type, or false when t has no C form. result admits void.
func (c *cForm) typ(t types.TypeID, result bool) (string, bool) { return c.c.Type(t, result) }

// cExport is one export's declaration, or its reason for none.
type cExport struct {
	name, decl, doc string
	// why is "" when decl is set, else why the export has no C form.
	why string
}

// export is the declaration of the export extern id, or why it has none:
// it can fail, it is variadic or generic, a parameter is const,
// or the first parameter or the result whose type has no C form.
func (c *cForm) export(id types.ItemID) cExport {
	w := c.w
	it := w.items.Get(id)
	fd := it.Decl.(*types.FuncDecl)
	sym := w.in.Str(it.Name)
	e := cExport{name: sym, doc: w.docOf(it)}
	// check.CFormFunc is E1109's predicate, so a library that
	// checks clean has nothing here; the reason stays for a build that
	// reaches this past check (a note, never a silent export).
	if miss := checker.CFormFunc(w.tab, w.items, &fd.Sig); len(miss) > 0 {
		m := miss[0]
		switch m.Kind {
		case checker.MissErrors:
			e.why = "it can fail (an error union)"
		case checker.MissVariadic:
			e.why = "it is variadic"
		case checker.MissGeneric:
			e.why = "it is generic"
		case checker.MissConst:
			e.why = "parameter " + w.in.Str(fd.Sig.Params[m.Param].Name) + " is const"
		case checker.MissParam:
			p := fd.Sig.Params[m.Param]
			e.why = "parameter " + w.in.Str(p.Name) + " has type " + w.tab.TypeString(p.Type, nil)
		default:
			e.why = "its result has type " + w.tab.TypeString(fd.Sig.Result, nil)
		}
		return e
	}
	var params []string
	for _, p := range fd.Sig.Params {
		s, _ := c.typ(p.Type, false)
		params = append(params, s+" "+cIdent(w.in.Str(p.Name)))
	}
	res, _ := c.typ(fd.Sig.Result, true)
	if len(params) == 0 {
		params = []string{"void"}
	}
	e.decl = res + " " + sym + "(" + strings.Join(params, ", ") + ");"
	return e
}

// exportVar is the declaration of the export extern var id,
// `extern T name;`, or why it has none: its type has no C form. dllimport
// prefixes __declspec(dllimport), which a host's reference to a DLL's data
// needs (a static library's and an ELF library's data need none).
func (c *cForm) exportVar(id types.ItemID, dllimport bool) cExport {
	w := c.w
	it := w.items.Get(id)
	gd := it.Decl.(*types.GlobalDecl)
	sym := w.in.Str(it.Name)
	e := cExport{name: sym, doc: w.docOf(it)}
	s, ok := c.typ(gd.Type, false)
	if !ok {
		e.why = "its type is " + w.tab.TypeString(gd.Type, nil)
		return e
	}
	pre := "extern "
	if dllimport {
		pre = "__declspec(dllimport) extern "
	}
	e.decl = pre + s + " " + sym + ";"
	return e
}

// cSplit splits a library's exports into those with a C form, which its
// .def or version script lists, and those without, each with its reason.
func (w *world) cSplit(exports []types.ItemID) (keep []types.ItemID, left []cExport) {
	c := newCForm(w)
	for _, id := range exports {
		if e := c.export(id); e.decl != "" {
			keep = append(keep, id)
		} else {
			left = append(left, e)
		}
	}
	return keep, left
}

// byValueOrder is c.structs with every struct after the structs its
// fields hold by value (a C definition needs a complete field type; a
// pointer needs only the forward declaration). c.structs' own order holds
// that only from the export that reached the holder first.
func (c *cForm) byValueOrder() []types.ItemID {
	in := map[types.ItemID]bool{}
	for _, id := range c.c.Structs {
		in[id] = true
	}
	var out []types.ItemID
	done := map[types.ItemID]bool{}
	var visit func(id types.ItemID)
	visit = func(id types.ItemID) {
		if done[id] {
			return
		}
		done[id] = true
		for _, f := range c.w.items.Get(id).Decl.(*types.StructDecl).Fields {
			if ft := c.w.tab.Get(f.Type); ft.Kind == types.KNamed && in[ft.Item] {
				visit(ft.Item)
			}
		}
		out = append(out, id)
	}
	for _, id := range c.c.Structs {
		visit(id)
	}
	return out
}

// libHeader is the text of name.h for the exports of a library (the
// export extern functions and vars of its own package, sorted by name): an
// include guard, clang's own <stdint.h>, <stddef.h> and <stdbool.h>,
// extern "C" under __cplusplus, the structs the exports need, then one
// declaration per export, each after its /// comment copied as a C
// comment. dllimport marks a DLL's data exports (exportVar); elfShared
// (a .so) adds, when it has data exports, the note that a host reaches
// them through the GOT (they are protected).
func (w *world) libHeader(name string, exports, vars []types.ItemID, dllimport, elfShared bool) string {
	c := newCForm(w)
	var decls []cExport
	for _, id := range exports {
		decls = append(decls, c.export(id))
	}
	for _, id := range vars {
		decls = append(decls, c.exportVar(id, dllimport))
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].name < decls[j].name })
	guard := "MK_" + strings.ToUpper(cIdent(name)) + "_H"
	var b strings.Builder
	fmt.Fprintf(&b, "/* %s.h: the C interface of the Mk library %s, written by mk build from its\n", name, name)
	if len(vars) == 0 {
		b.WriteString("   export extern functions (see doc/packages.md). Do not edit. */\n")
	} else {
		b.WriteString("   export extern functions and vars (see doc/packages.md). Do not edit. */\n")
	}
	if elfShared && len(vars) > 0 {
		b.WriteString("\n/* The data exports are protected symbols of a shared library: a host reaches them\n")
		b.WriteString("   through the GOT (-fPIC, or clang's -fPIE). ld.lld refuses a host that\n")
		b.WriteString("   would take a copy relocation (-fno-pic -no-pie, -fdirect-access-external-data);\n")
		b.WriteString("   with a linker that accepts one, the host reads a copy the library never sees. */\n")
	}
	fmt.Fprintf(&b, "#ifndef %s\n#define %s\n\n", guard, guard)
	b.WriteString("#include <stdint.h>\n#include <stddef.h>\n#include <stdbool.h>\n\n")
	b.WriteString("#ifdef __cplusplus\nextern \"C\" {\n#endif\n\n")
	if c.c.UsesStr {
		b.WriteString("#ifndef MK_STR_DEFINED\n#define MK_STR_DEFINED\n")
		b.WriteString("/* An Mk str: UTF-8 bytes and their length, not NUL-terminated. */\n")
		b.WriteString("typedef struct MkStr {\n    const uint8_t *ptr;\n    int64_t len;\n} MkStr;\n#endif\n\n")
	}
	// Every struct is declared before any is defined, so one that points
	// at itself or at a later one compiles; the definitions
	// follow with a struct held by value before its holder, whichever
	// export reached it first.
	structs := c.byValueOrder()
	for _, id := range structs {
		sn := w.in.Str(w.items.Get(id).Name)
		fmt.Fprintf(&b, "typedef struct %s %s;\n", sn, sn)
	}
	if len(c.c.Structs) > 0 {
		b.WriteString("\n")
	}
	for _, id := range structs {
		it := w.items.Get(id)
		sd := it.Decl.(*types.StructDecl)
		sn := w.in.Str(it.Name)
		b.WriteString(cComment(w.docOf(it), ""))
		fmt.Fprintf(&b, "struct %s {\n", sn)
		for _, f := range sd.Fields {
			s, _ := c.typ(f.Type, false)
			fmt.Fprintf(&b, "    %s %s;\n", s, cIdent(w.in.Str(f.Name)))
		}
		b.WriteString("};\n\n")
	}
	// The runtime's options and entries.
	id := cIdent(name)
	opts := strings.ToUpper(id[:1]) + id[1:] + "MkOptions"
	start, stop := codegen.LibEntryNames(id)
	fmt.Fprintf(&b, "/* The options of %s: carriers, 0 for the default (MK_CARRIERS or the\n", start)
	b.WriteString("   processor count); stack_reserve, each carrier's system stack in bytes, 0 for 8 MiB,\n")
	b.WriteString("   at least 256 KiB (a smaller value is raised to it), and 8 MiB above 1 GiB. */\n")
	fmt.Fprintf(&b, "typedef struct %s {\n    uint32_t carriers;\n    uint64_t stack_reserve;\n} %s;\n\n", opts, opts)
	b.WriteString("/* Starts the library's runtime before its first call (which otherwise starts it\n")
	b.WriteString("   with the defaults); options may be NULL. 0, or 1 when it already runs with\n")
	b.WriteString("   other options, or 2 after a stop. Safe from any thread; it waits for a stop in\n")
	b.WriteString("   progress to end. */\n")
	fmt.Fprintf(&b, "int32_t %s(const %s *options);\n\n", start, opts)
	b.WriteString("/* Stops the runtime when no call into the library is running: its threads end\n")
	b.WriteString("   and are joined, and its fault handler is removed. 0, or 1 while a call runs (nothing\n")
	b.WriteString("   stops). A stop before any start does nothing and answers 0. A call into the library\n")
	b.WriteString("   during a stop waits for a stop in progress to end, and goes on when it was refused.\n")
	b.WriteString("   A call into the library after a stop is a panic (code 16). */\n")
	fmt.Fprintf(&b, "int32_t %s(void);\n\n", stop)
	for _, e := range decls {
		if e.decl == "" {
			fmt.Fprintf(&b, "/* %s: no C form across the library boundary (E1109); not declared. */\n\n", e.name)
			continue
		}
		b.WriteString(cComment(e.doc, ""))
		b.WriteString(e.decl + "\n\n")
	}
	b.WriteString("#ifdef __cplusplus\n}\n#endif\n\n#endif\n")
	return b.String()
}

// cKeywords are C11's and C++17's keywords that an Mk name may spell.
var cKeywords = map[string]bool{
	"auto": true, "char": true, "default": true, "double": true, "extern": true, "float": true, "goto": true,
	"inline": true, "long": true, "register": true, "restrict": true, "short": true, "signed": true, "sizeof": true,
	"static": true, "typedef": true, "union": true, "unsigned": true, "volatile": true, "class": true, "delete": true,
	"new": true, "this": true, "template": true, "typename": true, "operator": true, "private": true, "public": true,
	"protected": true, "virtual": true, "friend": true, "namespace": true, "using": true, "bool": true, "int": true,
	"void": true, "const": true, "struct": true, "case": true, "do": true, "while": true, "for": true, "if": true,
	"else": true, "switch": true, "return": true, "break": true, "continue": true, "enum": true, "throw": true,
	"try": true, "catch": true, "true": true, "false": true, "explicit": true, "mutable": true, "export": true,
}

// cIdent is an Mk name as a C identifier: a keyword gets a trailing _.
func cIdent(s string) string {
	if cKeywords[s] {
		return s + "_"
	}
	return s
}

// cComment is doc as C line comments, each line prefixed with indent.
func cComment(doc, indent string) string {
	if doc == "" {
		return ""
	}
	var b strings.Builder
	for _, l := range strings.Split(doc, "\n") {
		b.WriteString(indent + "/* " + strings.ReplaceAll(l, "*/", "* /") + " */\n")
	}
	return b.String()
}

// docOf is an item's /// comment, the lines directly above its
// declaration with the /// and one space removed; "" when it has none.
func (w *world) docOf(it *types.Item) string {
	if !it.Span.File.IsValid() {
		return ""
	}
	f := w.sm.File(it.Span.File)
	if f == nil {
		return ""
	}
	text := f.Text
	start := int(it.Span.Start)
	if start > len(text) {
		return ""
	}
	// Back to the start of the declaration's line.
	i := strings.LastIndexByte(text[:start], '\n') + 1
	var lines []string
	for i > 0 {
		j := strings.LastIndexByte(text[:i-1], '\n') + 1
		l := strings.TrimSpace(text[j : i-1])
		if !strings.HasPrefix(l, "///") {
			break
		}
		lines = append([]string{strings.TrimPrefix(strings.TrimPrefix(l, "///"), " ")}, lines...)
		i = j
	}
	return strings.Join(lines, "\n")
}
