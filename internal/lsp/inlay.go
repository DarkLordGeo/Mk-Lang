// Why: inlay hints show what the text leaves out: argument names and the types
// of `:=` locals.
//
// What: inlayHints.
//
// How: parameter names come from the callee's declaration as definition finds it;
// local types come from the checker (typeAt) first, else from the lexical
// inference of infer.go, which never guesses.

package lsp

import (
	"strings"
)

type inlayHintParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

// Inlay hint kinds (LSP 3.17 InlayHintKind).
const (
	inlayType      = 1
	inlayParameter = 2
)

type inlayHint struct {
	Position     Position `json:"position"`
	Label        string   `json:"label"`
	Kind         int      `json:"kind"`
	PaddingRight bool     `json:"paddingRight,omitempty"`
}

// inlayHints answers textDocument/inlayHint for the calls in the range.
func (s *Server) inlayHints(p inlayHintParams) (any, *rpcError) {
	f, e := s.fileForURI(p.TextDocument.URI)
	if e != nil {
		return nil, e
	}
	u8 := s.utf8.Load()
	text := f.text.s
	from, to := f.text.off(p.Range.Start, u8), f.text.off(p.Range.End, u8)
	ct := code(f.toks)
	out := []inlayHint{}
	known := map[string][]string{} // callee spelling -> parameter names
	for i := 1; i < len(ct); i++ {
		if ct[i].Start < from || ct[i].Start >= to || word(text, ct, i) != "(" || !isIdent(ct, i-1) || ct[i-1].End != ct[i].Start {
			continue
		}
		name := word(text, ct, i-1)
		if keywords[name] || ruleWords[name] {
			continue
		}
		// A declaration's own parameter list is not a call.
		if isDeclHead(text, ct, i-1) {
			continue
		}
		c := cursor{f: f, ct: ct, i: i - 1, name: name}
		if i >= 3 && word(text, ct, i-2) == "." && isIdent(ct, i-3) {
			c.qual = word(text, ct, i-3)
		}
		key := c.qual + "." + name
		params, ok := known[key]
		if !ok {
			params = s.calleeParams(c)
			known[key] = params
		}
		if len(params) == 0 {
			continue
		}
		// A method called on a value: its receiver is written before the dot.
		if receiverParam(params[0]) && c.qual != "" && !isTypeName(c.qual) {
			params = params[1:]
		}
		for n, arg := range callArgs(text, ct, i) {
			if n >= len(params) {
				break
			}
			a := arg[0]
			pn := paramName(params[n])
			if pn == "" || receiverParam(params[n]) {
				continue
			}
			// A named argument, or one that spells the parameter (`io`,
			// `sys.io`), needs no hint.
			if isIdent(ct, a) && word(text, ct, a+1) == ":" && word(text, ct, a+2) != "=" || pathEndsIn(text, ct, arg, pn) {
				continue
			}
			out = append(out, inlayHint{Position: f.text.pos(ct[a].Start, u8), Label: pn + ":", Kind: inlayParameter, PaddingRight: true})
		}
	}
	return append(out, s.typeHints(f, ct, from, to)...), nil
}

// typeHints are `: T` after each `x :=` in [from, to) whose type the
// inference proves (infer.go), except where the right side starts by
// spelling it (`p := Pair(..)`, `b := Buf.New()`).
func (s *Server) typeHints(f srcFile, ct []token, from, to int) []inlayHint {
	text := f.text.s
	u8 := s.utf8.Load()
	var in *inferEnv
	var out []inlayHint
	for k := 0; k+3 < len(ct); k++ {
		if ct[k].Start < from || ct[k].Start >= to || !isIdent(ct, k) || word(text, ct, k+1) != ":" || word(text, ct, k+2) != "=" || ct[k+2].Start != ct[k+1].End {
			continue
		}
		if k > 0 && (word(text, ct, k-1) == "," || word(text, ct, k-1) == ".") || keywords[word(text, ct, k)] {
			continue
		}
		a, b := itemOf(text, ct, k)
		if in == nil {
			in = s.newInfer(f, ct)
		}
		t := in.localTypeAt(a, b, k, 0)
		if t == "" {
			continue
		}
		rhs := text[ct[k+3].Start:ct[min(stmtEnd(text, ct, k+3, b), len(ct)-1)].End]
		if strings.HasPrefix(rhs, t) && (len(rhs) == len(t) || !isIdentByte(rhs[len(t)])) {
			continue
		}
		out = append(out, inlayHint{Position: f.text.pos(ct[k].End, u8), Label: ": " + t, Kind: inlayType})
	}
	return out
}

