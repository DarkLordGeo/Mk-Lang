// Why: a capturing function literal reads its captures through a hidden
// environment parameter, and a loan escaping through it needs its own error,
// one that names the captured variable rather than a parameter the user
// never wrote.
//
// What: hiddenParam, envFields and captureName (naming a capture),
// loanCapture and pathCapture, captureE0609 and captureBackstop.
//
// How: the hidden parameter counts as a parameter for liveness and
// initialization but is no declared source, so a result loan or a stored
// loan rooted at it is E0609 instead of E0602 or E0604. A write, a mut
// borrow or a move of a capture's place that check's table missed is E0609
// instead of E0507 or E0409 (the backstop). Every form is reported in the
// flow group.

package borrow

import (
	"strings"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// hiddenParam reports whether parameter p is a hidden environment parameter:
// past the signature's declared parameters.
func (c *checker) hiddenParam(p types.ParamIndex) bool {
	return c.env.Sig != nil && p != types.NoParam && int(p) > len(c.env.Sig.Params) && int(p) <= int(c.body.ParamCount)
}

// envFields are the environment record's fields of hidden parameter p.
func (c *checker) envFields(p types.ParamIndex) []types.RecordField {
	if c.tab == nil || int(p) >= len(c.body.Locals) {
		return nil
	}
	t := c.tab.Get(c.body.Locals[p].Type)
	if t.Kind != types.KRef {
		return nil
	}
	r := c.tab.Get(t.Elem)
	if r.Kind != types.KRecord {
		return nil
	}
	return r.Fields
}

// captureName is the captured name field i of p's environment stands for;
// every name when i is out of range (a loan on the whole record).
func (c *checker) captureName(p types.ParamIndex, i int, known bool) string {
	fs := c.envFields(p)
	if known && i >= 0 && i < len(fs) {
		return c.symbol(fs[i].Name)
	}
	var names []string
	for _, f := range fs {
		names = append(names, c.symbol(f.Name))
	}
	return strings.Join(names, "`, `")
}

// loanCapture reports whether loan l is a hidden parameter's abstract loan,
// and the captured name it reaches (its tag's first field, the record's).
func (c *checker) loanCapture(l loanID) (string, bool) {
	lo := &c.loans[l]
	if !lo.abstract || !c.hiddenParam(lo.param) {
		return "", false
	}
	if lo.inner && len(lo.tag) > 0 {
		return c.captureName(lo.param, int(lo.tag[0]), true), true
	}
	return c.captureName(lo.param, 0, false), true
}

// pathCapture reports whether path p is rooted at a hidden environment
// parameter (the local itself or its abstract pointee), and the captured
// name: the record field the path selects.
func (c *checker) pathCapture(p pathID) (string, bool) {
	pi := c.paths.get(p)
	ri := &c.roots[pi.root]
	if (ri.kind != rkParam && ri.kind != rkAbstract) || !c.hiddenParam(ri.param) {
		return "", false
	}
	for _, s := range pi.steps {
		if s.kind == stField {
			return c.captureName(ri.param, int(s.a), true), true
		}
	}
	return c.captureName(ri.param, 0, false), true
}

// callbackRecipe is check.CallbackRecipe (borrow does not import check): the
// sentence every E0609 message ends with.
const callbackRecipe = "; to store a callback, store a `fn` value and its plain state; see the cookbook's stored-callback recipe"

// captureE0609 emits E0609 in the flow group with the captured field and
// reason as facts, and the rewrite the message names.
func (c *checker) captureE0609(sp mir.SpanID, name, reason, msg string) *diag.Diagnostic {
	d := c.errAt(diag.E0609, sp, "%s", msg+callbackRecipe).Fact("captured", diag.TextList(name)).Text("reason", reason).In(diag.PhaseFlow)
	d.Alt("a struct holding what the callback needs, and a named `fn` that takes it").Alt("a loop instead of the callback")
	switch reason {
	case "write", "mut-borrow", "mut-self":
		d.Alt("pass the state `mut` through an explicit parameter")
	}
	return d
}

// captureBackstop is the backstop: E0609 for a write, mut borrow or move of a place
// rooted at a hidden environment parameter, which check's table did not
// report; true when it reported, so the caller skips its own code.
func (c *checker) captureBackstop(ctx *stmtCtx, p pathID, reason string) bool {
	name, ok := c.pathCapture(p)
	if !ok {
		return false
	}
	if c.report {
		var msg string
		switch reason {
		case "write":
			msg = "the literal assigns `" + name + "`, which it captured; a capture is read through a shared reference"
		case "move":
			msg = "the literal moves `" + name + "`, which it captured; a capture is read through a shared reference, and only a plain one is copied"
		default:
			msg = "the literal borrows `" + name + "` `mut`, which it captured; a capture is read through a shared reference"
		}
		c.captureE0609(ctx.span, name, reason, msg)
	}
	return true
}
