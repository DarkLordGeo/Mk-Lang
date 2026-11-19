// Why: loops and conditionals carry most of the language's lowering rules:
// per-iteration temporaries, holders that keep the iterated value borrowed
// for the whole loop, element loans, and the check elisions a counted loop
// allows.
//
// What: ifStmt and ifBind (if, if x := e), forStmt, forBind, forIn,
// counterLoop, forSlice, sliceSource and forCounted (for in every form),
// labels via beginLoop and endLoop, and the write scans reassigned,
// mayWrite and indexWritten.
//
// How: a header evaluated once ends its other temporaries before the loop
// (endOnceTemps); the value iterated over lives for the whole loop. A
// counted loop's latch is c = AddNoWrap(c, 1) on a hidden counter the body
// cannot write.

package lower

import (
	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// ifStmt is if: a folded condition lowers only the taken
// branch; otherwise a bool switch, scoped arms and a join.
func (bl *builder) ifStmt(s *ast.IfStmt) {
	switch c := s.Cond.(type) {
	case *ast.BindCond:
		bl.ifBind(s, c)
		return
	case *ast.IsCond:
		bl.typeTest(s, c)
		return
	case ast.Expr:
		if l := bl.in.Literal(c.ID()); l.Kind == LitScalar && bl.tab.Kind(l.Type) == types.KBool {
			if l.Bits != 0 {
				bl.armBlock(s.Then, scopePlain)
			} else if s.Else != nil {
				bl.elseBranch(s.Else)
			}
			return
		}
		cond := bl.operand(c)
		then, els, join := bl.newBlock(), bl.newBlock(), bl.newBlock()
		bl.boolSwitch(cond, then, els, s.Span())
		reached := false
		bl.cur = then
		bl.armBlock(s.Then, scopePlain)
		reached = bl.joinTo(join, s.Span()) || reached
		bl.cur = els
		if s.Else != nil {
			bl.elseBranch(s.Else)
		}
		reached = bl.joinTo(join, s.Span()) || reached
		bl.continueAt(join, reached)
		return
	}
	source.ICEf(s.Span(), "lower: if condition %T", s.Cond)
}

func (bl *builder) elseBranch(n ast.Node) {
	switch e := n.(type) {
	case *ast.Block:
		bl.armBlock(e, scopePlain)
	case *ast.IfStmt:
		bl.pushFrame(e.Span())
		bl.ifStmt(e)
		bl.popFrame()
	}
}

// joinTo ends an open current block with a goto join and reports whether
// it did.
func (bl *builder) joinTo(join mir.BlockID, at source.Span) bool {
	if !bl.open() {
		return false
	}
	bl.gotoBlock(join, at)
	return true
}

// continueAt makes join current when some edge reached it.
func (bl *builder) continueAt(join mir.BlockID, reached bool) {
	if reached {
		bl.cur = join
	} else {
		bl.cur = mir.NoBlock
	}
}

// bindValue is the binding operand of x := e's payload: by value a move or
// copy, by reference a borrow.
type bindSrc struct {
	place mir.Place
	mode  BindMode
}

// condSource lowers the value of if x := e or for x := e: a borrow form
// (mut e, &e) uses e's place, a value goes into a temporary.
func (bl *builder) condSource(v ast.Expr) bindSrc {
	if u, ok := unparen(v).(*ast.UnaryExpr); ok && (u.Op == ast.TokMut || u.Op == ast.TokAmp) {
		mode := ByShared
		if u.Op == ast.TokMut {
			mode = ByMut
		}
		if bl.isPlaceExpr(u.X) {
			return bindSrc{bl.place(u.X), mode}
		}
		return bindSrc{bl.tempOf(u.X), mode}
	}
	if bl.isPlaceExpr(v) {
		p := bl.place(v)
		l := bl.newTemp(bl.b.PlaceType(p), v.Span())
		bl.assign(bl.b.Place(l), use(bl.valueOf(p, true, v.Span())), v.Span())
		return bindSrc{bl.b.Place(l), ByValue}
	}
	return bindSrc{bl.tempOf(v), ByValue}
}

// bindTo declares id from place p by mode.
func (bl *builder) bindTo(id *ast.Ident, p mir.Place, mode BindMode, at source.Span) {
	l := bl.declare(id)
	switch mode {
	case ByValue:
		bl.assign(bl.b.Place(l), use(bl.valueOf(p, true, at)), at)
	case ByShared:
		bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: p, Type: bl.b.Locals[l].Type}, at)
	case ByMut:
		bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: p, Type: bl.b.Locals[l].Type}, at)
	}
}

