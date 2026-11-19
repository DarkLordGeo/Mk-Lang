// Why: a hover is most useful when it says what a name means here: the type a
// local holds, the loan it holds, the error set a `fail` returns.
//
// What: keywordContext, localHoverMD, declMarkdown, labelHover, methodContext,
// errorSetsMD and typeSummary.
//
// How: each helper reads the enclosing function's signature and the
// declarations the text resolves to; the result is markdown appended below
// the main hover.

package lsp

import (
	"path/filepath"
	"strings"
)

// maxSummaryLines is the most lines of a type summary under a hover.
const maxSummaryLines = 14

// keywordContext is what a keyword means at the cursor, as markdown to
// append to its rule; "" when nothing more is known.
func (s *Server) keywordContext(c cursor) string {
	switch c.name {
	case "fail", "return", "onerr", "try", "else":
	default:
		return ""
	}
	fn, ok := s.enclosingFunc(c)
	if !ok {
		return ""
	}
	var b strings.Builder
	res := resultOf(declHead(c.f, fn))
	if c.name == "try" {
		if callee, ok := s.calleeAfter(c, c.i+1); ok {
			if cr := resultOf(declHead(callee.f, callee.decl)); cr != "" {
				b.WriteString("\n\nHere: `try " + callee.decl.Name + "(..)`, whose result is `" + cr + "`")
				if set := s.errorSetText(callee, cr); set != "" {
					b.WriteString("; it fails with " + set)
				}
			}
		}
	}
	if res == "" {
		res = "void"
	}
	b.WriteString("\n\nIn `" + fn.Name + "`, whose result is `" + res + "`")
	if set := s.errorSetText(hit{f: c.f, decl: fn}, res); set != "" {
		b.WriteString(": it fails with " + set)
	}
	return b.String()
}

// enclosingFunc is the innermost function or method declaration holding
// the cursor.
func (s *Server) enclosingFunc(c cursor) (decl, bool) {
	off := c.ct[c.i].Start
	var best decl
	found := false
	var walk func(ds []decl)
	walk = func(ds []decl) {
		for _, d := range ds {
			if d.Start <= off && off < d.End {
				if d.Kind == symFunction || d.Kind == symMethod {
					best, found = d, true
				}
				walk(d.Children)
			}
		}
	}
	walk(c.f.decls)
	return best, found
}

