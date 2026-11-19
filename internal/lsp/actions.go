// Why: editors offer fixes as code actions, and a novice needs more than the
// check's own fixes: an explanation, a missing import, the missing match arms.
//
// What: codeActions' extra actions: "explain this error" (the explainCommand
// command), "add the import", "fill match arms" and "organise imports".
//
// How: each action is computed from the buffer's tokens and the last check's
// diagnostics; the check's own fixes (every `mk fix` proven fix and the
// borrow errors' flow clauses) come from features.go codeActions.

package lsp

import (
	"sort"
	"strings"
)

type command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

// explainCommand is the command "explain this error" runs.
const explainCommand = "mk.explain"

// moreActions are the actions of a range that do not come from a fix.
func (s *Server) moreActions(r *root, path string, f srcFile, rng Range) []codeAction {
	var out []codeAction
	seen := map[string]bool{}
	hasImportFix := false
	for _, fd := range r.fixes[pathKey(path)] {
		for _, a := range fd.fixes {
			if strings.Contains(a.Title, "import") {
				hasImportFix = true
			}
		}
	}
	for _, d := range r.diags[pathKey(path)] {
		if before(d.Range.End, rng.Start) || before(rng.End, d.Range.Start) {
			continue
		}
		if d.Code != "" && !seen[d.Code] && d.CodeDescription != nil {
			seen[d.Code] = true
			out = append(out, codeAction{Title: "Explain " + d.Code, Kind: "quickfix", Diagnostics: []lspDiagnostic{d},
				Command: &command{Title: "Explain " + d.Code, Command: explainCommand, Arguments: []any{d.Code, path}}})
		}
		if d.Code == "E0201" && !hasImportFix {
			if a, ok := s.addImportAction(r, f, d); ok {
				out = append(out, a)
			}
		}
	}
	if a, ok := s.fillMatchAction(f, rng); ok {
		out = append(out, a)
	}
	if a, ok := s.organizeImportsAction(f); ok {
		out = append(out, a)
	}
	return out
}

// addImportAction adds `import std.m` for an unknown name used as a module
// binding (`fs.ReadText` with no import of std.fs).
func (s *Server) addImportAction(r *root, f srcFile, d lspDiagnostic) (codeAction, bool) {
	u8 := s.utf8.Load()
	text := f.text.s
	a, b := f.text.off(d.Range.Start, u8), f.text.off(d.Range.End, u8)
	if a >= b || b >= len(text) || text[b] != '.' {
		return codeAction{}, false
	}
	name := text[a:b]
	for _, m := range s.stdModules(r) {
		if m[strings.LastIndexByte(m, '.')+1:] != name {
			continue
		}
		at := importInsertAt(text, code(f.toks))
		p := f.text.pos(at, u8)
		return codeAction{Title: "Add import " + m, Kind: "quickfix", Diagnostics: []lspDiagnostic{d}, IsPreferred: true,
			Edit: &workspaceEdit{Changes: map[string][]textEdit{s.uriOf(f.path): {{Range: Range{Start: p, End: p}, NewText: "import " + m + "\n"}}}}}, true
	}
	return codeAction{}, false
}

// importInsertAt is the offset after a file's last import line, or 0.
func importInsertAt(text string, ct []token) int {
	at := 0
	for _, r := range items(text, ct) {
		if word(text, ct, r[0]) == "import" {
			at = lineEnd(text, ct[r[1]].End)
			if at < len(text) {
				at++
			}
		}
	}
	return at
}

