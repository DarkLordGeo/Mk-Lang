// Why: the package commands (install, remove, update, the workspace) change
// pkg.mk the way a person would, keeping every comment and byte outside the
// edited list.
//
// What: manifestEditor (openManifest, add, remove, setStrArg, finish) and
// checkManifestText, which edit a manifest's `deps` and `test_deps` lists
// and its string arguments.
//
// How: the manifest is parsed; `const Package` and its pkg.Package(...)
// call are found; an element is spliced into (or cut from) the `deps` or
// `test_deps` array literal, or the argument is added before the call's
// `)`; the text is formatted (format.Source keeps comments); the result is
// checked in memory with the manifest step and written by a rename. Every
// byte outside the edited list stays as it was. A manifest whose Package is
// not a constructor call, or whose list is not an array literal, is refused
// with the edit to make by hand.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/format"
	"mk/internal/source"
	"mk/internal/syntax"
)

// manifestEditor holds one pkg.mk being edited.
type manifestEditor struct {
	path string // pkg.mk on disk
	text string
	orig string // the text as read
	crlf bool   // the file's lines end in CRLF
	in   *source.Interner
	call *ast.CallExpr // the pkg.Package(...) call
}

// openManifest parses dir's pkg.mk for editing.
func openManifest(dir string) (*manifestEditor, error) {
	p := filepath.Join(dir, "pkg.mk")
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	e := &manifestEditor{path: p, text: source.Normalize(raw)}
	e.orig, e.crlf = e.text, strings.Contains(string(raw), "\r\n")
	if err := e.parse(); err != nil {
		return nil, err
	}
	return e, nil
}

// parse reads e.text again, so the spans are the text's after an edit.
func (e *manifestEditor) parse() error {
	e.in = source.NewInterner()
	f := source.NewSourceMap().Add("pkg.mk", []byte(e.text))
	file, ds := syntax.ParseFile(f, e.in, syntax.Options{})
	if len(ds) > 0 || file == nil {
		return fmt.Errorf("%s does not parse; mk check shows why", e.path)
	}
	for _, it := range file.Items {
		cd, ok := it.(*ast.ConstDecl)
		if !ok || cd.Name == nil || e.in.Str(cd.Name.Name) != "Package" {
			continue
		}
		c, ok := cd.Value.(*ast.CallExpr)
		if !ok || !isPkgCall(c, "Package", e.in) {
			return fmt.Errorf("%s: Package is not a pkg.Package(...) call, so mk cannot edit it; make the edit by hand", e.path)
		}
		e.call = c
		return nil
	}
	return fmt.Errorf("%s holds no const Package; make the edit by hand", e.path)
}

// arg is the named argument field of the Package call, or nil.
func (e *manifestEditor) arg(field string) *ast.Arg {
	for _, a := range e.call.Args {
		if a != nil && a.Name != nil && e.in.Str(a.Name.Name) == field {
			return a
		}
	}
	return nil
}

// list is field's array literal; nil and no error when the field is
// absent; an error naming the hand edit when it is not an array literal.
func (e *manifestEditor) list(field string) (*ast.ArrayLit, error) {
	a := e.arg(field)
	if a == nil {
		return nil, nil
	}
	al, ok := a.Value.(*ast.ArrayLit)
	if !ok {
		return nil, fmt.Errorf("%s: %s is not an array literal [...], so mk cannot edit it; make the edit by hand", e.path, field)
	}
	return al, nil
}

// splice replaces text[start:end] with s. The caller reparses (edit)
// before it reads a span again.
func (e *manifestEditor) splice(start, end int, s string) {
	e.text = e.text[:start] + s + e.text[end:]
}

// edited reparses after an edit; a text that no longer parses is an error
// of the edit.
func (e *manifestEditor) edited() error { return e.parse() }

// add appends elem (an expression's text) to field's list, adding the
// argument before the call's `)` when it is absent.
func (e *manifestEditor) add(field, elem string) error {
	if err := e.addRaw(field, elem); err != nil {
		return err
	}
	return e.edited()
}

