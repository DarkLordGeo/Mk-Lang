// Why: the borrow checker must never crash on a valid MIR body, whatever
// its shape.
// What: FuzzCheckNoICE, its generator genBody and genBodyShapes,
// TestGenBodyValidates and TestCopyWholeNonPlainIsICE.
// How: generates bodies over the fixture types that pass mir.Validate (with
// Facts), including a Copy of a whole local only when its type is plain, and
// checks that Check returns without an ICE; Validate rejects the seed whose
// Copy breaks that rule.

package borrow

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// genBody builds a body from fuzz bytes: a few locals of fixture types, then a
// sequence of statements and terminators drawn from the bytes, with operand
// modes drawn from Facts. The shapes: copies, borrows and reborrows, field
// borrows and writes, calls with and without flows, `Res` acquire and close,
// branches, lower's bounds check before an element borrow, a `for x in mut xs`
// loop, a fallible `try list.Push(a, r)` (an `into` flow and an error edge,
// ExitTry) and its `else` twin, an argument slot `t = move r`, and a scope
// with a spawn and a join.
func genBody(k *kit, data []byte) *mb {
	m, _ := genBodyShapes(k, data, 0)
	return m
}

// genShapes is the number of shapes genBody draws from.
const genShapes = 19

// genBodyShapes is genBody, also returning the set of shapes it emitted.
// With minStmts > 0 it reads data cyclically and stops at the first top-level
// shape boundary at or after minStmts statements (BenchmarkTypicalBody).
func genBodyShapes(k *kit, data []byte, minStmts int) (*mb, uint32) {
	var used uint32
	k.lib()
	k.taskLib()
	hl := k.named("HeapList", k.Res)
	m := k.body(k.sig("G", []types.Param{k.param("c", types.Bool), k.param("p", k.tab.Mut(types.I32)),
		k.param("xs", k.tab.MutSlice(types.I32)), k.param("a", k.tab.Ref(k.Alloc)), k.param("list", k.tab.Mut(hl)),
		k.param("io", k.Io)}, types.Void, k.errs("OutOfMemory")))
	ri := refI32(k)
	tys := []types.TypeID{types.I32, ri, k.Res, k.Point, k.tab.Mut(types.I32)}
	var locals []mir.LocalID
	for i, ty := range tys {
		locals = append(locals, m.user(string(rune('a'+i)), ty, true))
	}
	pos := 0
	next := func() int {
		if minStmts > 0 && pos >= len(data) {
			pos = 0
		}
		if pos >= len(data) {
			return 0
		}
		b := int(data[pos])
		pos++
		return b
	}
	pick := func(want types.TypeID) mir.LocalID {
		for _, l := range locals {
			if m.b.Locals[l].Type == want {
				return l
			}
		}
		return locals[0]
	}
	steps, depth := 0, 0
	var gen func()
	gen = func() {
		steps++
		shape := next() % genShapes
		used |= 1 << shape
		switch shape {
		case 0:
			m.set(pick(types.I32), use(imm(types.I32, uint64(next()))))
		case 1:
			m.set(pick(ri), ref(m.local(pick(types.I32))))
		case 2:
			m.set(pick(types.I32), use(cp(m.local(pick(types.I32)))))
		case 3:
			m.acquire(pick(k.Res))
		case 4:
			m.close(pick(k.Res))
		case 5:
			m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(pick(ri))))
		case 6:
			m.set(pick(k.tab.Mut(types.I32)), mutRef(m.local(pick(types.I32))))
		case 7:
			m.callv("UseMut", []types.TypeID{types.I32}, argReborrow(m.local(2)))
		case 8:
			yes, join := m.block(), m.block()
			m.branch(cp(m.local(1)), yes, join)
			m.at(yes)
			if next()%2 == 0 {
				m.set(pick(types.I32), use(imm(types.I32, 1)))
			}
			m.jump(join)
			m.at(join)
		case 9:
			m.end(locals[next()%len(locals)])
		case 10: // rf = &pt.x
			m.set(pick(ri), ref(m.pl(pick(k.Point), fld(uint32(next()%2), types.I32))))
		case 11: // pt.y = n
			m.assign(m.pl(pick(k.Point), fld(1, types.I32)), use(imm(types.I32, uint64(next()))))
		case 12: // q = mut xs[i], with lower's bounds check
			i := m.temp(types.Int)
			m.set(i, use(imm(types.Int, uint64(next()%4))))
			m.boundsCheck(m.local(3), i)
			m.set(pick(k.tab.Mut(types.I32)), mutRef(m.pl(3, index(i, types.I32))))
		case 13: // for x in mut xs { body }
			if depth > 1 {
				return
			}
			depth++
			m.mutLoop(3, func(x mir.LocalID) {
				switch next() % 3 {
				case 0:
					m.assign(m.pl(x, deref(types.I32)), use(imm(types.I32, 1)))
				case 1:
					m.set(pick(types.I32), use(cp(m.pl(x, deref(types.I32)))))
				default:
					gen()
				}
			})
			depth--
		case 14: // try list.Push(a, r)
			m.tryPush(k.Res, 5, 4, pick(k.Res), nil)
		case 15: // list.Push(a, r) else { body }
			ok, bad := m.block(), m.block()
			e := m.temp(k.tab.ErrorSetType(k.errs("OutOfMemory")))
			d := m.temp(types.Void)
			m.callErr(k.item("HeapList.Push"), []types.TypeID{k.Res},
				[]mir.Arg{argReborrow(m.local(5)), argCopy(m.local(4)), argMove(m.local(pick(k.Res)))}, m.local(d), ok, mir.EdgeElse, bad, e)
			m.at(bad)
			if next()%2 == 0 {
				m.close(pick(k.Res))
			}
			m.jump(ok)
			m.at(ok)
			m.end(d, e)
		case 16: // Take(r) through an argument slot t = move r
			sl := m.temp(k.Res)
			m.set(sl, use(mv(m.local(pick(k.Res)))))
			m.callv("Take", []types.TypeID{k.Res}, argMove(m.local(sl)))
			m.end(sl)
		case 17: // scope s := io.Scope() { s.go Bump(mut n) }
			s := m.openScope()
			tm := m.temp(k.tab.Mut(types.I32))
			m.set(tm, mutRef(m.local(pick(types.I32))))
			m.spawn(s, "Bump", argMove(m.local(tm)))
			m.end(tm)
			if next()%2 == 0 {
				gen()
			}
			m.join(s)
			m.end(s)
		case 18: // Exchange(mut x, mut y): stores both ways
			x, y := m.temp(k.tab.Mut(ri)), m.temp(k.tab.Mut(ri))
			m.set(x, mutRef(m.local(pick(ri))))
			m.set(y, mutRef(m.local(pick(ri))))
			m.callv("Exchange", []types.TypeID{ri}, argMove(m.local(x)), argMove(m.local(y)))
			m.end(y, x)
		}
	}
	if minStmts > 0 {
		for stmtCount(m.b) < minStmts {
			gen()
		}
	}
	for minStmts == 0 && pos < len(data) && steps < 64 {
		gen()
	}
	m.end(locals...)
	m.okVoid()
	return m, used
}

