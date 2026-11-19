// Why: a fn literal that captures locals is safe only where its environment
// of borrowed locals cannot outlive them: as the whole argument of a const
// parameter. The rules for where it stands and what its body may do with a
// capture live together here.
//
// What: LitEnv and CaptureType (the environment), literalCaptures,
// captureSource and waitCaptures (typing it on both sides), judgeCaptureUses
// and captureWrite (E0609), judgeConstFnUses (E0705 for a misused const fn
// parameter), CallbackRecipe.
//
// How: the enclosing unit types the environment and publishes it through a
// captureRec; the literal's unit waits on it, then adds one non-var local
// per capture, so every place rule (fields, indexing, &, receivers,
// auto-deref) treats a capture as a local of type T_i.

package check

import (
	"sync"
	"time"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// LitEnv is a capturing literal's environment as the enclosing unit typed
// it.
type LitEnv struct {
	Lit  ast.NodeID   // the FnLit node in the enclosing unit
	Item types.ItemID // the literal item
	Ref  types.TypeID // &R, R the record of &T_i fields: the hidden parameter's type
	// Sources are the captured names as the enclosing unit sees them, in
	// field order: a UseLocal of a local, a parameter or, in a literal that
	// captures them itself, its own capture local.
	Sources []Use
}

// CaptureType is one field of a literal's environment: the captured name
// and its type T (the field is &T).
type CaptureType struct {
	Name source.Symbol
	Type types.TypeID
}

// captureRec is one capturing literal's handoff from its enclosing unit to
// its own unit. The enclosing unit writes it while typing the FnLit
// and closes done when its run ends, normally or by panic; the literal's
// unit waits on done, so the writes are ordered before its reads (Go memory
// model) and no lock is needed.
type captureRec struct {
	done  chan struct{}
	once  sync.Once
	set   bool // written once; later typings (a fields-loop copy) compare
	ok    bool
	types []types.TypeID
	// diag is the enclosing unit's diagnostic that leaves the literal with
	// no environment; the literal's unit carries it too, so it is never
	// lowered (diag.Order keeps one of the two equal diagnostics).
	diag  *diag.Diagnostic
	reads types.ReadSet
}

func (r *captureRec) close() { r.once.Do(func() { close(r.done) }) }

func (r *captureRec) closed() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// fail records that the literal gets no environment because of d (nil: an
// earlier error of the enclosing unit, filled in when it closes).
func (r *captureRec) fail(d *diag.Diagnostic) {
	if r.closed() || r.set {
		return
	}
	r.set, r.ok, r.diag = true, false, d
}

// captureRecOf is literal lit's record, made on first use by either side.
func (m *Module) captureRecOf(lit types.ItemID) *captureRec {
	m.capMu.Lock()
	defer m.capMu.Unlock()
	if m.caps == nil {
		m.caps = map[types.ItemID]*captureRec{}
	}
	r := m.caps[lit]
	if r == nil {
		r = &captureRec{done: make(chan struct{})}
		m.caps[lit] = r
	}
	return r
}

// captureWait bounds the literal unit's wait for its enclosing unit. The
// driver starts units in Units() order with a worker slot taken first, so
// the enclosing unit always runs; the bound turns a broken ordering
// into an ICE instead of a hang.
const captureWait = 2 * time.Minute

// closeLiteralRecords closes the records of the capturing literals this
// unit's item owns (run's defer): a record nothing wrote gets ok false and
// the unit's first diagnostic, so the literal unit accepts silently.
func (u *unit) closeLiteralRecords() {
	ii := u.m.byItem[u.it.ID]
	if ii == nil {
		return
	}
	for _, lit := range ii.Literals {
		li := u.m.byItem[lit]
		if li == nil || len(li.Captures) == 0 {
			continue
		}
		r := u.m.captureRecOf(lit)
		if !r.closed() {
			if !r.set {
				r.set, r.ok = true, false
			}
			if !r.ok && r.diag == nil && len(u.b.Diags) > 0 {
				r.diag = u.b.Diags[0]
			}
			if r.ok {
				// The enclosing unit's read set when it ended: the
				// literal's covers every fact its environment's types came
				// from. A copy, so the literal
				// unit never shares the enclosing body's slices.
				var rs types.ReadSet
				rs.Merge(&u.b.Reads)
				r.reads = rs
			}
		}
		r.close()
	}
}

// markConstLit records in set that the FnLit or name a is, after
// parentheses, the whole argument of a const parameter of a call that is not
// s.go's (where a literal may stand, and how a const fn parameter is
// forwarded): set is constLit for a parameter of fn type, constNotFn for
// any other.
func (u *unit) markConstLit(set *Bits, a ast.Expr) {
	for {
		p, ok := a.(*ast.ParenExpr)
		if !ok {
			break
		}
		a = p.X
	}
	switch a.(type) {
	case *ast.FnLit, *ast.Ident: // a literal may capture; a const fn parameter is forwarded
	default:
		return
	}
	if set.w == nil {
		*set = newBits(len(u.b.Types))
	}
	u.b.slot(a.ID())
	set.set(a.ID(), u.b.Base)
}

// literalCaptures types FnLit e of literal item it, which
// captures ii.Captures: E0609 where it is no const argument, else the
// environment typed and published.
func (u *unit) literalCaptures(e *ast.FnLit, it *types.Item, ii *resolve.ItemInfo) {
	rec := u.m.captureRecOf(it.ID)
	caps := ii.Captures
	if !u.constLit.Has(e.ID(), u.b.Base) {
		first := caps[0].First
		var names []string
		for _, c := range caps {
			names = append(names, u.c.cfg.Interner.Str(c.Name))
			if c.First.Start < first.Start {
				first = c.First
			}
		}
		code, text := u.litContext(e)
		d := u.err(diag.E0609, first, "a `fn` literal that reads `"+u.c.cfg.Interner.Str(caps[0].Name)+
			"` is no value: only a literal written directly as the argument of a `const` parameter may read the enclosing function's locals (here it is "+text+")").
			Fact("captured", diag.TextList(names...)).Text("reason", code).Loc("literal", e.Span()).In(diag.PhaseTypes)
		captureAlts(d, false)
		rec.fail(d)
		return
	}
	for _, c := range caps {
		name := u.c.cfg.Interner.Str(c.Name)
		switch {
		case c.FieldsLoop:
			d := u.err(diag.E0609, c.First, "the literal reads `"+name+"`, which this `fields` loop declares; the loop body is checked once per field, so its literals cannot capture what it declares").
				Fact("captured", diag.TextList(name)).Text("reason", "fields-loop").In(diag.PhaseTypes)
			captureAlts(d, false)
			rec.fail(d)
			return
		case c.ConstFn || u.constFnSource(c.Of):
			// Resolve's flag is syntactic; an alias of a fn type is only
			// seen in the real type.
			d := u.err(diag.E0705, c.First, "a `fn` literal cannot read the `const` parameter `"+name+"`: call it outside the literal, or give the literal what it needs as a parameter").
				Text("param", name).Text("use", "captured")
			rec.fail(d)
			return
		}
	}
	ts := make([]types.TypeID, len(caps))
	srcs := make([]Use, len(caps))
	fields := make([]types.RecordField, len(caps))
	for i, c := range caps {
		t, use := u.captureSource(c.Of, e)
		if t == types.Poison || t == types.NoType {
			// The source's type is unknown: this unit reported (or another
			// stage did) the error that hid it.
			rec.fail(nil)
			return
		}
		ts[i], srcs[i] = t, use
		fields[i] = types.RecordField{Name: c.Name, Type: u.tab.Ref(t)}
	}
	ref := u.tab.Ref(u.tab.Record(fields))
	if !rec.closed() {
		if !rec.set {
			rec.set, rec.ok, rec.types = true, true, ts
		} else if rec.ok && !sameTypes(rec.types, ts) {
			source.ICEf(e.Span(), "check: a capturing literal typed twice with different capture types")
		}
	}
	for _, env := range u.b.Envs {
		if env.Lit == e.ID() {
			return // a fields-loop copy types the literal again
		}
	}
	u.b.Envs = append(u.b.Envs, LitEnv{Lit: e.ID(), Item: it.ID, Ref: ref, Sources: srcs})
}

func sameTypes(a, b []types.TypeID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// captureSource is a capture's type T_i and its Use in this, the enclosing
// unit: a local's declared or inferred type, a parameter's, this
// literal's own capture, or, in a requires or ensures predicate, the owner
// function's parameter or result, which the predicate holds as its &P
// parameter: T_i is P, and the source is that &P local,
// which lower borrows through. A Ref this unit cannot map is an ICE at
// literal e: silent Poison would leave a valid program's literal with no
// environment.
func (u *unit) captureSource(of resolve.Ref, e *ast.FnLit) (types.TypeID, Use) {
	switch of.Kind {
	case resolve.RefLocal:
		id := u.local(LocalID(of.A))
		if types.ItemID(of.B) == u.it.ID && int(id) < len(u.b.Locals) {
			return u.b.Locals[id].Type, Use{Kind: UseLocal, Local: id}
		}
	case resolve.RefParam, resolve.RefResult:
		id := LocalID(of.A)
		if of.Kind == resolve.RefParam && types.ItemID(of.B) == u.it.ID && id >= 1 && int(id) < len(u.b.Locals) {
			return u.b.Locals[id].Type, Use{Kind: UseLocal, Local: id}
		}
		if t, id, ok := u.predicateLocal(of); ok {
			if id == 0 {
				return types.Poison, Use{}
			}
			return t, Use{Kind: UseLocal, Local: id}
		}
	case resolve.RefCapture:
		if types.ItemID(of.B) == u.it.ID && int(of.A) < len(u.b.Captures) {
			id := u.capBase + LocalID(of.A)
			return u.b.Captures[of.A].Type, Use{Kind: UseLocal, Local: id}
		}
	}
	source.ICEf(e.Span(), "check: a capture of %s %d of item %d in the unit of %s", of.Kind, of.A, of.B, u.it.Key)
	return types.Poison, Use{}
}

// constFnSource reports whether capture source of is a const parameter of
// fn type of the function that declares it, by its real type:
// this unit's own, or, in a contract predicate, its owner's.
func (u *unit) constFnSource(of resolve.Ref) bool {
	if of.Kind != resolve.RefParam || of.A == 0 {
		return false
	}
	var sig *types.Signature
	switch types.ItemID(of.B) {
	case u.it.ID:
		sig = u.b.Sig
	case u.it.Owner:
		if _, ok := u.b.Node.(*ast.Contract); ok {
			if fd, ok := u.c.cfg.Items.Get(u.it.Owner).Decl.(*types.FuncDecl); ok {
				sig = &fd.Sig
			}
		}
	}
	if sig == nil || int(of.A) > len(sig.Params) {
		return false
	}
	p := sig.Params[of.A-1]
	return p.Const && u.tab.Kind(p.Type) == types.KFn
}

// waitCaptures is the literal unit's side: wait for the enclosing
// unit, then add one non-var local per capture, of type T_i (Poison, with
// the enclosing unit's diagnostic carried, when there is no environment).
func (u *unit) waitCaptures(ii *resolve.ItemInfo) {
	rec := u.m.captureRecOf(u.it.ID)
	select {
	case <-rec.done:
	case <-time.After(captureWait):
		source.ICEf(u.it.Span, "check: capturing literal %s waited %v for its enclosing unit (Units() order)", u.it.Key, captureWait)
	}
	b := u.b
	if len(b.Locals) == 0 {
		b.Locals = make([]Local, 1)
	}
	u.capBase = LocalID(len(b.Locals))
	var fields []types.RecordField
	for i, c := range ii.Captures {
		t := types.Poison
		if rec.ok && i < len(rec.types) {
			t = rec.types[i]
		}
		b.Locals = append(b.Locals, Local{Name: c.Name, Type: t, Capture: uint32(i) + 1, Span: c.First})
		b.Captures = append(b.Captures, CaptureType{Name: c.Name, Type: t})
		fields = append(fields, types.RecordField{Name: c.Name, Type: u.tab.Ref(t)})
	}
	if !rec.ok {
		// No environment. The enclosing unit's diagnostic is carried when it
		// has one; with none, what hid the literal is outside that unit (a
		// parse error recovered around it, FuzzCheck 63976af49801fb2b), so
		// the captures stay Poison and accept silently. Such a
		// body is never lowered: a build stops at the parse error, and a
		// capture with no environment parameter is lower's ICE, never a
		// silent miscompile.
		if rec.diag != nil {
			b.Diags = append(b.Diags, rec.diag)
		}
		return
	}
	b.EnvRef = u.tab.Ref(u.tab.Record(fields))
	b.Reads.Merge(&rec.reads)
}

// captureOf is the capture a place expression's root names in this literal
// unit, through fields, indexing, parentheses and dereferences of references
// (never a raw pointer's, which is unsafe's); ok false otherwise.
func (u *unit) captureOf(e ast.Expr) (source.Symbol, *ast.Ident, bool) {
	if len(u.b.Captures) == 0 {
		return 0, nil, false
	}
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.FieldExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != ast.TokStar || u.tab.Kind(u.b.TypeOf(x.X.ID())) == types.KRawPtr {
				return 0, nil, false
			}
			e = x.X
		case *ast.Ident:
			if u.b.Uses == nil {
				return 0, nil, false
			}
			use := u.b.Uses[u.b.slot(x.ID())]
			if use.Kind != UseLocal || int(use.Local) >= len(u.b.Locals) || u.b.Locals[use.Local].Capture == 0 {
				return 0, nil, false
			}
			return u.b.Locals[use.Local].Name, x, true
		default:
			return 0, nil, false
		}
	}
}

