// Why: semantic tokens let an editor colour names by what they are, not only by
// how they are spelled.
//
// What: semanticTokens, semanticTokensDelta, the legend (tokenLegend,
// tokenModifiers) and typeKinds.
//
// How: the six classes of the Mk highlighter first, then the name classes the
// declaration scan can tell, with modifiers declaration, unsafe, linear,
// view and unique; kinds come from the last check that reached types. The
// delta answers one edit against the previous result.

package lsp

import (
	"hash/fnv"
	"strconv"
	"strings"
)

// The name classes, after the highlighter's six in tokenLegend.
const (
	classFunction tokClass = 6 + iota
	classMethod
	classParameter
	classVariable
	classProperty
	classNamespace
	classEnumMember
	classMacro
)

// Token modifiers, as bits in tokenModifiers' order.
const (
	modDeclaration = 1 << iota
	modUnsafe
	modLinear
	modView
	modUnique
)

var tokenModifiers = []string{"declaration", "unsafe", "linear", "view", "unique"}

// typeInfo is what the scan knows of a declared type name.
type typeInfo struct {
	enum, linear, view bool
	members            map[string]bool // an enum's variants
	// kindable: a struct or enum with no generic parameters, whose kind
	// `mk query kind` answers; inRoot: the root's files declare it.
	kindable, inRoot bool
}

// knownTypes are the type names the root's files and std declare, read
// once per job: a request that resolves many locals (call hierarchy,
// inlay hints) scans the files once.
func (s *Server) knownTypes(r *root) map[string]*typeInfo {
	if out, ok := s.known[r]; ok {
		return out
	}
	out := s.scanTypes(r)
	if s.known == nil {
		s.known = map[*root]map[string]*typeInfo{}
	}
	s.known[r] = out
	return out
}

func (s *Server) scanTypes(r *root) map[string]*typeInfo {
	out := map[string]*typeInfo{}
	isType := func(d decl) bool {
		return d.Owner == "" && (d.Kind == symStruct || d.Kind == symEnum || d.Kind == symInterface || d.Kind == symTypeParam)
	}
	add := func(hs []hit) {
		for _, h := range hs {
			ti := out[h.decl.Name]
			if ti == nil {
				ti = &typeInfo{members: map[string]bool{}}
				out[h.decl.Name] = ti
			}
			head := h.f.text.s[h.decl.Start:h.decl.NameStart]
			if h.decl.Kind == symStruct || h.decl.Kind == symEnum {
				ti.kindable = !strings.HasPrefix(h.f.text.s[h.decl.NameEnd:], "[")
			}
			ti.linear = ti.linear || strings.Contains(head, "linear ")
			ti.view = ti.view || strings.Contains(head, "view ")
			if h.decl.Kind == symEnum {
				ti.enum = true
				for _, m := range h.decl.Children {
					ti.members[m.Name] = true
				}
			}
		}
	}
	root := s.search(r, isType)
	add(root)
	for _, h := range root {
		out[h.decl.Name].inRoot = true
	}
	add(s.searchStd("", isType))
	return out
}

// kindQueryBudget bounds the `mk query kind` calls of one request (each
// a check of its own before the first good check; after it, QueryChecked
// costs none), and the types left over are asked by the next request.
const kindQueryBudget = 16

// typeKinds are the kinds ("plain", "unique", "linear") of the named
// types, from `mk query kind`, cached: a root's types until the text of
// its type declarations changes, std's for the server's life. A type the
// package cannot answer for yet (it does not check to types) is asked
// again later.
func (s *Server) typeKinds(r *root, types map[string]*typeInfo, names []string) map[string]string {
	h := fnv.New64a()
	hits := s.search(r, func(d decl) bool {
		return d.Owner == "" && (d.Kind == symStruct || d.Kind == symEnum || d.Kind == symTypeParam)
	})
	for _, hit := range hits {
		h.Write([]byte(hit.decl.Name))
		h.Write([]byte(hit.f.text.s[hit.decl.Start:hit.decl.End]))
	}
	key := strconv.FormatUint(h.Sum64(), 16)
	if r.kindsKey != key || r.kinds == nil {
		r.kinds, r.kindsKey = map[string]string{}, key
	}
	// The answers come from the last check that reached types. When
	// that check read other type declarations than the current ones (the
	// buffer does not parse since an edit), they are shown but never
	// cached under the current declarations' key, so the next good check
	// answers them again.
	fresh := s.kindsFresh(r, hits)
	if s.stdKinds == nil {
		s.stdKinds = map[string]string{}
	}
	out := map[string]string{}
	budget := kindQueryBudget
	for _, n := range names {
		ti := types[n]
		if ti == nil || !ti.kindable {
			continue
		}
		cache := s.stdKinds
		if ti.inRoot {
			cache = r.kinds
		}
		k, ok := cache[n]
		if !ok {
			if budget == 0 {
				continue
			}
			budget--
			a, err := s.query(r, "kind", []string{n})
			if err != nil || a == nil || a.Partial {
				return out // not checked to types: ask on a later request
			}
			if a.Found && len(a.Answer) > 0 {
				if _, rest, ok := strings.Cut(a.Answer[0], " is "); ok {
					k, _, _ = strings.Cut(rest, " ")
					k = strings.TrimRight(k, ":,")
				}
			}
			if fresh || !ti.inRoot {
				cache[n] = k
			}
		}
		out[n] = k
	}
	return out
}

