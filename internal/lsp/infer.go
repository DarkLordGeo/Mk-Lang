// Why: inlay hints, the hover on a local and member completion after `x.`
// need the type of a local declared `x := e` even when the last good check
// has no answer for it (the file does not compile right now).
//
// What: inferEnv with localTypeAt and exprType, the declaration readers
// callResult, declResult and fieldType, the type-text helpers (spelledType,
// typeText, baseType, literalType) and localHover.
//
// How: lexical inference over tokens. It answers only when e is one form
// whose type the text proves: a literal, a construction `T(..)`, an enum
// variant `E.V`, a call of a function or method whose declaration spells
// its result (the root's files or std on disk), a field of a local whose
// type is known, a copy of another local, `try` of one of these, or `&` of
// one. Anything else (an operator, a generic result, a chain) answers "", so
// a hint is never a guess. The checker's own type (driver TypeAt, exact.go)
// answers first; this layer is the fallback.

package lsp

import (
	"strings"
)

// inferDepth bounds the chain of locals one inference follows.
const inferDepth = 4

// inferEnv is one file's inference: the type names the root and std
// declare and the file's imports, read once per request.
type inferEnv struct {
	s     *Server
	f     srcFile
	ct    []token
	r     *root
	types map[string]*typeInfo
	imps  map[string]string
}

func (s *Server) newInfer(f srcFile, ct []token) *inferEnv {
	r := s.rootOfPath(f.path)
	return &inferEnv{s: s, f: f, ct: ct, r: r, types: s.knownTypes(r), imps: imports(f.text.s, ct)}
}

// localTypeAt is the type of the local declared at ct[k] in the item
// ct[a..b]: the declared type as spelled (`x: &mut T`), or the inferred
// type of `x := e`; "" when the text does not prove one.
func (in *inferEnv) localTypeAt(a, b, k, depth int) string {
	text, ct := in.f.text.s, in.ct
	if depth > inferDepth || k < 0 || k+1 >= len(ct) {
		return ""
	}
	if word(text, ct, k+1) == ":" && word(text, ct, k+2) == "=" && ct[k+2].Start == ct[k+1].End {
		// The checker's type first (exact.go); the text's when the last
		// good check did not type this local.
		if t, ok := in.s.typeAt(in.f, ct[k].End); ok {
			return t
		}
		if k > a && word(text, ct, k-1) == "," {
			return "" // one name of several: the result is split
		}
		return in.exprType(a, b, k+3, stmtEnd(text, ct, k+3, b), depth+1)
	}
	if word(text, ct, k+1) == ":" {
		return spelledType(text, ct, k+2, b)
	}
	return ""
}

// stmtEnd is the index of the last token of the statement or argument
// that starts at ct[j]: before a token at depth 0 that starts a line, a
// `,` or `;` at depth 0, or the bracket that closes an enclosing one.
func stmtEnd(text string, ct []token, j, b int) int {
	d := 0
	for k := j; k <= b; k++ {
		if k > j && d == 0 && ct[k].NL {
			return k - 1
		}
		switch w := word(text, ct, k); {
		case depthDelta(text, ct[k]) > 0:
			d++
		case depthDelta(text, ct[k]) < 0:
			if d == 0 {
				return k - 1
			}
			d--
		case d == 0 && (w == "," || w == ";"):
			return k - 1
		}
	}
	return b
}

// spelledType is the type written from ct[j] to the end of a declaration:
// up to `=`, `,`, `)` or a line end at depth 0.
func spelledType(text string, ct []token, j, b int) string {
	var parts []string
	d := 0
	for k := j; k <= b; k++ {
		w := word(text, ct, k)
		if k > j && d == 0 && ct[k].NL {
			break
		}
		if d == 0 && (w == "=" || w == "," || w == "{" || w == ";") {
			break
		}
		if depthDelta(text, ct[k]) < 0 {
			if d == 0 {
				break
			}
			d--
		} else if depthDelta(text, ct[k]) > 0 {
			d++
		}
		parts = append(parts, w)
	}
	return typeText(parts)
}

