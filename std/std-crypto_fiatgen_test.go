// Why: Fiat Cryptography's field code is proved correct; a mechanical
// translation carries that proof to the Mk code.
// What: TestSTDCRYPTO_FiatTranslation, which writes or checks
// std/crypto/fiat_<field>.mk from Fiat's generated Go for P-256 and P-384.
// How: one Mk statement per Go statement; bits.Mul64 becomes raw.Mul128 and
// Add64/Sub64 become FiatAdd64/FiatSub64. Divstep and Msat are not translated.

package std

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cr2Fiat is one field: its source under Mk-evidence/fiat-crypto, its Mk
// prefix, and what it is.
type cr2Fiat struct {
	src, prefix, file, what string
}

var cr2Fiats = []cr2Fiat{
	{"fiat-go/64/p256/p256.go", "P256", "fiat_p256.mk", "the P-256 base field, p = 2^256 - 2^224 + 2^192 + 2^96 - 1"},
	{"fiat-go/64/p256scalar/p256scalar.go", "P256n", "fiat_p256n.mk", "the P-256 scalar field, the group order n"},
	{"fiat-go/64/p384/p384.go", "P384", "fiat_p384.mk", "the P-384 base field, p = 2^384 - 2^128 - 2^96 + 2^32 - 1"},
	{"fiat-go/64/p384scalar/p384scalar.go", "P384n", "fiat_p384n.mk", "the P-384 scalar field, the group order n"},
}

// cr2FiatFuncs are the functions translated, in the order written.
var cr2FiatFuncs = []string{"cmovznzU64", "Mul", "Square", "Add", "Sub", "Opp", "FromMontgomery", "ToMontgomery", "Nonzero", "Selectznz", "ToBytes", "FromBytes", "SetOne"}

// cr2FiatName is the Mk name of a fiat function.
func cr2FiatName(prefix, name string) string {
	if name == "cmovznzU64" {
		return prefix + "Cmovznz"
	}
	return prefix + name
}

// cr2FiatGen is the translator's state over one function.
type cr2FiatGen struct {
	prefix  string
	limbs   int
	out     strings.Builder
	types   map[string]string // Mk type of each local and argument
	pending map[string]bool   // declared by var, not yet given a value
	result  string            // the out parameter's name
	resType string
	temp    int
}

// goType maps a fiat parameter type to Mk.
func (g *cr2FiatGen) goType(e ast.Expr) (string, bool) {
	switch t := e.(type) {
	case *ast.StarExpr:
		mt, _ := g.goType(t.X)
		return mt, true
	case *ast.Ident:
		switch t.Name {
		case "uint64", "uint1":
			return "u64", false
		case "uint8":
			return "u8", false
		case "MontgomeryDomainFieldElement", "NonMontgomeryDomainFieldElement":
			return fmt.Sprintf("[%d]u64", g.limbs), false
		}
	case *ast.ArrayType:
		n := t.Len.(*ast.BasicLit).Value
		el, _ := g.goType(t.Elt)
		return "[" + n + "]" + el, false
	}
	panic(fmt.Sprintf("fiat translator: type %T", e))
}

// expr translates e and returns its Mk text and type.
func (g *cr2FiatGen) expr(e ast.Expr) (string, string) {
	switch x := e.(type) {
	case *ast.BasicLit:
		return strings.ToLower(x.Value), ""
	case *ast.Ident:
		return x.Name, g.types[x.Name]
	case *ast.ParenExpr:
		s, t := g.expr(x.X)
		return "(" + s + ")", t
	case *ast.IndexExpr:
		s, t := g.expr(x.X)
		i, _ := g.expr(x.Index)
		el := t[strings.Index(t, "]")+1:]
		return s + "[" + i + "]", el
	case *ast.UnaryExpr:
		if x.Op != token.XOR {
			panic("fiat translator: unary " + x.Op.String())
		}
		s, t := g.expr(x.X)
		return "~" + s, t
	case *ast.BinaryExpr:
		a, ta := g.expr(x.X)
		b, tb := g.expr(x.Y)
		op := map[token.Token]string{token.ADD: "+%", token.SUB: "-%", token.MUL: "*%", token.AND: "&", token.OR: "|", token.XOR: "^", token.SHL: "<<", token.SHR: ">>"}[x.Op]
		if op == "" {
			panic("fiat translator: binary " + x.Op.String())
		}
		t := ta
		if t == "" && x.Op != token.SHL && x.Op != token.SHR {
			t = tb
		}
		return a + " " + op + " " + b, t
	case *ast.CallExpr:
		fn, ok := x.Fun.(*ast.Ident)
		if !ok || len(x.Args) != 1 {
			panic(fmt.Sprintf("fiat translator: call in an expression: %T", x.Fun))
		}
		s, t := g.expr(x.Args[0])
		switch fn.Name {
		case "uint64", "uint1":
			if t == "u8" {
				return "u64(" + s + ")", "u64"
			}
			return s, "u64"
		case "uint8":
			return "Trunc[u8](" + s + ")", "u8"
		}
		panic("fiat translator: conversion " + fn.Name)
	}
	panic(fmt.Sprintf("fiat translator: expression %T", e))
}

