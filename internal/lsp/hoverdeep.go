// Why: hover is the main way to learn a name, so it must show the signature,
// the doc comment, the layout and the members, for user and std items alike.
//
// What: nameHover and its helpers: hoverSubjects, sameDecl, typeSections,
// lexMembers, importHover, moduleLoc and headerComment.
//
// How: the checked answer first (`mk query sig`, and for a type `kind`, `shape`
// and `members`), counted only when it resolves to the declaration the text
// resolves to; then the declaration's own text and comment (declHover);
// then an import binding's module; then the checker's type (TypeAt).

package lsp

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// builtinMembers are the methods of `?T` and `E!T`, which no source
// declares: the rows of the reference on errors and its library list.
var builtinMembers = map[string]string{
	"Must":   "`.Must()`: On `?T` or `E!T`: the value, or panic\n\n*reference: Errors*",
	"IsSome": "`.IsSome()`: a method of `?T`: `Must()`, `IsSome()`, `IsNone()`\n\n*reference: Library surface*",
	"IsNone": "`.IsNone()`: a method of `?T`: `Must()`, `IsSome()`, `IsNone()`\n\n*reference: Library surface*",
}

// maxMembers is the most member lines a type's hover lists.
const maxMembers = 40

// nameHover is the hover of the identifier under c, or nil.
func (s *Server) nameHover(c cursor) any {
	r := s.rootOfPath(c.f.path)
	u8 := s.utf8.Load()
	if h, ok := s.importHover(r, c); ok {
		return h
	}
	lex := s.defs(c)
	var lexDecl hit
	haveDecl := false
	for _, l := range lex {
		if h, ok := s.declAt(l); ok {
			lexDecl, haveDecl = h, true
			break
		}
	}
	if !haveDecl && len(lex) > 0 {
		// A payload field, a label, a pattern binding: no item of its own.
		if v, ok := s.labelHover(c, lex[0]); ok {
			return s.hoverAtCursor(c, v)
		}
	}
	if haveDecl && (lexDecl.decl.Kind == symField || lexDecl.decl.Kind == symEnumMember) {
		// A field or a variant: its own rendering (the query's answer is
		// its owner's).
		if v, ok := s.declMarkdown(c, lexDecl); ok {
			return s.hoverAtCursor(c, v)
		}
	}
	for _, subject := range s.hoverSubjects(r, c, lexDecl, haveDecl) {
		a, err := s.query(r, "sig", []string{subject})
		if err != nil || a == nil || !a.Found || len(a.Answer) == 0 || !s.sameDecl(r, subject, lex) || !namesWord(a.Answer[0], c.name) {
			continue // mk query folds case: `code` would find `Code`
		}
		lines := a.Answer
		typ := isTypeHead(lines[0])
		if typ {
			if k, err := s.query(r, "kind", []string{subject}); err == nil && k != nil && k.Found {
				lines = append(lines, k.Answer...)
			}
		}
		h := hoverOf(c, lines, u8)
		if haveDecl && !strings.Contains(strings.Join(lines, "\n"), "///") {
			// std's comments are `//` ones, which the query does not carry.
			if doc := docAbove(lexDecl.f.text.s, lexDecl.decl.Start); doc != "" {
				h.Contents.Value += "\n\n" + doc
			}
		}
		if typ {
			dir := s.typeDir(c.f.text.s, code(c.f.toks), subject)
			if dir == "" && haveDecl {
				dir = filepath.Dir(lexDecl.f.path) // the type's own directory
			}
			h.Contents.Value += s.typeSections(r, subject, dir)
			if haveDecl && lexDecl.decl.Detail == "error" {
				h.Contents.Value += s.errorSetsMD(lexDecl.f.path, lexDecl.decl.Name)
			}
		}
		d, surfaced := surfaceFor(subject)
		if surfaced {
			h.Contents.Value += "\n\n" + surfaceMarkdown(d)
		}
		if haveDecl && lexDecl.decl.Owner != "" {
			h.Contents.Value += s.methodContextOpt(c, lexDecl.decl.Owner, lexDecl.decl.Name, !surfaced)
		}
		return h
	}
	if h, ok := s.declHover(c); ok {
		return h
	}
	if v, ok := builtinMembers[c.name]; ok && afterDot(c) {
		t := c.ct[c.i]
		rng := c.f.text.span(t.Start, t.End, u8)
		return hover{Contents: markupContent{Kind: "markdown", Value: v}, Range: &rng}
	}
	if t, ok := s.typeAt(c.f, c.ct[c.i].End); ok {
		return hoverOf(c, []string{c.name + ": " + t}, u8)
	}
	return nil
}

