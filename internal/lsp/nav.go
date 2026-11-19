// Why: a reader moves through code with highlight, folding, symbol search and
// signature help.
//
// What: highlights, foldingRanges, workspaceSymbols and signatureHelp, with
// paramLabels.
//
// How: highlights and folds come from the tokens; workspace symbols from the
// declaration scan of the open roots; signature help from `mk query sig` of
// the call around the cursor, else the callee's declaration text.

package lsp

import (
	"sort"
	"strings"
)

type documentHighlight struct {
	Range Range `json:"range"`
	Kind  int   `json:"kind"` // 1 text
}

// highlights are the name's references in the document itself.
func (s *Server) highlights(p textDocumentPositionParams) (any, *rpcError) {
	var rp referenceParams
	rp.textDocumentPositionParams = p
	rp.Context.IncludeDeclaration = true
	res, e := s.references(rp)
	if e != nil {
		return nil, e
	}
	out := []documentHighlight{}
	for _, l := range res.([]Location) {
		if sameURI(l.URI, p.TextDocument.URI) {
			out = append(out, documentHighlight{Range: l.Range, Kind: 1})
		}
	}
	return out, nil
}

type foldingRange struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Kind      string `json:"kind,omitempty"`
}

// foldingRanges fold every bracket pair that spans lines (its closing line
// stays visible), every run of two or more comment lines, and the imports.
func (s *Server) foldingRanges(p documentParams) (any, *rpcError) {
	f, e := s.fileForURI(p.TextDocument.URI)
	if e != nil {
		return nil, e
	}
	u8 := s.utf8.Load()
	text := f.text.s
	line := func(off int) int { return f.text.pos(off, u8).Line }
	out := []foldingRange{}
	var stack []int
	for _, t := range code(f.toks) {
		if t.Kind != tPunct {
			continue
		}
		switch text[t.Start] {
		case '{', '(', '[':
			stack = append(stack, t.Start)
		case '}', ')', ']':
			if len(stack) == 0 {
				continue
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if a, b := line(open), line(t.Start)-1; b > a {
				out = append(out, foldingRange{StartLine: a, EndLine: b})
			}
		}
	}
	run := func(kind string, pred func(token) bool) {
		first, last := -1, -1
		flush := func() {
			if first >= 0 && last > first {
				out = append(out, foldingRange{StartLine: first, EndLine: last, Kind: kind})
			}
			first, last = -1, -1
		}
		for i, t := range f.toks {
			if !t.NL {
				continue
			}
			l := line(t.Start)
			if pred(f.toks[i]) && (last < 0 || l == last+1) {
				if first < 0 {
					first = l
				}
				last = l
				continue
			}
			flush()
			if pred(f.toks[i]) {
				first, last = l, l
			}
		}
		flush()
	}
	run("comment", func(t token) bool { return t.Kind == tComment })
	run("imports", func(t token) bool { return t.Kind == tIdent && text[t.Start:t.End] == "import" })
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartLine < out[j].StartLine })
	return out, nil
}

type symbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// workspaceSymbols are the declarations of every root an open document
// belongs to whose name holds the query's letters in order, ignoring case;
// at most 256.
func (s *Server) workspaceSymbols(query string) []symbolInformation {
	out := []symbolInformation{}
	seen := map[string]bool{}
	q := strings.ToLower(query)
	for _, d := range s.docs.all() {
		r := s.rootOfPath(d.path)
		if seen[foldKey(r.key)] {
			continue
		}
		seen[foldKey(r.key)] = true
		hs := s.search(r, func(dc decl) bool { return subsequence(strings.ToLower(dc.Name), q) })
		// The dependencies' exported names after the package's own.
		hs = append(hs, s.searchPaths(r.depFiles, func(dc decl) bool { return dc.Exported && subsequence(strings.ToLower(dc.Name), q) })...)
		for _, h := range hs {
			if len(out) == 256 {
				return out
			}
			out = append(out, symbolInformation{Name: h.decl.Name, Kind: h.decl.Kind, Location: s.location(h), ContainerName: h.decl.Owner})
		}
	}
	return out
}

func subsequence(s, q string) bool {
	i := 0
	for j := 0; j < len(s) && i < len(q); j++ {
		if s[j] == q[i] {
			i++
		}
	}
	return i == len(q)
}

type signatureHelp struct {
	Signatures      []signatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature"`
	ActiveParameter int                    `json:"activeParameter"`
}

type signatureInformation struct {
	Label         string                 `json:"label"`
	Documentation *markupContent         `json:"documentation,omitempty"`
	Parameters    []parameterInformation `json:"parameters"`
}

type parameterInformation struct {
	Label string `json:"label"`
}