// fillMatchAction adds the missing arms of a `match x {` in the range,
// when x's declared type is an enum the scan finds.
func (s *Server) fillMatchAction(f srcFile, rng Range) (codeAction, bool) {
	u8 := s.utf8.Load()
	text := f.text.s
	ct := code(f.toks)
	from, to := f.text.off(rng.Start, u8), f.text.off(rng.End, u8)
	lineA := strings.LastIndexByte(text[:from], '\n') + 1
	lineB := lineEnd(text, to)
	for k, t := range ct {
		if t.Start < lineA || t.Start > lineB || word(text, ct, k) != "match" || !isIdent(ct, k+1) {
			continue
		}
		open := k + 2
		for open < len(ct) && word(text, ct, open) != "{" {
			open++
		}
		if open >= len(ct) {
			return codeAction{}, false
		}
		end := matching(text, ct, open)
		c := cursor{f: f, ct: ct, i: k + 1, name: word(text, ct, k+1)}
		a, b := itemOf(text, ct, k+1)
		typ := ""
		if at, ok := localDecl(text, ct, a, b, ct[k+1].Start, c.name); ok {
			typ = s.localType(c, at, a, b)
		}
		if typ == "" {
			return codeAction{}, false
		}
		tc := cursor{f: f, ct: ct, i: k + 1, name: lastSegment(typ)}
		if q, _, ok := strings.Cut(typ, "."); ok && q != tc.name {
			tc.qual = q
		}
		var enum hit
		found := false
		for _, l := range s.defs(tc) {
			if h, ok := s.declAt(l); ok && h.decl.Kind == symEnum {
				enum, found = h, true
				break
			}
		}
		if !found {
			return codeAction{}, false
		}
		// The arms present: `.Name` at the start of a line in the body.
		have := map[string]bool{}
		d := 0
		for j := open + 1; j < end; j++ {
			if d == 0 && word(text, ct, j) == "." && isIdent(ct, j+1) {
				have[word(text, ct, j+1)] = true
			}
			d = max(0, d+depthDelta(text, ct[j]))
		}
		lineStart := strings.LastIndexByte(text[:ct[k].Start], '\n') + 1
		indent := text[lineStart:ct[k].Start]
		inner := indent + "    "
		var b2 strings.Builder
		n := 0
		for _, v := range enum.decl.Children {
			if v.Kind != symEnumMember || have[v.Name] {
				continue
			}
			n++
			b2.WriteString(inner + "." + v.Name)
			if ps := paramLabels(enum.f.text.s[v.NameStart:v.End]); len(ps) > 0 {
				var names []string
				for _, p := range ps {
					names = append(names, paramName(p))
				}
				b2.WriteString("(" + strings.Join(names, ", ") + ")")
			}
			b2.WriteString(" {\n" + inner + "}\n")
		}
		if n == 0 {
			return codeAction{}, false
		}
		// Insert before the closing brace's line.
		closeLine := strings.LastIndexByte(text[:ct[end].Start], '\n') + 1
		p := f.text.pos(closeLine, u8)
		return codeAction{Title: "Fill match arms (" + itoa(n) + ")", Kind: "quickfix",
			Edit: &workspaceEdit{Changes: map[string][]textEdit{s.uriOf(f.path): {{Range: Range{Start: p, End: p}, NewText: b2.String()}}}}}, true
	}
	return codeAction{}, false
}

// organizeImportsAction sorts the file's leading import lines and drops
// duplicates, when they are not in order already.
func (s *Server) organizeImportsAction(f srcFile) (codeAction, bool) {
	text := f.text.s
	ct := code(f.toks)
	var lines []string
	first, last := -1, -1
	for _, r := range items(text, ct) {
		w := word(text, ct, r[0])
		if w != "import" {
			if first >= 0 {
				break
			}
			continue
		}
		a := strings.LastIndexByte(text[:ct[r[0]].Start], '\n') + 1
		b := lineEnd(text, ct[r[1]].End)
		if first < 0 {
			first = a
		} else if strings.TrimSpace(text[last:a]) != "" {
			break // a comment between imports: leave the block alone
		}
		last = b
		lines = append(lines, strings.TrimSpace(text[a:b]))
	}
	if len(lines) < 2 {
		return codeAction{}, false
	}
	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)
	uniq := sorted[:0]
	for i, l := range sorted {
		if i == 0 || l != sorted[i-1] {
			uniq = append(uniq, l)
		}
	}
	if strings.Join(uniq, "\n") == strings.Join(lines, "\n") {
		return codeAction{}, false
	}
	u8 := s.utf8.Load()
	return codeAction{Title: "Organise imports", Kind: "source.organizeImports",
		Edit: &workspaceEdit{Changes: map[string][]textEdit{s.uriOf(f.path): {{Range: f.text.span(first, last, u8), NewText: strings.Join(uniq, "\n")}}}}}, true
}

// executeCommand runs a command of a code action: mk.explain asks the
// client to show the code's explain file.
func (s *Server) executeCommand(p struct {
	Command   string `json:"command"`
	Arguments []any  `json:"arguments"`
}) (any, *rpcError) {
	if p.Command != explainCommand || len(p.Arguments) < 1 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "unknown command " + p.Command}
	}
	code, _ := p.Arguments[0].(string)
	path := ""
	if len(p.Arguments) > 1 {
		path, _ = p.Arguments[1].(string)
	}
	var r *root
	if path != "" {
		r = s.rootOfPath(path)
	}
	if r == nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "mk.explain needs a code and a path"}
	}
	u := s.explainURI(r, code)
	if u == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "no explanation for " + code}
	}
	s.requestN++
	_ = s.out.send(map[string]any{"jsonrpc": "2.0", "id": "mk-show-" + itoa(s.requestN), "method": "window/showDocument",
		"params": map[string]any{"uri": u, "takeFocus": true}})
	return nil, nil
}
