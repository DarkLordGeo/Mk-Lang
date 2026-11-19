// Why: a check inside a small leaf function cannot be proved on its own: its
// arguments' ranges are known only at the call. spectralnorm's A(i, j), inside
// MulAv's two loops, gets i and j in [0, len) only there. Inlining the leaf
// and running the range analysis again proves its sums.
//
// What: InlineLeaves, which replaces each direct call of a small leaf in a
// release build's codegen body by the leaf's statements and reruns the range
// pass; callLeaf and leafOK decide what may be inlined, splice does it.
//
// How: it runs on the instance bodies the driver hands codegen (its
// codegenBody, after mono and every evaluation, before VersionLoops).
//   - The site is codegen's only: InlineLeaves returns a new body, so no
//     body is evaluated (the evaluator's step count), borrow judged (its
//     diagnostics) or kept by a cache with the change. The range pass's
//     rewrite and bounds fold on the new body are sound by its post-fixpoint,
//     as in Lower; the fold needs no borrow verdict on a body nothing judges.
//   - The driver offers only a callee of the caller's own pack, so a pack's
//     IR still reads no body outside its members: a key over the members'
//     content covers the inlined code.
//   - The result uses existing MIR forms only: the call's block gets the
//     argument copies, the leaf's statements on fresh LocalTemp locals and
//     the result copy, and its terminator becomes a goto to the call's
//     target. No block is added.
//
// A leaf (leafOK; callLeaf adds: in the caller's file, called directly with
// copied scalar arguments) is a body of one block that ends in a normal
// return, whose locals are all integers, bool, char or floats, whose
// statements are assignments of a whole local from a use, a unary or binary
// operator or an integer or float cast of whole locals, immediates and
// consts (and scope ends and nops, which count toward the limit), at most
// leafMaxStmts of them, with at least one checked integer + - * or checked
// cast the second pass could prove. A trap inside the leaf stays at its own
// statement's span, so a panic names the site it names out of line; the
// callee's spans are appended to the caller's table (spans are absolute:
// file and offsets).

package lower

import (
	"slices"

	"mk/internal/mir"
	"mk/internal/types"
)

// Limits of the inliner: a leaf's statements, the statements one caller
// may receive, and the caller's blocks.
const (
	leafMaxStmts   = 32
	inlineMaxStmts = 256
	inlineMaxBlock = 1 << 14
)

// inlineMut is the inliner's test-only fault (TestPI_MutantsCaught):
// zero in every build.
type inlineMut uint8

const (
	imutNone     inlineMut = iota
	imutArgSwap            // a leaf's first two parameters get each other's argument
	imutRetParam           // the call's destination gets the leaf's first parameter, not its result
)

// InlineLeaves is b with every call InlineLeaves may inline replaced by the
// leaf's statements and the range pass run again, or b itself when
// no call was inlined or the pass is off. leaf names the callee's instance
// body for a non-generic direct callee the caller may inline (the driver's
// conditions: a function of the caller's pack with a body, not naked, no
// cpu clause), else nil. b is never changed.
func InlineLeaves(cfg *Config, b *mir.Body, leaf func(types.ItemID) *mir.Body) *mir.Body {
	nb, _ := inlineLeaves(cfg, b, leaf)
	return nb
}

// inlineLeaves is InlineLeaves and the number of calls inlined.
func inlineLeaves(cfg *Config, b *mir.Body, leaf func(types.ItemID) *mir.Body) (*mir.Body, int) {
	if cfg.NoRanges || b == nil || b.GenericCount != 0 || len(b.Blocks) > inlineMaxBlock || leaf == nil {
		return b, 0
	}
	var nb *mir.Body
	budget, n := inlineMaxStmts, 0
	for bi := range b.Blocks {
		t := &b.Blocks[bi].Term
		if t.Kind != mir.TermCall {
			continue // a TermTailCall stays a tail call: never inlined
		}
		cb := callLeaf(cfg, b, t, leaf)
		if cb == nil || len(cb.Blocks[0].Stmts) > budget {
			continue
		}
		if nb == nil {
			nb = cloneForInline(b)
		}
		budget -= len(cb.Blocks[0].Stmts)
		splice(cfg, nb, mir.BlockID(bi), cb)
		n++
	}
	if nb == nil {
		return b, 0
	}
	// The second range pass, with the bounds fold: its proofs hold on every
	// execution of nb, which runs exactly as b does.
	rangeCheck(cfg, nb, true)
	return nb, n
}