// signatureHelp shows `mk query sig` of the call around the cursor, its
// parameters, and which one the cursor is in.
func (s *Server) signatureHelp(p textDocumentPositionParams) (any, *rpcError) {
	f, e := s.fileForURI(p.TextDocument.URI)
	if e != nil {
		return nil, e
	}
	text := f.text.s
	off := f.text.off(p.Position, s.utf8.Load())
	ct := code(f.toks)
	// The unmatched `(` before the cursor, and the commas at its depth.
	d, commas, open := 0, 0, -1
	for k := len(ct) - 1; k >= 0; k-- {
		t := ct[k]
		if t.Start >= off {
			continue
		}
		w := text[t.Start:t.End]
		switch {
		case w == ")" || w == "]" || w == "}":
			d++
		case (w == "(" || w == "[" || w == "{") && d > 0:
			d--
		case w == "(":
			open = k
		case w == "{" || w == "[":
			return nil, nil
		case w == "," && d == 0:
			commas++
		}
		if open >= 0 {
			break
		}
	}
	if open < 1 || !isIdent(ct, open-1) || keywords[word(text, ct, open-1)] {
		return nil, nil
	}
	c := cursor{f: f, ct: ct, i: open - 1, name: word(text, ct, open-1)}
	if open >= 3 && word(text, ct, open-2) == "." && isIdent(ct, open-3) {
		c.qual = word(text, ct, open-3)
	}
	r := s.rootOfPath(f.path)
	answer := func(label string, doc, contracts []string) signatureHelp {
		params := paramLabels(label)
		active := commas
		if len(params) > 0 && strings.Contains(params[0], "self") && c.qual != "" && !isTypeName(c.qual) {
			active++ // the receiver is written before the dot
		}
		info := signatureInformation{Label: label, Parameters: []parameterInformation{}}
		for _, pl := range params {
			info.Parameters = append(info.Parameters, parameterInformation{Label: pl})
		}
		if md := sigDoc(doc, contracts); md != "" {
			info.Documentation = &markupContent{Kind: "markdown", Value: md}
		}
		return signatureHelp{Signatures: []signatureInformation{info}, ActiveParameter: min(active, max(0, len(params)-1))}
	}
	for _, subject := range s.subjects(r, c) {
		a, err := s.query(r, "sig", []string{subject})
		if err != nil || a == nil || !a.Found || len(a.Answer) == 0 || !namesWord(a.Answer[0], c.name) {
			continue
		}
		var doc, contracts []string
		for _, l := range a.Answer[1:] {
			t := strings.TrimSpace(l)
			switch {
			case strings.HasPrefix(t, "///"):
				doc = append(doc, strings.TrimPrefix(strings.TrimPrefix(t, "///"), " "))
			case strings.HasPrefix(t, "requires "), strings.HasPrefix(t, "ensures "):
				contracts = append(contracts, t)
			}
		}
		if len(doc) == 0 {
			// The query does not carry every doc comment: read the
			// declaration's own.
			if h, ok := s.calleeDecl(c); ok {
				if d := docAbove(h.f.text.s, h.decl.Start); d != "" {
					doc = strings.Split(d, "\n")
				}
			}
		}
		return answer(a.Answer[0], doc, contracts), nil
	}
	// No checked answer (the file does not check yet): the callee's
	// declaration as the text resolves it.
	if h, ok := s.calleeDecl(c); ok {
		var doc []string
		if d := docAbove(h.f.text.s, h.decl.Start); d != "" {
			doc = strings.Split(d, "\n")
		}
		return answer(declSignature(h), doc, nil), nil
	}
	return nil, nil
}

// calleeDecl is the function or method declaration the name at c resolves
// to by the text, as definition finds it.
func (s *Server) calleeDecl(c cursor) (hit, bool) {
	for _, l := range s.defs(c) {
		if h, ok := s.declAt(l); ok && (h.decl.Kind == symFunction || h.decl.Kind == symMethod) && h.decl.Name == c.name {
			return h, true
		}
	}
	return hit{}, false
}

// declSignature is a function declaration's head as one line: from its
// start to its body's `{` (or the end of a bodiless declaration), without
// `export`, its lines joined.
func declSignature(h hit) string {
	text := h.f.text.s
	end := h.decl.End
	if end > len(text) || end < h.decl.Start {
		end = len(text)
	}
	head := text[h.decl.Start:end]
	if i := strings.Index(head, " {\n"); i >= 0 {
		head = head[:i]
	} else if i := strings.Index(head, "{"); i >= 0 && strings.HasSuffix(strings.TrimSpace(head), "}") {
		head = head[:i]
	}
	head = strings.Join(strings.Fields(head), " ")
	head = strings.ReplaceAll(head, "( ", "(")
	return strings.TrimPrefix(head, "export ")
}

// sigDoc renders a signature's doc comment as markdown prose and its
// contracts as code.
func sigDoc(doc, contracts []string) string {
	md := strings.TrimSpace(strings.Join(doc, "\n"))
	if len(contracts) > 0 {
		if md != "" {
			md += "\n\n"
		}
		md += "```mk\n" + strings.Join(contracts, "\n") + "\n```"
	}
	return md
}

// isTypeName guesses that a qualifier names a type, not a value: Mk's
// exported types start upper case, its locals by convention lower case.
func isTypeName(q string) bool { return q != "" && q[0] >= 'A' && q[0] <= 'Z' }

// paramLabels splits a signature line's parameter list at its top-level
// commas: "F(a: int, b: fn(int): int): int" gives "a: int", "b: fn(int): int".
func paramLabels(sig string) []string {
	open := strings.IndexByte(sig, '(')
	if open < 0 {
		return nil
	}
	// A generic list before the parameters: skip `[..]`.
	if br := strings.IndexByte(sig, '['); br >= 0 && br < open {
		d := 0
		for i := br; i < len(sig); i++ {
			if sig[i] == '[' {
				d++
			} else if sig[i] == ']' {
				d--
				if d == 0 {
					open = strings.IndexByte(sig[i:], '(') + i
					break
				}
			}
		}
	}
	var out []string
	d, start := 0, open+1
	for i := open; i < len(sig); i++ {
		switch sig[i] {
		case '(', '[':
			d++
		case ')', ']':
			d--
			if d == 0 {
				if p := strings.TrimSpace(sig[start:i]); p != "" {
					out = append(out, p)
				}
				return out
			}
		case ',':
			if d == 1 {
				out = append(out, strings.TrimSpace(sig[start:i]))
				start = i + 1
			}
		}
	}
	return out
}
