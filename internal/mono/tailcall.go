// Why: every mono site that reads a TermCall must also handle a
// TermTailCall (become) or raise an ICE, so that none lets one fall through.
//
// What: isCall, true for both call terminators, and noTail, the ICE for a
// rewrite that would replace a tail call by something else.
//
// How: a tail call names its callee and arguments as a call does, so the
// substitution, canonicalization, collection and copying of calls cover it.
// It has no Target or error edge and its Dest is _0, so a rewrite that
// replaces a call by a value (a fold, a dropped contract check, a Release
// resolved, a slice adaptation, a Print expansion) never meets one: check
// refuses every such callee (E0314: intrinsics, Print, a method through a
// bound), and reaching one is an ICE.

package mono

import "mk/internal/mir"

// isCall reports whether t calls a function: a TermCall or a TermTailCall.
func isCall(t *mir.Terminator) bool {
	return t.Kind == mir.TermCall || t.Kind == mir.TermTailCall
}

// noTail raises an ICE when t is a TermTailCall at a rewrite that replaces
// the call by something else.
func noTail(t *mir.Terminator, what string) {
	if t.Kind == mir.TermTailCall {
		ice("become of %s reached mono (check refuses it, E0314)", what)
	}
}
