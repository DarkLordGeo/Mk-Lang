// Why: completion is how a novice discovers a type's methods and a module's items;
// it must answer on every `.` and after `import `.
//
// What: the completion request: members after `x.` (`mk query members`), module
// items after `m.`, the module map after `import `, and names in scope
// (scopeItems), plus members and memberItem.
//
// How: reads the receiver before the cursor's partial word, resolves it as an
// import binding, a local (its spelled or inferred type), `self` or a type,
// and asks the last check; lexical scans answer where the check cannot.

package lsp

import (
	"sort"
	"strings"
)

func (s *Server) completion(p textDocumentPositionParams) (any, *rpcError) {
	f, e := s.fileForURI(p.TextDocument.URI)
	if e != nil {
		return nil, e
	}
	u8 := s.utf8.Load()
	off := f.text.off(p.Position, u8)
	text := f.text.s
	lineStart := strings.LastIndexByte(text[:off], '\n') + 1
	before := text[lineStart:off]
	r := s.rootOfPath(f.path)
	empty := completionList{Items: []completionItem{}}

	if rest, ok := strings.CutPrefix(strings.TrimLeft(before, " \t"), "import "); ok && isPathPrefix(rest) {
		return s.moduleItems(r), nil
	}
	// The receiver: the identifier just before a `.` ending the text before
	// the cursor's partial word.
	k := len(before)
	for k > 0 && isIdentByte(before[k-1]) {
		k--
	}
	if k == 0 || before[k-1] != '.' {
		if strings.HasPrefix(strings.TrimLeft(before, " \t"), "//") {
			return empty, nil
		}
		list := s.scopeItems(r, f, off)
		list.Items = withSnippets(list.Items, contextItems(text, code(f.toks), off, strings.TrimSpace(before[:k]) == ""))
		list.Items = append(list.Items, s.importItems(r, f)...)
		if fields, ok := s.constructionFields(f, off); ok {
			list.Items = append(fields, list.Items...)
		}
		return list, nil
	}
	q := k - 1
	for q > 0 && isIdentByte(before[q-1]) {
		q--
	}
	recv := before[q : k-1]
	if recv == "" || !isIdentStart(recv[0]) {
		return empty, nil
	}
	ct := code(f.toks)
	if mod, ok := imports(text, ct)[recv]; ok {
		if sd := s.stdModuleDir(mod); sd != "" {
			return s.stdModuleMembers(sd, recv, s.uriOf(f.path)), nil
		}
		return s.moduleMembers(r, mod), nil
	}
	subjects := []string{recv}
	// `m.T.`: the type of that module, which may share its name with
	// another module's (proc.Pass, net.Pass).
	if q > 1 && before[q-1] == '.' {
		p := q - 1
		for p > 0 && isIdentByte(before[p-1]) {
			p--
		}
		if _, ok := imports(text, ct)[before[p:q-1]]; ok {
			subjects = []string{before[p:q-1] + "." + recv}
		}
	}
	// A local: the type its declaration spells or the inference proves;
	// `self`: the method's owner.
	if i := tokenAt(ct, lineStart+q); i >= 0 {
		a, b := itemOf(text, ct, i)
		if recv == "self" {
			if o := selfType(text, ct, a); o != "" {
				subjects = []string{o}
			}
		} else if at, ok := localDecl(text, ct, a, b, lineStart+q, recv); ok {
			c := cursor{f: f, ct: ct, i: i, name: recv}
			if t := s.localType(c, at, a, b); t != "" {
				subjects = []string{t}
			}
		}
	}
	for _, sub := range subjects {
		items, ok := s.members(r, sub, s.typeDir(text, ct, sub))
		for n := range items {
			items[n].Data = &completionData{Subject: sub + "." + items[n].Label, URI: s.uriOf(f.path)}
		}
		if ok && sub == "Slice" {
			// The query answers Slice[T]'s impl members alone; its methods
			// are declared `Slice[T].M`, which the declarations give.
			have := map[string]bool{}
			for _, it := range items {
				have[it.Label] = true
			}
			for _, it := range s.lexMemberItems(r, sub, s.stdModuleDir("std.core"), s.uriOf(f.path)) {
				if !have[it.Label] {
					items = append(items, it)
				}
			}
		}
		if ok {
			return completionList{Items: items}, nil
		}
	}
	// No checked answer (the file does not check yet): the members the
	// declarations spell.
	for _, sub := range subjects {
		bt := baseType(sub)
		if items := s.lexMemberItems(r, bt, s.typeDir(text, ct, sub), s.uriOf(f.path)); len(items) > 0 {
			return completionList{Items: items}, nil
		}
	}
	return empty, nil
}

