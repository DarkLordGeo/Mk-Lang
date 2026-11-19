// Why: mk doc's page is read by people who use a module, not by the
// checker. They need types spelled as they write them, a constant's value as
// source, payloads and field docs, flows in words, and no item they cannot
// call. The query helpers spell these the checker's way for mk query and mk
// apidiff, so the doc spelling lives here, apart from them.
//
// What: docWritten (an item's or a field's declaration as its source writes
// it), docSpanDoc and docMemberDoc (the /// lines above an item or a
// member), docWrittenErrorSet, docErrorMembers, docFlows (a signature's
// loans as sentences), docHider (does a type need a module no program
// outside std can import) and docNames (can a program outside the module
// write this initializer; std.core names as a program writes them).
//
// How: source spelling first, read from the item's span: the lines from the
// span's line while brackets stay open, comments taken off. When the source
// does not start with the item's name (a re-export, a generated item) the
// checker's spelling is the fallback. docHider walks a type and stops at a
// named type of std.rt, std.sys, std.task, std.target or std.simd.kernel, or
// a std.core type outside the prelude, unless a public std module re-exports
// it by an alias; an interface keeps its other methods.

package driver

import (
	"slices"
	"strings"

	"mk/internal/source"
	"mk/internal/types"
)

// docSourceLines is the file text from the start of the line holding sp,
// split into lines; nil when the span has no file.
func docSourceLines(qx *queryIndex, sp source.Span) []string {
	f := qx.w.sm.File(sp.File)
	if f == nil || int(sp.Start) > len(f.Text) {
		return nil
	}
	start := strings.LastIndexByte(f.Text[:sp.Start], '\n') + 1
	return strings.Split(f.Text[start:], "\n")
}

// docSpanDoc is the /// text directly above the line holding sp, the
// markers taken off.
func docSpanDoc(qx *queryIndex, sp source.Span) []string {
	f := qx.w.sm.File(sp.File)
	if f == nil || int(sp.Start) > len(f.Text) {
		return nil
	}
	text := f.Text
	start := strings.LastIndexByte(text[:sp.Start], '\n') + 1
	var lines []string
	for start > 0 {
		prev := strings.LastIndexByte(text[:start-1], '\n') + 1
		l := strings.TrimSpace(text[prev : start-1])
		if !strings.HasPrefix(l, "///") {
			break
		}
		lines = append([]string{strings.TrimPrefix(strings.TrimPrefix(l, "///"), " ")}, lines...)
		start = prev
	}
	return lines
}

// docWritten is the declaration at sp as its source writes it: the first
// line and, while a bracket it opened is still open, the lines after it (at
// most max), each with its comment taken off and its right end trimmed, a
// line left empty by that dropped; the first line loses its indent. It is
// nil unless the first line, after export, starts with prefix.
func docWritten(qx *queryIndex, sp source.Span, prefix string, max int) []string {
	lines := docSourceLines(qx, sp)
	if len(lines) == 0 {
		return nil
	}
	first := strings.TrimSpace(codeOnly(lines[0], true))
	if !strings.HasPrefix(strings.TrimPrefix(first, "export "), prefix) {
		return nil
	}
	out := []string{first}
	depth := docDepth(first)
	for i := 1; depth > 0 && i < len(lines); i++ {
		if i == max {
			return nil
		}
		l := strings.TrimRight(codeOnly(lines[i], true), " \t\r")
		if l != "" {
			out = append(out, l)
		}
		depth += docDepth(l)
	}
	if depth != 0 {
		return nil
	}
	return out
}

// docDepth is the change in bracket depth over l, string literals skipped.
func docDepth(l string) int {
	d := 0
	code := codeOnly(l, false)
	for _, c := range code {
		switch c {
		case '(', '[', '{':
			d++
		case ')', ']', '}':
			d--
		}
	}
	return d
}

// docFieldLine is a struct field or a variant as written, without export,
// or the checker's spelling when the source line is not this field alone.
func docFieldLine(qx *queryIndex, f types.FieldDecl, gs []types.GenericParam) string {
	name := qx.str(f.Name)
	if w := docWritten(qx, f.Span, name+":", 1); len(w) == 1 {
		return strings.TrimPrefix(w[0], "export ")
	}
	return name + ": " + qx.w.tab.TypeString(f.Type, gs)
}