// ifBind is if x := e.
func (bl *builder) ifBind(s *ast.IfStmt, c *ast.BindCond) {
	src := bl.condSource(c.Value)
	d := bl.discr(src.place, c.Span())
	some, none, join := bl.newBlock(), bl.newBlock(), bl.newBlock()
	bl.switchOn(d, []mir.SwitchCase{{Value: 1, Target: some}}, none, c.Span())
	bl.cur = some
	bl.openScope(scopePlain)
	bl.bindTo(c.Name, bl.somePayload(src.place), src.mode, c.Name.Span())
	bl.armStmts(s.Then)
	bl.closeScope(closeBrace(s.Then))
	reached := bl.joinTo(join, s.Span())
	bl.cur = none
	if s.Else != nil {
		bl.elseBranch(s.Else)
	}
	reached = bl.joinTo(join, s.Span()) || reached
	bl.continueAt(join, reached)
}

// headerSpan is the for header: from the statement's start to its body.
func headerSpan(s *ast.ForStmt) source.Span {
	sp := s.Span()
	if b := s.Body.Span(); b.Start > sp.Start {
		sp.End = b.Start
		for sp.End > sp.Start+1 && sp.End-1 > sp.Start {
			break
		}
	}
	return sp
}

// beginLoop allocates the loop's info and pushes its context; the body
// scope opens after.
func (bl *builder) beginLoop(s *ast.ForStmt, header, cont, exit mir.BlockID) mir.LoopID {
	id := mir.LoopID(len(bl.b.Loops))
	var label = sourceSym(s.Label)
	bl.b.Loops = append(bl.b.Loops, mir.LoopInfo{Header: header, Exit: exit, Holder: mir.NoLocal, Label: label, Span: bl.span(headerSpan(s)), FieldsOf: types.NoType})
	bl.loops = append(bl.loops, loopCtx{id: id, label: label, cont: cont, exit: exit, depth: len(bl.scopes)})
	return id
}

// endLoop pops the loop and continues at its exit when anything reaches it.
func (bl *builder) endLoop(exitReached bool) {
	lc := bl.loops[len(bl.loops)-1]
	bl.loops = bl.loops[:len(bl.loops)-1]
	bl.continueAt(lc.exit, exitReached || lc.broken)
}

func (bl *builder) forStmt(s *ast.ForStmt) {
	hs := headerSpan(s)
	switch h := s.Head.(type) {
	case nil:
		header, body, exit := bl.newBlock(), bl.newBlock(), bl.newBlock()
		bl.gotoBlock(header, hs)
		bl.beginLoop(s, header, header, exit)
		bl.cur = header
		bl.gotoBlock(body, hs)
		bl.cur = body
		bl.block(s.Body, scopeLoopBody)
		bl.joinTo(header, closeBrace(s.Body))
		bl.endLoop(false)
	case *ast.InHead:
		bl.forIn(s, h)
	case *ast.BindCond:
		bl.forBind(s, h)
	case *ast.IsCond:
		source.ICEf(h.Span(), "lower: for T is X")
	case ast.Expr:
		header, body, exit := bl.newBlock(), bl.newBlock(), bl.newBlock()
		bl.gotoBlock(header, hs)
		bl.beginLoop(s, header, header, exit)
		bl.cur = header
		bl.pushFrame(hs)
		cond := bl.operand(h)
		hdr := bl.temps[len(bl.temps)-1].temps
		bl.loops[len(bl.loops)-1].hdr = hdr
		fail := bl.newBlock()
		bl.boolSwitch(cond, body, fail, hs)
		bl.cur = fail
		bl.endTemps(hdr, hs)
		bl.gotoBlock(exit, hs)
		bl.cur = body
		bl.block(s.Body, scopeLoopBody)
		if bl.open() {
			bl.endTemps(hdr, closeBrace(s.Body))
			bl.gotoBlock(header, closeBrace(s.Body))
		}
		bl.dropFrame()
		bl.endLoop(true)
	default:
		source.ICEf(s.Span(), "lower: for head %T", s.Head)
	}
}

