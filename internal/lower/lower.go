// Why: this is the package's entry point: the one function the driver calls
// to turn a checked body into MIR, and the configuration every call of a
// session shares.
//
// What: Config, BodyKind, Lower and LowerProved, lowerBody (body setup, the
// root block, the fall-off return) and closeBrace.
//
// How: Lower builds the body with a builder, runs the range analysis on it
// (unless cfg.NoRanges), trims it under cfg.Trim, validates it with
// mir.(*Body).Validate under cfg.Validate, and compacts it. Lower never
// reports a user diagnostic, only ICEs.

// Package lower turns one checked function, test, const or contract body
// into MIR: explicit control flow, places, temporaries with ends, and the
// run-time checks the language requires.
package lower

import (
	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// Config is what every Lower call of one session shares. The tables are
// safe for concurrent use.
type Config struct {
	Types    *types.Table
	Items    *types.Items
	Consts   *types.ConstTable
	Interner *source.Interner
	Known    *types.KnownItems
	// Callee returns the checked signature of a function item (argument
	// modes, error edges, the validator's call checks). Nil reads the item's
	// FuncDecl.
	Callee func(types.ItemID) *types.Signature
	// Impls returns the impl of interface iface for the closed type t, or
	// NoItem (check.Checker.ImplOf); lower uses it to find a Slice
	// implementer's Items and ItemsMut. Nil: implementer
	// loops and indexing are an ICE.
	Impls func(iface types.ItemID, args []types.GenericArg, t types.TypeID) types.ItemID
	// Sources gives the token text of a quote's template. Nil: a quote is
	// an ICE naming it.
	Sources *source.SourceMap
	// SitePath is the path a trace site names for a file: the driver's
	// package-relative site path, as codegen's panic sites use. Nil:
	// the SourceMap path.
	SitePath func(source.FileID) string
	// Trim enables the trim pass (generic.go's trim); off by default.
	Trim bool
	// Validate runs mir.(*Body).Validate on every body before Lower returns.
	Validate bool
	// NoRanges turns the range analysis off (rangecheck.go): every
	// check stays as lowered. The zero value runs it.
	NoRanges bool
	// matchLeaves overrides the decision tree's size-guard factor for
	// tests: 0 is the default 4, negative forces the fallback.
	matchLeaves int
	// rangeMut is the range analysis's wrong fact for the mutation test
	// (TestPC_MutationCaught); zero outside it.
	rangeMut rangeMut
	// inlineMut is the inliner's fault for its mutation test
	// (TestPI_MutantsCaught); zero outside it.
	inlineMut inlineMut
}

// callee is Config.Callee with the FuncDecl fallback.
func (c *Config) callee(id types.ItemID) *types.Signature {
	if c.Callee != nil {
		return c.Callee(id)
	}
	if id == types.NoItem {
		return nil
	}
	if d, ok := c.Items.Get(id).Decl.(*types.FuncDecl); ok {
		return &d.Sig
	}
	return nil
}

// BodyKind is what a body belongs to.
type BodyKind uint8

const (
	BodyFunc        BodyKind = iota // function or method with a body
	BodyLiteral                     // fn literal, an item of its own (KindLiteral)
	BodyTest                        // test "name" { }
	BodyConst                       // const initializer: local 0 is the const's type, no parameters
	BodyGlobal                      // var global initializer, as BodyConst
	BodyRequires                    // requires predicate: bool in local 0
	BodyEnsures                     // ensures predicate: bool in local 0, result as the last parameter
	BodyInvariant                   // invariant predicate over self: bool in local 0
	BodyErrorFormat                 // an error's provided Format method: no AST body
)

// Lower builds the MIR of one checked body. The result passes every check
// of mir.(*Body).Validate and is immutable once returned. Lower never emits a
// user diagnostic: a body that reaches it was accepted by check, and every
// internal inconsistency raises source.ICEf at the offending node's span.
// The caller runs it under source.Guard; the ICE becomes E0000 naming the
// item. Lower is safe to call concurrently on different bodies with
// one Config.
func Lower(cfg *Config, in Input) *mir.Body {
	b, _ := lowerRanges(cfg, in, false)
	return b
}

// LowerProved is Lower that also returns the bounds checks its range
// analysis proved, for ProvedBounds.Fold once borrow has accepted the body.
// With cfg.NoRanges, or under cfg.Trim (which renumbers blocks),
// it proves none. The body is Lower's: the folds are not applied.
func LowerProved(cfg *Config, in Input) (*mir.Body, ProvedBounds) {
	return lowerRanges(cfg, in, !cfg.Trim)
}

// lowerRanges is Lower; with bounds it keeps the proved bounds checks.
func lowerRanges(cfg *Config, in Input, bounds bool) (*mir.Body, ProvedBounds) {
	bl := newBuilder(cfg, in)
	bl.lowerBody()
	var proved ProvedBounds
	if !cfg.NoRanges {
		proved.at = rangeRun(cfg, bl.b, bounds) // the range analysis
		if !bounds {
			proved.at = nil
		}
	}
	if cfg.Trim {
		trim(bl)
	}
	if cfg.Validate {
		env := &mir.Env{Types: cfg.Types, Sig: bl.sig, Callee: cfg.callee, Known: cfg.Known,
			Facts: func(t types.TypeID) (types.Facts, bool) { return in.Facts(t) }}
		if err := bl.b.Validate(env); err != nil {
			source.ICEf(in.Root().Span(), "lower: %s: %v", cfg.Items.Get(in.Item()).Key, err)
		}
	}
	compact(bl.b)
	return bl.b, proved
}

// lowerBody is the body's setup, root and fall-off.
func (bl *builder) lowerBody() {
	in := bl.in
	bl.b.GenericCount = in.GenericCount()
	root := in.Root()
	bl.cur = bl.newBlock()
	bl.openScope(scopeFn)
	switch bl.kind {
	case BodyErrorFormat:
		bl.errorFormatBody(in.ErrorFormat())
		return
	case BodyConst, BodyGlobal, BodyRequires, BodyEnsures, BodyInvariant:
		e, ok := root.(ast.Expr)
		if !ok {
			source.ICEf(root.Span(), "lower: an expression body is %T", root)
		}
		bl.pushFrame(e.Span())
		v := bl.operand(e)
		if bl.cur != mir.NoBlock {
			bl.assign(bl.b.Place(mir.ReturnLocal), use(v), e.Span())
			bl.popFrame()
			bl.exitTo(0, exitReturn, e.Span())
			bl.term(mir.Terminator{Kind: mir.TermReturn, Exit: mir.ExitNormal}, e.Span())
		}
		return
	}
	blk, ok := root.(*ast.Block)
	if !ok {
		source.ICEf(root.Span(), "lower: a function body is %T", root)
	}
	if !bl.naked() {
		// A naked body is its StAsm and unreachable, nothing else.
		bl.externEntryChecks()
		bl.entryChecks()
	}
	bl.blockStmts(blk)
	if bl.cur == mir.NoBlock {
		return
	}
	// The fall-off: void or E!void returns success.
	end := closeBrace(blk)
	if bl.sig.Result != types.Void && bl.sig.Result != types.Never {
		source.ICEf(end, "lower: the body falls off the end of a %s function (E0805 is check's)",
			bl.tab.TypeString(bl.sig.Result, bl.sig.Generics))
	}
	bl.successExit(nil, end, mir.ExitNormal)
}

// closeBrace is the span of a block's closing brace.
func closeBrace(b *ast.Block) source.Span {
	sp := b.Span()
	if sp.End > sp.Start {
		return source.Span{File: sp.File, Start: sp.End - 1, End: sp.End}
	}
	return sp
}
