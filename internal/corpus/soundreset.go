// Why: a reset ends what was stored into an arena, but an arena made over
// another allocator still holds that allocator's loan afterwards; a checker
// that ended that loan at the Reset too let the backing be reset or freed
// under a live arena. The generator draws that form so a run shows it.
//
// What: resetBack, shape S89 (profiles full and fields), its writer
// resetText with resetForm, and resetMakes and resetViews, the forms of the
// arena it resets and of what is made in it after the Reset.
//
// How: the shape writes one function of its own, Rbn(a: &Arena): i64
// (resetText), and Run folds its result. Rbn makes a backing sub-arena p of
// a, an arena c over p (Arena.NewIn(p.Alloc()), the header's NewIn, or the
// Sub of an arena over p, whose chunks come from p too), resets c, then
// makes a view or allocator in c: a cell, a List, a mem.Pool, a str from
// Fmt or a sub-arena with a cell. One time in holeOdds (the hole) p is
// reset, after the intermediate arena is freed in the Sub form, while c is
// still live and before the view is read: c's chunks are p's memory, so the
// read is of memory p's Reset ended, and the tree rejects the Reset (c
// still holds p's loan). Otherwise the view is read, c is freed and only
// then is p reset (one time in two) and freed, which both checkers accept.

package corpus

import (
	"fmt"
	"strings"
)

// resetMakes are the ways S89 makes the reset arena c over the backing p:
// the arenas it declares in order, each with its fallible initializer, and
// whether it needs the header's NewIn. The Sub form's m is the arena over p
// whose Sub is c.
var resetMakes = []struct {
	decls [][2]string
	newIn bool
}{
	{[][2]string{{"c", "Arena.NewIn(p.Alloc())"}}, false},
	{[][2]string{{"c", "NewIn(&p)"}}, true},
	{[][2]string{{"m", "Arena.NewIn(p.Alloc())"}, {"c", "m.Sub()"}}, false},
}

// resetView is one thing S89 makes in c after its Reset: the setup lines
// (a line ending in " else" takes an else handler that frees the live
// arenas), the statement that reads it, and the teardown before c's Free.
type resetView struct {
	setup    []string
	read     string
	teardown []string
	arena    string // an arena the setup adds, freed in the handlers after it
	mem      bool   // needs import std.mem
}

// resetViews draws S89's view with literal l.
func resetViews(kind int, l string) resetView {
	switch kind {
	case 0:
		return resetView{setup: []string{"w := c.Put(i64(" + l + ")) else"}, read: "h = h *% 31 +% *w"}
	case 1:
		return resetView{setup: []string{"var l := List[i64].New(&c)", "l.Push(" + l + ") else"},
			read: "if x := l.Pop() {\n        h = h *% 31 +% x\n    }"}
	case 2:
		return resetView{setup: []string{"var pl := mem.Pool[i64].New(&c)", "g := pl.Add(" + l + ") else"},
			read: "if v := pl.Get(g) {\n        h = h *% 31 +% *v\n    }", mem: true}
	case 3:
		return resetView{setup: []string{"s := c.Fmt(\"mk\") else"}, read: "h = h *% 31 +% Cast[i64](s.Byte(0))"}
	}
	return resetView{setup: []string{"var d := c.Sub() else", "wd := d.Put(i64(" + l + ")) else"}, read: "h = h *% 31 +% *wd",
		teardown: []string{"d.Free()"}, arena: "d"}
}

// nResetViews is the number of resetViews kinds.
const nResetViews = 5

// resetForm is one S89 function: resetMakes[make], resetViews(view, lit),
// with the hole or not, content put in c before its Reset (pre, the
// literal pre's), and in the correct form p reset after c is freed (after).
type resetForm struct {
	make, view  int
	hole, after bool
	pre, lit    string
}

// resetBack is S89: Rbn as the file comment says, called from Run with the
// entry's arena or, one time in three, a sub-arena in scope.
func (g *sgen) resetBack() bool {
	if g.cur != nil || g.holeFull("Rb") {
		return false
	}
	name := g.holeName("Rb")
	f := resetForm{make: g.rng.intn(len(resetMakes)), view: g.rng.intn(nResetViews), lit: g.lit(), hole: g.holeDraw()}
	if g.chance(1, 3) {
		f.pre = g.lit()
	}
	f.after = !f.hole && g.chance(1, 2)
	if resetMakes[f.make].newIn {
		g.use(itemNewIn)
	}
	if resetViews(f.view, f.lit).mem {
		g.mem = true
	}
	g.hgen = append(g.hgen, resetText(name, f))
	g.usesA = true
	arena := "a"
	if p := g.pick(kArena, false); p != nil && g.chance(1, 3) {
		p.used = true
		arena = "&" + p.name
	}
	g.fold(fmt.Sprintf("%s(%s)", name, arena))
	return true
}

// resetText is S89's function name(a: &Arena): i64 of form f.
func resetText(name string, f resetForm) string {
	mk := resetMakes[f.make]
	v := resetViews(f.view, f.lit)
	var b strings.Builder
	var live []string // live arenas, innermost first
	line := func(s string) { b.WriteString("    " + s + "\n") }
	// orElse writes s, with an else handler freeing live when s ends in
	// " else".
	orElse := func(s string) {
		head, ok := strings.CutSuffix(s, " else")
		if !ok {
			line(s)
			return
		}
		line(head + " else {")
		for _, a := range live {
			line("    " + a + ".Free()")
		}
		line("    return h")
		line("}")
	}
	declare := func(name, init string) {
		orElse("var " + name + " := " + init + " else")
		live = append([]string{name}, live...)
	}
	line("var h: i64 = 0")
	declare("p", "a.Sub()")
	for _, d := range mk.decls {
		declare(d[0], d[1])
	}
	if f.pre != "" {
		// Content before the Reset, which the Reset ends.
		orElse("w0 := c.Put(i64(" + f.pre + ")) else")
		line("h = h *% 31 +% *w0")
	}
	line("c.Reset()")
	for _, s := range v.setup {
		orElse(s)
		if v.arena != "" && strings.HasPrefix(s, "var "+v.arena+" ") {
			live = append([]string{v.arena}, live...)
		}
	}
	sub := len(mk.decls) > 1
	if f.hole {
		if sub {
			line("m.Free()")
		}
		line("p.Reset()")
	}
	line(v.read)
	for _, s := range v.teardown {
		line(s)
	}
	line("c.Free()")
	if sub && !f.hole {
		line("m.Free()")
	}
	if f.after {
		line("p.Reset()")
	}
	line("p.Free()")
	line("return h")
	return fmt.Sprintf("%s(a: &Arena): i64 {\n%s}\n", name, b.String())
}