func (bl *builder) endTemps(ts []mir.LocalID, at source.Span) {
	for k := len(ts) - 1; k >= 0; k-- {
		bl.scopeEnd(ts[k], at)
	}
}

// forBind is for x := e: the header temporary ends on the none
// edge and before the back edge.
func (bl *builder) forBind(s *ast.ForStmt, c *ast.BindCond) {
	hs := headerSpan(s)
	header, exit := bl.newBlock(), bl.newBlock()
	bl.gotoBlock(header, hs)
	bl.beginLoop(s, header, header, exit)
	bl.cur = header
	bl.pushFrame(hs)
	src := bl.condSource(c.Value)
	hdr := bl.temps[len(bl.temps)-1].temps
	bl.loops[len(bl.loops)-1].hdr = hdr
	d := bl.discr(src.place, hs)
	hdr = bl.temps[len(bl.temps)-1].temps
	bl.loops[len(bl.loops)-1].hdr = hdr
	some, none := bl.newBlock(), bl.newBlock()
	bl.switchOn(d, []mir.SwitchCase{{Value: 1, Target: some}}, none, hs)
	bl.cur = none
	bl.endTemps(hdr, hs)
	bl.gotoBlock(exit, hs)
	bl.cur = some
	bl.openScope(scopeLoopBody)
	bl.bindTo(c.Name, bl.somePayload(src.place), src.mode, c.Name.Span())
	bl.blockStmts(s.Body)
	bl.closeScope(closeBrace(s.Body))
	if bl.open() {
		bl.endTemps(hdr, closeBrace(s.Body))
		bl.gotoBlock(header, closeBrace(s.Body))
	}
	bl.dropFrame()
	bl.endLoop(true)
}

// forIn is for i in a..b and for x in xs, &xs, mut xs.
func (bl *builder) forIn(s *ast.ForStmt, h *ast.InHead) {
	hs := headerSpan(s)
	switch src := h.Source.(type) {
	case *ast.RangeSource:
		ct := bl.typeOf(src.Lo)
		bl.pushFrame(hs)
		lo := bl.operand(src.Lo)
		// The counter is assigned after hi runs, so a lo that
		// hi may write is read into a slot first (for i in n..Bump(mut n)
		// counts from the n read before Bump).
		if bl.readsLate(lo) && !bl.inertFor(src.Hi, lo.Place) {
			lo = bl.slot(lo, src.Lo.Span())
		}
		hiOp := bl.operand(src.Hi)
		hi := bl.newTemp(ct, hs)
		bl.assign(bl.b.Place(hi), use(hiOp), hs)
		c := bl.newTemp(ct, hs)
		bl.b.Locals[c].Var = false
		bl.assign(bl.b.Place(c), use(lo), hs)
		bl.endOnceTemps(hs, func(l mir.LocalID) bool { return l == hi || l == c })
		cc, elide := bl.counterLoopFor(s, h, src)
		bl.counterLoop(s, c, mir.Copy(bl.b.Place(hi)), func() {
			if !blank(h.Key) {
				l := bl.declare(h.Key)
				bl.assign(bl.b.Place(l), use(mir.Copy(bl.b.Place(c))), h.Key.Span())
			}
			if elide {
				bl.counters = append(bl.counters, cc)
			}
		})
		if elide {
			bl.counters = bl.counters[:len(bl.counters)-1]
		}
	case *ast.RefSource:
		bl.forSlice(s, h, src)
	case *ast.FieldsSource:
		bl.fieldsLoop(s, h, src)
	default:
		source.ICEf(hs, "lower: for source %T", h.Source)
	}
}

