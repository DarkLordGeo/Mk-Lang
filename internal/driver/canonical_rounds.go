// Why: three of doc/style.md's mechanical rules are rewrites of statement
// shapes, each needing its own matcher; canonical.go runs them, this file
// finds their candidates.
//
// What: the rounds `value-arm` (valueArms), `for-in` (forIns) and
// `error-format` (errorFormats) of `mk fix --canonical`, with their
// helpers (assignsOther, assignedAfter, iteratorOnly, errorFormatCall,
// memberText and others).
//
// How: each matcher is narrow: it rewrites only the shape whose meaning it
// can see is unchanged, and the round keeps a rewrite only when the package
// still checks clean (canonical.go applyProven).

package driver

import (
	"strings"

	"mk/internal/ast"
	checker "mk/internal/check"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// valueArms is the value-arm round. A local declared with no value just before a
// statement `if` (with an `else` on every branch) or `match`, where every
// arm either ends by assigning that local with `=` or does not fall
// through (`return`, `fail`, `break`, `continue`), and the local is named
// nowhere else in the construct, becomes the binding of the construct as a
// value arm:
//
//	var kind: str            kind: str = match s {
//	match s {                    .A { "a" }
//	    .A { kind = "a" }  ->    .B { return "none" }
//	    .B { return "none" }  }
//	}
//
// The declared type is kept, so the arms convert to it as the assignments
// did. An arm that assigns anything else declared outside it, or a later
// statement that assigns the local, leaves the construct a statement
// (the style rule). `var` is kept when a later statement takes the local
// `mut` or calls a method on it.
func (s *Session) valueArms(res Result, ov *overlay) []fixCand {
	var out []fixCand
	s.unitBodies(ov, res, func(b *checker.Body, path, text string) {
		ast.Inspect(b.Node, func(n ast.Node) bool {
			blk, ok := n.(*ast.Block)
			if !ok || blk == nil {
				return n != nil
			}
			for i := 0; i+1 < len(blk.Stmts); i++ {
				bs, ok := blk.Stmts[i].(*ast.BindStmt)
				if !ok || bs.Value != nil || bs.Type == nil || bs.Name == nil {
					continue
				}
				construct := blk.Stmts[i+1]
				arms, ok := valueArmBlocks(construct)
				if !ok {
					continue
				}
				x := bs.Name.Name
				var assigns []*ast.AssignStmt
				good := true
				for _, a := range arms {
					if len(a.Stmts) == 0 {
						good = false
						break
					}
					switch last := a.Stmts[len(a.Stmts)-1].(type) {
					case *ast.AssignStmt:
						id, ok := last.Target.(*ast.Ident)
						if !ok || id.Name != x || last.Op != ast.TokAssign || last.Value == nil {
							good = false
						}
						assigns = append(assigns, last)
					case *ast.ReturnStmt, *ast.FailStmt, *ast.BreakStmt, *ast.ContinueStmt:
					default:
						good = false
					}
				}
				if !good || len(assigns) == 0 || namesOutside(construct, x, assigns) || assignsOther(arms, assigns) || assignedAfter(blk.Stmts[i+2:], x) {
					continue
				}
				bsp, csp := bs.Span(), construct.Span()
				if int(csp.End) > len(text) || bsp.End > csp.Start || !onlySpace(text[bsp.End:csp.Start]) || hasComment(text, assigns) {
					continue // a comment between them, or in an assignment, would be lost
				}
				head := text[bs.Name.Span().Start:bs.Name.Span().End] + ": " + text[bs.Type.Span().Start:bs.Type.Span().End] + " = "
				if bs.Var && mutatedAfter(blk.Stmts[i+2:], x) {
					head = "var " + head
				}
				edits := []diag.Edit{{Span: source.Span{File: bsp.File, Start: bsp.Start, End: csp.Start}, Text: head}}
				for _, as := range assigns {
					vs := as.Value.Span()
					edits = append(edits, diag.Edit{Span: as.Span(), Text: text[vs.Start:vs.End]})
				}
				out = append(out, fixCand{path: path, edits: edits,
					applied: AppliedFix{At: posText(res, bsp), Why: "a value chosen by a branch is a value arm"}})
			}
			return true
		})
	})
	sortCands(out)
	return out
}

// valueArmBlocks lists the arms of a statement `if` whose every branch
// ends in an `else` block, or of a `match`; false for any other statement.
func valueArmBlocks(st ast.Stmt) ([]*ast.Block, bool) {
	switch c := st.(type) {
	case *ast.IfStmt:
		var arms []*ast.Block
		for c != nil {
			if c.Then == nil {
				return nil, false
			}
			arms = append(arms, c.Then)
			switch e := c.Else.(type) {
			case *ast.Block:
				return append(arms, e), true
			case *ast.IfStmt:
				c = e
			default:
				return nil, false
			}
		}
	case *ast.MatchStmt:
		var arms []*ast.Block
		for _, a := range c.Arms {
			if a == nil || a.Body == nil {
				return nil, false
			}
			arms = append(arms, a.Body)
		}
		return arms, len(arms) > 0
	}
	return nil, false
}

// namesOutside reports whether n names x anywhere but as the target of one
// of the assignments: a read, another write, a pattern or a parameter
// binding of the same name. A field name `p.x` is not a use of x.
func namesOutside(n ast.Node, x source.Symbol, targets []*ast.AssignStmt) bool {
	skip := map[*ast.Ident]bool{}
	for _, a := range targets {
		if id, ok := a.Target.(*ast.Ident); ok {
			skip[id] = true
		}
	}
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if m == nil || found {
			return false
		}
		switch m := m.(type) {
		case *ast.FieldExpr:
			if m.Name != nil {
				skip[m.Name] = true
			}
		case *ast.Ident:
			if !skip[m] && m.Name == x {
				found = true
			}
		}
		return !found
	})
	return found
}

