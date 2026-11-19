// Why: the release-only rewrites of instance bodies (small-leaf inlining
// and loop versioning) must never reach the bodies eval runs, borrow judged
// or the caches keep; they belong at the one site that hands codegen its
// bodies.
//
// What: codegenBody, which applies lower.InlineLeaves with its second range
// pass (internal/lower/rangeinline.go) and lower.VersionLoops
// (internal/lower/rangeversion.go), and leafOf, the body a direct call may
// inline.
//
// How: the rewrites run on the instance bodies a build hands codegen
// (unitOf), after mono and after every evaluation. InlineLeaves and
// VersionLoops return new bodies, so the originals stay untouched.

package driver

import (
	"mk/internal/lower"
	"mk/internal/mir"
	"mk/internal/mono"
	"mk/internal/source"
	"mk/internal/types"
)

// noLoopVersions turns the versions off, and noLeafInline the inliner, for
// the tests that compare a build with them and without
// (loopversion_test.go). Never set elsewhere.
var noLoopVersions, noLeafInline bool

// codegenBody is the body codegen emits for instance i of in: in a release
// build, its body with lower.InlineLeaves' inlined leaves and then
// lower.VersionLoops' versions, validated as mono validates an instance (a
// violation is an ICE); otherwise its body. Dev builds gain nothing at -O0
// and keep their compile time. A --debug build gets no inlined leaves, so
// its full debug info (variables and lexical scopes) describes the code as
// written; release line tables, which every build emits, give an inlined
// statement the leaf's line in the caller's DISubprogram, with no inlinedAt,
// and panic sites name the leaf's own span. A benchmark-only build without
// bounds (or overflow) checks gets no bounds (or reduction) versions, so its
// column stays the build without checks. packOf is each instance's pack, -1
// for none.
func (w *world) codegenBody(in *mono.Instances, i int, packOf []int) *mir.Body {
	b := in.List[i].Body
	if b == nil || !w.cfg.Profile.Optimized() {
		return b
	}
	nb := b
	if !noLeafInline && !w.cfg.Debug {
		nb = lower.InlineLeaves(w.sem5.lcfg, b, func(id types.ItemID) *mir.Body { return w.leafOf(in, i, packOf, id) })
	}
	if !noLoopVersions {
		opt := lower.VersionOptions{NoBounds: w.cfg.TestHooks.NoBoundsChecks, NoReduce: w.cfg.TestHooks.NoOverflowChecks}
		nb = lower.VersionLoops(w.sem5.lcfg, nb, opt)
	}
	if nb == b {
		return b
	}
	env := &mir.Env{Types: w.tab, Known: &w.sem5.known, Callee: func(id types.ItemID) *types.Signature {
		if fd, ok := w.items.Get(id).Decl.(*types.FuncDecl); ok {
			return &fd.Sig
		}
		return nil
	}}
	if err := nb.Validate(env); err != nil {
		source.ICEf(source.Span{}, "codegen-only rewrites of %s: %v", w.items.Get(b.Item).Key, err)
	}
	return nb
}

// leafOf is the instance body InlineLeaves may inline for a direct call of
// the non-generic function id from instance i: that of id's one plain
// instance when it is another instance of i's own pack (so the pack's IR
// reads only its members' bodies, which the pack's pre-emission key covers)
// and has a body, and id is not naked, an intrinsic or an extern and has no
// cpu clause (whose code may not run in a caller without those features);
// else nil. InlineLeaves checks the body's shape.
func (w *world) leafOf(in *mono.Instances, i int, packOf []int, id types.ItemID) *mir.Body {
	j, ok := in.Find(id, nil)
	if !ok || j == i || j >= len(packOf) || packOf[i] < 0 || packOf[j] != packOf[i] || in.List[j].Body == nil {
		return nil
	}
	fd, ok := w.items.Get(id).Decl.(*types.FuncDecl)
	if !ok || fd.Naked || fd.Intrinsic || fd.Sig.Extern || len(fd.Sig.CPU) > 0 {
		return nil
	}
	return in.List[j].Body
}