// stmtCount is the number of statements and terminators of a body.
func stmtCount(b *mir.Body) int {
	n := 0
	for i := range b.Blocks {
		n += len(b.Blocks[i].Stmts) + 1
	}
	return n
}

// TestGenBodyValidates keeps the fuzz property honest: most generated
// bodies pass Validate (an invalid one is skipped, not checked), and every
// shape occurs in some valid body.
func TestGenBodyValidates(t *testing.T) {
	var shapes uint32
	valid, all := 0, 0
	for _, data := range genSeeds(400) {
		all++
		k := newKit(t)
		m, used := genBodyShapes(k, data, 0)
		if err := m.b.Validate(k.mirEnv(m.sig)); err != nil {
			continue
		}
		valid++
		shapes |= used
	}
	if valid*10 < all*8 {
		t.Errorf("only %d of %d generated bodies validate", valid, all)
	}
	for i := range genShapes {
		if shapes&(1<<i) == 0 {
			t.Errorf("shape %d never occurs in a valid body", i)
		}
	}
	t.Logf("%d of %d generated bodies validate", valid, all)
}

func FuzzCheckNoICE(f *testing.F) {
	f.Add([]byte{0, 1, 1, 0, 5, 3, 4, 8, 0})
	f.Add([]byte{3, 3, 4, 6, 7, 9, 2, 8, 1, 5})
	f.Add([]byte{1, 0, 9, 0, 5, 6, 0, 7})
	f.Fuzz(func(t *testing.T, data []byte) {
		k := newKit(t)
		m := genBody(k, data)
		if err := m.b.Validate(k.mirEnv(m.sig)); err != nil {
			return // not a body lower can produce
		}
		ie := source.Guard("borrow.Check", m.b.Spans[0], func() { Check(m.b, k.env(m.sig)) })
		if ie != nil {
			t.Fatalf("ICE: %v\n%s", ie, m.print())
		}
	})
}

// TestCopyWholeNonPlainIsICE: `_1: Res; _2 = Copy(_1)` copies a whole
// non-plain local; the contract's Validate rejects it, and Check itself raises
// an ICE rather than a diagnostic if such a body ever arrives.
func TestCopyWholeNonPlainIsICE(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	a := m.user("a", k.Res, false)
	b := m.user("b", k.Res, false)
	m.acquire(a)
	m.set(b, use(cp(m.local(a))))
	m.ret(mir.ExitNormal)
	if err := m.b.Validate(k.mirEnv(m.sig)); err == nil {
		t.Fatalf("Validate accepted a Copy of a whole non-plain local")
	}
	ie := source.Guard("borrow.Check", m.b.Spans[0], func() { Check(m.b, k.env(m.sig)) })
	if ie == nil {
		t.Fatalf("Check gave no ICE on a loan-rule violation")
	}
}
