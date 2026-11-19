// Why: the builder grows each list of a body by append, so a finished body's
// lists hold up to twice their length, and the session keeps every body until
// its last stage (the instance-limit walk, codegen). On a 1 MLOC program the
// driver's TestPerfMemory_Probe measured 2.37 M statement slots for 1.98 M
// statements and 0.84 M block slots for 0.64 M blocks.
//
// What: compact, which trims a lowered mir.Body's lists, and fit.
//
// How: each list whose capacity exceeds its length is copied into one of its
// length; the contents are unchanged and an empty list keeps its nil-ness.

package lower

import "mk/internal/mir"

// compact copies each of b's lists whose capacity exceeds its length into
// one of its length. The contents are unchanged, so nothing that reads the
// body sees a difference; a later append reallocates as before.
func compact(b *mir.Body) {
	b.Locals = fit(b.Locals)
	b.Blocks = fit(b.Blocks)
	for i := range b.Blocks {
		b.Blocks[i].Stmts = fit(b.Blocks[i].Stmts)
	}
	b.Projections = fit(b.Projections)
	b.GenericArgs = fit(b.GenericArgs)
	b.Operands = fit(b.Operands)
	b.Args = fit(b.Args)
	b.Cases = fit(b.Cases)
	b.Spans = fit(b.Spans)
	b.Loops = fit(b.Loops)
	b.Defers = fit(b.Defers)
	b.Asms = fit(b.Asms)
}

// fit is s with no spare capacity: s itself when it has none, else a copy.
// An empty list keeps its nil-ness.
func fit[T any](s []T) []T {
	if cap(s) == len(s) {
		return s
	}
	if len(s) == 0 {
		if s == nil {
			return nil
		}
		return make([]T, 0)
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}
