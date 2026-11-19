// Why: with mk build --debug a debugger must show a function's parameters
// and locals by name, each visible only in its own source scope, but MIR
// keeps no block nesting.
//
// What: the function half of full debug info: fullSubprogram,
// declareLocals, scopes, and the dominator and scope analyses
// (reversePostorder, predecessors, dominators, inScope). Only with
// Config.Debug; the type half is dityp.go.
//
// How: a typed DISubprogram in the namespace of its module, one
// DILocalVariable and llvm.dbg.declare per named local on its entry-block
// alloca (clang's -O0 scheme, LLVM "Source Level Debugging with LLVM"), and
// lexical scopes from the MIR dominator tree (Cooper, Harvey and Kennedy,
// "A Simple, Fast Dominance Algorithm", 2001).
//
// Scopes. MIR keeps no block nesting, but a local cannot shadow a local
// (E0206), so only siblings share a name. A local's home is the nearest
// common dominator of every block that names it, and each home block gets
// a lexical block. Dominance alone is not source nesting: a for {} loop
// left only by break has its exit dominated by its body. So a block's
// scope is the lexical block of its nearest dominator that is the home of
// a local still in scope in the block (inScope: from where it is named to
// its StScopeEnd, which lowering emits on every edge out of its source
// scope), and a home's lexical block sits in the scope so found for the
// home block; the subprogram when there is none. The lexical blocks then
// nest like source scopes, and two sibling locals of one name are visible
// in disjoint code, the loop case included.

package codegen

import (
	"strings"

	"mk/internal/llvm"
	"mk/internal/mir"
)

// fullSubprogram attaches the function's typed DISubprogram and computes
// its locals' homes and its blocks' scopes.
func (fg *fnGen) fullSubprogram() {
	g, f := fg.g, fg.f
	mt := g.m.Metadata()
	path, line, _ := g.lineCol(bodySpan(f))
	file := g.diFile(path)
	name, ns := g.debugName(f)
	// The subroutine type: the result (null for void), then the declared
	// parameters (hidden environment parameters are not part of it).
	b := fg.b
	nparams := fg.declaredParams()
	ps := make([]llvm.MD, nparams)
	for i := range ps {
		ps[i] = g.diType(b.Locals[i+1].Type)
	}
	sub := mt.DISubroutineTypeOf(g.diType(b.Locals[mir.ReturnLocal].Type), ps...)
	fg.dbgSP = mt.DISubprogramFull(llvm.SubprogramInfo{Name: name, LinkageName: fg.fn.Name(), Scope: ns,
		File: file, Line: line, Type: sub, Unit: g.dbgCU, Optimized: g.cfg.Profile.Optimized()})
	fg.fn.SetDbg(fg.dbgSP)
	fg.scopes(file)
}

// declaredParams is the number of the signature's parameters: ParamCount
// less the hidden environment parameters.
func (fg *fnGen) declaredParams() int {
	n := int(fg.b.ParamCount)
	for n > 0 && mir.IsEnvType(fg.g.tt, fg.b.Locals[n].Type) {
		n--
	}
	return n
}

// debugName is the function's name inside its module (Work, File.Close,
// an instance's arguments as its key spells them) and the namespace of the
// module path (main; std then fs). A key outside its module's prefix keeps
// its whole text at the top.
func (g *packGen) debugName(f *Func) (string, llvm.MD) {
	it := g.items.Get(f.Item)
	if it == nil {
		return f.Key, llvm.MD{}
	}
	mod := g.items.Module(it.Module)
	if mod == nil || mod.Path == "" || !strings.HasPrefix(f.Key, mod.Path+".") {
		return f.Key, llvm.MD{}
	}
	name := strings.ReplaceAll(f.Key[len(mod.Path)+1:], ":", ".")
	return name, g.namespace(mod.Path)
}

// namespace is the DINamespace chain of a module path, one per pack per
// path.
func (g *packGen) namespace(path string) llvm.MD {
	if md, ok := g.dbgNS[path]; ok {
		return md
	}
	var parent llvm.MD
	last := path
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		parent, last = g.namespace(path[:i]), path[i+1:]
	}
	md := g.m.Metadata().DINamespace(last, parent)
	g.dbgNS[path] = md
	return md
}

