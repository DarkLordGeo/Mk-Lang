// Why: two majors of one source are two identities and may coexist under
// two local names, but the build links both, so a C symbol both define
// (an `export extern` function, a global of a C or an assembly source)
// would clash at the link with a message that names neither package.
//
// What: checkSymbolClash (E0202 symbol-clash at the second Dep, naming the
// symbol and both versions) and the symbol scanners cSymbols,
// exportExterns, asmGlobals, cDefinitions and their helpers.
//
// How: the symbols are read from the packages' text before anything is
// checked: `export extern` definitions from each module's syntax; a C
// source's external definitions by a scan of its top level (a function or
// variable defined outside any brace, not `static`, `extern` or `typedef`);
// an assembly source's `.globl` and `.global` names. A symbol the scan
// misses still fails at the link.

package driver

import (
	"path"
	"slices"
	"strings"

	"mk/internal/ast"
	"mk/internal/source"
	"mk/internal/syntax"
)

// checkSymbolClash is E0202 symbol-clash for each later package of a
// source, in build order, that defines a C symbol an earlier major of the
// same source defines.
func (dw *depWalk) checkSymbolClash() {
	bySource := map[string][]*depPkg{}
	var sources []string
	for _, p := range dw.order {
		if p.source == "" {
			continue
		}
		if bySource[p.source] == nil {
			sources = append(sources, p.source)
		}
		bySource[p.source] = append(bySource[p.source], p)
	}
	for _, src := range sources {
		ps := bySource[src]
		if len(ps) < 2 {
			continue
		}
		owner := map[string]*depPkg{}
		for _, p := range ps {
			for _, sym := range dw.cSymbols(p) {
				q := owner[sym]
				if q == nil {
					owner[sym] = p
					continue
				}
				man, field := p.viaMan, p.via
				if man == nil {
					man, field = dw.root, "deps"
				}
				dw.diags = append(dw.diags, depFail(man, field, "symbol-clash",
					"%s v%s and %s v%s both define the C symbol %s, and one build links both majors; depend on one major, or use a version in which one renames it",
					src, q.version, src, p.version, sym))
				return
			}
		}
	}
}

