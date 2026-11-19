// Why: a generic std container over an unbounded T must free an element
// with T's own Free when it has one and forget it otherwise; which applies
// is known only per instance.
//
// What: the per-instance resolution of std.raw.Release[T, A](x, a) and the
// fold of std.raw.HasFree[T]() (resolveReleases, freeItems).
//
// How: where T has an impl of std.core's interface Free, Release becomes a
// direct call of that impl's method Free with the same arguments; where T
// has none (or std.core declares no interface Free), it becomes
// std.raw.Forget[T](x), and a, a shared reference, is left unused.
// HasFree[T]() folds to the constant answer of the same impl test, as an
// `if T is X` test folds (resolve.go). A is Release's second generic
// because std.raw cannot name std.core's Alloc (std.core imports std.raw);
// it is the Free method's allocator type in every instance check accepts.
//
// The impl lookup is env.ImplMethod, ifaceCall's own, so the dependency on
// `impl Free for T` is the one an interface call has: the rewritten callee
// (the impl's method, or Forget) is in the instance's body and so in
// Instance.Content (iface.Hasher.InstanceContentHash), and Collect runs
// every build; adding or removing the impl changes the content of every
// instance that released a T, and so the key of its pack.
//
// Order: HasFree folds in resolve, with its switches, so a Release under
// `if raw.HasFree[T]()` is pruned before Releases are resolved (after the
// prune, on live blocks only). A linear T with no impl reaching a live
// Release is an ICE: check's release bounds should rule it out with E0701,
// and the message names the gaps they still have.

package mono

import (
	"mk/internal/mir"
	"mk/internal/types"
)

// The items the Release rewrite reads, by stable key.
var (
	keyRelease   = types.ItemKey("std.raw", "Release")
	keyHasFree   = types.ItemKey("std.raw", "HasFree")
	keyForget    = types.ItemKey("std.raw", "Forget")
	keyFreeIface = types.ItemKey("std.core", "Free")
)

// freeItems are the items of one builder's Release rewrites, looked up on first
// use (Items.ByKey is a map read).
type freeItems struct {
	looked           bool
	release, hasFree types.ItemID
	// iface is std.core's interface Free and method its method Free's
	// index; iface is NoItem when std.core declares no such interface.
	ifaceLooked bool
	iface       types.ItemID
	method      uint32
}

type freeCall uint8

const (
	freeNone freeCall = iota
	freeRelease
	freeHasFree
)

// freeCallOf classifies a direct callee: Release, HasFree or neither.
func (bl *builder) freeCallOf(item types.ItemID) freeCall {
	fi := &bl.fi
	if !fi.looked {
		fi.looked = true
		fi.release, _ = bl.env.Items().ByKey(keyRelease)
		fi.hasFree, _ = bl.env.Items().ByKey(keyHasFree)
	}
	switch {
	case item == types.NoItem:
		return freeNone
	case item == fi.release:
		return freeRelease
	case item == fi.hasFree:
		return freeHasFree
	}
	return freeNone
}

// freeIface finds std.core's interface Free and its method Free.
func (bl *builder) freeIface() (types.ItemID, uint32, bool) {
	fi := &bl.fi
	if !fi.ifaceLooked {
		fi.ifaceLooked = true
		fi.iface = types.NoItem
		if id, ok := bl.env.Items().ByKey(keyFreeIface); ok {
			it := bl.env.Items().Get(id)
			if d, ok := it.Decl.(*types.InterfaceDecl); ok && it.Kind == types.KindInterface {
				in := bl.tab.Interner()
				for i := range d.Methods {
					if in.Str(d.Methods[i].Name) == "Free" {
						fi.iface, fi.method = id, uint32(i)
						break
					}
				}
			}
		}
	}
	return fi.iface, fi.method, fi.iface != types.NoItem
}

