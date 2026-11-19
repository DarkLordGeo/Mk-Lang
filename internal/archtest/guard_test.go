// Why: a panic in a worker goroutine must become an internal compiler error
// report, not crash the compiler, so every worker body runs under
// source.Guard.
// What: TestGoroutinesAreGuarded over every non-test file, and
// TestGuardScanItself over a planted sample.
// How: guardScan parses each package with go/ast and follows go statements
// to the function they start.

package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// guardScan holds one package's parsed non-test files.
type guardScan struct {
	fset  *token.FileSet
	pkg   string // DAG name; "source" may call Guard unqualified
	files []*ast.File
	funcs map[string][]*ast.FuncDecl // functions and methods by name
}

func newGuardScan(pkg string) *guardScan {
	return &guardScan{fset: token.NewFileSet(), pkg: pkg, funcs: map[string][]*ast.FuncDecl{}}
}

func (g *guardScan) add(name string, src []byte) error {
	f, err := parser.ParseFile(g.fset, name, src, 0)
	if err != nil {
		return err
	}
	g.files = append(g.files, f)
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
			g.funcs[fd.Name.Name] = append(g.funcs[fd.Name.Name], fd)
		}
	}
	return nil
}

// isGuardCall reports whether e is a call of source.Guard.
func (g *guardScan) isGuardCall(e ast.Expr) bool {
	c, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch f := c.Fun.(type) {
	case *ast.SelectorExpr:
		x, ok := f.X.(*ast.Ident)
		return ok && x.Name == "source" && f.Sel.Name == "Guard"
	case *ast.Ident:
		return g.pkg == "source" && f.Name == "Guard"
	}
	return false
}

// guarded reports whether body's first statement after any leading defers
// calls source.Guard (as a statement, an assignment, or an if's init).
func (g *guardScan) guarded(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	for _, st := range body.List {
		switch s := st.(type) {
		case *ast.DeferStmt:
			continue
		case *ast.ExprStmt:
			return g.isGuardCall(s.X)
		case *ast.AssignStmt:
			return len(s.Rhs) == 1 && g.isGuardCall(s.Rhs[0])
		case *ast.IfStmt:
			if a, ok := s.Init.(*ast.AssignStmt); ok {
				return len(a.Rhs) == 1 && g.isGuardCall(a.Rhs[0])
			}
			return false
		default:
			return false
		}
	}
	return false
}

// unguarded returns the position of every go statement whose goroutine body
// does not start under source.Guard: a function literal, or a function or
// method of this package (by name; every same-named method must be guarded).
func (g *guardScan) unguarded() []string {
	var bad []string
	for _, f := range g.files {
		ast.Inspect(f, func(n ast.Node) bool {
			gs, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			ok = false
			switch fn := gs.Call.Fun.(type) {
			case *ast.FuncLit:
				ok = g.guarded(fn.Body)
			case *ast.Ident:
				ok = g.allGuarded(fn.Name)
			case *ast.SelectorExpr:
				ok = g.allGuarded(fn.Sel.Name)
			}
			if !ok {
				bad = append(bad, g.fset.Position(gs.Pos()).String())
			}
			return true
		})
	}
	return bad
}

func (g *guardScan) allGuarded(name string) bool {
	ds := g.funcs[name]
	for _, d := range ds {
		if !g.guarded(d.Body) {
			return false
		}
	}
	return len(ds) > 0
}

// TestGoroutinesAreGuarded: every go statement in non-test code starts a
// function literal whose first statement (after defers) is a source.Guard
// call, or calls a function of its package that does. The tools under
// tools/ are exempt: they stand outside the compiler's DAG, and all but
// tools/release import only Go's standard library and each other, so they
// cannot reach source.Guard; tools/release imports release, fetch and git
// and is exempt with them, since the ICE convention Guard serves is the
// compiler's and a tool is no part of the compiler.
func TestGoroutinesAreGuarded(t *testing.T) {
	scans := map[string]*guardScan{}
	for _, f := range files(t) {
		if f.test || strings.HasPrefix(f.pkg, "tools/") {
			continue
		}
		g := scans[f.pkg]
		if g == nil {
			g = newGuardScan(f.pkg)
			scans[f.pkg] = g
		}
		if err := g.add(filepath.FromSlash(f.rel), f.src); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range scans {
		for _, at := range g.unguarded() {
			t.Errorf("%s: goroutine does not start under source.Guard", at)
		}
	}
}

// TestGuardScanItself checks the scanner on accepted and rejected forms.
func TestGuardScanItself(t *testing.T) {
	src := `package p
import "mk/internal/source"
func work() { source.Guard("w", source.Span{}, func() {}) }
func bare() {}
type pool struct{}
func (p *pool) run() { defer done(); _ = source.Guard("r", source.Span{}, func() {}) }
func done() {}
func A() {
	go func() { defer done(); if ie := source.Guard("a", source.Span{}, func() {}); ie != nil {} }()
	go work()
	go (&pool{}).run()
}
func B() {
	go func() { done(); source.Guard("b", source.Span{}, func() {}) }()
	go bare()
	go other.F()
}
`
	g := newGuardScan("p")
	if err := g.add("p.go", []byte(src)); err != nil {
		t.Fatal(err)
	}
	bad := g.unguarded()
	if len(bad) != 3 {
		t.Fatalf("want the three go statements of B, got %v", bad)
	}
	for _, b := range bad {
		if !strings.HasPrefix(b, "p.go:14:") && !strings.HasPrefix(b, "p.go:15:") && !strings.HasPrefix(b, "p.go:16:") {
			t.Errorf("unexpected position %s", b)
		}
	}
}