// named reports whether local li gets a variable record: a declared
// parameter, a user local or a scope's s, with a name.
func (fg *fnGen) named(li int, nparams int) bool {
	l := &fg.b.Locals[li]
	if !l.Name.IsValid() {
		return false
	}
	switch l.Kind {
	case mir.LocalParam:
		return li >= 1 && li <= nparams
	case mir.LocalUser, mir.LocalScope:
		return true
	}
	return false
}

// scopes computes fg.dbgHome and fg.dbgScopes (the file comment).
func (fg *fnGen) scopes(file llvm.MD) {
	b := fg.b
	n := len(b.Blocks)
	fg.dbgHome = make([]int, len(b.Locals))
	fg.dbgScopes = make([]llvm.MD, n)
	if n == 0 {
		return
	}
	rpo, order := reversePostorder(b)
	preds := predecessors(b, rpo, order)
	idom := dominators(rpo, order, preds)
	nparams := fg.declaredParams()
	want := make([]bool, len(b.Locals))
	for li := range b.Locals {
		want[li] = fg.named(li, nparams) && li > nparams
		fg.dbgHome[li] = -1
	}
	visit := func(l mir.LocalID, bi int) {
		if int(l) >= len(want) || !want[l] || order[bi] < 0 {
			return
		}
		if h := fg.dbgHome[l]; h < 0 {
			fg.dbgHome[l] = bi
		} else {
			fg.dbgHome[l] = intersect(idom, order, h, bi)
		}
	}
	for bi := range b.Blocks {
		forEachLocal(b, bi, func(l mir.LocalID) { visit(l, bi) })
	}
	// The homed locals, densely numbered for the in-scope sets, and each
	// home block's set of its locals.
	bit := make([]int, len(b.Locals))
	var homed []mir.LocalID
	for li, h := range fg.dbgHome {
		bit[li] = -1
		if h < 0 {
			fg.dbgHome[li] = 0 // never named in reachable code, or a parameter
			continue
		}
		bit[li] = len(homed)
		homed = append(homed, mir.LocalID(li))
	}
	words := (len(homed) + 63) / 64
	homeSet := make([][]uint64, n)
	for k, l := range homed {
		h := fg.dbgHome[l]
		if homeSet[h] == nil {
			homeSet[h] = make([]uint64, words)
		}
		homeSet[h][k/64] |= 1 << (k % 64)
	}
	vis := inScope(b, rpo, preds, bit, words)
	// enclosing is the scope of the innermost home h at or above from on
	// the dominator tree one of whose locals is in scope in block bi (the
	// subprogram at the entry block).
	enclosing := func(bi, from int) llvm.MD {
		for h := from; ; h = idom[h] {
			if h == 0 {
				return fg.dbgSP
			}
			if hs := homeSet[h]; hs != nil && intersects(vis[bi], hs) {
				return fg.dbgScopes[h]
			}
		}
	}
	mt := fg.g.m.Metadata()
	for _, bi := range rpo {
		switch {
		case bi == 0:
			fg.dbgScopes[bi] = fg.dbgSP
		case homeSet[bi] != nil:
			_, line, col := fg.g.lineCol(fg.spanOf(firstSpan(b, bi)))
			fg.dbgScopes[bi] = mt.DILexicalBlock(enclosing(bi, idom[bi]), file, line, col)
		default:
			fg.dbgScopes[bi] = enclosing(bi, idom[bi])
		}
	}
	for bi := range fg.dbgScopes {
		if fg.dbgScopes[bi].IsZero() { // unreachable
			fg.dbgScopes[bi] = fg.dbgSP
		}
	}
}

// firstSpan is the span of block bi's first statement, or of its
// terminator.
func firstSpan(b *mir.Body, bi int) mir.SpanID {
	blk := &b.Blocks[bi]
	if len(blk.Stmts) > 0 {
		return blk.Stmts[0].Span
	}
	return blk.Term.Span
}

