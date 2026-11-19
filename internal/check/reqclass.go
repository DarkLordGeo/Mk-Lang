// Why: a generic caller is asked to restate a callee's `requires` it cannot
// evaluate (restatedRequires, forwardedRequires), but a clause that is a
// run-time contract must not be demanded of callers.
//
// What: runtimeClause and sameModuleRuntime (the classification),
// clauseNames, litEnd, sigParam, sigGeneric and clauseUniverse.
//
// How: a clause that names a `var` global or a parameter that is not const
// is a run-time contract, which the function's entry check judges in dev
// and test builds and nothing judges in release, so no caller states it.
// Any other clause, a function called on const arguments included
// (`requires Valid(N)`), is compile-time and restated. Where the arguments
// are closed nothing is classified: the clause is evaluated or folded
// (requiresAt), and a refused run-time operation leaves it to run time
// (evaluateContract).

package check

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/resolve"
	"mk/internal/types"
)

// clauseUniverse are the universe and primitive names a compile-time clause
// may hold besides the function's const parameters and consts.
var clauseUniverse = map[string]bool{
	"true": true, "false": true, "SizeOf": true, "AlignOf": true, "Target": true, "Cast": true, "Trunc": true,
	"int": true, "uint": true, "i8": true, "i16": true, "i32": true, "i64": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "f32": true, "f64": true,
	"bool": true, "char": true, "str": true, "void": true, "Self": true,
}

// runtimeClause reports whether ct, a requires of the function whose
// signature is sig, is a run-time contract by what it names: a `var` global
// or a parameter that is not const (`requires Ready > 0`, `requires N <
// Limit`). A function is no such name (`requires Valid(N)` and `requires
// Now() > 0` stay compile-time, and are restated in a generic caller).
//
// A clause of this module is read from its resolved names (sameModuleRuntime),
// so a name is bound by its own file's imports (`import cfg = consts`). A
// clause of an imported module is read from its Contract.Text, which
// a module loaded from the cache has too, and that holds no file's import
// bindings: a bare name is looked up in the callee's module, and a
// qualified `x.Name` whose `x` is no item there (an import) keeps the
// compile-time reading, so the caller restates it. A clause
// with no text, a name found nowhere, and a form exprText cannot spell
// ("...") keep the compile-time reading too. The answer is the same with or
// without the callee's AST.
func (u *unit) runtimeClause(sig *types.Signature, ct types.Contract) bool {
	c := u.c
	pred := c.cfg.Items.Get(ct.Body)
	if pred.Module == u.m.ID {
		return u.sameModuleRuntime(sig, pred)
	}
	text := ct.Text
	if text == "" {
		return false
	}
	mod := c.cfg.Items.Module(pred.Module)
	if mod == nil {
		return false
	}
	names := clauseNames(text)
	for i := 0; i < len(names); i++ {
		n := names[i]
		if n.member {
			continue
		}
		if p, ok := sigParam(c, sig, n.name); ok {
			if !p.Const {
				return true
			}
			continue
		}
		if sigGeneric(c, sig, n.name) || clauseUniverse[n.name] {
			continue
		}
		id, ok := c.cfg.Items.ByKey(types.ItemKey(mod.Path, n.name))
		if !ok {
			// A prelude or unknown name, or with a member an import binding
			// this text cannot resolve: the compile-time reading, and its
			// member is that module's, not this one's.
			if n.dotted && i+1 < len(names) && names[i+1].member {
				i++
			}
			continue
		}
		if c.cfg.Items.Get(id).Kind == types.KindGlobal {
			return true
		}
	}
	return false
}

// sameModuleRuntime is runtimeClause for a predicate of this module, read
// from resolve's refs: a ref to a var global, or to a parameter of the
// function that is not const, makes it run-time.
func (u *unit) sameModuleRuntime(sig *types.Signature, pred *types.Item) bool {
	if u.m.byItem[pred.ID] == nil {
		return false
	}
	n, fx := u.m.nodeOf(pred.ID)
	ct, ok := n.(*ast.Contract)
	if !ok || ct.X == nil || fx == nil {
		return false
	}
	found := false
	ast.Inspect(ct.X, func(x ast.Node) bool {
		if x == nil || found {
			return false
		}
		if id := x.ID(); int(id) < len(fx.info.Refs) {
			switch r := fx.info.Refs[id]; r.Kind {
			case resolve.RefItem:
				if it := u.c.cfg.Items.Get(types.ItemID(r.A)); it != nil && it.Kind == types.KindGlobal {
					found = true
				}
			case resolve.RefParam:
				if types.ItemID(r.B) == pred.Owner && r.A >= 1 && int(r.A) <= len(sig.Params) && !sig.Params[r.A-1].Const {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// clauseName is one identifier of a clause's text: member when a '.'
// precedes it or it is a field label, dotted when a '.' follows it.
type clauseName struct {
	name           string
	member, dotted bool
}

// clauseNames lists the identifiers of text outside its literals: str and
// char literals with their escapes, the prefixed forms `b'a'`, `b"bytes"`
// and the raw `r"C:\"`, which takes no escape, and number literals (`0x1F`,
// `2_000`).
func clauseNames(text string) []clauseName {
	var out []clauseName
	isStart := func(r byte) bool { return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80 }
	isWord := func(r byte) bool { return isStart(r) || r >= '0' && r <= '9' }
	for i := 0; i < len(text); {
		ch := text[i]
		switch {
		case ch == '"' || ch == '\'':
			i = litEnd(text, i, false)
		case (ch == 'b' || ch == 'r') && i+1 < len(text) && (text[i+1] == '"' || text[i+1] == '\'') && (i == 0 || !isWord(text[i-1])):
			i = litEnd(text, i+1, ch == 'r')
		case ch >= '0' && ch <= '9':
			for i < len(text) && isWord(text[i]) {
				i++
			}
		case isStart(ch):
			j := i
			for j < len(text) && isWord(text[j]) {
				j++
			}
			// A field label (`P(a: k)`, labelAt) is no name either.
			out = append(out, clauseName{name: text[i:j], member: i > 0 && text[i-1] == '.' || labelAt(text, j), dotted: j < len(text) && text[j] == '.'})
			i = j
		default:
			i++
		}
	}
	return out
}

// litEnd is the end, exclusive and at most len(text), of the literal of a
// clause's text whose opening quote is at i: a `"""` multi-line literal
// ends at the next `"""` (syntax/lexlit.go multi), any other at the next
// quote of its kind; raw (the `r` prefix) takes no escape. clauseNames and
// substClause share it, so a quote inside a multi-line literal does not end
// it.
func litEnd(text string, i int, raw bool) int {
	if strings.HasPrefix(text[i:], `"""`) {
		j := i + 3
		for j < len(text) && !strings.HasPrefix(text[j:], `"""`) {
			if text[j] == '\\' && !raw {
				j++
			}
			j++
		}
		return min(j+3, len(text))
	}
	q := text[i]
	j := i + 1
	for j < len(text) && text[j] != q {
		if text[j] == '\\' && !raw {
			j++
		}
		j++
	}
	return min(j+1, len(text))
}

// sigParam is sig's parameter named name.
func sigParam(c *Checker, sig *types.Signature, name string) (types.Param, bool) {
	for _, p := range sig.Params {
		if c.cfg.Interner.Str(p.Name) == name {
			return p, true
		}
	}
	return types.Param{}, false
}

// sigGeneric reports whether name is a generic parameter of sig.
func sigGeneric(c *Checker, sig *types.Signature, name string) bool {
	for _, g := range sig.Generics {
		if c.cfg.Interner.Str(g.Name) == name {
			return true
		}
	}
	return false
}
