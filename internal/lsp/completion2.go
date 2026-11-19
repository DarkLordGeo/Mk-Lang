// Why: completion must also write code: snippets, struct fields, auto-imports and
// the documentation of the item under selection.
//
// What: contextItems (keyword snippets), constructionFields, importItems (std
// modules with an auto-import on accept), stdModuleMembers, and
// resolveCompletion (completionItem/resolve with the signature and doc).
//
// How: snippets depend on the cursor's context (statement start, inside a
// match); std members come from the declaration scan of the module's
// directory on disk; resolve asks `mk query sig` and falls back to the
// declaration's text.

package lsp

import (
	"sort"
	"strings"
)

// snippet is a keyword's snippet: a statement inside a body, or an item at
// the top level.
type snippet struct {
	label, detail, body string
	item                bool
}

var snippets = []snippet{
	{"if", "if block", "if ${1:cond} {\n\t$0\n}", false},
	{"for", "for loop", "for ${1:x} in ${2:xs} {\n\t$0\n}", false},
	{"match", "match on an enum", "match ${1:x} {\n\t.${2:Variant} {\n\t\t$0\n\t}\n}", false},
	{"scope", "structured concurrency", "scope s := ${1:io}.Scope() {\n\ts.go ${2:F}($3)\n}$0", false},
	{"unsafe", "unsafe block with its reason", "unsafe \"${1:reason}\" {\n\t$0\n}", false},
	{"defer", "run at block exit", "defer $0", false},
	{"fn", "function literal", "fn(${1:a}: ${2:T}): ${3:R} {\n\t$0\n}", false},
	{"try", "propagate the error", "try $0", false},
	{"struct", "struct declaration", "struct ${1:Name} {\n\t${2:field}: ${3:T}\n}", true},
	{"enum", "enum declaration", "enum ${1:Name} {\n\t${2:A}\n}", true},
	{"interface", "interface declaration", "interface ${1:Name} {\n\t${2:M}(&self): ${3:T}\n}", true},
	{"impl", "impl of an interface", "impl ${1:I} for ${2:T} {\n\t$0\n}", true},
	{"test", "test", "test \"${1:name}\" (sys: &System) {\n\t$0\n}", true},
	{"import", "import", "import ${1:std.}$0", true},
}

// contextItems are the snippets that fit at off: statements at the start
// of a line in a body, items at the start of a line at the top level, the
// expression snippets (fn, try) anywhere in a body.
func contextItems(text string, ct []token, off int, lineStart bool) []completionItem {
	n := sort.Search(len(ct), func(i int) bool { return ct[i].Start >= off })
	// A partial word being typed is not part of the context.
	if n > 0 && ct[n-1].End >= off && ct[n-1].Kind == tIdent {
		n--
	}
	top := depthAt(text, ct, n) == 0
	var out []completionItem
	for _, sn := range snippets {
		switch {
		case sn.item && !(top && lineStart):
			continue
		case !sn.item && top:
			continue
		case !sn.item && !lineStart && sn.label != "fn" && sn.label != "try":
			continue
		}
		out = append(out, completionItem{Label: sn.label, Kind: complSnippet, Detail: sn.detail,
			InsertText: sn.body, InsertTextFormat: formatSnippet, SortText: "1" + sn.label})
	}
	return out
}

// constructionFields are the fields of the struct a construction `T(..`
// around off names, less those already written, when off begins an
// argument; ok is false outside a construction.
func (s *Server) constructionFields(f srcFile, off int) ([]completionItem, bool) {
	text := f.text.s
	ct := code(f.toks)
	n := sort.Search(len(ct), func(i int) bool { return ct[i].Start >= off })
	k := n - 1
	if k >= 0 && ct[k].Kind == tIdent && ct[k].End >= off {
		k-- // the partial word
	}
	if k < 0 || (word(text, ct, k) != "(" && word(text, ct, k) != ",") {
		return nil, false
	}
	// The unmatched `(` before off.
	d, open := 0, -1
	for j := k; j >= 0; j-- {
		switch depthDelta(text, ct[j]) {
		case -1:
			d++
		case 1:
			if d == 0 {
				open = j
			} else {
				d--
			}
		}
		if open >= 0 {
			break
		}
	}
	if open < 1 || word(text, ct, open) != "(" || !isIdent(ct, open-1) {
		return nil, false
	}
	c := cursor{f: f, ct: ct, i: open - 1, name: word(text, ct, open-1)}
	if open >= 3 && word(text, ct, open-2) == "." && isIdent(ct, open-3) {
		c.qual = word(text, ct, open-3)
	}
	written := map[string]bool{}
	for _, a := range callArgs(text, ct, open) {
		if isIdent(ct, a[0]) && word(text, ct, a[0]+1) == ":" {
			written[word(text, ct, a[0])] = true
		}
	}
	for _, l := range s.defs(c) {
		h, ok := s.declAt(l)
		if !ok || h.decl.Kind != symStruct || h.decl.Detail != "struct" {
			continue
		}
		var out []completionItem
		for _, fd := range h.decl.Children {
			if fd.Kind != symField || written[fd.Name] {
				continue
			}
			out = append(out, completionItem{Label: fd.Name, Kind: complField,
				Detail:     strings.TrimSpace(h.f.text.s[fd.Start:fd.End]),
				InsertText: fd.Name + ": ", SortText: "0" + fd.Name})
		}
		return out, true
	}
	return nil, false
}