// declareLocals emits one llvm.dbg.declare per named local with storage,
// in local order, in the entry block after the allocas.
func (fg *fnGen) declareLocals() {
	g, b := fg.g, fg.b
	mt := g.m.Metadata()
	nparams := fg.declaredParams()
	for li := range b.Locals {
		if !fg.named(li, nparams) {
			continue
		}
		l := &b.Locals[li]
		if g.reprOf(l.Type).kind == rZero || (fg.promoted != nil && fg.promoted[li] != 0) {
			continue
		}
		a := fg.locals[li].v
		if a.IsZero() || !a.Ty.IsPtr() {
			continue
		}
		scope := fg.dbgSP
		arg := 0
		if l.Kind == mir.LocalParam {
			arg = li
		} else if h := fg.dbgHome[li]; h >= 0 && h < len(fg.dbgScopes) {
			scope = fg.dbgScopes[h]
		}
		path, line, col := g.lineCol(fg.spanOf(l.Span))
		file := g.diFile(path)
		v := mt.DILocalVariable(g.tt.Interner().Str(l.Name), arg, scope, file, line, g.diType(l.Type))
		fg.entry.DbgDeclare(a, v, mt.DILocation(line, col, scope))
	}
}

// reversePostorder is the blocks reachable from block 0 in reverse
// postorder, and each block's index in it (-1 when unreachable).
func reversePostorder(b *mir.Body) ([]int, []int) {
	n := len(b.Blocks)
	order := make([]int, n)
	for i := range order {
		order[i] = -1
	}
	seen := make([]bool, n)
	var post []int
	type frame struct {
		bi   int
		succ []mir.BlockID
		k    int
	}
	stack := []frame{{bi: 0, succ: b.Successors(0, nil)}}
	seen[0] = true
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.k < len(top.succ) {
			s := int(top.succ[top.k])
			top.k++
			if s >= 0 && s < n && !seen[s] {
				seen[s] = true
				stack = append(stack, frame{bi: s, succ: b.Successors(mir.BlockID(s), nil)})
			}
			continue
		}
		post = append(post, top.bi)
		stack = stack[:len(stack)-1]
	}
	rpo := make([]int, len(post))
	for i, bi := range post {
		rpo[len(post)-1-i] = bi
	}
	for i, bi := range rpo {
		order[bi] = i
	}
	return rpo, order
}

// predecessors is each block's reachable predecessors.
func predecessors(b *mir.Body, rpo, order []int) [][]int {
	n := len(b.Blocks)
	preds := make([][]int, n)
	var buf []mir.BlockID
	for _, bi := range rpo {
		buf = b.Successors(mir.BlockID(bi), buf[:0])
		for _, s := range buf {
			if int(s) < n && order[s] >= 0 {
				preds[s] = append(preds[s], bi)
			}
		}
	}
	return preds
}