// kindsFresh reports whether the session s.query answers kinds from read
// the type declarations hits as they are now: the last check that reached
// types did, or there is none (a query then checks the current texts and
// says Partial when they do not reach types).
func (s *Server) kindsFresh(r *root, hits []hit) bool {
	x := r.exact
	if x == nil {
		return true
	}
	for _, h := range hits {
		sf := x.fileOfKey(h.f.path)
		if sf == nil {
			return false
		}
		a, ok1 := toChecked(sf.Text, h.f.text.s, h.decl.Start)
		b, ok2 := toChecked(sf.Text, h.f.text.s, h.decl.End)
		if !ok1 || !ok2 || b < a || b > len(sf.Text) || sf.Text[a:b] != h.f.text.s[h.decl.Start:h.decl.End] {
			return false
		}
	}
	return true
}

// nameClasses classes each identifier token of a file that the six
// highlighter classes leave out, with its modifiers, by token start.
func (s *Server) nameClasses(r *root, f srcFile) map[int][2]int {
	text := f.text.s
	ct := code(f.toks)
	out := map[int][2]int{}
	types := s.knownTypes(r)
	imps := imports(text, ct)
	var used []string
	for k := range ct {
		if n := word(text, ct, k); isIdent(ct, k) && types[n] != nil && types[n].kindable {
			used = append(used, n)
		}
	}
	kinds := s.typeKinds(r, types, used)
	declAt := map[int]bool{}
	declKind := map[int]int{}
	var walk func(ds []decl)
	walk = func(ds []decl) {
		for _, d := range ds {
			declAt[d.NameStart] = true
			declKind[d.NameStart] = d.Kind
			walk(d.Children)
		}
	}
	walk(f.decls)
	// unsafe regions: the token ranges of `unsafe "reason" { ... }`.
	var unsafeRanges [][2]int
	for k := 0; k+2 < len(ct); k++ {
		if word(text, ct, k) == "unsafe" && ct[k+1].Kind == tString && word(text, ct, k+2) == "{" {
			unsafeRanges = append(unsafeRanges, [2]int{ct[k].Start, ct[matching(text, ct, k+2)].End})
		}
	}
	inUnsafe := func(off int) bool {
		for _, u := range unsafeRanges {
			if u[0] <= off && off < u[1] {
				return true
			}
		}
		return false
	}
	for _, ir := range items(text, ct) {
		a, b := ir[0], ir[1]
		// The item's locals: each declaration site, a parameter when it is
		// in a signature's or a literal's parentheses.
		locals := map[string]tokClass{}
		for k := a; k <= b; k++ {
			if ct[k].Kind != tIdent {
				continue
			}
			name := word(text, ct, k)
			if at, ok := localDecl(text, ct, a, b, ct[k].End, name); ok && at == ct[k].Start {
				if _, seen := locals[name]; !seen {
					cl := classVariable
					if word(text, ct, k+1) == ":" && word(text, ct, k+2) != "=" && isParam(text, ct, a, k) {
						cl = classParameter
					}
					locals[name] = cl
				}
			}
		}
		for k := a; k <= b; k++ {
			t := ct[k]
			if t.Kind != tIdent || class(text, t) != classNone {
				continue
			}
			name := word(text, ct, k)
			prevDot := k > 0 && word(text, ct, k-1) == "." && ct[k-1].End == t.Start
			call := word(text, ct, k+1) == "(" && ct[k+1].Start == t.End ||
				word(text, ct, k+1) == "[" && word(text, ct, matching(text, ct, k+1)+1) == "("
			mods := 0
			if declAt[t.Start] {
				mods |= modDeclaration
			}
			if inUnsafe(t.Start) {
				mods |= modUnsafe
			}
			cl := classNone
			qual := ""
			if prevDot && k >= 2 && isIdent(ct, k-2) {
				qual = word(text, ct, k-2)
			}
			switch ti := types[name]; {
			case k > 0 && word(text, ct, k-1) == "gen":
				cl = classMacro
			case !prevDot && imps[name] != "" && word(text, ct, k+1) == ".":
				cl = classNamespace
			case ti != nil && !(prevDot && qual != "" && imps[qual] == "" && types[qual] == nil):
				cl = classType
				if ti.linear || kinds[name] == "linear" {
					mods |= modLinear
				}
				if kinds[name] == "unique" {
					mods |= modUnique
				}
				if ti.view {
					mods |= modView
				}
			case prevDot && qual != "" && types[qual] != nil && types[qual].enum && types[qual].members[name]:
				cl = classEnumMember
			case prevDot && call && (qual == "" || imps[qual] == ""):
				cl = classMethod
			case prevDot && imps[qual] != "":
				cl = classFunction
				if !call {
					cl = classVariable
				}
			case prevDot:
				cl = classProperty
			case locals[name] != 0:
				cl = locals[name]
			case call:
				cl = classFunction
			}
			if declAt[t.Start] {
				switch declKind[t.Start] {
				case symField:
					cl = classProperty
				case symEnumMember:
					cl = classEnumMember
				case symMethod:
					cl = classMethod
				case symFunction:
					cl = classFunction
				case symConstant, symVariable:
					cl = classVariable
				}
			}
			if cl != classNone {
				out[t.Start] = [2]int{int(cl), mods}
			}
		}
	}
	return out
}