// counterLoop is the shape every counted loop shares: header c < n, body,
// latch c = AddNoWrap(c, 1) (no overflow check:
// the header tested c < n in c's own type and c is a hidden temp the body
// cannot write, so c + 1 <= n lies in the type and codegen may mark the add
// nsw or nuw).
func (bl *builder) counterLoop(s *ast.ForStmt, c mir.LocalID, n mir.Operand, bind func()) mir.LoopID {
	hs := headerSpan(s)
	ct := bl.b.Locals[c].Type
	header, body, latch, exit := bl.newBlock(), bl.newBlock(), bl.newBlock(), bl.newBlock()
	bl.gotoBlock(header, hs)
	id := bl.beginLoop(s, header, latch, exit)
	bl.cur = header
	k := bl.hiddenLocal(types.Bool, mir.LocalTemp, hs)
	bl.assign(bl.b.Place(k), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Lt, A: mir.Copy(bl.b.Place(c)), B: n, Type: types.Bool}, hs)
	bl.boolSwitch(mir.Copy(bl.b.Place(k)), body, exit, hs)
	bl.cur = body
	bl.openScope(scopeLoopBody)
	bind()
	bl.blockStmts(s.Body)
	bl.closeScope(closeBrace(s.Body))
	bl.joinTo(latch, closeBrace(s.Body))
	bl.cur = latch
	bl.assign(bl.b.Place(c), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.AddNoWrap, A: mir.Copy(bl.b.Place(c)), B: mir.Imm(ct, 1), Type: ct}, hs)
	bl.gotoBlock(header, hs)
	bl.endLoop(true)
	return id
}

// forSlice is for ... in over slices and arrays.
func (bl *builder) forSlice(s *ast.ForStmt, h *ast.InHead, src *ast.RefSource) {
	if c := bl.in.Call(src.ID()); c.Kind == CallFunc || c.Kind == CallIface {
		bl.forIter(s, h, src, c) // check recorded Next on the source
		return
	}
	hs := headerSpan(s)
	bl.pushFrame(hs) // the source's own temporaries, ended before the loop
	var base mir.Place
	isPlace := bl.isPlaceExpr(src.X)
	if isPlace {
		base = bl.place(src.X)
	} else {
		base = bl.tempOf(src.X)
		for n := bl.in.Adjust(src.X.ID()).Deref; n > 0; n-- {
			base = bl.deref(base, hs)
		}
	}
	bt := bl.tab.Get(bl.b.PlaceType(base))
	elem := bt.Elem
	loopID := mir.LoopID(len(bl.b.Loops))
	var iter mir.Place // the place indexed per iteration
	holder := mir.NoLocal
	implementer := bt.Kind == types.KNamed
	if implementer {
		if h.Pat != nil {
			elem = bl.in.Type(h.Pat.ID()) // check's loopVar records it
		} else {
			val := h.Key
			if h.Value != nil {
				val = h.Value
			}
			elem = bl.in.Binding(val.ID()).Type
			if src.Mode != ast.ByCopy {
				elem = bl.tab.Get(elem).Elem
			}
		}
		iter, holder = bl.itemsOf(base, elem, src.Mode == ast.ByMutRef, loopID, hs)
	} else if bt.Kind != types.KSlice && bt.Kind != types.KMutSlice && bt.Kind != types.KArray {
		// check accepts `for a in n` with n: int and types a Poison, with
		// no diagnostic: a check gap (typeOf's tagged ICE), not a form
		// lower is missing; any other source kind here is lower's.
		if h.Value != nil {
			bl.typeOf(h.Value)
		} else if h.Key != nil {
			bl.typeOf(h.Key)
		}
		source.ICEf(hs, "lower: for over a %v", bt.Kind)
	}
	switch {
	case implementer:
	default:
		bl.sliceSource(src, s.Body, base, bt, elem, isPlace, loopID, &iter, &holder, hs)
	}
	// The value iterated over lives for the whole loop: the indexed place's
	// root and the locals its index steps read, the holder, and the
	// materialized source value (a slice value, an array iterated by copy,
	// the array a view borrows, the implementer Items reads through).
	bl.endOnceTemps(hs, func(l mir.LocalID) bool {
		if l == iter.Local || l == holder || (!isPlace && l == base.Local) {
			return true
		}
		for _, p := range bl.b.Proj(iter) {
			if p.Kind == mir.ProjIndex && mir.LocalID(p.A) == l {
				return true
			}
		}
		return false
	})
	bl.forCounted(s, h, src, iter, elem, holder, loopID)
}

// endOnceTemps closes the frame pushed around a for header that runs once
// (a range's bounds, a for-in source). The value iterated
// over lives for the whole loop, every other temporary of the header lives
// one iteration; a header evaluated once, before the first iteration, so
// its other temporaries end here, before the loop's header block, so that
// no holder keeps a loan on one through the loop (E0506 is borrow's). The
// kept ones move to the enclosing frame and end at the loop's exit.
func (bl *builder) endOnceTemps(at source.Span, keep func(mir.LocalID) bool) {
	ts := bl.dropFrame()
	var kept, gone []mir.LocalID
	for _, t := range ts {
		if keep(t) {
			kept = append(kept, t)
		} else {
			gone = append(gone, t)
		}
	}
	if bl.open() {
		bl.endTemps(gone, at)
	}
	if n := len(bl.temps); n > 0 {
		bl.temps[n-1].temps = append(bl.temps[n-1].temps, kept...)
	}
}

