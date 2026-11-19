// Why: release --pgo-gen builds an instrumented program whose run records
// how often each function is entered and each branch is taken, for a later
// --pgo build.
//
// What: planCounters (the counter walk that fixes a function's counter
// count n), the per-function counter array S.pgo (counterName,
// defineCounters), the entry and edge increments (bump, entryCounter, edge)
// and the support pack's std.rt.ProfileTable (profileTable).
//
// How: counter 0 is the entry, then one counter per outgoing edge of each
// conditional terminator (a TermSwitch, or a TermCall with an error edge). The
// table holds one ProfileRecord per instrumented function, sorted by key, with
// a checksum of the body's CFG.

package codegen

import (
	"sort"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// counterPlan is a function's counter layout: counter 0 is the entry;
// then, for each MIR block in order whose terminator is conditional (a
// TermSwitch, or a TermCall with an error edge), one counter per outgoing
// edge in mir.Successors order. base[b] is the first counter of block b's
// edges, or -1 when b's terminator is not counted. Trap edges are codegen's
// own and never counted. The walk reads only the block structure, which is
// what CFGChecksum hashes, so equal checksums give equal plans.
type counterPlan struct {
	n    int
	base []int32
}

// countedEdges is the number of counters a terminator gets: its successors
// when it is conditional, else 0.
func countedEdges(b *mir.Body, id mir.BlockID, buf []mir.BlockID) int {
	t := &b.Blocks[id].Term
	switch {
	case t.Kind == mir.TermSwitch:
		return len(b.Successors(id, buf[:0]))
	case t.Kind == mir.TermCall && t.HasErr && t.Spawn == mir.NoLocal:
		return 2
	case t.Kind == mir.TermTailCall:
		return 0 // no successor to count
	}
	return 0
}

// planCounters is the counter walk over an instance body.
func planCounters(b *mir.Body) counterPlan {
	p := counterPlan{n: 1, base: make([]int32, len(b.Blocks))}
	var buf []mir.BlockID
	for i := range b.Blocks {
		k := countedEdges(b, mir.BlockID(i), buf)
		if k == 0 {
			p.base[i] = -1
			continue
		}
		p.base[i] = int32(p.n)
		p.n += k
	}
	return p
}

// pgoChecksum is the record checksum of a body: the first 16 bytes of its
// CFGChecksum.
func pgoChecksum(b *mir.Body) source.Hash128 { return b.CFGChecksum().Trunc128() }

// counterName is the symbol of S's counter array.
func counterName(sym string) string { return sym + ".pgo" }

// counterAttrs are the attributes of a counter array, defined (init not
// zero) or declared: hidden external on ELF, dso_local on COFF, so the
// native support pack's ProfileTable reaches it in every pack, ThinLTO
// bitcode included (like the FuncTable descriptors).
func (g *packGen) counterAttrs(n int, define bool) llvm.GlobalAttrs {
	m := g.m
	t := m.Array(uint64(n), m.I(64))
	a := llvm.GlobalAttrs{Linkage: llvm.External, Visibility: llvm.Hidden, DSOLocal: true, Align: 8, Type: t}
	if g.cfg.Target.OS == types.OSWindows {
		a.Visibility = llvm.Default
	}
	if define {
		a.Init = m.ZeroInit(t)
	} else {
		a.Type = m.I(64)
	}
	return a
}

// fnPGO is one member's PGO state: its counter plan, its counter array
// (--pgo-gen), and the counts the profile holds for it (--pgo=), nil when it
// has none.
type fnPGO struct {
	plan   counterPlan
	cnt    llvm.Value
	gen    bool
	counts []uint64
}

// pgoOf computes Funcs[fi]'s PGO state before its header is defined: the
// plan when counters are added or a profile is applied, and the profile's
// counts (pgo_use.go). nil when neither is configured.
func (g *packGen) pgoOf(fi int) *fnPGO {
	if !g.cfg.PGOGen && g.cfg.PGO == nil {
		return nil
	}
	if p, ok := g.pgo[fi]; ok {
		return p
	}
	f := &g.u.Funcs[fi]
	p := &fnPGO{plan: planCounters(f.Body), gen: g.cfg.PGOGen}
	if g.cfg.PGO != nil {
		g.lookupProfile(f, p)
	}
	g.pgo[fi] = p
	return p
}

// defineCounters defines S.pgo for an instrumented member.
func (g *packGen) defineCounters(fi int, p *fnPGO) {
	gl := g.m.AddGlobal(counterName(g.u.Funcs[fi].Symbol), g.counterAttrs(p.plan.n, true))
	p.cnt = g.m.GlobalRef(gl)
}

// bump increments counter k of the current function in the current block:
// a plain load, add and store (racy across carriers by design).
func (fg *fnGen) bump(k int) {
	m := fg.g.m
	i64 := m.I(64)
	at := fg.gep(fg.pgo.cnt, uint64(k)*8)
	v := fg.cur.Load(i64, at, 8)
	fg.cur.Store(fg.cur.Bin(llvm.Add, v, m.ConstInt(i64, 1), 0), at, 8)
}

// entryCounter puts counter 0's increment between the prologue and MIR block
// 0, in its own block (block 0 may be a loop header); it returns the block
// the entry branches to.
func (fg *fnGen) entryCounter() *llvm.Block {
	if fg.pgo == nil || !fg.pgo.gen {
		return fg.blocks[0]
	}
	cnt := fg.newBlock()
	save := fg.cur
	fg.cur = cnt
	fg.bump(0)
	cnt.Br(fg.blocks[0])
	fg.cur = save
	return cnt
}

// edge is the block a counted terminator's successor j branches to: target
// itself, or with --pgo-gen a split block that increments the edge's
// counter first. Successors that are not counted go to target.
func (fg *fnGen) edge(j int, target *llvm.Block) *llvm.Block {
	if fg.pgo == nil || !fg.pgo.gen || fg.pgo.plan.base[fg.bi] < 0 {
		return target
	}
	split := fg.newBlock()
	save := fg.cur
	fg.cur = split
	fg.bump(int(fg.pgo.plan.base[fg.bi]) + j)
	split.Br(target)
	fg.cur = save
	return split
}

// bumpEdge increments the counter of successor j of the current block's
// terminator in the current block (the error split of a call, whose ok and
// error blocks are already per edge).
func (fg *fnGen) bumpEdge(j int) {
	if fg.pgo == nil || !fg.pgo.gen || fg.pgo.plan.base[fg.bi] < 0 {
		return
	}
	fg.bump(int(fg.pgo.plan.base[fg.bi]) + j)
}

// profileTable is the support pack's std.rt.ProfileTable(): []ProfileRecord
// in a --pgo-gen build: one record { key: str, sum: [16]u8, counts: []u64 }
// per member of every pack, sorted by key (the symbol), its counts the
// pack's S.pgo array; the runtime writes them in table order, which is
// canonical. Outside --pgo-gen the table is empty.
func (s *support) profileTable(g *packGen, sig *types.Signature, u *Unit) {
	if !s.cfg.PGOGen {
		s.emptySlice(g, sig, "mk.std.rt.ProfileTable")
		return
	}
	m := g.m
	i64 := m.I(64)
	rec := g.tt.Get(sig.Result).Elem
	l := g.layout(rec)
	if len(l.Fields) != 3 || l.Fields[0].Offset != 0 || l.Fields[1].Offset != 16 || l.Fields[2].Offset != 32 || l.Shape.Size != 48 {
		source.ICEf(sig.Span, "codegen: ProfileRecord is not { key: str, sum: [16]u8, counts: []u64 } at 0, 16, 32 (48 bytes)")
	}
	type entry struct {
		sym string
		sum source.Hash128
		n   int
	}
	var es []entry
	for p := range u.Packs {
		for _, fi := range u.Packs[p] {
			f := &u.Funcs[fi]
			if f.Body == nil {
				continue
			}
			es = append(es, entry{f.Symbol, pgoChecksum(f.Body), planCounters(f.Body).n})
		}
	}
	sort.Slice(es, func(i, j int) bool { return es[i].sym < es[j].sym })
	for i := 1; i < len(es); i++ {
		if es[i].sym == es[i-1].sym {
			source.ICEf(sig.Span, "codegen: two instrumented functions named %s", es[i].sym)
		}
	}
	recT := m.Struct(false, m.Ptr(), i64, m.Array(16, m.I(8)), m.Ptr(), i64)
	var vals []llvm.Value
	for _, e := range es {
		cnt := m.GlobalRef(m.AddGlobal(counterName(e.sym), g.counterAttrs(e.n, false)))
		vals = append(vals, m.ConstStruct(false, g.bytesGlobal([]byte(e.sym)), m.ConstInt(i64, uint64(len(e.sym))),
			m.Bytes(e.sum[:]), cnt, m.ConstInt(i64, uint64(e.n))))
	}
	fg := s.defineMk(g, sig, "mk.std.rt.ProfileTable")
	defer fg.done()
	out := fg.locals[mir.ReturnLocal]
	if len(vals) == 0 {
		fg.cur.Store(m.ConstIntToPtr(8), out.v, 8)
	} else {
		init := m.ConstArray(recT, vals...)
		table := m.AddGlobal("mk.support.profile", llvm.GlobalAttrs{Linkage: llvm.Private, Constant: true,
			UnnamedAddr: true, Align: 8, Type: init.Ty, Init: init})
		fg.cur.Store(m.GlobalRef(table), out.v, 8)
	}
	fg.cur.Store(m.ConstInt(i64, uint64(len(vals))), fg.gep(out.v, 8), 8)
	fg.ret()
}