// hoverSubjects are subjects, with a std file's own module first for a
// bare name (`std.compress.Inflate` before `Inflate`, which may be another
// module's).
func (s *Server) hoverSubjects(r *root, c cursor, h hit, have bool) []string {
	out := s.subjects(r, c)
	if c.qual == "" {
		if m := s.moduleOf(c.f.path); m != "" && m != "std" {
			out = append([]string{m + "." + c.name}, out...)
		}
	}
	if have {
		// The declaration the text resolves the name to, as the query
		// spells it: `Slice.Len` for `out.Len()` on a slice.
		var pre []string
		m := s.moduleOf(h.f.path)
		short := lastSegment(m)
		if h.decl.Owner != "" {
			// The module's spelling first: `Conn.Close` alone may be
			// another module's type of that name (net.Conn, tls.Conn).
			if m != "" && m != "std.core" {
				pre = append(pre, short+"."+h.decl.Owner+"."+h.decl.Name)
			}
			pre = append(pre, h.decl.Owner+"."+h.decl.Name)
		} else if m != "" && m != "std" {
			pre = append(pre, m+"."+h.decl.Name)
		}
		out = append(pre, out...)
	}
	return out
}

// sameDecl reports whether the item the last check resolves subject to is
// declared where the text resolves the name (any of lex); true when either
// side does not say.
func (s *Server) sameDecl(r *root, subject string, lex []Location) bool {
	if len(lex) == 0 {
		return true
	}
	l, ok := s.lookupDef(r, subject, "")
	if !ok {
		return true
	}
	for _, x := range lex {
		if sameURI(x.URI, l.URI) && x.Range.Start.Line == l.Range.Start.Line {
			return true
		}
	}
	return false
}

// typeSections are a checked type's layout and members, as markdown.
// stdDir is the type's std module directory, as members takes it.
func (s *Server) typeSections(r *root, subject, stdDir string) string {
	var b strings.Builder
	if a, err := s.query(r, "shape", []string{subject}); err == nil && a != nil && a.Found && len(a.Answer) > 0 && strings.Contains(a.Answer[0], "size") {
		b.WriteString("\n\n**Layout:** " + strings.Join(a.Answer, "; "))
	}
	if a, err := s.query(r, "members", []string{subject}); err == nil && a != nil && a.Found && len(a.Answer) > 0 {
		lines := a.Answer
		if a.Truncated > 0 {
			// The query caps its answer; the declarations give the rest.
			have := map[string]bool{}
			for _, l := range lines {
				if it, ok := memberItem(l); ok {
					have[it.Label] = true
				}
			}
			for _, it := range s.lexMemberItems(r, subject, stdDir, "") {
				if !have[it.Label] {
					lines = append(lines, it.Detail)
				}
			}
		}
		b.WriteString("\n\n**Members**\n" + codeBlock(capLines(lines)))
	}
	return b.String()
}

// capLines keeps the first maxMembers lines, noting how many it left out.
func capLines(ls []string) []string {
	if len(ls) <= maxMembers {
		return ls
	}
	return append(ls[:maxMembers:maxMembers], "// ... "+strconv.Itoa(len(ls)-maxMembers)+" more")
}

func codeBlock(ls []string) string { return "```mk\n" + strings.Join(ls, "\n") + "\n```" }

// lexMembers are a type's methods (declared T.M in its module) and impls
// (`impl I for T`), each as its head, read from the declarations.
func (s *Server) lexMembers(h hit) []string {
	dir := pathKey(filepath.Dir(h.f.path))
	var methods, impls []string
	r := s.rootOfPath(h.f.path)
	paths := append(s.rootFiles(r), s.stdFiles()...)
	seen := map[string]bool{}
	for _, x := range s.searchPaths(dedupPaths(paths), func(d decl) bool {
		if d.Owner == h.decl.Name && d.Kind == symMethod {
			return true
		}
		if d.Detail == "impl" {
			_, typ := implParts(d.Name)
			return typ == h.decl.Name
		}
		return false
	}) {
		if pathKey(filepath.Dir(x.f.path)) != dir {
			continue
		}
		head := declHead(x.f, x.decl)
		if i := strings.IndexByte(head, '\n'); i >= 0 && x.decl.Detail != "impl" {
			head = head[:i]
		}
		if seen[head] {
			continue
		}
		seen[head] = true
		if x.decl.Detail == "impl" {
			if i := strings.IndexByte(head, '{'); i >= 0 {
				head = strings.TrimSpace(head[:i])
			}
			impls = append(impls, head)
		} else {
			methods = append(methods, head)
		}
	}
	return append(methods, impls...)
}