// semanticResult is the last full answer for a document, for delta.
type semanticResult struct {
	id   string
	data []uint32
}

// semanticData encodes a file's tokens, one per line of a multi-line
// literal (no multilineTokenSupport is assumed).
func (s *Server) semanticData(f srcFile) []uint32 {
	u8 := s.utf8.Load()
	text := f.text.s
	names := s.nameClasses(s.rootOfPath(f.path), f)
	data := []uint32{}
	prevLine, prevChar := 0, 0
	emit := func(start, end int, cl tokClass, mods int) {
		ps := f.text.pos(start, u8)
		pe := f.text.pos(end, u8)
		if pe.Line != ps.Line || pe.Character <= ps.Character {
			return
		}
		dl := ps.Line - prevLine
		dc := ps.Character
		if dl == 0 {
			dc -= prevChar
		}
		data = append(data, uint32(dl), uint32(dc), uint32(pe.Character-ps.Character), uint32(cl), uint32(mods))
		prevLine, prevChar = ps.Line, ps.Character
	}
	for _, t := range f.toks {
		cl := class(text, t)
		mods := 0
		if cl == classNone {
			nc, ok := names[t.Start]
			if !ok {
				continue
			}
			cl, mods = tokClass(nc[0]), nc[1]
		}
		for a := t.Start; a < t.End; {
			b := a
			for b < t.End && text[b] != '\n' {
				b++
			}
			if b > a {
				emit(a, b, cl, mods)
			}
			a = b + 1
		}
	}
	return data
}

// semanticTokens answers full: the data and a result id for delta.
func (s *Server) semanticTokens(p documentParams) (any, *rpcError) {
	f, e := s.fileForURI(p.TextDocument.URI)
	if e != nil {
		return nil, e
	}
	data := s.semanticData(f)
	s.semanticN++
	res := semanticResult{id: itoa(s.semanticN), data: data}
	s.semantic[pathKey(f.path)] = res
	return semanticTokens{ResultID: res.id, Data: data}, nil
}

// semanticTokensDelta answers full/delta: one edit from the previous
// result's data to the new, or the full data when the previous id is not
// the last one sent.
func (s *Server) semanticTokensDelta(p struct {
	TextDocument     textDocumentIdentifier `json:"textDocument"`
	PreviousResultID string                 `json:"previousResultId"`
}) (any, *rpcError) {
	f, e := s.fileForURI(p.TextDocument.URI)
	if e != nil {
		return nil, e
	}
	prev, ok := s.semantic[pathKey(f.path)]
	data := s.semanticData(f)
	s.semanticN++
	res := semanticResult{id: itoa(s.semanticN), data: data}
	s.semantic[pathKey(f.path)] = res
	if !ok || prev.id != p.PreviousResultID {
		return semanticTokens{ResultID: res.id, Data: data}, nil
	}
	pre := 0
	for pre < len(prev.data) && pre < len(data) && prev.data[pre] == data[pre] {
		pre++
	}
	suf := 0
	for suf < len(prev.data)-pre && suf < len(data)-pre && prev.data[len(prev.data)-1-suf] == data[len(data)-1-suf] {
		suf++
	}
	edits := []map[string]any{}
	if pre != len(prev.data) || pre != len(data) {
		edits = append(edits, map[string]any{"start": pre, "deleteCount": len(prev.data) - pre - suf, "data": data[pre : len(data)-suf]})
	}
	return map[string]any{"resultId": res.id, "edits": edits}, nil
}