// cSymbols are the C symbols p defines, sorted and without repeats.
func (dw *depWalk) cSymbols(p *depPkg) []string {
	var out []string
	if p.tree == nil {
		return nil
	}
	files, _ := p.tree.mkFiles("", false, func(rel, name string) bool { return name == "vendor" || name == "gen" })
	for _, f := range files {
		raw, err := p.tree.readFile(f)
		if err != nil {
			continue
		}
		out = append(out, exportExterns(f, raw)...)
	}
	if p.man != nil {
		for _, c := range p.man.CSources {
			if raw, err := p.tree.readFile(path.Clean(c.Path)); err == nil {
				out = append(out, cDefinitions(string(raw))...)
			}
		}
		for _, a := range p.man.AsmSources {
			if raw, err := p.tree.readFile(path.Clean(a.Path)); err == nil {
				out = append(out, asmGlobals(string(raw))...)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// exportExterns are the names of the `export extern` definitions (an
// extern with a body) of one module file.
func exportExterns(name string, raw []byte) []string {
	in := source.NewInterner()
	f := source.NewSourceMap().Add(name, raw)
	file, _ := syntax.ParseFile(f, in, syntax.Options{})
	if file == nil {
		return nil
	}
	var out []string
	for _, it := range file.Items {
		if x, ok := it.(*ast.ExternDecl); ok && x.Body != nil && x.Sig != nil && x.Sig.Name != nil && x.Sig.Name.Name != nil {
			out = append(out, in.Str(x.Sig.Name.Name.Name))
		}
	}
	return out
}

// asmGlobals are the names an assembly source makes global.
func asmGlobals(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(strings.ReplaceAll(l, ",", " "))
		if len(f) >= 2 && (f[0] == ".globl" || f[0] == ".global") {
			out = append(out, f[1:]...)
		}
	}
	return out
}

// cDefinitions are the external definitions of a C source, by a scan of
// its top level: comments, strings and preprocessor lines are skipped; a
// top-level declaration that is not static, extern or a typedef defines
// the identifier before its first `(` (a function, when a body follows)
// or, for a variable, the identifier before its `=`, `[` or `;`.
func cDefinitions(text string) []string {
	var out []string
	var decl strings.Builder
	depth := 0
	flush := func(body bool) {
		d := strings.TrimSpace(decl.String())
		decl.Reset()
		if d == "" {
			return
		}
		words := strings.FieldsFunc(d, func(r rune) bool { return !isCIdent(r) })
		for _, w := range words {
			if w == "static" || w == "extern" || w == "typedef" {
				return
			}
		}
		if strings.IndexByte(d, '(') >= 0 {
			if !body {
				return // a prototype, or a declaration through a macro
			}
			if n := cFuncName(d); n != "" {
				out = append(out, n)
			}
			return
		}
		if body {
			return // struct, union or enum definition
		}
		cut := len(d)
		for _, c := range "=[" {
			if i := strings.IndexRune(d, c); i >= 0 && i < cut {
				cut = i
			}
		}
		if n := lastCIdent(d[:cut]); n != "" && len(words) >= 2 {
			out = append(out, n)
		}
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			j := strings.Index(text[i+2:], "*/")
			if j < 0 {
				return out
			}
			i += j + 3
		case c == '#' && depth == 0 && strings.TrimSpace(decl.String()) == "":
			for i < len(text) && (text[i] != '\n' || text[i-1] == '\\') {
				i++
			}
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(text) && text[j] != c {
				if text[j] == '\\' {
					j++
				}
				j++
			}
			if depth == 0 {
				decl.WriteString(text[i:min(j+1, len(text))])
			}
			i = j
		case c == '{':
			if depth == 0 {
				flush(true)
			}
			depth++
		case c == '}':
			if depth > 0 {
				depth--
			}
		case c == ';' && depth == 0:
			flush(false)
		default:
			if depth == 0 {
				decl.WriteByte(c)
			}
		}
	}
	return out
}

// cFuncName is the name a function definition's head d defines: the
// identifier before the one top-level parenthesis group that follows a
// plain identifier (not a keyword, not a `__` name such as
// __attribute__). With none, or more than one (a macro call before the
// definition), it is "": a miss, never a wrong name.
func cFuncName(d string) string {
	var names []string
	depth := 0
	for i := 0; i < len(d); i++ {
		switch d[i] {
		case '(':
			if depth == 0 {
				if n := lastCIdent(d[:i]); n != "" && !strings.HasPrefix(n, "__") && !cDeclKeywords[n] {
					names = append(names, n)
				}
			}
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
	}
	if len(names) != 1 {
		return ""
	}
	return names[0]
}

// cDeclKeywords are the C words that may stand before a parenthesis.
var cDeclKeywords = map[string]bool{"int": true, "char": true, "void": true, "long": true, "short": true, "unsigned": true,
	"signed": true, "float": true, "double": true, "const": true, "volatile": true, "struct": true, "union": true,
	"enum": true, "sizeof": true, "return": true, "if": true, "while": true, "for": true, "switch": true,
	"_Alignas": true, "_Static_assert": true, "alignas": true, "static_assert": true, "typeof": true, "_Atomic": true}

// isCIdent reports whether r may be part of a C identifier.
func isCIdent(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// lastCIdent is the last identifier of s ("" when s ends in none).
func lastCIdent(s string) string {
	s = strings.TrimRight(s, " \t\r\n*")
	i := len(s)
	for i > 0 && isCIdent(rune(s[i-1])) {
		i--
	}
	if i == len(s) || s[i] >= '0' && s[i] <= '9' {
		return ""
	}
	return s[i:]
}
