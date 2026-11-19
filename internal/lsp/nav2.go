// Why: navigation beyond definition: from a value to its type, from an interface
// to its impls, through callers and callees.
//
// What: typeDefinition, implementation, the call hierarchy (prepare, incoming,
// outgoing) and selectionRanges.
//
// How: like definition, these read the declaration scan of the root's files and
// of std on disk; references and rename use the exact uses of exact.go.

package lsp

import (
	"sort"
	"strings"
)

// typeDefinition finds the declaration of the type of the name under the
// cursor: a local's or parameter's declared type, a field's type, or the
// type itself.
func (s *Server) typeDefinition(p textDocumentPositionParams) (any, *rpcError) {
	c, ok, e := s.cursorAt(p.TextDocument.URI, p.Position)
	if e != nil || !ok || keywords[c.name] {
		return nil, e
	}
	text := c.f.text.s
	t := ""
	if c.qual == "" {
		a, b := itemOf(text, c.ct, c.i)
		if at, ok := localDecl(text, c.ct, a, b, c.ct[c.i].End, c.name); ok {
			t = s.localType(c, at, a, b)
		}
	}
	if t == "" {
		// A field: the type its declaration spells.
		for _, l := range s.defs(c) {
			if ft := s.declaredTypeAt(l); ft != "" {
				t = ft
				break
			}
		}
	}
	if t == "" {
		return nil, nil
	}
	tc := cursor{f: c.f, ct: c.ct, i: c.i, name: lastSegment(t)}
	if q, _, ok := strings.Cut(t, "."); ok && q != tc.name {
		tc.qual = q
	}
	out := s.defs(tc)
	if len(out) == 0 && tc.qual == "" {
		// A generic parameter of the item: `[T: Shape]` before its
		// parameters.
		a, _ := itemOf(text, c.ct, c.i)
		open := -1
		for k := a; k < len(c.ct) && open < 0; k++ {
			switch word(text, c.ct, k) {
			case "[":
				open = k
			case "(", "{":
				k = len(c.ct)
			}
		}
		if open >= 0 {
			end := matching(text, c.ct, open)
			for k := open + 1; k < end; k++ {
				if p := word(text, c.ct, k-1); word(text, c.ct, k) == tc.name && (p == "[" || p == ",") && depthAt(text, c.ct, k) == depthAt(text, c.ct, open)+1 {
					out = append(out, Location{URI: s.uriOf(c.f.path), Range: c.f.text.span(c.ct[k].Start, c.ct[k].End, s.utf8.Load())})
					break
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// declaredTypeAt is the type a field or parameter declaration at a
// location spells after its `:` (`hi: int` is int, `next: ?&Node` is Node),
// or "".
func (s *Server) declaredTypeAt(l Location) string {
	path, ok := uriPath(l.URI)
	if !ok {
		return ""
	}
	f, ok := s.fileOf(path)
	if !ok {
		return ""
	}
	ct := code(f.toks)
	i := tokenAt(ct, f.text.off(l.Range.Start, s.utf8.Load()))
	if i < 0 || word(f.text.s, ct, i+1) != ":" || word(f.text.s, ct, i+2) == "=" {
		return ""
	}
	j := i + 2
	for {
		switch word(f.text.s, ct, j) {
		case "&", "mut", "?", "*", "[", "]":
			j++
			continue
		}
		break
	}
	return qualified(f.text.s, ct, j)
}

// implParts reads an impl's name ("impl[T: Hash] Hash for HeapList[T]")
// into its interface and type, without generic arguments or modules.
func implParts(name string) (iface, typ string) {
	rest := strings.TrimPrefix(name, "impl")
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "[") {
		rest = strings.TrimSpace(rest[skipBracketsAt(rest, 0):])
	}
	i, t, ok := strings.Cut(rest, " for ")
	if !ok {
		return "", ""
	}
	clean := func(s string) string {
		s = strings.TrimSpace(s)
		if k := strings.IndexByte(s, '['); k >= 0 {
			s = s[:k]
		}
		return lastSegment(strings.TrimSpace(s))
	}
	return clean(i), clean(t)
}

// skipBracketsAt is the index after the bracket closing s[i].
func skipBracketsAt(s string, i int) int {
	d := 0
	for ; i < len(s); i++ {
		switch s[i] {
		case '[':
			d++
		case ']':
			d--
			if d == 0 {
				return i + 1
			}
		}
	}
	return i
}

// implementation answers textDocument/implementation: on an interface, its
// impls; on a type, the impls for it; on a method of an interface, that
// method in each impl; on an impl, its interface.
func (s *Server) implementation(p textDocumentPositionParams) (any, *rpcError) {
	c, ok, e := s.cursorAt(p.TextDocument.URI, p.Position)
	if e != nil || !ok {
		return nil, e
	}
	r := s.rootOfPath(c.f.path)
	all := func(pred func(decl) bool) []hit {
		return append(s.search(r, pred), s.searchStd("", pred)...)
	}
	isImpl := func(d decl) bool { return d.Detail == "impl" }
	var out []Location
	if c.name == "impl" {
		// On an impl: its interface.
		for _, d := range c.f.decls {
			if isImpl(d) && d.NameStart == c.ct[c.i].Start {
				iface, _ := implParts(d.Name)
				for _, h := range all(func(x decl) bool { return x.Kind == symInterface && x.Name == iface }) {
					out = append(out, s.location(h))
				}
			}
		}
		return locationsOrNil(out), nil
	}
	// The method of an interface: its declarations in the impls.
	for _, h := range all(func(d decl) bool { return d.Kind == symInterface }) {
		for _, m := range h.decl.Children {
			if m.Name == c.name && m.NameStart == c.ct[c.i].Start && pathKey(h.f.path) == pathKey(c.f.path) {
				for _, ih := range all(isImpl) {
					if iface, _ := implParts(ih.decl.Name); iface == h.decl.Name {
						for _, im := range ih.decl.Children {
							if im.Name == c.name {
								out = append(out, s.location(hit{f: ih.f, decl: im}))
							}
						}
					}
				}
				if len(out) == 0 {
					// No impl declares it in the text (a generator emits
					// them): the member itself.
					out = s.defs(c)
				}
				return locationsOrNil(out), nil
			}
		}
	}
	for _, ih := range all(isImpl) {
		iface, typ := implParts(ih.decl.Name)
		if iface == c.name || typ == c.name {
			out = append(out, s.location(ih))
		}
	}
	if len(out) == 0 {
		// A method of an impl block: the interface member it implements.
		for _, d := range c.f.decls {
			if !isImpl(d) {
				continue
			}
			for _, m := range d.Children {
				if m.Name != c.name || m.NameStart != c.ct[c.i].Start {
					continue
				}
				iface, _ := implParts(d.Name)
				for _, h := range all(func(x decl) bool { return x.Kind == symInterface && x.Name == lastSegment(iface) }) {
					for _, im := range h.decl.Children {
						if im.Name == c.name {
							out = append(out, s.location(hit{f: h.f, decl: im}))
						}
					}
				}
			}
		}
	}
	if len(out) == 0 && !keywords[c.name] {
		// Anything else (a free function, a concrete method, a field, a
		// variant, a local): its declaration is its implementation.
		out = s.defs(c)
	}
	return locationsOrNil(out), nil
}

func locationsOrNil(l []Location) any {
	if len(l) == 0 {
		return nil
	}
	return l
}

// callHierarchyItem is LSP 3.17's CallHierarchyItem; Data carries the
// declaration's owner and name so the follow-up requests find it again.
type callHierarchyItem struct {
	Name           string   `json:"name"`
	Kind           int      `json:"kind"`
	Detail         string   `json:"detail,omitempty"`
	URI            string   `json:"uri"`
	Range          Range    `json:"range"`
	SelectionRange Range    `json:"selectionRange"`
	Data           callData `json:"data"`
}

type callData struct {
	Owner string `json:"owner,omitempty"`
	Name  string `json:"name"`
}

type callHierarchyCall struct {
	From       *callHierarchyItem `json:"from,omitempty"`
	To         *callHierarchyItem `json:"to,omitempty"`
	FromRanges []Range            `json:"fromRanges"`
}

// itemOfHit is a function or method declaration as a call hierarchy item.
func (s *Server) itemOfHit(h hit) callHierarchyItem {
	u8 := s.utf8.Load()
	name := h.decl.Name
	if h.decl.Owner != "" {
		name = h.decl.Owner + "." + name
	}
	return callHierarchyItem{Name: name, Kind: h.decl.Kind, Detail: h.decl.Detail, URI: s.uriOf(h.f.path),
		Range:          h.f.text.span(h.decl.Start, max(h.decl.End, h.decl.NameEnd), u8),
		SelectionRange: h.f.text.span(h.decl.NameStart, h.decl.NameEnd, u8),
		Data:           callData{Owner: h.decl.Owner, Name: h.decl.Name}}
}

// prepareCallHierarchy names the function under the cursor: its
// declaration, as definition finds it.
func (s *Server) prepareCallHierarchy(p textDocumentPositionParams) (any, *rpcError) {
	c, ok, e := s.cursorAt(p.TextDocument.URI, p.Position)
	if e != nil || !ok || keywords[c.name] {
		return nil, e
	}
	var out []callHierarchyItem
	for _, l := range s.defs(c) {
		if h, ok := s.funcAt(l); ok {
			out = append(out, s.itemOfHit(h))
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// funcAt is the function or method declared at a location.
func (s *Server) funcAt(l Location) (hit, bool) {
	path, ok := uriPath(l.URI)
	if !ok {
		return hit{}, false
	}
	f, ok := s.fileOf(path)
	if !ok {
		return hit{}, false
	}
	off := f.text.off(l.Range.Start, s.utf8.Load())
	var found hit
	var walk func(ds []decl) bool
	walk = func(ds []decl) bool {
		for _, d := range ds {
			if d.NameStart == off && (d.Kind == symFunction || d.Kind == symMethod) && d.Detail != "test" {
				found = hit{f: f, decl: d}
				return true
			}
			if walk(d.Children) {
				return true
			}
		}
		return false
	}
	return found, walk(f.decls)
}

// callsIn lists the calls in the token range ct[a..b] of a file: each
// `name(` and `x.name(`, with its cursor for definition.
func callsIn(f srcFile, ct []token, a, b int) []cursor {
	text := f.text.s
	var out []cursor
	for i := a + 1; i <= b && i < len(ct); i++ {
		if word(text, ct, i) != "(" || !isIdent(ct, i-1) || ct[i-1].End != ct[i].Start {
			continue
		}
		name := word(text, ct, i-1)
		if keywords[name] || isDeclHead(text, ct, i-1) {
			continue
		}
		c := cursor{f: f, ct: ct, i: i - 1, name: name}
		if i >= 3 && word(text, ct, i-2) == "." && isIdent(ct, i-3) {
			c.qual = word(text, ct, i-3)
		}
		out = append(out, c)
	}
	return out
}

// incomingCalls lists the functions of the root whose bodies call the
// item: each call whose definition is the item's declaration.
func (s *Server) incomingCalls(item callHierarchyItem) (any, *rpcError) {
	path, ok := uriPath(item.URI)
	if !ok {
		return []callHierarchyCall{}, nil
	}
	r := s.rootOfPath(path)
	u8 := s.utf8.Load()
	target := item.URI + "#" + posKey(item.SelectionRange.Start)
	out := []callHierarchyCall{}
	for _, p := range s.rootFiles(r) {
		f, ok := s.fileOf(p)
		if !ok {
			continue
		}
		ct := code(f.toks)
		for _, d := range f.decls {
			if d.Kind != symFunction && d.Kind != symMethod && d.Detail != "impl" {
				continue
			}
			callers := []decl{d}
			if d.Detail == "impl" {
				callers = d.Children
			}
			for _, cd := range callers {
				a, b := cd.first, cd.last
				if d.Detail == "impl" {
					a, b = tokRange(ct, cd.Start, cd.End)
				}
				var ranges []Range
				for _, call := range callsIn(f, ct, a, b) {
					if call.name != item.Data.Name {
						continue
					}
					for _, l := range s.defs(call) {
						if l.URI+"#"+posKey(l.Range.Start) == target || sameURI(l.URI, item.URI) && l.Range.Start == item.SelectionRange.Start {
							t := ct[call.i]
							ranges = append(ranges, f.text.span(t.Start, t.End, u8))
							break
						}
					}
				}
				if len(ranges) > 0 {
					from := s.itemOfHit(hit{f: f, decl: cd})
					out = append(out, callHierarchyCall{From: &from, FromRanges: ranges})
				}
			}
		}
	}
	return out, nil
}

// outgoingCalls lists the functions the item's body calls, grouped by
// callee, each with the ranges of its calls.
func (s *Server) outgoingCalls(item callHierarchyItem) (any, *rpcError) {
	out := []callHierarchyCall{}
	path, ok := uriPath(item.URI)
	if !ok {
		return out, nil
	}
	h, ok := s.funcAt(Location{URI: item.URI, Range: item.SelectionRange})
	if !ok {
		return out, nil
	}
	f, _ := s.fileOf(path)
	u8 := s.utf8.Load()
	ct := code(f.toks)
	a, b := tokRange(ct, h.decl.Start, max(h.decl.End, h.decl.NameEnd))
	if a > b {
		return out, nil
	}
	index := map[string]int{}
	for _, call := range callsIn(f, ct, a, b) {
		for _, l := range s.defs(call) {
			callee, ok := s.funcAt(l)
			if !ok {
				continue
			}
			key := l.URI + "#" + posKey(l.Range.Start)
			t := ct[call.i]
			rng := f.text.span(t.Start, t.End, u8)
			if k, ok := index[key]; ok {
				out[k].FromRanges = append(out[k].FromRanges, rng)
			} else {
				to := s.itemOfHit(callee)
				index[key] = len(out)
				out = append(out, callHierarchyCall{To: &to, FromRanges: []Range{rng}})
			}
			break
		}
	}
	return out, nil
}

// selectionRange is LSP 3.17's SelectionRange.
type selectionRange struct {
	Range  Range           `json:"range"`
	Parent *selectionRange `json:"parent,omitempty"`
}

type selectionRangeParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Positions    []Position             `json:"positions"`
}

// selectionRanges answers each position with the chain token, the inside
// of each enclosing bracket pair, the pair itself, the item, the file.
func (s *Server) selectionRanges(p selectionRangeParams) (any, *rpcError) {
	f, e := s.fileForURI(p.TextDocument.URI)
	if e != nil {
		return nil, e
	}
	u8 := s.utf8.Load()
	text := f.text.s
	ct := code(f.toks)
	out := []selectionRange{}
	for _, pos := range p.Positions {
		off := f.text.off(pos, u8)
		var spans [][2]int
		if i := tokenAt(f.toks, off); i >= 0 {
			spans = append(spans, [2]int{f.toks[i].Start, f.toks[i].End})
		} else {
			spans = append(spans, [2]int{off, off})
		}
		// The enclosing bracket pairs, innermost first.
		var stack []int
		var pairs [][2]int
		for k, t := range ct {
			if t.Start >= off {
				break
			}
			switch depthDelta(text, t) {
			case 1:
				stack = append(stack, k)
			case -1:
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}
		for n := len(stack) - 1; n >= 0; n-- {
			o := stack[n]
			cl := matching(text, ct, o)
			if cl <= o {
				continue
			}
			if ct[o].End < ct[cl].Start {
				pairs = append(pairs, [2]int{ct[o].End, ct[cl].Start})
			}
			pairs = append(pairs, [2]int{ct[o].Start, ct[cl].End})
		}
		spans = append(spans, pairs...)
		if i := tokenAt(ct, off); i >= 0 || len(stack) > 0 {
			k := i
			if k < 0 {
				k = stack[0]
			}
			a, b := itemOf(text, ct, k)
			spans = append(spans, [2]int{ct[a].Start, ct[b].End})
		}
		spans = append(spans, [2]int{0, len(text)})
		// Keep a chain of strictly growing ranges that contain the position.
		var chain [][2]int
		for _, sp := range spans {
			if sp[0] > off || sp[1] < off {
				continue
			}
			if n := len(chain); n > 0 {
				last := chain[n-1]
				if sp[0] > last[0] || sp[1] < last[1] || sp == last {
					continue
				}
			}
			chain = append(chain, sp)
		}
		var sr *selectionRange
		for n := len(chain) - 1; n >= 0; n-- {
			sr = &selectionRange{Range: f.text.span(chain[n][0], chain[n][1], u8), Parent: sr}
		}
		if sr == nil {
			sr = &selectionRange{Range: f.text.span(off, off, u8)}
		}
		out = append(out, *sr)
	}
	return out, nil
}

// tokRange is the index range of the tokens of ct inside [start, end), or
// a > b when there are none.
func tokRange(ct []token, start, end int) (a, b int) {
	a = sort.Search(len(ct), func(i int) bool { return ct[i].Start >= start })
	b = sort.Search(len(ct), func(i int) bool { return ct[i].End > end }) - 1
	return a, b
}