// resultOf is a signature's result type: the text after the parameter
// list's `):`, without its flow clauses and contracts; "" for none.
func resultOf(head string) string {
	open := strings.IndexByte(head, '(')
	if open < 0 {
		return ""
	}
	d := 0
	for i := open; i < len(head); i++ {
		switch head[i] {
		case '(', '[':
			d++
		case ')', ']':
			d--
			if d == 0 && head[i] == ')' {
				rest := strings.TrimSpace(head[i+1:])
				if !strings.HasPrefix(rest, ":") {
					return ""
				}
				rest = strings.TrimSpace(rest[1:])
				for _, cut := range []string{"\n", " from ", " into ", " ends ", " requires ", " ensures "} {
					if j := strings.Index(rest, cut); j >= 0 {
						rest = rest[:j]
					}
				}
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}

// errorSetText spells the error set of a result `E!T` (as declared at h):
// `E`, and for an alias its variants, `Error = A | B`; "" for no `!`.
func (s *Server) errorSetText(h hit, res string) string {
	i := topLevelBang(res)
	if i < 0 {
		return ""
	}
	set := strings.TrimSpace(res[:i])
	if strings.Contains(set, "|") {
		return "`" + set + "`"
	}
	if alias, ok := s.aliasOf(h.f.path, set); ok {
		return "`" + set + " = " + alias + "`"
	}
	return "`" + set + "`"
}

// aliasOf is the right side of `type name = ...` seen from path's module:
// that module first, then the root and std.
func (s *Server) aliasOf(path, name string) (string, bool) {
	name = lastSegment(name)
	dir := pathKey(filepath.Dir(path))
	var far string
	for _, h := range s.typeDecls(path, func(d decl) bool { return d.Detail == "type" && d.Name == name }) {
		head := declHead(h.f, h.decl)
		_, rhs, ok := strings.Cut(head, "=")
		if !ok {
			continue
		}
		rhs = strings.Join(strings.Fields(rhs), " ")
		if pathKey(filepath.Dir(h.f.path)) == dir {
			return rhs, true
		}
		if far == "" {
			far = rhs
		}
	}
	return far, far != ""
}

// typeDecls are the declarations pred matches in the root of path and in
// std.
func (s *Server) typeDecls(path string, pred func(decl) bool) []hit {
	r := s.rootOfPath(path)
	return s.searchPaths(dedupPaths(append(s.rootFiles(r), s.stdFiles()...)), pred)
}

// errorSetsOf are the aliases whose variants list an error named name, as
// `Error = A | B (module std.compress)`.
func (s *Server) errorSetsOf(path, name string) []string {
	var out []string
	for _, h := range s.typeDecls(path, func(d decl) bool { return d.Detail == "type" }) {
		head := declHead(h.f, h.decl)
		_, rhs, ok := strings.Cut(head, "=")
		if !ok {
			continue
		}
		for _, v := range strings.Split(rhs, "|") {
			if lastSegment(strings.TrimSpace(v)) == name {
				line := h.decl.Name + " = " + strings.Join(strings.Fields(rhs), " ")
				if m := s.moduleOf(h.f.path); m != "" {
					line += "  (" + m + ")"
				}
				out = append(out, line)
				break
			}
		}
	}
	return out
}

// calleeAfter is the declaration of the function called at ct[k]
// (`F(`, `m.F(`, `x.M(`), by definition's rules.
func (s *Server) calleeAfter(c cursor, k int) (hit, bool) {
	text, ct := c.f.text.s, c.ct
	for isIdent(ct, k) && word(text, ct, k+1) == "." && isIdent(ct, k+2) {
		k += 2
	}
	if !isIdent(ct, k) || word(text, ct, k+1) != "(" {
		return hit{}, false
	}
	cc := cursor{f: c.f, ct: ct, i: k, name: word(text, ct, k)}
	if k >= 2 && word(text, ct, k-1) == "." && isIdent(ct, k-2) {
		cc.qual = word(text, ct, k-2)
	}
	for _, l := range s.defs(cc) {
		if h, ok := s.declAt(l); ok && (h.decl.Kind == symFunction || h.decl.Kind == symMethod) {
			return h, true
		}
	}
	return hit{}, false
}

// typeSummary is the declaration of the named type t refers to (`mut Dec`
// is Dec), as a markdown section under a hover; "" for a primitive, a
// type the text does not declare, or a type no larger than its name.
func (s *Server) typeSummary(c cursor, t string) string {
	base := baseType(t)
	if i := strings.IndexByte(base, '['); i >= 0 {
		base = base[:i]
	}
	if base == "" || primTypes[base] || strings.ContainsAny(base, "() |") {
		return ""
	}
	tc := cursor{f: c.f, ct: c.ct, i: c.i, name: lastSegment(base)}
	if q, _, ok := strings.Cut(base, "."); ok && q != tc.name {
		tc.qual = q
	}
	for _, l := range s.defs(tc) {
		h, ok := s.declAt(l)
		if !ok || h.decl.Name != tc.name {
			continue
		}
		switch h.decl.Detail {
		case "struct", "enum", "error", "interface", "type":
		default:
			continue
		}
		head := strings.Split(declHead(h.f, h.decl), "\n")
		if len(head) > maxSummaryLines {
			head = append(head[:maxSummaryLines:maxSummaryLines], "    // ...")
		}
		out := "\n\n**" + tc.name + "**"
		if m := s.moduleOf(h.f.path); m != "" {
			out += " (" + m + ")"
		}
		return out + "\n" + codeBlock(head)
	}
	return ""
}

// parentOf is the declaration whose children hold d (a field's struct).
func parentOf(ds []decl, d decl) (decl, bool) {
	for _, p := range ds {
		for _, ch := range p.Children {
			if ch.NameStart == d.NameStart {
				return p, true
			}
		}
		if q, ok := parentOf(p.Children, d); ok {
			return q, true
		}
	}
	return decl{}, false
}

// fieldType is the type a field's declaration spells: the text after its
// `:` to the end of its line, or to `,` or `)` in a payload list.
func fieldType(text string, nameEnd int) string {
	rest := text[nameEnd:]
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, ":") {
		return ""
	}
	rest = rest[1:]
	d := 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '(', '[':
			d++
		case ')', ']':
			if d == 0 {
				return strings.TrimSpace(rest[:i])
			}
			d--
		case ',':
			if d == 0 {
				return strings.TrimSpace(rest[:i])
			}
		case '\n':
			return strings.TrimSpace(rest[:i])
		}
	}
	return strings.TrimSpace(rest)
}