// declAt is the declaration (any kind) whose name is at a location.
func (s *Server) declAt(l Location) (hit, bool) {
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
			if d.NameStart == off {
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

// stdModules are std's importable modules (`std.fs`), from `mk query
// modules`, asked once per root.
func (s *Server) stdModules(r *root) []string {
	if r.stdMods != nil {
		return r.stdMods
	}
	r.stdMods = []string{}
	a, err := s.query(r, "modules", nil)
	if err != nil || a == nil {
		return r.stdMods
	}
	for _, l := range a.Answer {
		if rest, ok := strings.CutPrefix(l, "std:"); ok {
			r.stdMods = strings.Fields(rest)
		}
	}
	return r.stdMods
}

// importItems offer each std module the file does not import, by its
// binding, with the import added on accept after the file's last import
// (or at its top).
func (s *Server) importItems(r *root, f srcFile) []completionItem {
	text := f.text.s
	ct := code(f.toks)
	imps := imports(text, ct)
	have := map[string]bool{}
	for alias, mod := range imps {
		have[alias], have[mod] = true, true
	}
	// The insertion point: the line after the last import.
	at := 0
	for _, r := range items(text, ct) {
		if word(text, ct, r[0]) == "import" {
			at = lineEnd(text, ct[r[1]].End)
			if at < len(text) {
				at++
			}
		}
	}
	u8 := s.utf8.Load()
	pos := f.text.pos(at, u8)
	var out []completionItem
	for _, m := range s.stdModules(r) {
		bind := m[strings.LastIndexByte(m, '.')+1:]
		if have[m] || have[bind] {
			continue
		}
		out = append(out, completionItem{Label: bind, Kind: complModule, Detail: m + " (adds import " + m + ")",
			SortText:            "9" + bind,
			AdditionalTextEdits: []textEdit{{Range: Range{Start: pos, End: pos}, NewText: "import " + m + "\n"}}})
	}
	return out
}

// stdModuleMembers are a std module's exported top-level names, from the
// declaration scan of its directory on disk, each with its declaring line.
func (s *Server) stdModuleMembers(dir, alias, uri string) completionList {
	out := completionList{Items: []completionItem{}}
	seen := map[string]bool{}
	// The scan walks into declarations: a struct's fields, an enum's
	// variants and an interface's methods are not the module's names.
	top := func(d decl) bool {
		return d.Owner == "" && d.Exported && d.Kind != symField && d.Kind != symEnumMember && d.Kind != symMethod
	}
	for _, h := range s.searchStd(dir, top) {
		base := h.f.path[strings.LastIndexAny(h.f.path, `/\`)+1:]
		if seen[h.decl.Name] || strings.HasPrefix(base, "tests_") || h.decl.Kind == symNamespace || strings.HasPrefix(h.decl.Name, "test ") {
			continue
		}
		seen[h.decl.Name] = true
		out.Items = append(out.Items, completionItem{Label: h.decl.Name, Kind: itemKind(h.decl.Kind),
			Detail: declLine(h), Data: &completionData{Subject: alias + "." + h.decl.Name, URI: uri}})
	}
	return out
}

// itemKind is a declaration's completion kind.
func itemKind(k int) int {
	switch k {
	case symStruct, symEnum, symInterface, symTypeParam:
		return complStruct
	case symFunction, symMethod:
		return complMethod
	}
	return complValue
}

// declLine is a declaration's first line, without `export` and an opening
// brace.
func declLine(h hit) string {
	text := h.f.text.s
	end := lineEnd(text, h.decl.Start)
	l := strings.TrimSpace(text[h.decl.Start:end])
	l = strings.TrimSuffix(strings.TrimPrefix(l, "export "), " {")
	return l
}

// resolveCompletion fills an item's documentation with `mk query sig` of
// its subject: the signature, its `///` lines and contracts.
func (s *Server) resolveCompletion(it completionItem) (any, *rpcError) {
	if it.Data == nil || it.Data.Subject == "" || it.Data.URI == "" {
		return it, nil
	}
	path, ok := uriPath(it.Data.URI)
	if !ok {
		return it, nil
	}
	r := s.rootOfPath(path)
	a, err := s.query(r, "sig", []string{it.Data.Subject})
	if err != nil || a == nil || !a.Found || len(a.Answer) == 0 {
		return it, nil
	}
	if it.Detail == "" {
		it.Detail = a.Answer[0]
	}
	value := "```mk\n" + strings.Join(a.Answer, "\n") + "\n```"
	if d, ok := surfaceFor(it.Data.Subject); ok {
		value += "\n\n" + surfaceMarkdown(d)
	}
	it.Documentation = &markupContent{Kind: "markdown", Value: value}
	return it, nil
}

// withSnippets adds the snippets, each in place of the keyword item of its
// label.
func withSnippets(items, snips []completionItem) []completionItem {
	has := map[string]bool{}
	for _, sn := range snips {
		has[sn.Label] = true
	}
	out := items[:0:0]
	for _, it := range items {
		if !(it.Kind == complKeyword && has[it.Label]) {
			out = append(out, it)
		}
	}
	return append(out, snips...)
}