// cloneForInline is a private copy of b: every slice the splice appends to
// or the range pass writes (each block's statements) is its own.
func cloneForInline(b *mir.Body) *mir.Body {
	c := *b
	c.Locals = slices.Clone(b.Locals)
	c.Spans = slices.Clone(b.Spans)
	c.Blocks = slices.Clone(b.Blocks)
	for i := range c.Blocks {
		c.Blocks[i].Stmts = slices.Clone(c.Blocks[i].Stmts)
	}
	return &c
}

// callLeaf is the leaf body the call t of b inlines, or nil. Obligation:
// the call is a plain direct call (no error edge, not spawned, no generic
// arguments) of another function of the same file whose body is a leaf,
// with one copied argument per parameter of the parameter's type, into a
// whole local of the leaf's result type; so the copies, the leaf's
// statements and the result copy compute what the call computes, at the
// same point.
func callLeaf(cfg *Config, b *mir.Body, t *mir.Terminator, leaf func(types.ItemID) *mir.Body) *mir.Body {
	c := &t.Callee
	if c.Kind != mir.CallDirect || c.Fn.Args.Len != 0 || c.Fn.Item == b.Item || t.HasErr || t.Spawn != mir.NoLocal || t.Dest.Proj.Len != 0 {
		return nil
	}
	cb := leaf(c.Fn.Item)
	// Same file: codegen's line tables (emitted in every build, --debug or
	// not) give an inlined statement the leaf's line in the caller's
	// DISubprogram, with no inlinedAt, and that scope's file is the
	// caller's; a leaf from another file would get its line in the wrong
	// file. Panic sites are unaffected: they name the span's own file.
	if cb == nil || cb.Item != c.Fn.Item || cb.Spans[0].File != b.Spans[0].File || !leafOK(cfg.Types, cb) {
		return nil
	}
	if b.Locals[t.Dest.Local].Type != cb.Locals[mir.ReturnLocal].Type || int(t.Args.Len) != int(cb.ParamCount) {
		return nil
	}
	for k, a := range b.Args[t.Args.Start:t.Args.End()] {
		if a.Mode != mir.ArgCopy || b.OperandType(a.Operand) != cb.Locals[k+1].Type {
			return nil
		}
	}
	return cb
}

// scalarOK reports whether t is an integer, bool, char or float: a value
// with no address, loan or drop, which a local copy moves exactly.
func scalarOK(tab *types.Table, t types.TypeID) bool {
	if t == types.Bool {
		return true
	}
	ty := tab.Get(t)
	return ty.IsInteger() || ty.Kind == types.KFloat || ty.Kind == types.KChar
}

