// Why: every syntax test parses text and checks the same tree invariants.
// What: parsed, parse, treeDepth, codes, codeList, tree and factText.
// How: parse checks ast.Validate and the depth limit on every tree it returns.

package syntax

import (
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// parsed is one parse for a test.
type parsed struct {
	sm    *source.SourceMap
	in    *source.Interner
	src   *source.File
	file  *ast.File
	diags []*diag.Diagnostic
}

// parse parses text as the file "t.mk" and checks the tree's invariants:
// ast.Validate holds and no node is deeper than the limit.
func parse(t testing.TB, text string, opts Options) parsed {
	t.Helper()
	sm := source.NewSourceMap()
	src := sm.Add("t.mk", []byte(text))
	in := source.NewInterner()
	f, ds := ParseFile(src, in, opts)
	if err := ast.Validate(f, src.Text); err != nil {
		t.Fatalf("Validate: %v\n%s", err, text)
	}
	limit := opts.MaxDepth
	if limit <= 0 {
		limit = DefaultMaxDepth
	}
	for _, it := range f.Items {
		if d := treeDepth(it); d > limit {
			t.Fatalf("item at %v has depth %d over %d", it.Span(), d, limit)
		}
	}
	return parsed{sm: sm, in: in, src: src, file: f, diags: ds}
}

// treeDepth is the depth of the tree under n, n counting 1, walked with an
// explicit stack.
func treeDepth(n ast.Node) int {
	type entry struct {
		n ast.Node
		d int
	}
	max := 0
	stack := []entry{{n, 1}}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e.d > max {
			max = e.d
		}
		ast.EachChild(e.n, func(c ast.Node) { stack = append(stack, entry{c, e.d + 1}) })
	}
	return max
}

// codes renders the diagnostics as "E0101@line:col message" lines.
func (r parsed) codes() []string {
	var out []string
	for _, d := range r.diags {
		lc := r.src.LineCol(d.At.Start)
		out = append(out, d.Code.String()+"@"+itoa(int(lc.Line))+":"+itoa(int(lc.Col))+" "+d.Message)
	}
	return out
}

func (r parsed) codeList() string {
	var cs []string
	for _, d := range r.diags {
		cs = append(cs, d.Code.String())
	}
	return strings.Join(cs, " ")
}

// tree is ast.Sprint of the whole file.
func (r parsed) tree() string { return ast.Sprint(r.file, r.in) }

// fact returns a diagnostic's fact rendered as text, "" when absent.
func factText(d *diag.Diagnostic, key string) string {
	f, ok := d.FactValue(key)
	if !ok {
		return ""
	}
	switch f.Kind {
	case diag.FactText:
		return f.Str
	case diag.FactList:
		var parts []string
		for _, x := range f.List {
			parts = append(parts, x.Str)
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	return "?"
}
