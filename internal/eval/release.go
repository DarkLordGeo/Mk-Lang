// Why: std.raw.Release[T, A](x, a) and std.raw.HasFree[T]() depend on
// whether T has an impl of std.core.Free. mono rewrites both in every body it
// builds, but a non-generic body the Program serves as lowered (a std
// function or test that names a concrete T) still holds the calls.
//
// What: freeImpl, releaseForget and hasFree.
//
// How: eval resolves the calls through the Program's FreeImpls: Release
// calls T's impl of std.core.Free with the same arguments (callFn redirects
// the call) or forgets x, and HasFree is the same impl test. The lookup's
// reads join the evaluation's read set.

package eval

import (
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

var keyRelease = types.ItemKey("std.raw", "Release")

// freeImpl resolves T's Free for a Release or HasFree call fn: the impl's
// method and generic arguments, or false.
func (m *Machine) freeImpl(f *frame, fn types.FnRef) (types.FnRef, bool) {
	key := m.items().Get(fn.Item).Key
	if len(fn.Args) == 0 || fn.Args[0].IsConst {
		source.ICEf(f.curSpan(), "eval: %s without a type argument", key)
	}
	p, ok := m.cfg.Program.(FreeImpls)
	if !ok {
		source.ICEf(f.curSpan(), "eval: %s[%s] in a body mono did not build, and the Program resolves no impl of std.core.Free",
			key, m.tab().TypeString(fn.Args[0].Type, nil))
	}
	impl, found, reads := p.FreeImpl(fn.Args[0].Type)
	if len(reads) > 0 {
		s := m.seg()
		for _, r := range reads {
			s.reads.Add(r)
		}
	}
	return impl, found
}

// releaseForget is Release for a T with no impl of std.core.Free: x is
// forgotten. A linear T should have an impl wherever check accepted the
// call; where check's release rules have a gap, it is an ICE.
func releaseForget(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
	if m.targ(f, fn, 0).layout.Shape.Kind == types.Linear {
		source.ICEf(f.curSpan(), "eval: std.raw.Release[%s]: a linear type with no impl of std.core.Free cannot be released; check accepted a release that needs one and should have given E0701 (its known gaps: mono's ICE text)",
			m.tab().TypeString(fn.Args[0].Type, nil))
	}
}

// hasFree is HasFree[T](): whether T has an impl of std.core.Free.
func hasFree(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
	_, ok := m.freeImpl(f, fn)
	m.store(m.dest(f, t), b2u(ok))
}