func (e *manifestEditor) addRaw(field, elem string) error {
	al, err := e.list(field)
	if err != nil {
		return err
	}
	if al == nil {
		end := int(e.call.Span().End) - 1 // the ')'
		i := end
		for i > 0 && (e.text[i-1] == ' ' || e.text[i-1] == '\t' || e.text[i-1] == '\n') {
			i--
		}
		sep := ", "
		if e.text[i-1] == ',' || e.text[i-1] == '(' {
			sep = " "
			if e.text[i-1] == '(' {
				sep = ""
			}
		}
		if strings.Contains(e.text[e.call.Span().Start:end], "\n") && e.addLineAfterLast(field, elem, end) {
			return nil
		}
		if strings.Contains(e.text[e.call.Span().Start:end], "\n") {
			// A call written one argument per line: one more line.
			ins := field + ": [" + elem + "],\n"
			if e.text[i-1] != ',' && e.text[i-1] != '(' {
				ins = ",\n    " + ins
			} else {
				ins = "\n    " + ins
			}
			e.splice(i, end, ins)
			return nil
		}
		e.splice(i, i, sep+field+": ["+elem+"]")
		return nil
	}
	if len(al.Elems) == 0 {
		e.splice(int(al.Span().Start), int(al.Span().End), "["+elem+"]")
		return nil
	}
	last := al.Elems[len(al.Elems)-1].Span()
	rb := int(al.Span().End) - 1 // the ']'
	between := e.text[last.End:rb]
	if strings.Contains(between, "\n") || strings.Contains(e.text[al.Span().Start:last.Start], "\n") {
		// One element per line: a new line after the last, indented as it.
		ls := strings.LastIndexByte(e.text[:last.Start], '\n') + 1
		indent := e.text[ls:last.Start]
		// The rest of the last element's line: a comment there stays on it.
		eol := int(last.End) + strings.IndexByte(between+"\n", '\n')
		if j := strings.IndexByte(between, ','); j >= 0 && strings.TrimSpace(between[:j]) == "" {
			// A trailing comma: the new element keeps one too.
			at := int(last.End) + j + 1
			if eol < rb && strings.HasPrefix(strings.TrimSpace(e.text[at:eol]), "//") {
				e.splice(eol+1, eol+1, indent+elem+",\n")
			} else {
				e.splice(at, at, "\n"+indent+elem+",")
			}
		} else if eol < rb && strings.HasPrefix(strings.TrimSpace(e.text[last.End:eol]), "//") {
			e.splice(eol+1, eol+1, indent+elem+"\n")
			e.splice(int(last.End), int(last.End), ",")
		} else {
			e.splice(int(last.End), int(last.End), ",\n"+indent+elem)
		}
		return nil
	}
	e.splice(int(last.End), int(last.End), ", "+elem)
	return nil
}

// addLineAfterLast adds `field: [elem],` on a line of its own after the
// line of the call's last argument, indented as it, when that line ends
// before the call's `)` (at end). A comment ending the line stays on it
// with its text: the comma the last argument needs goes right after the
// argument, never after the comment. False when the shape is
// another (the caller's splice before `)` applies).
func (e *manifestEditor) addLineAfterLast(field, elem string, end int) bool {
	if len(e.call.Args) == 0 || e.call.Args[len(e.call.Args)-1] == nil {
		return false
	}
	a := e.call.Args[len(e.call.Args)-1].Span()
	eol := int(a.End) + strings.IndexByte(e.text[a.End:]+"\n", '\n')
	if eol >= end {
		return false // the `)` is on the last argument's line
	}
	ls := strings.LastIndexByte(e.text[:a.Start], '\n') + 1
	indent := e.text[ls:a.Start]
	if strings.TrimLeft(indent, " \t") != "" {
		indent = "    "
	}
	j := int(a.End)
	for j < eol && (e.text[j] == ' ' || e.text[j] == '\t') {
		j++
	}
	e.splice(eol+1, eol+1, indent+field+": ["+elem+"],\n")
	if j == eol || e.text[j] != ',' {
		e.splice(int(a.End), int(a.End), ",")
	}
	return true
}

// depElems finds, in deps then test_deps, the Dep whose name is name: the
// list's field and the element's index.
func (e *manifestEditor) depElem(name string) (string, *ast.ArrayLit, int, error) {
	for _, field := range []string{"deps", "test_deps"} {
		al, err := e.list(field)
		if err != nil {
			return "", nil, -1, err
		}
		if al == nil {
			continue
		}
		for i, el := range al.Elems {
			c, ok := el.(*ast.CallExpr)
			if !ok || !isPkgCall(c, "Dep", e.in) {
				continue
			}
			if v, ok := e.strArg(c, "name"); ok && v == name {
				return field, al, i, nil
			}
		}
	}
	return "", nil, -1, nil
}

// strArg is c's named string argument.
func (e *manifestEditor) strArg(c *ast.CallExpr, name string) (string, bool) {
	for _, a := range c.Args {
		if a == nil || a.Name == nil || e.in.Str(a.Name.Name) != name {
			continue
		}
		if lit, ok := a.Value.(*ast.BasicLit); ok && lit.Kind == ast.TokStr {
			if v, ok := syntax.StringValue(lit.Kind, lit.Raw); ok {
				return string(v), true
			}
		}
	}
	return "", false
}