// sliceSource is what runs before the loop for slices and arrays.
func (bl *builder) sliceSource(src *ast.RefSource, body *ast.Block, base mir.Place, bt *types.Type, elem types.TypeID, isPlace bool, loopID mir.LoopID, iterp *mir.Place, holderp *mir.LocalID, hs source.Span) {
	var iter mir.Place
	holder := mir.NoLocal
	root := rootName(src.X)
	switch src.Mode {
	case ast.ByCopy:
		switch {
		case bt.Kind == types.KMutSlice && isPlace && (bl.mayWrite(base) && (root == 0 || reassigned(body)[root]) || bl.indexWritten(base, body)):
			// The pointer or length may change in the loop: a shared view
			// holds it for the loop.
			st := bl.tab.Slice(elem)
			l := bl.newTemp(st, hs)
			bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Shared, Place: base, Type: st}, hs)
			iter = bl.b.Place(l)
		case bt.Kind == types.KMutSlice && isPlace:
			iter = base
		case bt.Kind == types.KArray && isPlace && !bl.indexWritten(base, body) && (!bl.mayWrite(base) || (root != 0 && !writtenNames(body)[root])):
			iter = base
		case isPlace:
			l := bl.newTemp(bl.b.PlaceType(base), hs)
			bl.assign(bl.b.Place(l), use(mir.Copy(base)), hs)
			iter = bl.b.Place(l)
		default:
			iter = base
		}
	case ast.ByRef:
		switch {
		case bt.Kind == types.KMutSlice && isPlace && (bl.mayWrite(base) && (root == 0 || reassigned(body)[root]) || bl.indexWritten(base, body)):
			// As for x in xs: a view holds the pointer and length the
			// loop's n was read from.
			st := bl.tab.Slice(elem)
			l := bl.newTemp(st, hs)
			bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Shared, Place: base, Type: st}, hs)
			iter = bl.b.Place(l)
		case bt.Kind == types.KArray:
			st := bl.tab.Slice(elem)
			l := bl.newTemp(st, hs)
			bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Shared, Place: base, Type: st}, hs)
			iter = bl.b.Place(l)
		case bt.Kind == types.KSlice && isPlace:
			l := bl.newTemp(bl.b.PlaceType(base), hs)
			bl.assign(bl.b.Place(l), use(mir.Copy(base)), hs)
			iter = bl.b.Place(l)
		default:
			iter = base
		}
	case ast.ByMutRef:
		ht := bl.tab.MutSlice(elem)
		holder = bl.b.AddLocal(mir.Local{Type: ht, Kind: mir.LocalLoopHolder, Loop: loopID, Span: bl.span(hs)})
		bl.temps[len(bl.temps)-1].temps = append(bl.temps[len(bl.temps)-1].temps, holder)
		bl.assign(bl.b.Place(holder), mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Exclusive, Place: base, Origin: mir.OriginLoopHolder, Loop: loopID, Type: ht}, hs)
		iter = bl.b.Place(holder)
	}
	*iterp, *holderp = iter, holder
}