// localLoan is the loan a local holds by its binding: `x := y`,
// `var x := mut y[a..b]` hold y's when the type is a reference.
func localLoan(text string, ct []token, k int, t string) string {
	if !strings.HasPrefix(t, "&") && !strings.HasPrefix(t, "mut ") {
		return ""
	}
	if word(text, ct, k+1) != ":" || word(text, ct, k+2) != "=" {
		return ""
	}
	j := k + 3
	for word(text, ct, j) == "mut" || word(text, ct, j) == "&" {
		j++
	}
	if !isIdent(ct, j) {
		return ""
	}
	if w := word(text, ct, j+1); w == "(" || w == "." {
		return "" // a call's or a member's result: its loan is the callee's flow
	}
	return word(text, ct, j)
}

// localHoverMD is a local's or parameter's hover: `(local) x: T` or
// `(param) x: T`, the loan it holds, and its type's declaration.
func (s *Server) localHoverMD(c cursor, a, b, at int) hover {
	text, ct := c.f.text.s, c.ct
	lines := s.localHover(c, a, b, at)
	k := tokenStarting(ct, at)
	param := k >= 0 && localDeclKind(text, ct, a, b, k) == declParam
	if param {
		lines[0] = "(param)" + strings.TrimPrefix(lines[0], "(local)")
	}
	h := hoverOf(c, lines, s.utf8.Load())
	_, t, _ := strings.Cut(lines[0], ": ")
	switch {
	case t == "":
	case param && strings.HasPrefix(t, "mut "):
		h.Contents.Value += "\n\nA `mut` loan the caller passes: the body may change what it refers to."
	case param && strings.HasPrefix(t, "&"):
		h.Contents.Value += "\n\nA shared loan the caller passes."
	case k >= 0:
		if from := localLoan(text, ct, k, t); from != "" {
			h.Contents.Value += "\n\nHolds a loan from `" + from + "`."
		}
	}
	h.Contents.Value += s.typeSummary(c, t)
	return h
}

// modSuffix is " (module m)" for a module, else "".
func modSuffix(m string) string {
	if m == "" {
		return ""
	}
	return " (module " + m + ")"
}

// declMarkdown renders a declaration the cursor's name resolves to by the
// text: a field with its type, its struct and its type's declaration; a
// variant with its enum; else the declaration's head, its comment, and for
// a type its methods and impls (and an error's sets), for a method its
// receiver here and its std surface entry.
func (s *Server) declMarkdown(c cursor, h hit) (string, bool) {
	d := h.decl
	head := declHead(h.f, d)
	if head == "" {
		return "", false
	}
	mod := s.moduleOf(h.f.path)
	doc := docAbove(h.f.text.s, d.Start)
	var b strings.Builder
	switch d.Kind {
	case symField, symEnumMember:
		what, line := "(field) ", d.Name
		if d.Kind == symEnumMember {
			what, line = "(variant) ", head
		} else if t := fieldType(h.f.text.s, d.NameEnd); t != "" {
			line += ": " + t
		}
		b.WriteString(codeBlock([]string{what + line}))
		if p, ok := parentOf(h.f.decls, d); ok {
			b.WriteString("\n\nIn `" + p.Detail + " " + p.Name + "`" + modSuffix(mod))
		}
		if doc != "" {
			b.WriteString("\n\n" + doc)
		}
		if t, ok := s.typeAt(c.f, c.ct[c.i].End); ok {
			b.WriteString("\n\nChecked type here: `" + t + "`")
		}
		if d.Kind == symField {
			b.WriteString(s.typeSummary(c, fieldType(h.f.text.s, d.NameEnd)))
		}
		return b.String(), true
	}
	lines := []string{head}
	if mod != "" {
		lines = append(lines, "module "+mod)
	}
	b.WriteString(codeBlock(lines))
	if doc != "" {
		b.WriteString("\n\n" + doc)
	}
	switch d.Detail {
	case "struct", "enum", "error", "interface", "type":
		if d.Detail == "error" {
			b.WriteString(s.errorSetsMD(h.f.path, d.Name))
		}
		if ms := s.lexMembers(h); len(ms) > 0 {
			b.WriteString("\n\n**Methods and impls**\n" + codeBlock(capLines(ms)))
		}
	default:
		b.WriteString(s.methodContext(c, d.Owner, d.Name))
		if d.Kind != symMethod {
			if t, ok := s.typeAt(c.f, c.ct[c.i].End); ok {
				b.WriteString("\n\nChecked type here: `" + t + "`")
			}
		}
	}
	return b.String(), true
}