// captureWrite is E0609 for a write, a mut borrow or a mut self call on a
// place rooted at a capture, reported at the path's root.
func (u *unit) captureWrite(place ast.Expr, reason string) bool {
	if u.b.EnvRef == types.NoType {
		return false // no environment: the enclosing unit's error explains it
	}
	name, root, ok := u.captureOf(place)
	if !ok {
		return false
	}
	n := u.c.cfg.Interner.Str(name)
	var msg string
	switch reason {
	case "write":
		msg = "the literal assigns `" + n + "`, which it captured; a capture is read through a shared reference"
	case "mut-self":
		msg = "the literal calls a `mut self` method on `" + n + "`, which it captured; a capture is read through a shared reference"
	default:
		msg = "the literal borrows `" + n + "` `mut`, which it captured; a capture is read through a shared reference"
	}
	d := u.err(diag.E0609, root.Span(), msg).Fact("captured", diag.TextList(n)).Text("reason", reason).In(diag.PhaseTypes)
	captureAlts(d, true)
	return true
}

// CallbackRecipe is the sentence every E0609 message ends with: the one
// way to keep a callback.
const CallbackRecipe = "; to store a callback, store a `fn` value and its plain state; see the cookbook's stored-callback recipe"

// captureAlts are the rewrites every E0609 form names.
func captureAlts(d *diag.Diagnostic, mutating bool) {
	d.Message += CallbackRecipe
	d.Alt("a struct holding what the callback needs, and a named `fn` that takes it").Alt("a loop instead of the callback")
	if mutating {
		d.Alt("pass the state `mut` through an explicit parameter")
	}
}