// docVariantLine is an enum variant as written, or the checker's spelling.
func docVariantLine(qx *queryIndex, v types.VariantDecl, gs []types.GenericParam) string {
	name := qx.str(v.Name)
	if w := docWritten(qx, v.Span, name, 1); len(w) == 1 {
		l := strings.TrimSuffix(w[0], ",")
		if l == name || strings.HasPrefix(l, name+"(") && strings.HasSuffix(l, ")") || strings.HasPrefix(l, name+" =") {
			return l
		}
	}
	l := name
	if len(v.Fields) > 0 {
		var fs []string
		for _, f := range v.Fields {
			fs = append(fs, qx.str(f.Name)+": "+qx.w.tab.TypeString(f.Type, gs))
		}
		l += "(" + strings.Join(fs, ", ") + ")"
	}
	return l
}

// docErrorDecl is an error item's name and payload as written, Os(code:
// i64), or the checker's spelling.
func docErrorDecl(qx *queryIndex, id types.ItemID) string {
	it := qx.w.items.Get(id)
	name := qx.str(it.Name)
	if w := docWritten(qx, it.Span, "error "+name, 1); len(w) == 1 {
		if rest := strings.TrimPrefix(strings.TrimPrefix(w[0], "export "), "error "); rest == name || strings.HasPrefix(rest, name+"(") {
			return rest
		}
	}
	d, ok := it.Decl.(*types.ErrorDecl)
	if !ok || len(d.Fields) == 0 {
		return name
	}
	var fs []string
	for _, f := range d.Fields {
		fs = append(fs, qx.str(f.Name)+": "+qx.w.tab.TypeString(f.Type, nil))
	}
	return name + "(" + strings.Join(fs, ", ") + ")"
}

// docWrittenErrorSet is the error set of a function head as written, the
// text before the result's top-level "!": Error|OutOfMemory in
// `Absolute(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from a`.
// Empty when the head has no such result.
func docWrittenErrorSet(head []string, name string) string {
	h := docStripReceiverGenerics(strings.Join(head, " "))
	i := strings.Index(h, name)
	if i < 0 {
		return ""
	}
	h = h[i+len(name):]
	// Skip the generics and the parameters: brackets from the first one
	// until depth 0, twice at most.
	for range 2 {
		h = strings.TrimLeft(h, " ")
		if h == "" || (h[0] != '[' && h[0] != '(') {
			break
		}
		d := 0
		j := 0
		for ; j < len(h); j++ {
			switch h[j] {
			case '(', '[', '{':
				d++
			case ')', ']', '}':
				d--
			}
			if d == 0 {
				break
			}
		}
		if j >= len(h) {
			return ""
		}
		h = h[j+1:]
	}
	r, ok := strings.CutPrefix(strings.TrimLeft(h, " "), ":")
	if !ok {
		return ""
	}
	d := 0
	for j := 0; j < len(r); j++ {
		switch r[j] {
		case '(', '[', '{':
			d++
		case ')', ']', '}':
			d--
		case '!':
			if d == 0 {
				return strings.TrimSpace(r[:j])
			}
		}
	}
	return ""
}

// docErrorMembers is a signature's error set, one member per entry, each
// with its payload as declared. A member of another public module is
// qualified as a program that imports that module writes it (net.Timeout);
// std.core's members are prelude names and stay bare.
func docErrorMembers(qx *queryIndex, s *types.Signature, mod string) []string {
	var out []string
	for _, m := range qx.w.tab.Members(s.Errors) {
		if m.IsParam {
			if int(m.Param) < len(s.Generics) {
				out = append(out, qx.str(s.Generics[m.Param].Name))
			}
			continue
		}
		it := qx.w.items.Get(m.Item)
		l := docErrorDecl(qx, m.Item)
		if p := qx.w.items.Module(it.Module).Path; p != mod && p != preludeModule && !isStdInternal(p) {
			l = lastSeg(p) + "." + l
		}
		out = append(out, l)
	}
	return out
}