// lexMemberItems are a named type's members as its declarations spell
// them: the fields and variants of its declaration and the methods
// declared `T.M` in the root's files or std, each with its declaring line.
// A std type of a known module has stdDir, its module's directory, so that
// another module's type of the same name does not join it.
func (s *Server) lexMemberItems(r *root, typ, stdDir, uri string) []completionItem {
	name := typ
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	name = lastSegment(name)
	if name == "" || !isIdentStart(name[0]) {
		return nil
	}
	pred := func(d decl) bool {
		return d.Owner == name && d.Kind == symMethod || d.Owner == "" && d.Name == name && len(d.Children) > 0
	}
	hits := s.search(r, pred)
	own := len(hits)
	std := s.searchStd(stdDir, pred)
	if len(std) == 0 && stdDir != "" {
		// An alias of another module's type (`type Duration =
		// core.Duration`): its members are declared there.
		std = s.searchStd("", pred)
	}
	hits = append(hits, std...)
	var out []completionItem
	seen := map[string]bool{}
	add := func(h hit, kind int) {
		if seen[h.decl.Name] || strings.HasPrefix(h.decl.Name, "test ") {
			return
		}
		seen[h.decl.Name] = true
		out = append(out, completionItem{Label: h.decl.Name, Kind: kind, Detail: declLine(h),
			Data: &completionData{Subject: name + "." + h.decl.Name, URI: uri}})
	}
	for i, h := range hits {
		// Outside its package only an exported member is reachable.
		if i >= own && !h.decl.Exported {
			continue
		}
		if h.decl.Owner != "" {
			add(h, complMethod)
			continue
		}
		for _, ch := range h.decl.Children {
			if i >= own && ch.Kind == symField && !ch.Exported {
				continue
			}
			kind := complField
			if ch.Kind == symEnumMember {
				kind = complValue
			}
			add(hit{f: h.f, decl: ch}, kind)
		}
	}
	return out
}

func isPathPrefix(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i]) && s[i] != '.' {
			return false
		}
	}
	return true
}

// typeDir is the std module directory of a type spelled `m.T` through
// one of the file's imports, or "" when the spelling names no std module.
func (s *Server) typeDir(text string, ct []token, typ string) string {
	bt := baseType(typ)
	if j := strings.LastIndexByte(bt, '.'); j > 0 {
		if m, ok := imports(text, ct)[bt[:j]]; ok {
			return s.stdModuleDir(m)
		}
	}
	return ""
}

// members answers `mk query members` for a type as completion items. A
// function's answer names its result type, whose members are asked once.
// stdDir is the type's std module directory, or "": when the query caps
// its answer, the declarations of that directory give the rest, so that
// another module's type of the same name does not join them.
func (s *Server) members(r *root, subject, stdDir string) ([]completionItem, bool) {
	for range 2 {
		a, err := s.query(r, "members", []string{subject})
		if err != nil || a == nil || !a.Found {
			return nil, false
		}
		if len(a.Answer) == 1 && strings.Contains(a.Answer[0], " is a function, not a type: ") {
			if t := resultType(a.Answer[0]); t != "" {
				if strings.Contains(t, ".") {
					stdDir = "" // another module's type
				}
				subject = t
				continue
			}
			return nil, false
		}
		var items []completionItem
		have := map[string]bool{}
		for _, l := range a.Answer {
			if it, ok := memberItem(l); ok {
				items = append(items, it)
				have[it.Label] = true
			}
		}
		if a.Truncated > 0 {
			// The query caps its answer; the declarations give the rest.
			for _, it := range s.lexMemberItems(r, subject, stdDir, "") {
				if !have[it.Label] {
					items = append(items, it)
				}
			}
		}
		return items, true
	}
	return nil, false
}

// resultType is the named result type of a signature line: the text after
// the last "): ", without an error set, a `?` or generic arguments.
func resultType(l string) string {
	i := strings.LastIndex(l, "): ")
	if i < 0 {
		return ""
	}
	t := l[i+3:]
	if j := strings.LastIndexByte(t, '!'); j >= 0 {
		t = t[j+1:]
	}
	t = strings.TrimLeft(t, "?&")
	end := 0
	for end < len(t) && (isIdentByte(t[end]) || t[end] == '.') {
		end++
	}
	return t[:end]
}

// memberItem reads one `mk query members` line: a field `name: T`, a
// variant `Name(..)` or `Name`, a method `[export] Name(..): T`.
func memberItem(l string) (completionItem, bool) {
	l = strings.TrimPrefix(l, "export ")
	// "type X[T: B]" is the head line members prints for a bounded type.
	if strings.HasPrefix(l, "impl ") || strings.HasPrefix(l, "type ") || l == "" || !isIdentStart(l[0]) {
		return completionItem{}, false
	}
	n := 0
	for n < len(l) && isIdentByte(l[n]) {
		n++
	}
	it := completionItem{Label: l[:n], Detail: l, Kind: complValue}
	switch {
	case n < len(l) && l[n] == ':':
		it.Kind = complField
	case n < len(l) && (l[n] == '(' || l[n] == '['):
		it.Kind = complMethod
	}
	return it, true
}

// moduleItems is the module map: the package's modules and std's.
func (s *Server) moduleItems(r *root) completionList {
	out := completionList{Items: []completionItem{}}
	a, err := s.query(r, "modules", nil)
	if err != nil || a == nil {
		return out
	}
	for _, l := range a.Answer {
		switch {
		case strings.HasPrefix(l, "prelude:"), strings.HasPrefix(l, "std classes:"):
		case strings.HasPrefix(l, "std:"):
			for _, m := range strings.Fields(strings.TrimPrefix(l, "std:")) {
				out.Items = append(out.Items, completionItem{Label: m, Kind: complModule, Detail: "std"})
			}
		default:
			if f := strings.Fields(l); len(f) > 0 {
				out.Items = append(out.Items, completionItem{Label: f[0], Kind: complModule, Detail: l})
			}
		}
	}
	return out
}