// litContext is the reason a capturing literal is no const argument:
// the context of FnLit e's nearest non-parenthesis ancestor in the unit.
func (u *unit) litContext(e *ast.FnLit) (code, text string) {
	var stack []ast.Node
	var parent, grand ast.Node
	ast.Inspect(u.b.Node, func(n ast.Node) bool {
		if parent != nil || grand != nil {
			return false
		}
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if n == ast.Node(e) {
			for i := len(stack) - 1; i >= 0; i-- {
				if _, ok := stack[i].(*ast.ParenExpr); ok {
					continue
				}
				parent = stack[i]
				if i > 0 {
					grand = stack[i-1]
				}
				break
			}
			if parent == nil {
				parent = u.b.Node
			}
			return false
		}
		stack = append(stack, n)
		return true
	})
	switch parent.(type) {
	case *ast.BindStmt, *ast.AssignStmt, *ast.DestructureStmt:
		return "bound", "bound to a name"
	case *ast.ReturnStmt:
		return "returned", "returned"
	case *ast.Arg:
		if c, ok := grand.(*ast.CallExpr); ok {
			if u.goCalls != nil && u.goCalls[c.ID()] {
				return "spawn", "in an `s.go` call"
			}
			if u.constructs(c) {
				return "stored", "stored in a value"
			}
		}
		if u.constNotFn.Has(e.ID(), u.b.Base) {
			// The callee may store or return a const F.
			return "argument", "passed to a `const` parameter whose type is not a `fn` type, which the callee may use as any value"
		}
		return "argument", "passed to a parameter that is not `const`"
	case *ast.RecField, *ast.RecordLit, *ast.ArrayLit:
		return "stored", "stored in a value"
	case *ast.IndexExpr:
		return "bracket", "a bracket argument"
	}
	return "value", "used as a value"
}