// docFlows is what a signature's flow clauses (declared or automatic) say,
// as sentences: "The result borrows from a.", "Ends every borrow of self."
// Nil when the function holds no loan.
func docFlows(qx *queryIndex, s *types.Signature) []string {
	var out []string
	var from []string
	var behind []types.Source
	for _, src := range s.Flows.Sources {
		if src.Behind {
			behind = append(behind, src)
		} else {
			from = append(from, qx.paramName(s, src.Param)+qx.fieldPath(s, src))
		}
	}
	if len(from) > 0 {
		out = append(out, "The result borrows from "+docAnd(from)+".")
	}
	for _, src := range behind {
		out = append(out, "The result borrows what "+qx.paramName(s, src.Param)+qx.fieldPath(s, src)+" refers to; "+qx.paramName(s, src.Param)+" is free after the call.")
	}
	if len(s.Flows.Fields) > 0 {
		var fs []string
		for _, f := range s.Flows.Fields {
			var ps []string
			for _, src := range f.Sources {
				ps = append(ps, qx.paramName(s, src.Param)+qx.fieldPath(s, src))
			}
			fs = append(fs, docResultField(qx, s, f.Field)+" borrows from "+docAnd(ps))
		}
		out = append(out, "In the result, "+strings.Join(fs, "; ")+"; any other field borrows from all of the above.")
	}
	for _, x := range s.Flows.Stores {
		// *q into p: what q points at is written into p, q may be p.
		if x.From == x.Into {
			out = append(out, "What "+qx.paramName(s, x.From)+" points at may be written back into "+qx.paramName(s, x.Into)+".")
			continue
		}
		out = append(out, "Stores the loans "+qx.paramName(s, x.From)+" holds in "+qx.paramName(s, x.Into)+".")
	}
	for _, p := range s.Flows.Ends {
		out = append(out, "Ends every borrow of "+qx.paramName(s, p)+".")
	}
	if s.Flows.Ambiguous {
		out = append(out, "Which parameter the result borrows from is ambiguous: the function needs a from clause (E0601).")
	}
	return out
}

// docResultField names field i of the result's struct or record, through
// ?T and E!T.
func docResultField(qx *queryIndex, s *types.Signature, i uint32) string {
	tab := qx.w.tab
	r := s.Result
	for tab.Kind(r) == types.KOptional || tab.Kind(r) == types.KErrorUnion {
		r = tab.Get(r).Elem
	}
	ty := tab.Get(r)
	switch ty.Kind {
	case types.KNamed:
		if d, ok := qx.w.items.Get(ty.Item).Decl.(*types.StructDecl); ok && int(i) < len(d.Fields) {
			return qx.str(d.Fields[i].Name)
		}
	case types.KRecord:
		if int(i) < len(ty.Fields) {
			return qx.str(ty.Fields[i].Name)
		}
	}
	return "?"
}

// docAnd joins names as prose: a, b and c.
func docAnd(ns []string) string {
	if len(ns) < 2 {
		return strings.Join(ns, "")
	}
	return strings.Join(ns[:len(ns)-1], ", ") + " and " + ns[len(ns)-1]
}

// docHiddenModule reports whether path is a std module no program outside
// std can import whose types make an item unusable outside std. std.core
// and std.raw are not among them: the prelude names std.core's types, and
// a package with unsafe modules may import std.raw.
func docHiddenModule(path string) bool {
	return isStdInternal(path) && path != preludeModule && path != "std.raw"
}

// docHider answers docHidden for one index: the named types of hidden
// modules that a public std module or the prelude re-exports by an alias
// are nameable.
type docHider struct {
	qx      *queryIndex
	aliased map[types.ItemID]bool
	memo    map[types.TypeID]bool
}

func newDocHider(qx *queryIndex) *docHider {
	h := &docHider{qx: qx, aliased: map[types.ItemID]bool{}, memo: map[types.TypeID]bool{}}
	for _, e := range qx.entries {
		if e.std && (!isStdInternal(e.mod) || e.mod == preludeModule) && qx.w.items.Get(e.id).Kind == types.KindAlias {
			if t := qx.aliasTarget(e.id); t != types.NoItem {
				h.aliased[t] = true
			}
		}
	}
	return h
}

// sig reports whether a parameter, the result or an error of s needs a
// hidden type.
func (h *docHider) sig(s *types.Signature) bool {
	for _, p := range s.Params {
		if h.typ(p.Type) {
			return true
		}
	}
	return h.typ(s.Result) || h.errors(s.Errors)
}