// isDeclHead reports whether the identifier at ct[i] names a function in
// its declaration: it begins an item, or is the method of `T.M` that does.
func isDeclHead(text string, ct []token, i int) bool {
	start := i
	if i >= 2 && word(text, ct, i-1) == "." && isIdent(ct, i-2) {
		start = i - 2
	}
	for start > 0 && (word(text, ct, start-1) == "export" || word(text, ct, start-1) == "unsafe" || word(text, ct, start-1) == "naked") {
		start--
	}
	if start == 0 || ct[start].NL {
		return depthAt(text, ct, start) == 0 || inBody(text, ct, start)
	}
	return false
}

// depthAt is the bracket depth before ct[i].
func depthAt(text string, ct []token, i int) int {
	d := 0
	for k := 0; k < i; k++ {
		d = max(0, d+depthDelta(text, ct[k]))
	}
	return d
}

// inBody reports whether ct[i] begins a member line of an interface or
// impl body (depth 1 under `interface` or `impl`).
func inBody(text string, ct []token, i int) bool {
	a, _ := itemOf(text, ct, i)
	for k := a; k < i && k < a+16; k++ {
		if w := word(text, ct, k); w == "interface" || w == "impl" {
			return depthAt(text, ct, i)-depthAt(text, ct, a) == 1
		}
	}
	return false
}

// calleeParams are the parameter labels of the function a call names, from
// its declaration's text, or nil.
func (s *Server) calleeParams(c cursor) []string {
	for _, l := range s.defs(c) {
		path, ok := uriPath(l.URI)
		if !ok {
			continue
		}
		df, ok := s.fileOf(path)
		if !ok {
			continue
		}
		off := df.text.off(l.Range.Start, s.utf8.Load())
		if off >= len(df.text.s) {
			continue
		}
		head := df.text.s[off:min(len(df.text.s), off+4096)]
		k := 0
		for k < len(head) && isIdentByte(head[k]) {
			k++
		}
		if k < len(head) && (head[k] == '(' || head[k] == '[') {
			return paramLabels(head)
		}
	}
	return nil
}

// callArgs are the token index ranges [first, last] of the arguments of
// the call whose `(` is ct[open].
func callArgs(text string, ct []token, open int) [][2]int {
	end := matching(text, ct, open)
	var out [][2]int
	start, d := open+1, 0
	for k := open + 1; k <= end; k++ {
		switch w := word(text, ct, k); {
		case k == end || w == "," && d == 0:
			if start < k {
				out = append(out, [2]int{start, k - 1})
			}
			start = k + 1
		case depthDelta(text, ct[k]) > 0:
			d++
		case depthDelta(text, ct[k]) < 0:
			d--
		}
	}
	return out
}

// pathEndsIn reports whether an argument is a name or a field path
// (`a.b.c`) whose last name is name.
func pathEndsIn(text string, ct []token, arg [2]int, name string) bool {
	for k := arg[0]; k <= arg[1]; k++ {
		if (k-arg[0])%2 == 0 && !isIdent(ct, k) || (k-arg[0])%2 == 1 && word(text, ct, k) != "." {
			return false
		}
	}
	return (arg[1]-arg[0])%2 == 0 && word(text, ct, arg[1]) == name
}

// paramName is the name of a parameter label (`const less: fn(..)` is
// less), or "" for one with no name.
func paramName(label string) string {
	label = strings.TrimPrefix(strings.TrimSpace(label), "const ")
	n, _, ok := strings.Cut(label, ":")
	if !ok {
		return ""
	}
	n = strings.TrimSpace(n)
	if n == "" || !isIdentStart(n[0]) || strings.ContainsAny(n, " &") {
		return ""
	}
	return n
}

// receiverParam reports whether a parameter label is a method's receiver,
// with or without its `into` clause.
func receiverParam(label string) bool {
	head, _, _ := strings.Cut(strings.TrimSpace(label), " into ")
	switch head {
	case "self", "&self", "mut self":
		return true
	}
	return false
}