// constructs reports whether call c builds a value (a struct, `some`,
// `ok`): its arguments are stored, not passed. Read from resolution, since
// the literal is typed before the call's Use is recorded.
func (u *unit) constructs(c *ast.CallExpr) bool {
	id, ok := c.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	switch u.c.cfg.Interner.Str(id.Name) {
	case "some", "ok", "err":
		return true
	}
	if r := u.ref(id); r.Kind == resolve.RefItem {
		k := u.c.cfg.Items.Get(types.ItemID(r.A)).Kind
		return k == types.KindStruct || k == types.KindError
	}
	return false
}

// useOf is node id's Use, the zero Use when none was recorded.
func (u *unit) useOf(id ast.NodeID) Use {
	if u.b.Uses == nil {
		return Use{}
	}
	i := int(id) - int(u.b.Base)
	if i < 0 || i >= len(u.b.Uses) {
		return Use{}
	}
	return u.b.Uses[i]
}

// judgeCaptureUses walks a capturing literal's unit after its
// statements are typed: every maximal place path rooted at a capture
// (the root, then field, index, parenthesis and reference-dereference steps)
// is judged by its parent context. A mut borrow (unary mut, mut p[a..b],
// for .. in mut p, a captured mut T or mut []T passed on) is E0609
// mut-borrow, a mut self receiver E0609 mut-self, a non-Plain path in a value
// position E0609 move; a shared borrow, a &self receiver and a Plain copy are
// accepted. An assignment's place is captureWrite's, at the statement; a
// match scrutinee and a destructuring source are borrow's backstop's, which
// sees the bindings. A path through a raw pointer's dereference is unsafe's.
func (u *unit) judgeCaptureUses() {
	if u.b.EnvRef == types.NoType || len(u.b.Captures) == 0 {
		return
	}
	var stack []ast.Node
	type site struct {
		id    *ast.Ident
		chain []ast.Node // the ancestors, outermost first
	}
	var sites []site
	ast.Inspect(u.b.Node, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if id, ok := n.(*ast.Ident); ok {
			if use := u.useOf(id.ID()); use.Kind == UseLocal && int(use.Local) < len(u.b.Locals) && u.b.Locals[use.Local].Capture != 0 {
				sites = append(sites, site{id, append([]ast.Node(nil), stack...)})
			}
		}
		if _, ok := n.(*ast.FnLit); ok && n != u.b.Node {
			return false // a nested literal is its own unit
		}
		stack = append(stack, n)
		return true
	})
	for _, s := range sites {
		u.judgeCapturePath(s.id, s.chain)
	}
}