// leafOK reports whether cb is a leaf (the file comment).
func leafOK(tab *types.Table, cb *mir.Body) bool {
	if cb.GenericCount != 0 || len(cb.Blocks) != 1 || len(cb.Loops) != 0 || len(cb.Defers) != 0 || len(cb.Asms) != 0 {
		return false
	}
	blk := &cb.Blocks[0]
	if blk.Origin != mir.OriginBody || blk.Term.Kind != mir.TermReturn || blk.Term.Exit != mir.ExitNormal || len(blk.Stmts) > leafMaxStmts {
		return false
	}
	for _, l := range cb.Locals {
		if !scalarOK(tab, l.Type) || l.Kind == mir.LocalScope || l.Kind == mir.LocalLoopHolder {
			return false
		}
	}
	whole := func(o mir.Operand) bool {
		switch o.Kind {
		case mir.OpCopy, mir.OpMove:
			return o.Place.Proj.Len == 0
		case mir.OpImm, mir.OpConst:
			return true
		}
		return false
	}
	gain := false
	for _, s := range blk.Stmts {
		switch s.Kind {
		case mir.StNop, mir.StScopeEnd:
			continue
		case mir.StAssign:
		default:
			return false
		}
		if s.Place.Proj.Len != 0 {
			return false
		}
		r := &s.Rvalue
		switch r.Kind {
		case mir.RvUse:
			if !whole(r.A) {
				return false
			}
		case mir.RvUnary:
			if !whole(r.A) {
				return false
			}
		case mir.RvBinary:
			if !whole(r.A) || !whole(r.B) {
				return false
			}
			if (r.Bin == mir.Add || r.Bin == mir.Sub || r.Bin == mir.Mul) && intOf(tab, r.Type).ok {
				gain = true
			}
		case mir.RvCast:
			if !whole(r.A) {
				return false
			}
			switch r.Cast {
			case mir.CastLossless, mir.CastTrunc:
			case mir.CastChecked:
				if intOf(tab, r.Type).ok {
					gain = true
				}
			default:
				return false
			}
		default:
			return false
		}
	}
	return gain
}

// splice inlines the leaf cb at block bi of nb, whose terminator is the
// call: fresh temporaries for cb's locals (its result local among them),
// the argument copies in parameter order, cb's statements on them, the
// destination copy, and a goto to the call's target, all at the spans
// they had (cb's appended to nb's table).
func splice(cfg *Config, nb *mir.Body, bi mir.BlockID, cb *mir.Body) {
	blk := &nb.Blocks[bi]
	call := blk.Term
	soff := mir.SpanID(len(nb.Spans))
	nb.Spans = append(nb.Spans, cb.Spans...)
	loc := make([]mir.LocalID, len(cb.Locals))
	for i, l := range cb.Locals {
		loc[i] = nb.AddLocal(mir.Local{Type: l.Type, Kind: mir.LocalTemp, Loop: mir.NoLoop, Span: soff + l.Span})
	}
	op := func(o mir.Operand) mir.Operand {
		if o.Kind == mir.OpCopy || o.Kind == mir.OpMove {
			o.Place = mir.Place{Local: loc[o.Place.Local]}
		}
		return o
	}
	args := nb.Args[call.Args.Start:call.Args.End()]
	for k := range args {
		a := args[k].Operand
		if cfg.inlineMut == imutArgSwap && len(args) >= 2 && k < 2 && cb.Locals[1].Type == cb.Locals[2].Type {
			a = args[1-k].Operand
		}
		blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAssign, Place: mir.Place{Local: loc[k+1]},
			Rvalue: mir.Rvalue{Kind: mir.RvUse, A: a}, Span: call.Span})
	}
	for _, s := range cb.Blocks[0].Stmts {
		s.Span += soff
		switch s.Kind {
		case mir.StScopeEnd:
			s.Local = loc[s.Local]
		case mir.StAssign:
			s.Place = mir.Place{Local: loc[s.Place.Local]}
			s.Rvalue.A = op(s.Rvalue.A)
			if s.Rvalue.Kind == mir.RvBinary {
				s.Rvalue.B = op(s.Rvalue.B)
			}
		}
		blk.Stmts = append(blk.Stmts, s)
	}
	res := loc[mir.ReturnLocal]
	if cfg.inlineMut == imutRetParam && cb.ParamCount > 0 && cb.Locals[1].Type == cb.Locals[0].Type {
		res = loc[1]
	}
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAssign, Place: call.Dest,
		Rvalue: mir.Rvalue{Kind: mir.RvUse, A: mir.Copy(mir.Place{Local: res})}, Span: call.Span})
	blk.Term = mir.Terminator{Kind: mir.TermGoto, Target: call.Target, Span: call.Span}
}