func (g *cr2FiatGen) line(format string, args ...any) {
	fmt.Fprintf(&g.out, "    "+format+"\n", args...)
}

// define gives name the value text, declaring it on its first value.
func (g *cr2FiatGen) define(name, text, typ string) {
	if name == "_" {
		return
	}
	if g.pending[name] {
		delete(g.pending, name)
		g.line("%s := %s", name, text)
		return
	}
	if _, ok := g.types[name]; ok {
		panic("fiat translator: a second value for " + name)
	}
	g.types[name] = typ
	g.line("%s := %s", name, text)
}

func (g *cr2FiatGen) stmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.DeclStmt:
		for _, sp := range x.Decl.(*ast.GenDecl).Specs {
			vs := sp.(*ast.ValueSpec)
			t, _ := g.goType(vs.Type)
			for _, n := range vs.Names {
				g.types[n.Name] = t
				g.pending[n.Name] = true
			}
		}
	case *ast.AssignStmt:
		if len(x.Lhs) == 2 {
			call := x.Rhs[0].(*ast.CallExpr)
			sel := call.Fun.(*ast.SelectorExpr)
			var args []string
			for _, a := range call.Args {
				s, _ := g.expr(a)
				args = append(args, s)
			}
			g.temp++
			tmp := fmt.Sprintf("w%d", g.temp)
			n0, n1 := x.Lhs[0].(*ast.Ident).Name, x.Lhs[1].(*ast.Ident).Name
			switch sel.Sel.Name {
			case "Mul64":
				g.line("%s := raw.Mul128(%s)", tmp, strings.Join(args, ", "))
				g.define(n0, tmp+".hi", "u64")
				g.define(n1, tmp+".lo", "u64")
			case "Add64":
				g.line("%s := FiatAdd64(%s)", tmp, strings.Join(args, ", "))
				g.define(n0, tmp+".s", "u64")
				g.define(n1, tmp+".c", "u64")
			case "Sub64":
				g.line("%s := FiatSub64(%s)", tmp, strings.Join(args, ", "))
				g.define(n0, tmp+".d", "u64")
				g.define(n1, tmp+".b", "u64")
			default:
				panic("fiat translator: bits." + sel.Sel.Name)
			}
			return
		}
		rhs, t := g.expr(x.Rhs[0])
		switch l := x.Lhs[0].(type) {
		case *ast.Ident:
			if x.Tok == token.DEFINE {
				g.define(l.Name, rhs, t)
			} else {
				g.define(l.Name, rhs, t)
			}
		case *ast.StarExpr:
			g.line("%s = %s", l.X.(*ast.Ident).Name, rhs)
		case *ast.IndexExpr:
			s, _ := g.expr(l)
			g.line("%s = %s", s, rhs)
		default:
			panic(fmt.Sprintf("fiat translator: assignment to %T", l))
		}
	case *ast.ExprStmt:
		call := x.X.(*ast.CallExpr)
		fn := call.Fun.(*ast.Ident).Name
		dst := call.Args[0].(*ast.UnaryExpr).X.(*ast.Ident).Name
		var args []string
		for _, a := range call.Args[1:] {
			s, _ := g.expr(a)
			args = append(args, s)
		}
		g.define(dst, cr2FiatName(g.prefix, fn)+"("+strings.Join(args, ", ")+")", "u64")
	default:
		panic(fmt.Sprintf("fiat translator: statement %T", s))
	}
}