func (h *docHider) errors(es types.ErrorSetID) bool {
	for _, m := range h.qx.w.tab.Members(es) {
		if !m.IsParam && h.item(m.Item) {
			return true
		}
	}
	return false
}

// item reports whether no program outside std can name the type id: a
// type of a hidden module, or a std.core type outside the prelude, that no
// public std module or the prelude re-exports by an alias.
func (h *docHider) item(id types.ItemID) bool {
	if h.aliased[id] {
		return false
	}
	it := h.qx.w.items.Get(id)
	path := h.qx.w.items.Module(it.Module).Path
	if path == preludeModule {
		return !slices.Contains(preludeNames, h.qx.str(it.Name))
	}
	return docHiddenModule(path)
}

// iface is the written declaration w of an interface with the method lines
// that need a hidden type left out and one comment naming them: a program
// can name such an interface as a bound but cannot implement it.
func (h *docHider) iface(d *types.InterfaceDecl, w []string) []string {
	var hidden []string
	for i := range d.Methods {
		if h.sig(&d.Methods[i]) {
			hidden = append(hidden, h.qx.str(d.Methods[i].Name))
		}
	}
	if len(hidden) == 0 || len(w) < 2 {
		return w
	}
	out := []string{w[0]}
	for _, l := range w[1 : len(w)-1] {
		t := strings.TrimSpace(l)
		if slices.ContainsFunc(hidden, func(m string) bool { return strings.HasPrefix(t, m+"(") || strings.HasPrefix(t, m+"[") }) {
			continue
		}
		out = append(out, l)
	}
	out = append(out, "    // std-internal methods: "+docAnd(hidden))
	return append(out, w[len(w)-1])
}

// typ reports whether t names a hidden type anywhere inside it.
func (h *docHider) typ(t types.TypeID) bool {
	if t == types.NoType {
		return false
	}
	if v, ok := h.memo[t]; ok {
		return v
	}
	h.memo[t] = false // a cycle through a named type's arguments is not hidden
	ty := h.qx.w.tab.Get(t)
	v := false
	switch ty.Kind {
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KArray, types.KOptional:
		v = h.typ(ty.Elem)
	case types.KErrorUnion:
		v = h.typ(ty.Elem) || h.errors(ty.Errors)
	case types.KErrorSet:
		v = h.errors(ty.Errors)
	case types.KRecord:
		for _, f := range ty.Fields {
			v = v || h.typ(f.Type)
		}
	case types.KFn:
		for _, p := range ty.Fn.Params {
			v = v || h.typ(p.Type)
		}
		v = v || h.typ(ty.Fn.Result) || h.errors(ty.Fn.Errors)
	case types.KNamed:
		v = h.item(ty.Item)
		for _, a := range ty.Args {
			v = v || (!a.IsConst && h.typ(a.Type))
		}
	}
	h.memo[t] = v
	return v
}

// docStripReceiverGenerics is a method head with its receiver type's
// generic parameters taken out, `Atomic[T].Load(` read as `Atomic.Load(`,
// so the head can be matched against the method's display name. Any other
// text is returned as it is.
func docStripReceiverGenerics(h string) string {
	exp := ""
	if rest, ok := strings.CutPrefix(h, "export "); ok {
		exp, h = "export ", rest
	}
	i := 0
	for i < len(h) && isDocNameByte(h[i]) {
		i++
	}
	if i == 0 || i >= len(h) || h[i] != '[' {
		return exp + h
	}
	d := 0
	for j := i; j < len(h); j++ {
		switch h[j] {
		case '[':
			d++
		case ']':
			d--
		}
		if d == 0 {
			if j+1 < len(h) && h[j+1] == '.' {
				return exp + h[:i] + h[j+1:]
			}
			break
		}
	}
	return exp + h
}

// docMemberDoc is the /// text above a field or a variant, or nil when the
// member does not start its own line (`export struct Pair { export a: int
// }`): the lines above it are then its type's.
func docMemberDoc(qx *queryIndex, sp source.Span, name string) []string {
	lines := docSourceLines(qx, sp)
	if len(lines) == 0 || !docNameEnds(strings.TrimSpace(lines[0]), name) {
		return nil
	}
	return docSpanDoc(qx, sp)
}