// setStrArg replaces the value of c's named string argument.
func (e *manifestEditor) setStrArg(c *ast.CallExpr, name, v string) bool {
	for _, a := range c.Args {
		if a != nil && a.Name != nil && e.in.Str(a.Name.Name) == name {
			if lit, ok := a.Value.(*ast.BasicLit); ok && lit.Kind == ast.TokStr {
				e.splice(int(lit.Span().Start), int(lit.Span().End), mkQuote(v))
				return true
			}
		}
	}
	return false
}

// remove cuts element i of al: on a line of its own, with its line; else
// with the comma that separates it from a neighbour.
func (e *manifestEditor) remove(al *ast.ArrayLit, i int) error {
	e.removeRaw(al, i)
	return e.edited()
}

func (e *manifestEditor) removeRaw(al *ast.ArrayLit, i int) {
	sp := al.Elems[i].Span()
	ls := int(sp.Start)
	for ls > 0 && (e.text[ls-1] == ' ' || e.text[ls-1] == '\t') {
		ls--
	}
	le := int(sp.End)
	if le < len(e.text) && e.text[le] == ',' {
		le++
	}
	for le < len(e.text) && (e.text[le] == ' ' || e.text[le] == '\t') {
		le++
	}
	if ls == 0 || e.text[ls-1] == '\n' {
		// An element that starts its line, and whose line holds nothing
		// after it but a comment, goes with its line, its comment and the
		// comment lines directly above it: they are its own, never its
		// neighbour's.
		eol := le + strings.IndexByte(e.text[le:]+"\n", '\n')
		if rest := e.text[le:eol]; (rest == "" || strings.HasPrefix(rest, "//")) && eol < len(e.text) {
			for ls > 0 {
				pls := strings.LastIndexByte(e.text[:ls-1], '\n') + 1
				if !strings.HasPrefix(strings.TrimSpace(e.text[pls:ls-1]), "//") {
					break
				}
				ls = pls
			}
			e.splice(ls, eol+1, "")
			return
		}
	}
	switch {
	case len(al.Elems) == 1:
		e.splice(int(al.Span().Start), int(al.Span().End), "[]")
	case i > 0:
		e.splice(int(al.Elems[i-1].Span().End), int(sp.End), "")
	default:
		e.splice(int(sp.Start), int(al.Elems[1].Span().Start), "")
	}
}

// mkQuote is s as an Mk string literal.
func mkQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// finish formats the edited text, checks it with the manifest step (cfg
// rooted at the manifest's package) and writes it by a rename; with dryRun
// it only checks. It returns the new text.
func (e *manifestEditor) finish(cfg Config, dryRun bool) (string, error) {
	// A manifest in mk fmt form stays in it; any other keeps every byte
	// outside the edited list as written.
	out := []byte(e.text)
	if was, err := format.Source(e.path, []byte(e.orig)); err == nil && string(was) == e.orig {
		if out, err = format.Source(e.path, []byte(e.text)); err != nil {
			return "", fmt.Errorf("the edited %s does not format: %v", e.path, err)
		}
	} else if e.crlf {
		// Every byte outside the list, its CRLF line ends too.
		out = []byte(strings.ReplaceAll(e.text, "\n", "\r\n"))
	}
	if ds := checkManifestText(cfg, filepath.Dir(e.path), out); len(ds) > 0 {
		var msgs []string
		for _, d := range ds {
			msgs = append(msgs, d.Message)
		}
		return "", fmt.Errorf("the edited %s does not check: %s", e.path, strings.Join(msgs, "; "))
	}
	if !dryRun {
		if err := writeFileAtomic(e.path, out); err != nil {
			return "", err
		}
	}
	return string(out), nil
}

// checkManifestText runs the manifest step over text as dir's pkg.mk,
// without its dependencies.
func checkManifestText(cfg Config, dir string, text []byte) []*diag.Diagnostic {
	cfg.Root, cfg.CacheDir = dir, ""
	s, err := NewSession(cfg)
	if err != nil {
		return []*diag.Diagnostic{diag.Toolchain("fs", "", err.Error(), err.Error())}
	}
	w := newWorld(s.cfg)
	ms := w.newManifestStep(w.stdTree())
	if _, ds := ms.load(w.stdTree(), stdLayout.Manifest, ""); len(ds) > 0 {
		return ds
	}
	t := w.diskTree(s.root.dir, s.root.prefix).withOverlay(map[string][]byte{"pkg.mk": text})
	_, ds := ms.load(t, "pkg.mk", "main")
	return ds
}