// fn translates one function declaration.
func (g *cr2FiatGen) fn(d *ast.FuncDecl, doc string) string {
	g.out.Reset()
	g.types = map[string]string{}
	g.pending = map[string]bool{}
	g.temp = 0
	var params []string
	for i, f := range d.Type.Params.List {
		t, ptr := g.goType(f.Type)
		for _, n := range f.Names {
			if i == 0 {
				g.result, g.resType = n.Name, t
				continue
			}
			g.types[n.Name] = t
			if ptr {
				params = append(params, n.Name+": &"+t)
			} else {
				params = append(params, n.Name+": "+t)
			}
		}
	}
	g.types[g.result] = g.resType
	var head strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(doc), "\n") {
		head.WriteString(strings.TrimRight("// "+l, " ") + "\n")
	}
	fmt.Fprintf(&head, "%s(%s): %s {\n", cr2FiatName(g.prefix, d.Name.Name), strings.Join(params, ", "), g.resType)
	if strings.HasPrefix(g.resType, "[") {
		n := g.resType[1:strings.Index(g.resType, "]")]
		el := g.resType[strings.Index(g.resType, "]")+1:]
		fmt.Fprintf(&head, "    var %s: %s = [0; %s]\n", g.result, g.resType, n)
		_ = el
	} else {
		fmt.Fprintf(&head, "    var %s: %s = 0\n", g.result, g.resType)
	}
	for _, s := range d.Body.List {
		g.stmt(s)
	}
	if len(g.pending) > 0 {
		panic(fmt.Sprintf("fiat translator: %s leaves %v without a value", d.Name.Name, g.pending))
	}
	g.line("return %s", g.result)
	return head.String() + g.out.String() + "}\n"
}

// cr2FiatEvidence is Mk-evidence/fiat-crypto, or "" when absent.
func cr2FiatEvidence() string {
	root := os.Getenv("MK_EVIDENCE")
	if root == "" {
		root = filepath.Join("..", "..", "Mk-evidence")
	}
	dir := filepath.Join(root, "fiat-crypto")
	if _, err := os.Stat(filepath.Join(dir, "SHA256SUMS")); err != nil {
		return ""
	}
	return dir
}

// cr2FiatTranslate is the Mk file for f.
func cr2FiatTranslate(t *testing.T, dir string, f cr2Fiat) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.src)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sums), hex.EncodeToString(sum[:])+" *"+f.src) {
		t.Fatalf("%s: SHA-256 %x is not the pinned one in SHA256SUMS", f.src, sum)
	}
	commit := ""
	if src, err := os.ReadFile(filepath.Join(dir, "SOURCE.txt")); err == nil {
		if i := strings.Index(string(src), "commit "); i >= 0 {
			commit = strings.TrimRight(strings.Fields(string(src)[i+7:])[0], ",")
		}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, f.src, b, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	limbs := 4
	if strings.Contains(f.src, "p384") {
		limbs = 6
	}
	g := &cr2FiatGen{prefix: f.prefix, limbs: limbs}
	decls := map[string]*ast.FuncDecl{}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			decls[fd.Name.Name] = fd
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "// Code generated by std/std-crypto_fiatgen_test.go from Fiat Cryptography. DO NOT EDIT.\n")
	fmt.Fprintf(&out, "// The constant-time arithmetic of %s,\n// in the Montgomery domain over %d 64-bit limbs, translated statement by statement from\n", f.what, limbs)
	fmt.Fprintf(&out, "// %s of github.com/mit-plv/fiat-crypto (commit %s, SHA-256\n// %x). Fiat Cryptography is licensed under MIT, Apache-2.0 or BSD-1-Clause, at your option.\n// Copyright (c) 2015-2020 the fiat-crypto authors (see the AUTHORS file). SPDX-License-Identifier: MIT OR Apache-2.0 OR BSD-1-Clause.\n\nimport std.raw\n", f.src, commit, sum)
	for _, name := range cr2FiatFuncs {
		d := decls[name]
		if d == nil {
			t.Fatalf("%s: no function %s", f.src, name)
		}
		doc := ""
		if d.Doc != nil {
			doc = d.Doc.Text()
		}
		if strings.HasPrefix(doc, name+" ") {
			doc = cr2FiatName(f.prefix, name) + doc[len(name):]
		}
		out.WriteString("\n" + g.fn(d, doc))
	}
	return out.String()
}

// TestSTDCRYPTO_FiatTranslation: each std/crypto/fiat_<field>.mk is the
// translator's output for its pinned source (MK_FIATGEN=1 writes them):
//
//	MK_FIATGEN=1 go test ./std -run TestSTDCRYPTO_FiatTranslation
func TestSTDCRYPTO_FiatTranslation(t *testing.T) {
	dir := cr2FiatEvidence()
	if dir == "" {
		t.Skip("Mk-evidence/fiat-crypto not found (set MK_EVIDENCE)")
	}
	for _, f := range cr2Fiats {
		want := cr2FiatTranslate(t, dir, f)
		path := filepath.Join("crypto", f.file)
		if os.Getenv("MK_FIATGEN") == "1" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with MK_FIATGEN=1)", path, err)
		}
		if string(got) != want {
			t.Errorf("%s differs from the translation of %s: run with MK_FIATGEN=1 and read the diff", path, f.src)
		}
	}
}