// forCounted is the counted loop over iter, the element per mode.
func (bl *builder) forCounted(s *ast.ForStmt, h *ast.InHead, src *ast.RefSource, iter mir.Place, elem types.TypeID, holder mir.LocalID, loopID mir.LoopID) {
	hs := headerSpan(s)
	n := bl.newTemp(types.Int, hs)
	bl.assign(bl.b.Place(n), mir.Rvalue{Kind: mir.RvLen, Place: iter, Type: types.Int}, hs)
	c := bl.newTemp(types.Int, hs)
	bl.assign(bl.b.Place(c), use(mir.Imm(types.Int, 0)), hs)
	key, val := h.Key, h.Value
	if val == nil && h.Pat == nil {
		key, val = nil, h.Key
	}
	id := bl.counterLoop(s, c, mir.Copy(bl.b.Place(n)), func() {
		if !blank(key) {
			l := bl.declare(key)
			bl.assign(bl.b.Place(l), use(mir.Copy(bl.b.Place(c))), key.Span())
		}
		if h.Pat != nil {
			// The element is copied and destructured (plain, E0409).
			ep := bl.extend(iter, mir.Projection{Kind: mir.ProjIndex, A: uint32(c), Type: elem})
			bl.loopPattern(h.Pat, ep, false, h.Pat.Span())
			return
		}
		if blank(val) {
			return
		}
		ep := bl.extend(iter, mir.Projection{Kind: mir.ProjIndex, A: uint32(c), Type: elem})
		l := bl.declare(val)
		switch src.Mode {
		case ast.ByCopy:
			bl.assign(bl.b.Place(l), use(mir.Copy(ep)), val.Span())
		case ast.ByRef:
			bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: ep, Type: bl.b.Locals[l].Type}, val.Span())
		case ast.ByMutRef:
			bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: ep, Origin: mir.OriginLoopElement, Loop: loopID, Type: bl.b.Locals[l].Type}, val.Span())
		}
	})
	if id != loopID {
		source.ICEf(hs, "lower: loop numbering")
	}
	bl.b.Loops[id].Holder = holder
}

// rootName is the name a place expression's path starts at (a local or a
// parameter), or 0.
func rootName(e ast.Expr) source.Symbol {
	for {
		switch x := unparen(e).(type) {
		case *ast.Ident:
			return x.Name
		case *ast.FieldExpr:
			e = x.X
		default:
			return 0
		}
	}
}

// reassigned are the names a block may change as a whole, or through a
// field path of one: assignment targets, mut borrows and receivers whose
// path holds no index (the scan for a mut []T source: element writes
// xs[j] = v change neither pointer nor length).
func reassigned(b *ast.Block) map[source.Symbol]bool {
	out := map[source.Symbol]bool{}
	whole := func(e ast.Expr) {
		for {
			switch x := e.(type) {
			case *ast.Ident:
				out[x.Name] = true
				return
			case *ast.FieldExpr:
				e = x.X
			case *ast.ParenExpr:
				e = x.X
			case *ast.UnaryExpr:
				if x.Op != ast.TokStar {
					return
				}
				e = x.X
			default:
				return
			}
		}
	}
	ast.Inspect(b, func(n ast.Node) bool {
		switch x := n.(type) {
		case nil:
			return false
		case *ast.AssignStmt:
			whole(x.Target)
		case *ast.AsmStmt:
			for _, op := range x.Operands { // an asm output writes its place
				if op.Kind.IsDirection() && op.Kind != ast.AsmIn && op.Value != nil {
					whole(op.Value)
				}
			}
		case *ast.UnaryExpr:
			if x.Op == ast.TokMut {
				whole(x.X)
			}
		case *ast.CallExpr:
			if fe, ok := methodField(x.Fun); ok { // x.M or x.M[A]
				whole(fe.X)
			}
		}
		return true
	})
	return out
}

// mayWrite reports whether a place may change during its loop at all: a
// var local or a place through a deref; the scans above then decide.
func (bl *builder) mayWrite(p mir.Place) bool {
	if bl.b.Locals[p.Local].Var {
		return true
	}
	for _, s := range bl.b.Proj(p) {
		if s.Kind == mir.ProjDeref {
			return true
		}
	}
	return false
}

// indexWritten reports whether the body may write a var local that an
// Index step of the source place reads (for x in m[j] { j = 7 }). The step
// reads its local on every iteration, so such a write would move the loop
// to another element, past the bounds check and the length read once
// before it (the source is evaluated once). The source is
// then copied or viewed once. The scan is writtenNames over the local's
// name: a write needs the name (a mut borrow of it made before the loop
// and used in the body conflicts with the header's read, borrow's E05xx).
func (bl *builder) indexWritten(p mir.Place, body *ast.Block) bool {
	var w map[source.Symbol]bool
	for _, s := range bl.b.Proj(p) {
		if s.Kind != mir.ProjIndex || !bl.b.Locals[s.A].Var {
			continue
		}
		if w == nil {
			w = writtenNames(body)
		}
		if w[bl.b.Locals[s.A].Name] {
			return true
		}
	}
	return false
}

func sourceSym(id *ast.Ident) (s source.Symbol) {
	if id != nil {
		s = id.Name
	}
	return s
}