// errorSetsMD lists the error sets that name an error, as markdown.
func (s *Server) errorSetsMD(path, name string) string {
	sets := s.errorSetsOf(path, name)
	if len(sets) == 0 {
		return ""
	}
	return "\n\n**In the error sets**\n" + codeBlock(sets)
}

// methodContext is a method's receiver here (`out: mut []u8`) and its
// std surface entry with its owner's notes (Slice[T]'s rule for slices,
// arrays and lists), as markdown; "" for a function with neither.
func (s *Server) methodContext(c cursor, owner, name string) string {
	return s.methodContextOpt(c, owner, name, true)
}

// methodContextOpt is methodContext, with the std surface entry only when
// surface is set (a caller that showed it already).
func (s *Server) methodContextOpt(c cursor, owner, name string, surface bool) string {
	var b strings.Builder
	if owner != "" && afterDot(c) && c.i >= 2 {
		if t, ok := s.typeAt(c.f, c.ct[c.i-2].End); ok {
			recv := word(c.f.text.s, c.ct, c.i-2)
			if !isIdent(c.ct, c.i-2) {
				recv = "(..)"
			}
			b.WriteString("\n\nHere on `" + recv + ": " + t + "`")
		}
	}
	key := name
	if owner != "" {
		key = owner + "." + name
	} else if m := s.moduleOf(c.f.path); m != "" {
		key = lastSegment(m) + "." + name
	}
	if d, ok := surfaceFor(key); ok && surface {
		b.WriteString("\n\n" + surfaceMarkdown(d))
	}
	if owner != "" {
		if d, ok := surfaceDocs[owner]; ok && len(d.Notes) > 0 {
			b.WriteString("\n\n" + strings.Join(d.Notes, "\n\n"))
		}
	}
	return b.String()
}

// labelHover is the hover of a label the cursor's name resolves to at l:
// a payload field `offset: int` of an error, a struct literal's field, a
// record's field, or a loop label.
func (s *Server) labelHover(c cursor, l Location) (string, bool) {
	p, ok := uriPath(l.URI)
	if !ok {
		return "", false
	}
	f, ok := s.fileOf(p)
	if !ok {
		return "", false
	}
	u8 := s.utf8.Load()
	a, z := f.text.off(l.Range.Start, u8), f.text.off(l.Range.End, u8)
	if a >= z || z > len(f.text.s) {
		return "", false
	}
	name := f.text.s[a:z]
	rest := strings.TrimLeft(f.text.s[z:], " \t")
	if !strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, ":=") {
		return "", false
	}
	if strings.HasPrefix(strings.TrimSpace(rest[1:]), "for") {
		return codeBlock([]string{"(label) " + name}) + "\n\nA loop label: `break " + name + "` and `continue " + name + "` name this loop.", true
	}
	var holder decl
	var walk func(ds []decl)
	walk = func(ds []decl) {
		for _, d := range ds {
			if d.Start <= a && a < d.End {
				holder = d
				walk(d.Children)
			}
		}
	}
	walk(f.decls)
	t := fieldType(f.text.s, z)
	var b strings.Builder
	mod := s.moduleOf(p)
	if (holder.Kind == symFunction || holder.Kind == symMethod) && !strings.Contains(f.text.s[holder.Start:a], "{") {
		// A parameter of a signature with no body yet before it (an
		// interface's method, a bodiless declaration).
		b.WriteString(codeBlock([]string{"(param) " + name + ": " + t}))
		b.WriteString("\n\nParameter of `" + holder.Name + "`" + modSuffix(mod))
		b.WriteString(s.typeSummary(c, t))
		return b.String(), true
	}
	if holder.Kind == symFunction || holder.Kind == symMethod {
		// A record's field in a body: its value's checked type.
		line := "(record field) " + name
		if vt, ok := s.typeAt(f, z+len(f.text.s[z:])-len(strings.TrimLeft(f.text.s[z:], " \t:"))+len(t)); ok {
			line += ": " + vt
		}
		b.WriteString(codeBlock([]string{line}))
		return b.String(), true
	}
	b.WriteString(codeBlock([]string{"(field) " + name + ": " + t}))
	if holder.Name != "" {
		role := "In"
		if holder.Detail == "error" {
			role = "Payload of"
		}
		b.WriteString("\n\n" + role + " `" + holder.Detail + " " + holder.Name + "`" + modSuffix(mod))
		if holder.Detail == "error" {
			b.WriteString(s.errorSetsMD(p, holder.Name))
		}
	}
	b.WriteString(s.typeSummary(c, t))
	return b.String(), true
}