// judgeCapturePath climbs id's maximal place path through chain (its
// ancestors) and judges the path's parent context.
func (u *unit) judgeCapturePath(id *ast.Ident, chain []ast.Node) {
	var p ast.Expr = id
	i := len(chain) - 1
climb:
	for ; i >= 0; i-- {
		switch x := chain[i].(type) {
		case *ast.ParenExpr:
			p = x
		case *ast.FieldExpr:
			if x.X != p || u.useOf(x.ID()).Kind != UseField {
				break climb
			}
			p = x
		case *ast.IndexExpr:
			if x.X != p || len(x.Args) != 1 {
				break climb
			}
			p = x
		case *ast.UnaryExpr:
			if x.Op != ast.TokStar || x.X != p {
				break climb
			}
			if u.tab.Kind(u.b.TypeOf(p.ID())) == types.KRawPtr {
				return // a raw pointer's target is unsafe's
			}
			p = x
		default:
			break climb
		}
	}
	if i < 0 {
		return
	}
	var grand ast.Node
	if i > 0 {
		grand = chain[i-1]
	}
	t := u.b.TypeOf(p.ID())
	switch x := chain[i].(type) {
	case *ast.AssignStmt:
		if x.Target == p {
			return // captureWrite's, reported while typing the statement
		}
	case *ast.DestructureStmt, *ast.MatchStmt:
		return // the bindings decide: borrow's backstop
	case *ast.UnaryExpr:
		switch x.Op {
		case ast.TokMut:
			u.captureWrite(p, "mut-borrow")
			return
		case ast.TokAmp:
			return
		}
	case *ast.SliceExpr:
		if x.X == p {
			if g, ok := grand.(*ast.UnaryExpr); ok && g.Op == ast.TokMut {
				u.captureWrite(p, "mut-borrow")
			}
			return
		}
	case *ast.RefSource:
		if x.X == p {
			if x.Mode == ast.ByMutRef {
				u.captureWrite(p, "mut-borrow")
			}
			return
		}
	case *ast.FieldExpr:
		if x.X == p {
			if c, ok := grand.(*ast.CallExpr); ok && c.Fun == ast.Expr(x) {
				switch u.receiverMode(c, p) {
				case types.ByMut:
					u.captureWrite(p, "mut-self")
					return
				case types.ByShared:
					return
				}
				// a by-value self: a value position, below
			} else {
				return // a method named without a call (E0609 already) or a member read
			}
		}
	case *ast.CallExpr:
		if x.Fun == p {
			return // a call through a captured fn value reads it
		}
	case *ast.Arg:
		if k := u.tab.Kind(t); k == types.KMut || k == types.KMutSlice {
			u.captureWrite(p, "mut-borrow") // an implicit reborrow, or a move of a unique reference
			return
		}
	}
	if t == types.NoType || t == types.Poison || u.c.FactsIn(u.env, t).Kind == types.Plain {
		return // a copy
	}
	u.captureMove(p, t)
}