// moduleMembers are the exported top-level names of one of the package's
// modules or a dependency's, from the declaration scan of its directory. A std module's
// names need the driver.
func (s *Server) moduleMembers(r *root, mod string) completionList {
	out := completionList{Items: []completionItem{}}
	seen := map[string]bool{}
	for _, h := range s.searchModule(r, mod, func(d decl) bool { return d.Owner == "" && d.Exported }) {
		if seen[h.decl.Name] || h.decl.Kind == symField || h.decl.Kind == symEnumMember || h.decl.Kind == symMethod {
			continue
		}
		seen[h.decl.Name] = true
		kind := complValue
		switch h.decl.Kind {
		case symStruct, symEnum, symInterface, symTypeParam:
			kind = complStruct
		case symFunction:
			kind = complMethod
		}
		out.Items = append(out.Items, completionItem{Label: h.decl.Name, Kind: kind, Detail: h.decl.Detail})
	}
	return out
}

// preludeNames are the names of std.core every module sees with no
// import: the reference's prelude list (TestPreludeNamesQuoteTheReference).
// `Path` is listed and not declared: it is reserved.
var preludeNames = setOf("Alloc Arch Arena Block Box Canceled Cell Chan Chunks ChunksMut Closed Code Eq Field Flow FlowKind Format Free Gen Guard Hash HashKey Hasher Heap HeapList HeapMap HeapText Io Limits Lines List Map MapIter Method Mutex Ord Os OutOfMemory Param ParseError Path Print ReadError Reader Receiver Replace Scalars Scope Set Slice Split StackSize Swap System TargetInfo Text TextMap TextMapIter Type TypeKind Utf8Error Variant Words WriteError Writer")

// scopeItems are the names a bare word may be at off: the locals declared
// before it in its item, the module's top-level names (its directory's
// files), the file's import bindings, the prelude's names and the
// keywords. Lexical: a local's scope end needs the driver.
func (s *Server) scopeItems(r *root, f srcFile, off int) completionList {
	text := f.text.s
	ct := code(f.toks)
	out := completionList{Items: []completionItem{}}
	seen := map[string]bool{}
	add := func(label string, kind int, detail string) {
		if !seen[label] {
			seen[label] = true
			out.Items = append(out.Items, completionItem{Label: label, Kind: kind, Detail: detail})
			if kind != complKeyword && detail != "local" {
				out.Items[len(out.Items)-1].Data = &completionData{Subject: label, URI: s.uriOf(f.path)}
			}
		}
	}
	i := tokenAt(ct, off)
	if i < 0 {
		// Between tokens: the item of the token before off.
		for i = len(ct) - 1; i >= 0 && ct[i].Start >= off; i-- {
		}
	}
	if i >= 0 {
		a, b := itemOf(text, ct, i)
		for k := a; k <= b && ct[k].Start < off; k++ {
			if ct[k].Kind != tIdent || ct[k].End == off {
				continue
			}
			name := word(text, ct, k)
			if at, ok := localDecl(text, ct, a, b, off, name); ok && at == ct[k].Start {
				add(name, complValue, "local")
			}
		}
	}
	// Sorted: map order never reaches the answer.
	imps := imports(text, ct)
	for _, alias := range sortedKeys(imps) {
		add(alias, complModule, "import "+imps[alias])
	}
	dir := pathKey(dirOf(f.path))
	for _, h := range s.search(r, func(d decl) bool {
		return d.Owner == "" && d.Kind != symField && d.Kind != symEnumMember && d.Kind != symMethod && d.Kind != symNamespace
	}) {
		if pathKey(dirOf(h.f.path)) != dir || strings.HasPrefix(h.decl.Name, "test ") {
			continue
		}
		kind := complValue
		switch h.decl.Kind {
		case symStruct, symEnum, symInterface, symTypeParam:
			kind = complStruct
		case symFunction:
			kind = complMethod
		}
		add(h.decl.Name, kind, h.decl.Detail)
	}
	// The prelude, as std.core declares it; a module-level name hides one.
	core := s.stdModuleDir("std.core")
	for _, h := range s.searchStd(core, func(d decl) bool {
		return core != "" && d.Owner == "" && preludeNames[d.Name] && d.Kind != symField && d.Kind != symEnumMember && d.Kind != symMethod && d.Kind != symNamespace
	}) {
		kind := complValue
		switch h.decl.Kind {
		case symStruct, symEnum, symInterface, symTypeParam:
			kind = complStruct
		case symFunction:
			kind = complMethod
		}
		add(h.decl.Name, kind, h.decl.Detail)
	}
	for _, w := range sortedKeys(keywords) {
		add(w, complKeyword, "keyword")
	}
	return out
}

// sortedKeys are a map's keys in order, so no map iteration order reaches
// an answer.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