// assignsOther reports whether an arm assigns anything but the one local
// (the style rule: "every arm assigns only that local"; arms that assign
// several locals stay statements): an assignment, other than the arm's last,
// whose target's root is no local the arm itself declares with a binding.
func assignsOther(arms []*ast.Block, last []*ast.AssignStmt) bool {
	skip := map[*ast.AssignStmt]bool{}
	for _, a := range last {
		skip[a] = true
	}
	for _, arm := range arms {
		own := map[source.Symbol]bool{}
		found := false
		ast.Inspect(arm, func(m ast.Node) bool {
			if m == nil || found {
				return false
			}
			switch m := m.(type) {
			case *ast.BindStmt:
				if m.Name != nil {
					own[m.Name.Name] = true
				}
			case *ast.DestructureStmt:
				if m.Pat != nil {
					for _, f := range m.Pat.Fields {
						if f != nil && f.Name != nil {
							own[f.Name.Name] = true
						}
					}
				}
			case *ast.AssignStmt:
				if !skip[m] {
					root, ok := placeRoot(m.Target)
					found = !ok || !own[root]
				}
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// placeRoot is the name a place starts at (x for x.f[i]), false for a
// place that starts at no name (a dereference, a call's result).
func placeRoot(e ast.Expr) (source.Symbol, bool) {
	for {
		switch v := e.(type) {
		case *ast.Ident:
			return v.Name, true
		case *ast.FieldExpr:
			e = v.X
		case *ast.IndexExpr:
			e = v.X
		case *ast.ParenExpr:
			e = v.X
		default:
			return 0, false
		}
	}
}

// assignedAfter reports whether a statement of rest assigns x (the style
// rule: "nothing else assigns it").
func assignedAfter(rest []ast.Stmt, x source.Symbol) bool {
	found := false
	for _, st := range rest {
		ast.Inspect(st, func(m ast.Node) bool {
			if m == nil || found {
				return false
			}
			if a, ok := m.(*ast.AssignStmt); ok {
				found = rootIs(a.Target, x)
			}
			return !found
		})
	}
	return found
}

// mutatedAfter reports whether a statement of rest may change x: an
// assignment whose target starts at x, a `mut x`, or a method call on x
// (its receiver may be `mut self`).
func mutatedAfter(rest []ast.Stmt, x source.Symbol) bool {
	found := false
	for _, st := range rest {
		ast.Inspect(st, func(m ast.Node) bool {
			if m == nil || found {
				return false
			}
			switch m := m.(type) {
			case *ast.AssignStmt:
				found = rootIs(m.Target, x)
			case *ast.UnaryExpr:
				found = m.Op == ast.TokMut && rootIs(m.X, x)
			case *ast.CallExpr:
				if fe, ok := m.Fun.(*ast.FieldExpr); ok {
					found = rootIs(fe.X, x)
				}
			}
			return !found
		})
	}
	return found
}

// rootIs reports whether place e is x or a field, index or slice of it.
func rootIs(e ast.Expr, x source.Symbol) bool {
	for {
		switch v := e.(type) {
		case *ast.Ident:
			return v.Name == x
		case *ast.FieldExpr:
			e = v.X
		case *ast.IndexExpr:
			e = v.X
		case *ast.ParenExpr:
			e = v.X
		default:
			return false
		}
	}
}

// forIns is the for-in round: `var it := X` directly followed by `for e :=
// it.Next() { ... }`, with `it` named nowhere else in the rest of the
// block, becomes `for e in X { ... }` (the iterator loop). The recheck
// proves X has the iterator shape `for in` needs.
func (s *Session) forIns(res Result, ov *overlay) []fixCand {
	if s.w == nil || s.w.in == nil {
		return nil
	}
	next, ok := s.w.in.Lookup("Next")
	if !ok {
		return nil
	}
	var out []fixCand
	s.unitBodies(ov, res, func(b *checker.Body, path, text string) {
		ast.Inspect(b.Node, func(n ast.Node) bool {
			blk, ok := n.(*ast.Block)
			if !ok || blk == nil {
				return n != nil
			}
			for i := 0; i+1 < len(blk.Stmts); i++ {
				bs, ok := blk.Stmts[i].(*ast.BindStmt)
				if !ok || !bs.Var || bs.Type != nil || bs.Value == nil || bs.Name == nil {
					continue
				}
				// X must be a call: a place (`var it := saved`) would be
				// iterated in place by `for e in saved`, and `mut` or `&`
				// before it changes how `for in` reads it.
				if _, call := bs.Value.(*ast.CallExpr); !call {
					continue
				}
				fs, ok := blk.Stmts[i+1].(*ast.ForStmt)
				if !ok || fs.Body == nil {
					continue
				}
				bc, ok := fs.Head.(*ast.BindCond)
				if !ok || bc.Name == nil {
					continue
				}
				call, ok := bc.Value.(*ast.CallExpr)
				if !ok || len(call.Args) != 0 {
					continue
				}
				fe, ok := call.Fun.(*ast.FieldExpr)
				if !ok || fe.Name == nil || fe.Name.Name != next {
					continue
				}
				recv, ok := fe.X.(*ast.Ident)
				if !ok || recv.Name != bs.Name.Name || namedIn(blk.Stmts[i+1:], bs.Name.Name) != 1 {
					continue
				}
				// `for e in X` iterates X as a slice when X is an
				// array, a slice, a str or a Slice[T] implementer, whatever
				// its Next does; only an X that for in takes as an
				// iterator calls the same Next.
				if !s.iteratorOnly(b, bs.Value) {
					continue
				}
				bsp, fsp, hsp, vs := bs.Span(), fs.Span(), bc.Span(), bs.Value.Span()
				start := fsp.Start
				if fs.Label != nil {
					start = min(start, fs.Label.Span().Start)
				}
				if bsp.End > start || int(hsp.End) > len(text) || int(vs.End) > len(text) || !onlySpace(text[bsp.End:start]) {
					continue // a comment between them would be lost
				}
				name := text[bc.Name.Span().Start:bc.Name.Span().End]
				out = append(out, fixCand{path: path, edits: []diag.Edit{
					{Span: source.Span{File: bsp.File, Start: bsp.Start, End: start}},
					{Span: hsp, Text: name + " in " + text[vs.Start:vs.End]},
				}, applied: AppliedFix{At: posText(res, bsp), Why: "a loop over an iterator is for in"}})
			}
			return true
		})
	})
	sortCands(out)
	return out
}

// iteratorOnly reports whether `for e in x` would take x as an iterator,
// calling its Next as `for e := it.Next()` does: x's type, references
// removed, is known and is not an array, a slice, a str or a Slice[T]
// implementer (check/iterfor.go iterHead). An unknown type is no.
func (s *Session) iteratorOnly(b *checker.Body, x ast.Expr) bool {
	if s.w == nil || s.w.sem5 == nil || s.w.sem5.chk == nil || s.w.tab == nil {
		return false
	}
	i := int(x.ID()) - int(b.Base)
	if i < 0 || i >= len(b.Types) {
		return false
	}
	t := b.Types[i]
	for k := 0; k < 8 && t != types.NoType && t != types.Poison; k++ {
		if k := s.w.tab.Kind(t); k != types.KRef && k != types.KMut {
			break
		}
		t = s.w.tab.Get(t).Elem
	}
	if t == types.NoType || t == types.Poison {
		return false
	}
	switch s.w.tab.Kind(t) {
	case types.KSlice, types.KMutSlice, types.KArray, types.KStr, types.KPoison, types.KRef, types.KMut:
		return false
	case types.KNamed:
		// A generic body's type may mention its parameters: Facts answers
		// only closed types, FactsIn any type of the unit.
		chk := s.w.sem5.chk
		env := chk.NewEnv(append(append([]types.GenericParam(nil), b.Generics...), b.Hidden...))
		return !chk.FactsIn(env, t).SliceImpl
	}
	return true
}

// namedIn counts the identifiers named x in sts (field names excluded).
func namedIn(sts []ast.Stmt, x source.Symbol) int {
	n := 0
	field := map[*ast.Ident]bool{}
	for _, st := range sts {
		ast.Inspect(st, func(m ast.Node) bool {
			switch m := m.(type) {
			case nil:
				return false
			case *ast.FieldExpr:
				if m.Name != nil {
					field[m.Name] = true
				}
			case *ast.Ident:
				if !field[m] && m.Name == x {
					n++
				}
			}
			return true
		})
	}
	return n
}

// errorFormats is the error-format round: a statement `match e` over an error or an
// error set whose every arm is one call printing exactly what the error's
// provided Format writes (the name, then `(f: {f}, ...)` over every
// field in declaration order), with the same callee, the same arguments
// before the text, and the same text before and after the member in every
// arm, becomes one call with the hole `{e}` (the error's provided Format):
//
//	match e {                                      try Print(w, "error: {e}\n", (e))
//	    NotFound(id) {                       ->
//	        try Print(w, "error: NotFound(id: {id})\n", (id))
//	    }
//	    Denied { try Print(w, "error: Denied\n") }
//	}
//
// e must be a name; an arm with a wildcard, an alternative, a nested
// pattern or anything but the one call leaves the match as written.
func (s *Session) errorFormats(res Result, ov *overlay) []fixCand {
	if s.w == nil || s.w.items == nil || s.w.in == nil {
		return nil
	}
	var out []fixCand
	s.unitBodies(ov, res, func(b *checker.Body, path, text string) {
		ast.Inspect(b.Node, func(n ast.Node) bool {
			blk, ok := n.(*ast.Block)
			if !ok || blk == nil {
				return n != nil
			}
			for _, st := range blk.Stmts {
				m, ok := st.(*ast.MatchStmt)
				if !ok {
					continue
				}
				if sp := m.Span(); int(sp.End) <= len(text) && strings.Contains(text[sp.Start:sp.End], "//") {
					continue // the match's comments would be lost
				}
				if call, ok := s.errorFormatCall(b, m, text); ok {
					out = append(out, fixCand{path: path, edits: []diag.Edit{{Span: m.Span(), Text: call}},
						applied: AppliedFix{At: posText(res, m.Span()), Why: "an error prints itself with {e}"}})
				}
			}
			return true
		})
	})
	sortCands(out)
	return out
}

// errorFormatCall is the one call that replaces m, when m has the round's shape.
func (s *Session) errorFormatCall(b *checker.Body, m *ast.MatchStmt, text string) (string, bool) {
	x, ok := m.X.(*ast.Ident)
	if !ok || len(m.Arms) == 0 || int(m.Span().End) > len(text) {
		return "", false
	}
	name := s.w.in.Str(x.Name)
	var callee, lead, pre, suf string
	for k, a := range m.Arms {
		// A member with fields is a PathPattern; a payload-free one, a
		// NamePattern the checker resolved to the error.
		pp, ok := a.Pat.(*ast.PathPattern)
		if np, isName := a.Pat.(*ast.NamePattern); isName && np.Name != nil {
			pp, ok = &ast.PathPattern{Base: np.Base, Path: &ast.Path{Names: []*ast.Ident{np.Name}}}, true
		}
		if !ok || pp.Path == nil || len(pp.Path.Names) == 0 || a.Body == nil || len(a.Body.Stmts) != 1 {
			return "", false
		}
		i := int(pp.ID()) - int(b.Base)
		if i < 0 || i >= len(b.Uses) || b.Uses[i].Kind != checker.UseError {
			return "", false
		}
		ed, ok := s.w.items.Get(b.Uses[i].Item).Decl.(*types.ErrorDecl)
		if !ok {
			return "", false
		}
		member, holes, ok := s.memberText(pp, ed)
		if !ok {
			return "", false
		}
		es, ok := a.Body.Stmts[0].(*ast.ExprStmt)
		if !ok {
			return "", false
		}
		c, prefix := es.X, ""
		if u, ok := c.(*ast.UnaryExpr); ok && u.Op == ast.TokTry {
			c, prefix = u.X, "try "
		}
		call, ok := c.(*ast.CallExpr)
		if !ok {
			return "", false
		}
		// The text argument: the first string literal; one record after it
		// at most, naming exactly the holes.
		at := -1
		for j, arg := range call.Args {
			if lit, ok := arg.Value.(*ast.BasicLit); ok && lit.Kind == ast.TokStr && arg.Name == nil {
				at = j
				break
			}
		}
		if at < 0 || len(call.Args) > at+2 {
			return "", false
		}
		lit := call.Args[at].Value.(*ast.BasicLit)
		raw := lit.Raw
		if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
			return "", false
		}
		body := raw[1 : len(raw)-1]
		j := strings.Index(body, member)
		if j < 0 || strings.ContainsAny(body[:j]+body[j+len(member):], "{}") {
			return "", false
		}
		if !recordNames(call.Args[at+1:], holes, s.w.in) {
			return "", false
		}
		fs := call.Fun.Span()
		armCallee := prefix + text[fs.Start:fs.End]
		armLead := text[call.Args[0].Span().Start:call.Args[at].Span().Start]
		if k == 0 {
			callee, lead, pre, suf = armCallee, armLead, body[:j], body[j+len(member):]
			continue
		}
		if armCallee != callee || armLead != lead || body[:j] != pre || body[j+len(member):] != suf {
			return "", false
		}
	}
	return callee + "(" + lead + `"` + pre + "{" + name + "}" + suf + `", (` + name + "))", true
}

// memberText is what the provided Format prints for the member pp matches, with the
// pattern's bindings as holes in the error's declaration order, and the
// binding names; false when the pattern does not bind every field to a
// local of its own name.
func (s *Session) memberText(pp *ast.PathPattern, ed *types.ErrorDecl) (string, []string, bool) {
	name := s.w.in.Str(pp.Path.Names[len(pp.Path.Names)-1].Name)
	if len(ed.Fields) == 0 {
		return name, nil, !pp.HasFields || len(pp.Fields) == 0
	}
	if len(pp.Fields) != len(ed.Fields) {
		return "", nil, false
	}
	bound := map[source.Symbol]bool{}
	for _, f := range pp.Fields {
		if f == nil || f.Name == nil || f.Pat != nil {
			return "", nil, false
		}
		bound[f.Name.Name] = true
	}
	var parts, holes []string
	for _, f := range ed.Fields {
		if !bound[f.Name] {
			return "", nil, false
		}
		n := s.w.in.Str(f.Name)
		parts = append(parts, n+": {"+n+"}")
		holes = append(holes, n)
	}
	return name + "(" + strings.Join(parts, ", ") + ")", holes, true
}

// recordNames reports whether rest (the arguments after the text) is no
// argument or `()` when holes is empty, else one record literal of bare
// names equal to holes in order.
func recordNames(rest []*ast.Arg, holes []string, in *source.Interner) bool {
	if len(rest) == 0 {
		return len(holes) == 0
	}
	r, ok := rest[0].Value.(*ast.RecordLit)
	if !ok || rest[0].Name != nil || len(r.Fields) != len(holes) {
		return false
	}
	for i, f := range r.Fields {
		if f == nil || f.Name == nil || f.Value != nil || in.Str(f.Name.Name) != holes[i] {
			return false
		}
	}
	return true
}

// onlySpace reports whether s is blanks and line breaks only.
func onlySpace(s string) bool {
	return strings.TrimSpace(s) == ""
}

// hasComment reports whether an assignment's text holds a comment.
func hasComment(text string, as []*ast.AssignStmt) bool {
	for _, a := range as {
		sp := a.Span()
		if int(sp.End) <= len(text) && strings.Contains(text[sp.Start:sp.End], "//") {
			return true
		}
	}
	return false
}