// receiverMode is how method call c takes its receiver p: by check's
// Adjust on p (&self, mut self), else by the callee's declared receiver (a
// slice passed to a Slice[T] method with no Adjust).
func (u *unit) receiverMode(c *ast.CallExpr, p ast.Expr) types.Receiver {
	if a := u.b.Adjust; a != nil {
		if i := int(p.ID()) - int(u.b.Base); i >= 0 && i < len(a) {
			switch a[i].Kind {
			case AdjRefMut:
				return types.ByMut
			case AdjRefShared:
				return types.ByShared
			case AdjCopyThrough:
				return types.ByValue
			}
		}
	}
	use := u.useOf(c.ID())
	if use.Item == types.NoItem {
		return types.ByShared
	}
	if d, ok := u.c.cfg.Items.Get(use.Item).Decl.(*types.FuncDecl); ok && d.Sig.Receiver != types.NoReceiver {
		return d.Sig.Receiver
	}
	return types.ByShared
}

// captureMove is E0609 move: a non-Plain capture in a value position.
func (u *unit) captureMove(p ast.Expr, t types.TypeID) {
	name, root, ok := u.captureOf(p)
	if !ok {
		return
	}
	n := u.c.cfg.Interner.Str(name)
	d := u.err(diag.E0609, root.Span(), "the literal moves `"+n+"`, which it captured; a capture is read through a shared reference, and only a plain one is copied").
		Fact("captured", diag.TextList(n)).Text("reason", "move").Text("type", u.str(t)).In(diag.PhaseTypes)
	captureAlts(d, false)
}

// constFnParams are the unit signature's const parameters of fn type, by
// local.
func (u *unit) constFnParams(sig *types.Signature) map[LocalID]source.Symbol {
	var out map[LocalID]source.Symbol
	if sig == nil {
		return nil
	}
	for i, p := range sig.Params {
		if p.Const && u.tab.Kind(p.Type) == types.KFn {
			if out == nil {
				out = map[LocalID]source.Symbol{}
			}
			out[LocalID(i+1)] = p.Name
		}
	}
	return out
}