// docNames answers which names a program outside the documented module can
// write: the exported items mk doc's index holds, by module and name. A
// constant's initializer is shown only when a user could write it, and a
// std.core spelling becomes the name a user writes.
type docNames struct {
	qx       *queryIndex
	exported map[string]types.ItemID // module + "\x00" + display name
	bySeg    map[string][]string     // a qualifier (a module's last segment) to the importable modules
	alias    map[string]string       // module + "\x00" + std.core name to the module's alias of it
	prelude  map[string]bool
}

func newDocNames(qx *queryIndex) *docNames {
	n := &docNames{qx: qx, exported: map[string]types.ItemID{}, bySeg: map[string][]string{}, alias: map[string]string{}, prelude: map[string]bool{}}
	for _, p := range preludeNames {
		n.prelude[p] = true
	}
	for _, e := range qx.entries {
		it := qx.w.items.Get(e.id)
		if !it.Exported {
			continue
		}
		n.exported[e.mod+"\x00"+e.name] = e.id
		if !e.std || !isStdInternal(e.mod) {
			if seg := lastSeg(e.mod); !slices.Contains(n.bySeg[seg], e.mod) {
				n.bySeg[seg] = append(n.bySeg[seg], e.mod)
			}
		}
		if e.std && it.Kind == types.KindAlias {
			if t := qx.aliasTarget(e.id); t != types.NoItem {
				ti := qx.w.items.Get(t)
				if qx.w.items.Module(ti.Module).Path == preludeModule {
					n.alias[e.mod+"\x00"+qx.str(ti.Name)] = e.name
				}
			}
		}
	}
	return n
}

// docPrimitives are the built-in type names an initializer may use
// (f64.FromBits(...), x as u8); docLiteralWords are the words that are
// values or operators.
var (
	docPrimitives   = []string{"bool", "char", "f32", "f64", "i8", "i16", "i32", "i64", "int", "str", "u8", "u16", "u32", "u64", "uint", "void", "never"}
	docLiteralWords = []string{"true", "false", "none", "some", "as"}
)

// lookup finds the exported item a dotted name written in module mod
// stands for: an item of mod, a prelude name, an item of an importable
// module named by its last segment (time.Time), or an enum's variant. The
// second result is false when no program outside mod can write the name.
func (n *docNames) lookup(mod, path string) (types.ItemID, bool) {
	segs := strings.Split(path, ".")
	if slices.Contains(docPrimitives, segs[0]) || len(segs) == 1 && slices.Contains(docLiteralWords, path) {
		return types.NoItem, true
	}
	if id, ok := n.exported[mod+"\x00"+path]; ok {
		return id, true
	}
	if n.prelude[segs[0]] {
		if id, ok := n.exported[preludeModule+"\x00"+path]; ok {
			return id, true
		}
	}
	if len(segs) > 1 {
		for _, m := range n.bySeg[segs[0]] {
			if id, ok := n.exported[m+"\x00"+strings.Join(segs[1:], ".")]; ok {
				return id, true
			}
		}
		if id, ok := n.lookup(mod, strings.Join(segs[:len(segs)-1], ".")); ok && id != types.NoItem {
			if d, ok := n.qx.w.items.Get(id).Decl.(*types.EnumDecl); ok {
				for _, v := range d.Variants {
					if n.qx.str(v.Name) == segs[len(segs)-1] {
						return types.NoItem, true
					}
				}
			}
		}
	}
	return types.NoItem, false
}