// freeImpl is the method Free of t's impl of std.core's Free and the impl's
// generic arguments, by ifaceCall's lookup; false when there is none.
func (bl *builder) freeImpl(t types.TypeID) (types.ItemID, []types.GenericArg, bool) {
	ifc, m, ok := bl.freeIface()
	if !ok {
		return types.NoItem, nil, false
	}
	return bl.env.ImplMethod(ifc, m, t)
}

// freeArg is the closed type argument k of a Release or HasFree call in the
// instance body; one still generic is an ICE.
func (bl *builder) freeArg(t *mir.Terminator, k uint32, what string) types.TypeID {
	f := t.Callee.Fn
	if k >= f.Args.Len || bl.b.GenericArgs[f.Args.Start+k].IsConst {
		ice("std.raw.%s with %d generic arguments", what, f.Args.Len)
	}
	x := bl.b.GenericArgs[f.Args.Start+k].Type
	if !closed(bl.tab, x) {
		ice("std.raw.%s[%s] is still generic in an instance of %s", what, bl.tab.TypeString(x, nil), nameOf(bl.env, bl.item, bl.args))
	}
	return x
}

// foldHasFree replaces the call ending blk, HasFree[T](), by the assignment
// of its constant answer and a goto, and folds the switches on it. It
// reports whether a switch was folded.
func (bl *builder) foldHasFree(blk *mir.Block) bool {
	t := &blk.Term
	_, _, ok := bl.freeImpl(bl.freeArg(t, 0, "HasFree"))
	v := b2u(ok)
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAssign, Place: t.Dest,
		Rvalue: mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, v)}, Span: t.Span})
	*t = mir.Terminator{Kind: mir.TermGoto, Span: t.Span, Target: t.Target}
	return foldSwitchesOn(bl.b, blk.Stmts[len(blk.Stmts)-1].Place, v)
}

// resolveReleases rewrites every Release call of the (pruned) instance
// body: into the impl's Free, or into Forget[T](x).
func (bl *builder) resolveReleases() {
	b := bl.b
	for bi := range b.Blocks {
		t := &b.Blocks[bi].Term
		if !isCall(t) || t.Callee.Kind != mir.CallDirect || bl.freeCallOf(t.Callee.Fn.Item) != freeRelease {
			continue
		}
		noTail(t, "std.raw.Release")
		x := bl.freeArg(t, 0, "Release")
		a := bl.freeArg(t, 1, "Release")
		if t.Args.Len != 2 {
			ice("std.raw.Release[%s] with %d arguments", bl.tab.TypeString(x, nil), t.Args.Len)
		}
		if fn, fargs, ok := bl.freeImpl(x); ok {
			fd, isFn := bl.env.Items().Get(fn).Decl.(*types.FuncDecl)
			if !isFn || len(fd.Sig.Params) != 2 || Subst(bl.tab, fd.Sig.Params[1].Type, fargs) != bl.tab.Ref(a) {
				ice("std.raw.Release[%s, %s]: the Free of %s does not take &%s", bl.tab.TypeString(x, nil),
					bl.tab.TypeString(a, nil), bl.env.Items().Get(fn).Key, bl.tab.TypeString(a, nil))
			}
			t.Callee = mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: fn, Args: b.AddGenericArgs(fargs...)}}
			continue
		}
		if l := bl.env.Layout(x); l != nil && l.Shape.Kind == types.Linear {
			xs := bl.tab.TypeString(x, nil)
			ice("std.raw.Release[%s] in %s: %s is linear and has no impl of std.core.Free, so it cannot be released. "+
				"reaching here is a check bug (the release should have been E0701 at the call) or a direct "+
				"std.raw.Release call on such a type", xs, nameOf(bl.env, bl.item, bl.args), xs)
		}
		forget, ok := bl.env.Items().ByKey(keyForget)
		if !ok {
			ice("std.raw.Release[%s]: std.raw declares no Forget", bl.tab.TypeString(x, nil))
		}
		t.Callee = mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: forget, Args: b.AddGenericArgs(types.TypeArg(x))}}
		t.Args.Len = 1 // x; a is a shared reference, nothing to release
	}
}