// judgeConstFnUses checks the uses of const fn parameters over a body: a
// const parameter of fn type may only be called (not by s.go) or passed on
// as the whole argument of a const parameter of a call that is not s.go's;
// every other use is E0705 with the use's kind. A use inside a literal is
// a capture, E0705 captured (literalCaptures).
func (u *unit) judgeConstFnUses() {
	params := u.constFnParams(u.b.Sig)
	if len(params) == 0 {
		return
	}
	var stack []ast.Node
	ast.Inspect(u.b.Node, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if _, ok := n.(*ast.FnLit); ok && n != u.b.Node {
			return false
		}
		if id, ok := n.(*ast.Ident); ok {
			if use := u.useOf(id.ID()); use.Kind == UseLocal {
				if name, ok := params[use.Local]; ok {
					u.judgeConstFnUse(id, name, stack)
				}
			}
		}
		stack = append(stack, n)
		return true
	})
}

// judgeConstFnUse judges one use id of const fn parameter name, whose
// ancestors are chain.
func (u *unit) judgeConstFnUse(id *ast.Ident, name source.Symbol, chain []ast.Node) {
	var child ast.Node = id
	i := len(chain) - 1
	for ; i >= 0; i-- {
		if _, ok := chain[i].(*ast.ParenExpr); !ok {
			break
		}
		child = chain[i]
	}
	use := "value"
	if i >= 0 {
		var grand ast.Node
		if i > 0 {
			grand = chain[i-1]
		}
		switch x := chain[i].(type) {
		case *ast.CallExpr:
			if x.Fun == child {
				if u.goCalls != nil && u.goCalls[x.ID()] {
					use = "spawn"
					break
				}
				return // a call
			}
		case *ast.Arg:
			if u.constLit.Has(id.ID(), u.b.Base) {
				return // passed on as a const argument
			}
			use = "argument"
			if u.constNotFn.Has(id.ID(), u.b.Base) {
				// A const parameter whose type is a type parameter
				// takes the value but no environment.
				n := u.c.cfg.Interner.Str(name)
				u.err(diag.E0705, id.Span(), "`"+n+"` is a `const` parameter of `fn` type: its argument may be a literal that captures, so it can only be called, or passed on as the argument of a `const` parameter of `fn` type, and this `const` parameter's type is not a `fn` type").
					Text("param", n).Text("use", use)
				return
			}
			if c, ok := grand.(*ast.CallExpr); ok {
				if u.goCalls != nil && u.goCalls[c.ID()] {
					use = "spawn"
				} else if u.constructs(c) {
					use = "stored"
				}
			}
		case *ast.BindStmt, *ast.AssignStmt, *ast.DestructureStmt:
			use = "bound"
		case *ast.ReturnStmt:
			use = "returned"
		case *ast.RecField, *ast.RecordLit, *ast.ArrayLit:
			use = "stored"
		case *ast.BinaryExpr:
			if x.Op == ast.TokEqEq || x.Op == ast.TokNotEq {
				use = "compared"
			}
		}
	}
	n := u.c.cfg.Interner.Str(name)
	u.err(diag.E0705, id.Span(), "`"+n+"` is a `const` parameter of `fn` type: its argument may be a literal that captures, so it can only be called, or passed on as the argument of a `const` parameter").
		Text("param", n).Text("use", use)
}

// judgeContractConstFn is judgeConstFnUses's predicate form: a requires or
// ensures predicate that names a const fn parameter of its function is
// E0705 use predicate (the predicate receives &P per named parameter, and
// would call through it).
func (u *unit) judgeContractConstFn() {
	owner := u.it.Owner
	if owner == types.NoItem {
		return
	}
	fd, ok := u.c.cfg.Items.Get(owner).Decl.(*types.FuncDecl)
	if !ok {
		return
	}
	params := u.constFnParams(&fd.Sig)
	if len(params) == 0 {
		return
	}
	ast.Inspect(u.b.Node, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return n != nil
		}
		r := u.ref(id)
		if r.Kind != resolve.RefParam || types.ItemID(r.B) != owner {
			return true
		}
		if name, ok := params[LocalID(r.A)]; ok {
			nm := u.c.cfg.Interner.Str(name)
			u.err(diag.E0705, id.Span(), "a contract cannot name the `const` parameter `"+nm+"` of `fn` type: its argument may be a literal that captures").
				Text("param", nm).Text("use", "predicate")
		}
		return true
	})
}