// writable reports whether a program outside mod can write the expression
// init: lookup finds every name in it, and every literal that names fields
// is of a struct whose fields are all exported (a literal gives every
// field). A record literal's fields and a call's named arguments pass.
func (n *docNames) writable(mod, init string) bool {
	code := codeOnly(init, false)
	type open struct {
		paren bool
		typ   string // the name before the bracket, or ""
	}
	var stack []open
	prev := "" // the last name read, while it is the last token
	qual := "" // the generic type before `].Name`, with its dot
	for i := 0; i < len(code); {
		c := code[i]
		switch {
		case c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z':
			j := i
			for j < len(code) && (isDocNameByte(code[j]) || code[j] == '.' && j+1 < len(code) && isDocNameByte(code[j+1])) {
				j++
			}
			path := qual + code[i:j]
			qual = ""
			k := j
			for k < len(code) && strings.IndexByte(" \t\r\n", code[k]) >= 0 {
				k++
			}
			if k < len(code) && code[k] == ':' && len(stack) > 0 && stack[len(stack)-1].paren {
				// A field name of the literal the innermost '(' opens.
				if t := stack[len(stack)-1].typ; t != "" {
					id, ok := n.lookup(mod, t)
					if !ok {
						return false
					}
					// A literal through an alias builds the alias's target.
					for range 16 {
						t := n.qx.aliasTarget(id)
						if id == types.NoItem || t == types.NoItem {
							break
						}
						id = t
					}
					if id != types.NoItem {
						if d, ok := n.qx.w.items.Get(id).Decl.(*types.StructDecl); ok {
							for _, f := range d.Fields {
								if !f.Exported {
									return false
								}
							}
						}
					}
				}
				prev = ""
				i = k + 1
				continue
			}
			if _, ok := n.lookup(mod, path); !ok {
				return false
			}
			prev = path
			i = j
			continue
		case '0' <= c && c <= '9':
			for i < len(code) && (isDocNameByte(code[i]) || code[i] == '.' && i+1 < len(code) && '0' <= code[i+1] && code[i+1] <= '9') {
				i++
			}
			prev = ""
			continue
		case c == '"' || c == '\'':
			// codeOnly blanked the contents: skip to the closing quote.
			j := strings.IndexByte(code[i+1:], c)
			if j < 0 {
				return false
			}
			i += j + 2
			prev = ""
			continue
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
			continue
		case c == '(' || c == '[' || c == '{':
			stack = append(stack, open{paren: c == '(', typ: prev})
		case c == ']':
			// Vec[int]( opens a literal of Vec.
			if len(stack) > 0 {
				t := stack[len(stack)-1].typ
				stack = stack[:len(stack)-1]
				prev = t
				i++
				if t != "" && i+1 < len(code) && code[i] == '.' && isDocNameByte(code[i+1]) {
					// Atomic[int].New names the method Atomic.New.
					qual = t + "."
					i++
				}
				continue
			}
		case c == ')' || c == '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
		prev = ""
		i++
	}
	return true
}

// initializer is a constant's or a var's written declaration w, or its
// head alone (`export const Append: Options`) when the initializer names
// something a program outside the module cannot write: a private item or
// field, or an item of a std-only module. typ is the checker's spelling of
// the type, for a head that writes none.
func (n *docNames) initializer(e qEntry, w []string, typ string) []string {
	joined := strings.Join(w, "\n")
	code := codeOnly(joined, false)
	eq := -1
	depth := 0
	for i := 0; i < len(code) && eq < 0; i++ {
		switch code[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '=':
			if depth == 0 && (i+1 >= len(code) || code[i+1] != '=') && (i == 0 || strings.IndexByte("=<>!", code[i-1]) < 0) {
				eq = i
			}
		}
	}
	if eq < 0 || n.writable(e.mod, joined[eq+1:]) {
		return w
	}
	head := strings.TrimRight(joined[:eq], " \t\r\n")
	if _, after, _ := strings.Cut(head, e.name); !strings.Contains(after, ":") {
		head += ": " + typ
	}
	return []string{head}
}

// spell rewrites the std.core spellings in a std module's declaration
// lines as a program writes them: a prelude name bare (&core.Heap is
// &Heap), a type the module re-exports by an alias under that alias
// (core.IoTable is IoTable in std.runtime). docHider leaves out the items
// that need any other std.core type, so none reaches a page.
func (n *docNames) spell(mod string, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		var b strings.Builder
		for {
			j := strings.Index(l, "core.")
			if j < 0 {
				break
			}
			k := j + len("core.")
			for k < len(l) && isDocNameByte(l[k]) {
				k++
			}
			name := l[j+len("core.") : k]
			repl := ""
			if j == 0 || !isDocNameByte(l[j-1]) && l[j-1] != '.' {
				if a, ok := n.alias[mod+"\x00"+name]; ok {
					repl = a
				} else if n.prelude[name] {
					repl = name
				}
			}
			if repl == "" {
				b.WriteString(l[:k])
			} else {
				b.WriteString(l[:j] + repl)
			}
			l = l[k:]
		}
		out[i] = b.String() + l
	}
	return out
}