// typeText spells type tokens: a space between two words and after a
// comma or a colon, none elsewhere (`&mut T`, `E!T`, `fn(int): bool`).
func typeText(parts []string) string {
	var sb strings.Builder
	for i, p := range parts {
		if i > 0 {
			prev := parts[i-1]
			if prev == "," || prev == ":" || isWordText(prev) && isWordText(p) {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(p)
	}
	return sb.String()
}

func isWordText(w string) bool { return w != "" && isIdentByte(w[0]) }

// exprType is the type of the expression ct[j..e], or "".
func (in *inferEnv) exprType(a, b, j, e, depth int) string {
	s, f, ct := in.s, in.f, in.ct
	text := f.text.s
	if j > e || j >= len(ct) || depth > inferDepth {
		return ""
	}
	w := word(text, ct, j)
	switch {
	case w == "try":
		t := in.exprType(a, b, j+1, e, depth)
		if i := topLevelBang(t); i >= 0 {
			return t[i+1:]
		}
		return t
	case w == "&":
		if word(text, ct, j+1) == "mut" {
			if t := in.exprType(a, b, j+2, e, depth); t != "" {
				return "&mut " + t
			}
			return ""
		}
		if t := in.exprType(a, b, j+1, e, depth); t != "" {
			return "&" + t
		}
		return ""
	case w == "-" && j+1 == e && ct[e].Kind == tNumber:
		return literalType(text, ct[e])
	case j == e && (ct[j].Kind == tNumber || ct[j].Kind == tString):
		return literalType(text, ct[j])
	case j == e && (w == "true" || w == "false"):
		return "bool"
	}
	// A path `a.b.c`, then an optional `[..]` and an optional call `(..)`
	// that ends the expression.
	var segs []int
	k := j
	for isIdent(ct, k) && k <= e {
		segs = append(segs, k)
		if k+1 <= e && word(text, ct, k+1) == "." && isIdent(ct, k+2) {
			k += 2
			continue
		}
		break
	}
	if len(segs) == 0 {
		return ""
	}
	last := segs[len(segs)-1]
	n := last + 1
	brackets := ""
	if n <= e && word(text, ct, n) == "[" {
		m := matching(text, ct, n)
		var parts []string
		for q := n; q <= m; q++ {
			parts = append(parts, word(text, ct, q))
		}
		brackets = typeText(parts)
		n = m + 1
	}
	call := false
	if n <= e && word(text, ct, n) == "(" {
		n = matching(text, ct, n) + 1
		call = true
	}
	if n != e+1 {
		return "" // an operator, an index or a chain after the primary
	}
	names := make([]string, len(segs))
	for i, q := range segs {
		names[i] = word(text, ct, q)
	}
	r, types, imps := in.r, in.types, in.imps
	head := names[0]
	switch len(names) {
	case 1:
		if keywords[head] || ruleWords[head] {
			return ""
		}
		if ti := types[head]; ti != nil && call && !ti.enum {
			return head + brackets // a construction
		}
		if !call {
			if brackets != "" {
				return ""
			}
			if at, ok := localDecl(text, ct, a, b, ct[j].Start, head); ok {
				return in.localTypeAt(a, b, tokenStarting(ct, at), depth+1)
			}
			return ""
		}
		if brackets != "" {
			return "" // a generic call: its result follows its arguments
		}
		return s.callResult(cursor{f: f, ct: ct, i: last, name: head}, "")
	case 2:
		m := names[1]
		if imps[head] != "" {
			if !call || brackets != "" {
				return ""
			}
			if ti := types[m]; ti != nil && !ti.enum {
				return head + "." + m // a construction of another module's type
			}
			return s.callResult(cursor{f: f, ct: ct, i: last, name: m, qual: head}, "")
		}
		owner := ""
		if ti := types[head]; ti != nil {
			if ti.enum && ti.members[m] {
				return head // a variant
			}
			owner = head
		} else if head == "self" {
			if owner = selfType(text, ct, a); owner == "" {
				return ""
			}
			if !call {
				return s.fieldType(r, owner, m)
			}
		} else if at, ok := localDecl(text, ct, a, b, ct[j].Start, head); ok {
			owner = baseType(in.localTypeAt(a, b, tokenStarting(ct, at), depth+1))
			if owner == "" {
				return ""
			}
			if !call {
				return s.fieldType(r, owner, m)
			}
		} else {
			return ""
		}
		if !call || brackets != "" {
			return ""
		}
		return s.callResult(cursor{f: f, ct: ct, i: last, name: m, qual: owner}, owner)
	}
	return ""
}

// selfType is the type that declares the method whose item starts at ct[a]
// (`[export] T.M(`, `T[..].M(`), or "".
func selfType(text string, ct []token, a int) string {
	for a < len(ct) && (word(text, ct, a) == "export" || word(text, ct, a) == "unsafe" || word(text, ct, a) == "naked") {
		a++
	}
	if !isIdent(ct, a) || keywords[word(text, ct, a)] {
		return ""
	}
	k := a + 1
	if word(text, ct, k) == "[" {
		k = matching(text, ct, k) + 1
	}
	if word(text, ct, k) == "." && isIdent(ct, k+1) {
		return word(text, ct, a)
	}
	return ""
}

// tokenStarting is the index of the code token starting at off, or -1.
func tokenStarting(ct []token, off int) int {
	lo, hi := 0, len(ct)
	for lo < hi {
		m := (lo + hi) / 2
		if ct[m].Start < off {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo < len(ct) && ct[lo].Start == off {
		return lo
	}
	return -1
}

// literalType is a literal's type with no expected type.
func literalType(text string, t token) string {
	w := text[t.Start:t.End]
	if t.Kind == tNumber {
		if strings.HasPrefix(w, "0x") || strings.HasPrefix(w, "0o") || strings.HasPrefix(w, "0b") {
			return "int"
		}
		if strings.ContainsAny(w, ".eE") {
			return "f64"
		}
		return "int"
	}
	switch {
	case strings.HasPrefix(w, "b'"):
		return "u8"
	case strings.HasPrefix(w, "'"):
		return "char"
	case strings.HasPrefix(w, `b"`):
		return "[]u8"
	case strings.HasPrefix(w, `"`), strings.HasPrefix(w, `r"`):
		return "str"
	}
	return ""
}

// topLevelBang is the index of the `!` of an error result `E!T` outside
// brackets, or -1.
func topLevelBang(t string) int {
	d := 0
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '(', '[':
			d++
		case ')', ']':
			d--
		case '!':
			if d == 0 {
				return i
			}
		}
	}
	return -1
}

// baseType is the named type a type's members come from: past an error
// set, `?`, `&` and `mut`, without generic arguments (`E!&mut List[T]` is
// List).
func baseType(t string) string {
	if i := topLevelBang(t); i >= 0 {
		t = t[i+1:]
	}
	for {
		switch {
		case strings.HasPrefix(t, "?"), strings.HasPrefix(t, "&"):
			t = t[1:]
			continue
		case strings.HasPrefix(t, "mut "):
			t = t[4:]
			continue
		}
		break
	}
	end := 0
	for end < len(t) && (isIdentByte(t[end]) || t[end] == '.') {
		end++
	}
	return t[:end]
}

// callResult is the result type the declaration of the called function
// or method spells, `Self` read as owner; "" when it has none to read, or
// when it names a generic parameter.
func (s *Server) callResult(c cursor, owner string) string {
	for _, l := range s.defs(c) {
		h, ok := s.declAt(l)
		if !ok || h.decl.Kind != symFunction && h.decl.Kind != symMethod {
			continue
		}
		res, generic, ok := declResult(h.f, h.decl)
		if !ok {
			continue
		}
		if res == "" {
			return "void"
		}
		if namesWord(res, "Self") {
			if owner == "" || generic[h.decl.Owner] {
				return ""
			}
			res = replaceWord(res, "Self", owner)
		}
		for g := range generic {
			if g != h.decl.Owner && namesWord(res, g) {
				return ""
			}
		}
		return res
	}
	return ""
}

// declResult reads a function's declaration: its result type as spelled
// ("" for none), and the generic parameters of the function and of its
// receiver type (`List[T].Len`), with that type itself marked when it is generic.
func declResult(f srcFile, d decl) (string, map[string]bool, bool) {
	text := f.text.s
	ct := code(f.toks)
	i := tokenStarting(ct, d.NameStart)
	if i < 0 {
		return "", nil, false
	}
	generic := map[string]bool{}
	// The owner's brackets: `List[T].Len` from the line's start.
	ls := i
	for ls > 0 && !ct[ls].NL {
		ls--
	}
	for k := ls; k < i; k++ {
		if word(text, ct, k) == "[" {
			m := matching(text, ct, k)
			for q := k + 1; q < m && q < i; q++ {
				if isIdent(ct, q) && word(text, ct, q-1) != ":" {
					generic[word(text, ct, q)] = true
				}
			}
			if d.Owner != "" {
				generic[d.Owner] = true
			}
			k = m
		}
	}
	k := i + 1
	if word(text, ct, k) == "[" {
		m := matching(text, ct, k)
		for q := k + 1; q < m; q++ {
			prev := word(text, ct, q-1)
			if prev == "const" {
				prev = word(text, ct, q-2)
			}
			if isIdent(ct, q) && word(text, ct, q) != "const" && (prev == "[" || prev == ",") {
				generic[word(text, ct, q)] = true
			}
		}
		k = m + 1
	}
	if word(text, ct, k) != "(" {
		return "", nil, false
	}
	k = matching(text, ct, k) + 1
	if word(text, ct, k) != ":" || k < len(ct) && ct[k].NL {
		return "", generic, true
	}
	var parts []string
	dep := 0
	for q := k + 1; q < len(ct); q++ {
		w := word(text, ct, q)
		if q > k+1 && dep == 0 && ct[q].NL {
			break
		}
		if dep == 0 && (w == "{" || flowWords[w] || w == "requires" || w == "ensures" || w == "cpu" || w == "section") {
			break
		}
		if depthDelta(text, ct[q]) > 0 {
			dep++
		} else if depthDelta(text, ct[q]) < 0 {
			dep--
		}
		parts = append(parts, w)
	}
	return typeText(parts), generic, len(parts) > 0
}

// namesWord reports whether type text t names the identifier w.
func namesWord(t, w string) bool {
	for i := 0; i+len(w) <= len(t); i++ {
		if t[i:i+len(w)] == w && (i == 0 || !isIdentByte(t[i-1])) && (i+len(w) == len(t) || !isIdentByte(t[i+len(w)])) {
			return true
		}
	}
	return false
}

// replaceWord replaces each whole-word w in t with by.
func replaceWord(t, w, by string) string {
	var sb strings.Builder
	for i := 0; i < len(t); {
		if i+len(w) <= len(t) && t[i:i+len(w)] == w && (i == 0 || !isIdentByte(t[i-1])) && (i+len(w) == len(t) || !isIdentByte(t[i+len(w)])) {
			sb.WriteString(by)
			i += len(w)
			continue
		}
		sb.WriteByte(t[i])
		i++
	}
	return sb.String()
}

// fieldType is the spelled type of field name of the type named owner, declared
// in the root's files or std, when that type is not generic.
func (s *Server) fieldType(r *root, owner, name string) string {
	isOwner := func(d decl) bool { return d.Owner == "" && d.Name == owner && d.Kind == symStruct }
	hs := s.search(r, isOwner)
	if len(hs) == 0 {
		hs = s.searchStd("", isOwner)
	}
	for _, h := range hs {
		text := h.f.text.s
		if strings.Contains(text[h.decl.NameEnd:min(len(text), h.decl.NameEnd+1)], "[") {
			return "" // generic: the field's type follows the arguments
		}
		ct := code(h.f.toks)
		for _, ch := range h.decl.Children {
			if ch.Name != name || ch.Kind != symField {
				continue
			}
			k := tokenStarting(ct, ch.NameStart)
			if k < 0 || word(text, ct, k+1) != ":" {
				return ""
			}
			return spelledType(text, ct, k+2, len(ct)-1)
		}
	}
	return ""
}

// localHover is a local's hover: `(local) x: T` with its declared or
// inferred type, then `mk query kind T` when T is a named type the text
// spells bare (a reference, an optional or a slice has its own kind), and
// only from a package that checks to types.
func (s *Server) localHover(c cursor, a, b, at int) []string {
	line := "(local) " + c.name
	t := s.newInfer(c.f, c.ct).localTypeAt(a, b, tokenStarting(c.ct, at), 0)
	if t == "" {
		// The checker's type of this use (for example `i` in `for i in 0..n`).
		t, _ = s.typeAt(c.f, c.ct[c.i].End)
	}
	if t == "" {
		return []string{line}
	}
	out := []string{line + ": " + t}
	if t != baseType(t) || primTypes[t] {
		return out
	}
	r := s.rootOfPath(c.f.path)
	if k, err := s.query(r, "kind", []string{t}); err == nil && k != nil && k.Found && !k.Partial {
		out = append(out, k.Answer...)
	}
	return out
}