// inScope is, per block, the set of homed locals (bit[l], -1 for an
// untracked local) that are in scope somewhere in it: in scope at its
// entry, or named by it. A local enters scope where a block names it and
// leaves at its StScopeEnd, which lowering puts on every edge out of the
// local's source scope (fall-through, break, continue, return); in scope
// at a block's entry is the union over its predecessors (a forward
// may-analysis, iterated over reverse postorder to its fixpoint). So a
// loop's exit block, dominated by the loop body when the loop is left only
// by break, is not in the body's scope.
func inScope(b *mir.Body, rpo []int, preds [][]int, bit []int, words int) [][]uint64 {
	n := len(b.Blocks)
	out := make([][]uint64, n)
	vis := make([][]uint64, n)
	for _, bi := range rpo {
		out[bi] = make([]uint64, words)
		vis[bi] = make([]uint64, words)
	}
	cur := make([]uint64, words)
	set := func(s []uint64, l mir.LocalID) {
		if int(l) < len(bit) && bit[l] >= 0 {
			s[bit[l]/64] |= 1 << (bit[l] % 64)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, bi := range rpo {
			clear(cur)
			for _, p := range preds[bi] {
				for w := range cur {
					cur[w] |= out[p][w]
				}
			}
			v := vis[bi]
			copy(v, cur)
			blk := &b.Blocks[bi]
			for si := range blk.Stmts {
				s := &blk.Stmts[si]
				if s.Kind == mir.StScopeEnd {
					set(v, s.Local)
					if int(s.Local) < len(bit) && bit[s.Local] >= 0 {
						cur[bit[s.Local]/64] &^= 1 << (bit[s.Local] % 64)
					}
					continue
				}
				stmtLocals(b, s, func(l mir.LocalID) { set(cur, l); set(v, l) })
			}
			termLocals(b, &blk.Term, func(l mir.LocalID) { set(cur, l); set(v, l) })
			for w := range cur {
				if out[bi][w] != cur[w] {
					out[bi][w] = cur[w]
					changed = true
				}
			}
		}
	}
	return vis
}

// intersects reports whether two sets share a member.
func intersects(a, b []uint64) bool {
	for w := range a {
		if a[w]&b[w] != 0 {
			return true
		}
	}
	return false
}

// dominators is the immediate dominator of each reachable block (block 0
// is its own), by Cooper, Harvey and Kennedy's iteration over reverse
// postorder; -1 for an unreachable block.
func dominators(rpo, order []int, preds [][]int) []int {
	n := len(order)
	idom := make([]int, n)
	for i := range idom {
		idom[i] = -1
	}
	idom[0] = 0
	for changed := true; changed; {
		changed = false
		for _, bi := range rpo[1:] {
			nd := -1
			for _, p := range preds[bi] {
				if idom[p] < 0 {
					continue
				}
				if nd < 0 {
					nd = p
				} else {
					nd = intersect(idom, order, p, nd)
				}
			}
			if nd >= 0 && idom[bi] != nd {
				idom[bi] = nd
				changed = true
			}
		}
	}
	return idom
}

// intersect is the nearest common dominator of blocks a and b.
func intersect(idom, order []int, a, b int) int {
	for a != b {
		for order[a] > order[b] {
			a = idom[a]
		}
		for order[b] > order[a] {
			b = idom[b]
		}
	}
	return a
}

// forEachLocal calls fn for every local block bi names: assigned places,
// read operands, borrowed places, index locals of projections, scope ends,
// joins, and its terminator's operands and destinations.
func forEachLocal(b *mir.Body, bi int, fn func(mir.LocalID)) {
	blk := &b.Blocks[bi]
	for si := range blk.Stmts {
		stmtLocals(b, &blk.Stmts[si], fn)
	}
	termLocals(b, &blk.Term, fn)
}

// placeLocals calls fn for a place's local and its index locals.
func placeLocals(b *mir.Body, p mir.Place, fn func(mir.LocalID)) {
	fn(p.Local)
	for _, pr := range b.Projections[p.Proj.Start:p.Proj.End()] {
		if pr.Kind == mir.ProjIndex {
			fn(mir.LocalID(pr.A))
		}
	}
}

// operandLocals calls fn for the locals a copy or move operand names.
func operandLocals(b *mir.Body, o mir.Operand, fn func(mir.LocalID)) {
	if o.Kind == mir.OpCopy || o.Kind == mir.OpMove {
		placeLocals(b, o.Place, fn)
	}
}

// stmtLocals calls fn for every local statement s names.
func stmtLocals(b *mir.Body, s *mir.Statement, fn func(mir.LocalID)) {
	switch s.Kind {
	case mir.StAssign:
		placeLocals(b, s.Place, fn)
		rv := &s.Rvalue
		placeLocals(b, rv.Place, fn) // the zero Place names local 0, never a variable
		operandLocals(b, rv.A, fn)
		operandLocals(b, rv.B, fn)
		if rv.Ops.Len > 0 {
			for _, o := range b.Operands[rv.Ops.Start:rv.Ops.End()] {
				operandLocals(b, o, fn)
			}
		}
	case mir.StScopeEnd, mir.StJoin:
		fn(s.Local)
	}
}

// termLocals calls fn for every local terminator t names.
func termLocals(b *mir.Body, t *mir.Terminator, fn func(mir.LocalID)) {
	switch t.Kind {
	case mir.TermSwitch:
		operandLocals(b, t.On, fn)
	case mir.TermCall, mir.TermTailCall:
		placeLocals(b, t.Dest, fn)
		operandLocals(b, t.Callee.Value, fn)
		for _, a := range b.Args[t.Args.Start:t.Args.End()] {
			operandLocals(b, a.Operand, fn)
		}
		if t.HasErr {
			fn(t.Err.Error)
		}
	case mir.TermReturn:
		fn(mir.ReturnLocal)
	}
}