// dedupPaths drops repeated paths, keeping the first.
func dedupPaths(ps []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range ps {
		if k := pathKey(p); !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}

// importHover is the hover of an import binding (`raw` in `raw.FenceOrd`,
// or the module path of an import line): the import and the module's
// header comment.
func (s *Server) importHover(r *root, c cursor) (hover, bool) {
	mod, ok := s.bindingAt(c)
	if !ok {
		return hover{}, false
	}
	v := codeBlock([]string{"import " + mod})
	if l, ok := s.moduleLoc(r, mod); ok {
		if p, ok := uriPath(l.URI); ok {
			if f, ok := s.fileOf(p); ok {
				if doc := headerComment(f.text.s); doc != "" {
					v += "\n\n" + doc
				}
			}
		}
	}
	t := c.ct[c.i]
	rng := c.f.text.span(t.Start, t.End, s.utf8.Load())
	return hover{Contents: markupContent{Kind: "markdown", Value: v}, Range: &rng}, true
}

// bindingAt is the module an import binding under c names: a bare name
// that the file imports and that a `.` follows, or a segment of an import
// line's path.
func (s *Server) bindingAt(c cursor) (string, bool) {
	text := c.f.text.s
	imps := imports(text, c.ct)
	if c.qual == "" && word(text, c.ct, c.i+1) == "." && !nameIsLocal(c) {
		if mod, ok := imps[c.name]; ok {
			return mod, true
		}
	}
	// On an import line: the whole path.
	start := strings.LastIndexByte(text[:c.ct[c.i].Start], '\n') + 1
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text) - start
	}
	line := strings.TrimSpace(text[start : start+end])
	if rest, ok := strings.CutPrefix(line, "import "); ok {
		rest = strings.TrimSpace(rest)
		if _, path, ok := strings.Cut(rest, "="); ok {
			rest = strings.TrimSpace(path)
		}
		if f := strings.Fields(rest); len(f) > 0 {
			return f[0], true
		}
	}
	return "", false
}

// moduleLoc is the start of a module's main file: the file named for the
// module's last segment, else its first non-test file.
func (s *Server) moduleLoc(r *root, mod string) (Location, bool) {
	dir := ""
	if rest, ok := strings.CutPrefix(mod, "std."); ok {
		if tree, ok := strings.CutPrefix(r.key, "std:"); ok {
			dir = filepath.Join(tree, filepath.FromSlash(strings.ReplaceAll(rest, ".", "/")))
		} else {
			dir = s.stdModuleDir(mod)
		}
	} else if first, rest, _ := strings.Cut(mod, "."); r.depDirs[first] != "" {
		// A dependency's module (`units.conv` in ../units/conv).
		dir = r.depDirs[first]
		if rest != "" {
			dir = joinModule(dir, rest)
		}
	} else if r.file == "" {
		for _, try := range []string{mod, strings.SplitN(mod, ".", 2)[len(strings.SplitN(mod, ".", 2))-1]} {
			if d := joinModule(r.dir, try); isDir(d) {
				dir = d
				break
			}
		}
	}
	if dir == "" {
		return Location{}, false
	}
	return s.moduleLocDir(dir, mod)
}

// moduleLocDir is moduleLoc in the module directory dir.
func (s *Server) moduleLocDir(dir, mod string) (Location, bool) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return Location{}, false
	}
	var files []string
	for _, e := range es {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, ".mk") && (n != "pkg.mk" || mod == "std.pkg") && !strings.HasPrefix(n, "tests_") && !strings.HasSuffix(n, "_test.mk") {
			files = append(files, n)
		}
	}
	if len(files) == 0 {
		// A module a manifest selects per target (std.sys is
		// sys/windows here): this OS's directory.
		if sub := filepath.Join(dir, runtime.GOOS); isDir(sub) && !strings.HasSuffix(dir, runtime.GOOS) {
			return s.moduleLocDir(sub, mod)
		}
		return Location{}, false
	}
	sort.Strings(files)
	pick := files[0]
	last := mod[strings.LastIndexByte(mod, '.')+1:]
	for _, n := range files {
		if n == last+".mk" {
			pick = n
		}
	}
	return Location{URI: s.uriOf(filepath.Join(dir, pick)), Range: Range{}}, true
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// headerComment is a file's leading run of `//` lines, slashes removed.
func headerComment(text string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "//") {
			break
		}
		out = append(out, strings.TrimPrefix(strings.TrimPrefix(t, "//"), " "))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
