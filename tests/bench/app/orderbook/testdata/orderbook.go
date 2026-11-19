// orderbook, Go, std: the task of README.md, the replay of
// Dogfoods/orderbook (`orderbook replay FEED [--quiet] [--snapshot N]`).
// The feed is read by encoding/csv over a 64 KiB bufio.Reader, a record at
// a time (ReuseRecord, any field count), fields trimmed by
// strings.TrimSpace, numbers by strconv. Orders live in a slot pool (a
// slice of slots, each with a generation, and a slice of vacant slot
// indices used last in first out), named by (slot, generation) handles; a
// []uint64 bitset of live arrival numbers sized from the file (a third of
// its bytes, plus two); a map from client id to handle and a map set of
// the ids that are done. Each side of the book is a slice of price levels
// sorted by key (price times -1 for bids, times 1 for asks) in descending
// order, the best level last, so a new or emptied best level moves no
// other level; it is searched by sort.Search and kept sorted by
// slices.Insert and slices.Delete: Go's std has no ordered map, and this
// sorted slice is the labelled stand-in. The queues are plain slices, as
// Go's std has no ring buffer (container/ring is a circular linked list):
// a level's FIFO of handles is read at its front and advanced past each
// order it is done with (lvl.orders[1:]), the work queue is a stack whose
// top is its front, and the 8-fill tape is a window that slides along a
// fixed slice of 1,024 fills and is copied back to its start when it
// reaches the end, so a fill moves no other fill but once in 1,016.
// Stops wait in two
// container/heap min-heaps ordered by (key, arrival number). Trades are a
// slice of every fill, a slice of the five largest, an 8-fill tape, and a
// map of volume by price. The checksum is FNV-1a over bytes of 64-bit
// words, wrapping uint64 arithmetic. After the replay, the statistics run
// whether or not --quiet is given: a sorted copy of the fill prices for
// percentiles, the VWAPs of all fills and of each aggressor side (a copy
// partitioned in place), bars of 1,000 fills (running totals, a short last
// bar merged), the fill-size histogram and the volume by account (a map,
// then sort.Slice by account and sort.SliceStable by volume); the depth of
// 5 levels a side; the report's values; then the close, which counts the
// live orders left in each side's queues. Integers are int64 and int
// (64-bit), whose overflow wraps in Go: no value of a feed reaches it.
// Without --quiet the program prints the dogfood's text report but its
// "engine heap" line, which reads the dogfood's own counting allocator.
package main

import (
	"bufio"
	"container/heap"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type Side uint8

const (
	Buy Side = iota
	Sell
)

func (s Side) String() string {
	if s == Buy {
		return "Buy"
	}
	return "Sell"
}

type Kind uint8

const (
	Limit Kind = iota
	Market
	Stop
	StopLimit
)

func (k Kind) String() string {
	return [...]string{"Limit", "Market", "Stop", "StopLimit"}[k]
}

type ClientID struct{ Account, Serial uint32 }

func (c ClientID) String() string { return fmt.Sprintf("%d/%d", c.Account, c.Serial) }

type Order struct {
	ID                         ClientID
	Seq                        int
	Side                       Side
	Kind                       Kind
	Price, Stop, Qty, Left, Ts int64
}

type Fill struct {
	Taker, Maker       ClientID
	TakerSeq, MakerSeq int
	Side               Side
	Price, Qty, Ts     int64
}

// Command ops.
const (
	opAdd = iota
	opCancel
	opAmend
	opHalt
	opResume
	opBust
	opTrigger
)

type Command struct {
	Op                 uint8
	Side               Side
	Kind               Kind
	ID                 ClientID
	Account            uint32
	Ts, Qty, Price, St int64
	H                  Handle
}

// Reject codes, in the dogfood's order.
const (
	rDuplicate = iota
	rHalted
	rUnknown
	rAlreadyDone
	rNoLiquidity
	rBadQuantity
	rNothingToBust
	rejectKinds
)

var rejectNames = [rejectKinds]string{"duplicate id", "account halted", "unknown id", "already done", "no liquidity", "bad quantity", "nothing to bust"}

// ---- the slot pool

type Handle struct{ idx, gen uint32 }

type slot struct {
	gen  uint32
	used bool
	o    Order
}

type Pool struct {
	slots  []slot
	vacant []uint32
	n      int
}

func (p *Pool) Add(o Order) Handle {
	if k := len(p.vacant); k > 0 {
		i := p.vacant[k-1]
		p.vacant = p.vacant[:k-1]
		s := &p.slots[i]
		s.used, s.o = true, o
		p.n++
		return Handle{i, s.gen}
	}
	p.slots = append(p.slots, slot{used: true, o: o})
	p.n++
	return Handle{uint32(len(p.slots) - 1), 0}
}

func (p *Pool) Get(h Handle) *Order {
	if int(h.idx) >= len(p.slots) {
		return nil
	}
	s := &p.slots[h.idx]
	if !s.used || s.gen != h.gen {
		return nil
	}
	return &s.o
}

func (p *Pool) free(i uint32) {
	s := &p.slots[i]
	s.used = false
	s.gen++
	s.o = Order{}
	p.vacant = append(p.vacant, i)
	p.n--
}

func (p *Pool) Free(h Handle) bool {
	if p.Get(h) == nil {
		return false
	}
	p.free(h.idx)
	return true
}

func (p *Pool) Sweep(keep func(*Order) bool) int {
	n := 0
	for i := range p.slots {
		if p.slots[i].used && !keep(&p.slots[i].o) {
			p.free(uint32(i))
			n++
		}
	}
	return n
}

// ---- the order store

type Orders struct {
	pool  Pool
	flags []uint64
	nbits int
	index map[ClientID]Handle
	done  map[ClientID]struct{}
	seq   int
}

func newOrders(capacity int) *Orders {
	return &Orders{flags: make([]uint64, (capacity+63)/64), nbits: capacity, index: map[ClientID]Handle{}, done: map[ClientID]struct{}{}}
}

func (s *Orders) has(seq int) bool { return s.flags[seq>>6]&(1<<(seq&63)) != 0 }

func (s *Orders) set(seq int) {
	if seq >= s.nbits {
		panic("orders past the capacity")
	}
	s.flags[seq>>6] |= 1 << (seq & 63)
}

func (s *Orders) clear(seq int) { s.flags[seq>>6] &^= 1 << (seq & 63) }

func (s *Orders) Live(h Handle) *Order {
	o := s.pool.Get(h)
	if o == nil || !s.has(o.Seq) {
		return nil
	}
	return o
}

func (s *Orders) Add(o Order) Handle {
	o.Seq = s.seq
	h := s.pool.Add(o)
	s.index[o.ID] = h
	s.set(o.Seq)
	s.seq++
	delete(s.done, o.ID)
	return h
}

func (s *Orders) Reissue(o Order) Handle {
	o.Seq = s.seq
	h := s.pool.Add(o)
	if old, ok := s.index[o.ID]; ok {
		if prev := s.pool.Get(old); prev != nil {
			s.clear(prev.Seq)
		}
	}
	s.index[o.ID] = h
	s.set(o.Seq)
	s.seq++
	return h
}

func (s *Orders) Retire(h Handle) {
	o := s.pool.Get(h)
	if o == nil {
		return
	}
	s.clear(o.Seq)
	delete(s.index, o.ID)
	s.done[o.ID] = struct{}{}
}

func (s *Orders) LiveCount() int {
	c := 0
	for _, w := range s.flags {
		c += bits.OnesCount64(w)
	}
	if c != len(s.index) {
		panic("live flags and index disagree")
	}
	return c
}

func (s *Orders) Sweep() int {
	return s.pool.Sweep(func(o *Order) bool { return s.has(o.Seq) })
}

// ---- one side of the book

type Level struct {
	qty    int64
	live   int
	orders []Handle // the FIFO: the front is orders[0]
}

type entry struct {
	key int64
	lvl *Level
}

type Ladder struct {
	levels []entry // descending key: best price last, so a new or emptied best level moves nothing
	sign   int64
}

type Depth struct {
	Price, Qty int64
	Orders     int
}

func (l *Ladder) find(key int64) (int, bool) {
	i := sort.Search(len(l.levels), func(i int) bool { return l.levels[i].key <= key })
	return i, i < len(l.levels) && l.levels[i].key == key
}

func (l *Ladder) get(key int64) *Level {
	if i, ok := l.find(key); ok {
		return l.levels[i].lvl
	}
	return nil
}

func (l *Ladder) Best() (int64, bool) {
	if len(l.levels) == 0 {
		return 0, false
	}
	return l.levels[len(l.levels)-1].key * l.sign, true
}

func (l *Ladder) Crosses(price, limit int64) bool {
	if l.sign > 0 {
		return price <= limit
	}
	return price >= limit
}

func (l *Ladder) Rest(price, qty int64, h Handle) {
	key := price * l.sign
	i, ok := l.find(key)
	if ok {
		lvl := l.levels[i].lvl
		lvl.orders = append(lvl.orders, h)
		lvl.qty += qty
		lvl.live++
		return
	}
	lvl := &Level{qty: qty, live: 1, orders: []Handle{h}}
	l.levels = slices.Insert(l.levels, i, entry{key, lvl})
}

func (l *Ladder) drop(key int64) {
	if i, ok := l.find(key); ok {
		l.levels = slices.Delete(l.levels, i, i+1)
	}
}

func (l *Ladder) Withdraw(price, qty int64, h Handle) {
	key := price * l.sign
	lvl := l.get(key)
	if lvl == nil {
		return
	}
	lvl.qty -= qty
	lvl.live--
	if n := len(lvl.orders); n > 0 && lvl.orders[n-1] == h {
		lvl.orders = lvl.orders[:n-1]
	}
	if lvl.qty == 0 {
		l.drop(key)
	}
}

const compactSlack = 8

func (l *Ladder) Compact(price int64, book *Orders) {
	key := price * l.sign
	i, ok := l.find(key)
	if !ok {
		return
	}
	old := l.levels[i].lvl
	if len(old.orders) <= 2*old.live+compactSlack {
		return
	}
	fresh := &Level{qty: old.qty, live: old.live}
	for _, h := range old.orders {
		if book.Live(h) != nil {
			fresh.orders = append(fresh.orders, h)
		}
	}
	l.levels[i].lvl = fresh
}

type Expired struct {
	Orders int
	Qty    int64
}

func (l *Ladder) Close(book *Orders) Expired {
	var e Expired
	for _, x := range l.levels {
		e.Qty += x.lvl.qty
		for _, h := range x.lvl.orders {
			if book.Live(h) != nil {
				e.Orders++
			}
		}
	}
	l.levels = nil
	return e
}

func (l *Ladder) Match(book *Orders, taker Handle, now int64, out *Trades) {
	for {
		t := book.Live(taker)
		if t == nil || t.Left == 0 {
			return
		}
		limited := t.Kind == Limit || t.Kind == StopLimit
		if len(l.levels) == 0 {
			return
		}
		key := l.levels[len(l.levels)-1].key
		if limited && !l.Crosses(key*l.sign, t.Price) {
			return
		}
		l.sweep(key, book, taker, now, out)
	}
}

func (l *Ladder) sweep(key int64, book *Orders, taker Handle, now int64, out *Trades) {
	price := key * l.sign
	lvl := l.get(key)
	if lvl == nil {
		return
	}
	// The front order is read in place and the FIFO advanced past it once
	// it is dead, the taker itself, or filled: a partly filled maker keeps
	// its place at the front.
	for lvl.qty > 0 && len(lvl.orders) > 0 {
		maker := lvl.orders[0]
		if book.Live(maker) == nil {
			lvl.orders = lvl.orders[1:]
			continue
		}
		t, m := book.pool.Get(taker), book.pool.Get(maker)
		if t == nil || m == nil || taker == maker {
			lvl.orders = lvl.orders[1:]
			continue
		}
		q := min(t.Left, m.Left)
		t.Left -= q
		m.Left -= q
		lvl.qty -= q
		out.Record(Fill{Taker: t.ID, Maker: m.ID, TakerSeq: t.Seq, MakerSeq: m.Seq, Side: t.Side, Price: price, Qty: q, Ts: now})
		if m.Left == 0 {
			lvl.orders = lvl.orders[1:]
			lvl.live--
			book.Retire(maker)
		}
		if t.Left == 0 {
			break
		}
	}
	if lvl.qty == 0 {
		l.drop(key)
	}
}

func (l *Ladder) Top(n int) []Depth {
	var out []Depth
	for i := len(l.levels) - 1; i >= 0 && len(out) < n; i-- {
		x := l.levels[i]
		out = append(out, Depth{x.key * l.sign, x.lvl.qty, x.lvl.live})
	}
	return out
}

func (l *Ladder) Quantity() int64 {
	var s int64
	for _, x := range l.levels {
		s += x.lvl.qty
	}
	return s
}

// ---- trades and the checksum

const (
	tapeLength = 8
	tapeWindow = 1024 // the slice the tape's window slides along
	topFills   = 5
	fnvBasis   = 0xcbf29ce484222325
	fnvPrime   = 0x100000001b3
)

func mix(h, x uint64) uint64 {
	for range 8 {
		h = (h ^ (x & 0xff)) * fnvPrime
		x >>= 8
	}
	return h
}

func mixInt(h uint64, x int64) uint64 { return mix(h, uint64(x)) }

type Trades struct {
	fills    []Fill
	top      []Fill
	tape     []Fill // the newest last, at most tapeLength: a window of tapeBuf
	tapeBuf  []Fill // tapeWindow fills, the array the window slides along
	volume   map[int64]int64
	last     int64
	hasLast  bool
	traded   int64
	notional int64
	busted   int
	checksum uint64
}

func (t *Trades) Record(f Fill) {
	t.fills = append(t.fills, f)
	at := sort.Search(len(t.top), func(i int) bool { return t.top[i].Qty < f.Qty })
	if at < topFills {
		t.top = slices.Insert(t.top, at, f)
		if len(t.top) > topFills {
			t.top = t.top[:topFills]
		}
	}
	if t.tapeBuf == nil {
		t.tapeBuf = make([]Fill, tapeWindow)
		t.tape = t.tapeBuf[:0]
	}
	if len(t.tape) == tapeLength {
		if len(t.tape) == cap(t.tape) {
			// the window is at the end of tapeBuf: back to its start
			n := copy(t.tapeBuf, t.tape[1:])
			t.tape = t.tapeBuf[:n]
		} else {
			t.tape = t.tape[1:]
		}
	}
	t.tape = append(t.tape, f)
	t.volume[f.Price] += f.Qty
	t.last, t.hasLast = f.Price, true
	t.traded += f.Qty
	t.notional += f.Qty * f.Price
	t.checksum = mixInt(mixInt(mixInt(mix(mix(t.checksum, uint64(f.TakerSeq)), uint64(f.MakerSeq)), f.Price), f.Qty), f.Ts)
}

func (t *Trades) Bust() bool {
	n := len(t.fills)
	if n == 0 {
		return false
	}
	f := t.fills[n-1]
	t.fills = t.fills[:n-1]
	if k := len(t.tape); k > 0 {
		t.tape = t.tape[:k-1]
	}
	if at := slices.Index(t.top, f); at >= 0 {
		t.top = slices.Delete(t.top, at, at+1)
	}
	if v, ok := t.volume[f.Price]; ok {
		if v-f.Qty == 0 {
			delete(t.volume, f.Price)
		} else {
			t.volume[f.Price] = v - f.Qty
		}
	}
	t.traded -= f.Qty
	t.notional -= f.Qty * f.Price
	t.busted++
	t.checksum = mix(t.checksum, 0xb057)
	return true
}

// ---- the engine

type stopEntry struct {
	key int64
	seq int
	h   Handle
}

type stopHeap []stopEntry

func (s stopHeap) Len() int { return len(s) }
func (s stopHeap) Less(i, j int) bool {
	if s[i].key != s[j].key {
		return s[i].key < s[j].key
	}
	return s[i].seq < s[j].seq
}
func (s stopHeap) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s *stopHeap) Push(x any)   { *s = append(*s, x.(stopEntry)) }
func (s *stopHeap) Pop() any {
	old := *s
	x := old[len(old)-1]
	*s = old[:len(old)-1]
	return x
}

type Snapshot struct {
	Event, Live, Fills int
}

const sweepEvery = 4096

type Engine struct {
	bids, asks          Ladder
	orders              *Orders
	log                 Trades
	buyStops, sellStops stopHeap
	work                []Command // a stack: its top, the last element, is the front of the work queue
	fired               []Handle
	halted              map[uint32]struct{}
	rejects             [rejectKinds]int
	snap                Snapshot
	every               int
	now                 int64
	events, cancels     int
	swept               int
}

func newEngine(capacity, every int) *Engine {
	return &Engine{
		bids:   Ladder{sign: -1},
		asks:   Ladder{sign: 1},
		orders: newOrders(capacity),
		log:    Trades{volume: map[int64]int64{}, checksum: fnvBasis},
		halted: map[uint32]struct{}{},
		every:  every,
	}
}

func (e *Engine) Submit(c Command) {
	e.work = append(e.work[:0], c)
	for len(e.work) > 0 {
		c := e.work[len(e.work)-1]
		e.work = e.work[:len(e.work)-1]
		e.apply(c)
	}
	e.events++
	if e.events%sweepEvery == 0 {
		e.swept += e.orders.Sweep()
	}
	if e.every > 0 && e.events%e.every == 0 {
		e.snap = Snapshot{Event: e.events, Live: e.orders.LiveCount(), Fills: len(e.log.fills)}
	}
}

func (e *Engine) apply(c Command) {
	switch c.Op {
	case opAdd:
		e.now = c.Ts
		e.add(Order{ID: c.ID, Side: c.Side, Kind: c.Kind, Price: c.Price, Stop: c.St, Qty: c.Qty, Left: c.Qty, Ts: c.Ts})
	case opCancel:
		e.now = c.Ts
		e.cancel(c.ID)
	case opAmend:
		e.now = c.Ts
		e.amend(c.Ts, c.ID, c.Qty, c.Price)
	case opHalt:
		e.now = c.Ts
		e.halted[c.Account] = struct{}{}
	case opResume:
		e.now = c.Ts
		delete(e.halted, c.Account)
	case opBust:
		e.now = c.Ts
		if !e.log.Bust() {
			e.refuse(rNothingToBust)
		}
	case opTrigger:
		e.fire(c.H)
	}
}

func (e *Engine) refuse(code int) {
	e.rejects[code]++
	e.log.checksum = mix(e.log.checksum, 0x7e00+uint64(code))
}

func (e *Engine) add(o Order) {
	if o.Qty <= 0 || (o.Kind != Market && o.Kind != Stop && o.Price <= 0) {
		e.refuse(rBadQuantity)
		return
	}
	if _, ok := e.halted[o.ID.Account]; ok {
		e.refuse(rHalted)
		return
	}
	if _, ok := e.orders.index[o.ID]; ok {
		e.refuse(rDuplicate)
		return
	}
	h := e.orders.Add(o)
	if o.Kind == Stop || o.Kind == StopLimit {
		e.arm(h)
		return
	}
	e.execute(h)
}

func (e *Engine) arm(h Handle) {
	p := e.orders.Live(h)
	if p == nil {
		return
	}
	o := *p
	if e.log.hasLast {
		last := e.log.last
		if (o.Side == Buy && last >= o.Stop) || (o.Side == Sell && last <= o.Stop) {
			e.fire(h)
			return
		}
	}
	if o.Side == Buy {
		heap.Push(&e.buyStops, stopEntry{o.Stop, o.Seq, h})
	} else {
		heap.Push(&e.sellStops, stopEntry{-o.Stop, o.Seq, h})
	}
}

func (e *Engine) fire(h Handle) {
	if e.orders.Live(h) == nil {
		return
	}
	o := e.orders.pool.Get(h)
	if o.Kind == StopLimit {
		o.Kind = Limit
	} else {
		o.Kind = Market
	}
	e.execute(h)
}

func (e *Engine) execute(h Handle) {
	before := len(e.log.fills)
	p := e.orders.Live(h)
	if p == nil {
		return
	}
	side := p.Side
	if side == Buy {
		e.asks.Match(e.orders, h, e.now, &e.log)
	} else {
		e.bids.Match(e.orders, h, e.now, &e.log)
	}
	p = e.orders.Live(h)
	if p == nil {
		return
	}
	o := *p
	switch {
	case o.Left == 0:
		e.orders.Retire(h)
	case o.Kind == Limit:
		if side == Buy {
			e.bids.Rest(o.Price, o.Left, h)
		} else {
			e.asks.Rest(o.Price, o.Left, h)
		}
	default:
		// The rest of a market order is cancelled.
		e.orders.Retire(h)
		if len(e.log.fills) == before {
			e.orders.pool.Free(h)
			e.refuse(rNoLiquidity)
		}
	}
	if len(e.log.fills) > before {
		e.triggers()
	}
}

func (e *Engine) triggers() {
	if !e.log.hasLast {
		return
	}
	last := e.log.last
	for len(e.buyStops) > 0 && e.buyStops[0].key <= last {
		x := heap.Pop(&e.buyStops).(stopEntry)
		if e.orders.Live(x.h) != nil {
			e.fired = append(e.fired, x.h)
		}
	}
	for len(e.sellStops) > 0 && e.sellStops[0].key <= -last {
		x := heap.Pop(&e.sellStops).(stopEntry)
		if e.orders.Live(x.h) != nil {
			e.fired = append(e.fired, x.h)
		}
	}
	// Pushed last to first, so the first fired order is on top: the front.
	for i := len(e.fired) - 1; i >= 0; i-- {
		e.work = append(e.work, Command{Op: opTrigger, H: e.fired[i]})
	}
	e.fired = e.fired[:0]
}

func (e *Engine) doneOrUnknown(id ClientID) {
	if _, ok := e.orders.done[id]; ok {
		e.refuse(rAlreadyDone)
	} else {
		e.refuse(rUnknown)
	}
}

func (e *Engine) cancel(id ClientID) {
	h, ok := e.orders.index[id]
	if !ok {
		e.doneOrUnknown(id)
		return
	}
	if p := e.orders.Live(h); p != nil {
		o := *p
		e.orders.Retire(h)
		if o.Kind == Limit {
			l := &e.asks
			if o.Side == Buy {
				l = &e.bids
			}
			l.Withdraw(o.Price, o.Left, h)
			l.Compact(o.Price, e.orders)
		}
	}
	e.cancels++
}

func (e *Engine) amend(ts int64, id ClientID, qty, price int64) {
	h, ok := e.orders.index[id]
	if !ok {
		e.doneOrUnknown(id)
		return
	}
	if qty <= 0 || price <= 0 {
		e.refuse(rBadQuantity)
		return
	}
	p := e.orders.Live(h)
	if p == nil {
		return
	}
	o := *p
	if o.Kind == Limit {
		if o.Side == Buy {
			e.bids.Withdraw(o.Price, o.Left, h)
		} else {
			e.asks.Withdraw(o.Price, o.Left, h)
		}
	}
	// An amended order is a new arrival.
	o.Qty, o.Left, o.Ts = qty, qty, ts
	if o.Kind == Stop {
		o.Stop = price
	} else {
		o.Price = price
	}
	h2 := e.orders.Reissue(o)
	if o.Kind == Stop || o.Kind == StopLimit {
		e.arm(h2)
		return
	}
	e.execute(h2)
}

func (e *Engine) Checksum() uint64 {
	h := e.log.checksum
	bb, _ := e.bids.Best()
	ba, _ := e.asks.Best()
	h = mixInt(h, bb)
	h = mixInt(h, ba)
	h = mixInt(h, e.bids.Quantity())
	h = mixInt(h, e.asks.Quantity())
	return mix(h, uint64(e.orders.LiveCount()))
}

// ---- the feed

var errRecord = errors.New("bad record")

func parsePrice(s string) (int64, bool) {
	whole, frac, dot := strings.Cut(s, ".")
	w, ok := digits(whole)
	if !ok {
		return 0, false
	}
	var c int64
	if dot {
		if len(frac) == 0 || len(frac) > 2 {
			return 0, false
		}
		if c, ok = digits(frac); !ok {
			return 0, false
		}
		if len(frac) == 1 {
			c *= 10
		}
	}
	if w > 9_999_999_999_999 {
		return 0, false
	}
	return w*100 + c, true
}

func digits(s string) (int64, bool) {
	if len(s) == 0 || len(s) > 15 {
		return 0, false
	}
	var n int64
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < '0' || b > '9' {
			return 0, false
		}
		n = n*10 + int64(b-'0')
	}
	return n, true
}

func account(s string) (uint32, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > 0xffffffff {
		return 0, false
	}
	return uint32(n), true
}

func clientID(s string) (ClientID, bool) {
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return ClientID{}, false
	}
	x, ok1 := account(a)
	y, ok2 := account(b)
	return ClientID{x, y}, ok1 && ok2
}

// parse turns one record into a command; ok is false for a comment or a
// blank record.
func parse(rec []string) (c Command, ok bool, err error) {
	count := len(rec)
	var cols [7]string
	for i, f := range rec {
		f = strings.TrimSpace(f)
		if i == 0 && strings.HasPrefix(f, "#") {
			return c, false, nil
		}
		if i < 7 {
			cols[i] = f
		}
	}
	if count == 0 || count == 1 && cols[0] == "" {
		return c, false, nil
	}
	want := 0
	switch cols[0] {
	case "X":
		want = 2
	case "C", "H", "R":
		want = 3
	case "M", "A":
		want = 5
	case "N", "S":
		want = 6
	case "T":
		want = 7
	}
	if want == 0 || count != want {
		return c, false, errRecord
	}
	ts, e1 := strconv.ParseInt(cols[1], 10, 64)
	if e1 != nil {
		return c, false, errRecord
	}
	c.Ts = ts
	switch cols[0] {
	case "X":
		c.Op = opBust
		return c, true, nil
	case "H", "R":
		a, ok := account(cols[2])
		if !ok {
			return c, false, errRecord
		}
		c.Op, c.Account = opResume, a
		if cols[0] == "H" {
			c.Op = opHalt
		}
		return c, true, nil
	}
	id, good := clientID(cols[2])
	if !good {
		return c, false, errRecord
	}
	c.ID = id
	if cols[0] == "C" {
		c.Op = opCancel
		return c, true, nil
	}
	if cols[0] == "A" {
		q, e2 := strconv.ParseInt(cols[3], 10, 64)
		p, good := parsePrice(cols[4])
		if e2 != nil || !good {
			return c, false, errRecord
		}
		c.Op, c.Qty, c.Price = opAmend, q, p
		return c, true, nil
	}
	switch cols[3] {
	case "B":
		c.Side = Buy
	case "S":
		c.Side = Sell
	default:
		return c, false, errRecord
	}
	q, e2 := strconv.ParseInt(cols[4], 10, 64)
	if e2 != nil {
		return c, false, errRecord
	}
	c.Op, c.Qty = opAdd, q
	good = true
	switch cols[0] {
	case "N":
		c.Kind = Limit
		c.Price, good = parsePrice(cols[5])
	case "M":
		c.Kind = Market
	case "S":
		c.Kind = Stop
		c.St, good = parsePrice(cols[5])
	case "T":
		var g2 bool
		c.Kind = StopLimit
		c.St, good = parsePrice(cols[5])
		c.Price, g2 = parsePrice(cols[6])
		good = good && g2
	}
	if !good {
		return c, false, errRecord
	}
	return c, true, nil
}

// ---- the statistics

type Bar struct {
	Open, High, Low, Close, Volume int64
	Trades                         int
}

type AccountRow struct {
	Account      uint32
	Bought, Sold int64
	Fills        int
}

type Summary struct {
	P50, P90, P99, Vwap, BuyVwap, SellVwap optPrice
	AtOrBelow                              int
	Bars                                   []Bar
	Sizes                                  [7]int
	Accounts                               []AccountRow
}

type optPrice struct {
	v  int64
	ok bool
}

func percentile(sorted []int64, p int) optPrice {
	if len(sorted) == 0 {
		return optPrice{}
	}
	rank := (len(sorted)*p + 99) / 100
	at := max(rank-1, 0)
	return optPrice{sorted[at], true}
}

func vwap(fills []Fill) optPrice {
	var q, n int64
	for i := range fills {
		q += fills[i].Qty
		n += fills[i].Qty * fills[i].Price
	}
	if q == 0 {
		return optPrice{}
	}
	return optPrice{n / q, true}
}

var sizeLimits = [6]int64{10, 50, 100, 500, 1000, 5000}

func summarize(fills []Fill, perBar int) Summary {
	var s Summary
	n := len(fills)
	prices := make([]int64, n)
	for i := range fills {
		prices[i] = fills[i].Price
	}
	sorted := make([]int64, n)
	copy(sorted, prices)
	slices.Sort(sorted)
	s.Vwap = vwap(fills)
	if s.Vwap.ok {
		s.AtOrBelow = sort.Search(n, func(i int) bool { return sorted[i] > s.Vwap.v })
	}
	xs := make([]Fill, n)
	copy(xs, fills)
	cut := 0
	for i := range xs {
		if xs[i].Side == Buy {
			xs[i], xs[cut] = xs[cut], xs[i]
			cut++
		}
	}
	s.BuyVwap, s.SellVwap = vwap(xs[:cut]), vwap(xs[cut:])
	s.P50, s.P90, s.P99 = percentile(sorted, 50), percentile(sorted, 90), percentile(sorted, 99)
	s.Bars = bars(fills, perBar)
	for i := range fills {
		class := 0
		for _, l := range sizeLimits {
			if fills[i].Qty <= l {
				break
			}
			class++
		}
		s.Sizes[class]++
	}
	s.Accounts = accounts(fills)
	return s
}

func bars(fills []Fill, per int) []Bar {
	var out []Bar
	if len(fills) == 0 || per < 1 {
		return out
	}
	volumes := make([]int64, len(fills))
	for i := range fills {
		volumes[i] = fills[i].Qty
	}
	for lo := 0; lo < len(volumes); lo += per {
		chunk := volumes[lo:min(lo+per, len(volumes))]
		var sum int64
		for i := range chunk {
			sum += chunk[i]
			chunk[i] = sum
		}
	}
	for lo := 0; lo < len(fills); lo += per {
		hi := min(lo+per, len(fills))
		c := fills[lo:hi]
		b := Bar{Open: c[0].Price, High: c[0].Price, Low: c[0].Price, Close: c[len(c)-1].Price, Volume: volumes[hi-1], Trades: len(c)}
		for i := range c {
			b.High = max(b.High, c[i].Price)
			b.Low = min(b.Low, c[i].Price)
		}
		out = append(out, b)
	}
	if n := len(out); n >= 2 && out[n-1].Trades*2 < per {
		prev, last := &out[n-2], out[n-1]
		prev.High = max(prev.High, last.High)
		prev.Low = min(prev.Low, last.Low)
		prev.Close = last.Close
		prev.Volume += last.Volume
		prev.Trades += last.Trades
		out = out[:n-1]
	}
	return out
}

func accounts(fills []Fill) []AccountRow {
	by := map[uint32]*AccountRow{}
	row := func(a uint32) *AccountRow {
		r := by[a]
		if r == nil {
			r = &AccountRow{Account: a}
			by[a] = r
		}
		return r
	}
	for i := range fills {
		f := &fills[i]
		buyer, seller := f.Taker.Account, f.Maker.Account
		if f.Side != Buy {
			buyer, seller = seller, buyer
		}
		b := row(buyer)
		b.Bought += f.Qty
		b.Fills++
		s := row(seller)
		s.Sold += f.Qty
		s.Fills++
	}
	rows := make([]AccountRow, 0, len(by))
	for _, r := range by {
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Account < rows[j].Account })
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Bought+rows[i].Sold > rows[j].Bought+rows[j].Sold })
	return rows
}

// ---- the report

func priceText(t int64) string {
	sign := ""
	if t < 0 {
		sign, t = "-", -t
	}
	return fmt.Sprintf("%s%d.%02d", sign, t/100, t%100)
}

func (p optPrice) text() string {
	if !p.ok {
		return "-"
	}
	return priceText(p.v)
}

// reportValues is what the dogfood's JSON report holds, built whether or
// not it is written, as the dogfood builds it.
type reportValues struct {
	prices       []string
	levels       []string
	largest      []string
	bars         []string
	accounts     []AccountRow
	rejectsFound int
}

func buildReport(e *Engine, s *Summary, bids, asks []Depth) reportValues {
	var r reportValues
	bb, okb := e.bids.Best()
	ba, oka := e.asks.Best()
	lp := optPrice{e.log.last, e.log.hasLast}
	for _, p := range []optPrice{s.Vwap, s.BuyVwap, s.SellVwap, s.P50, s.P99, lp, {bb, okb}, {ba, oka}} {
		if p.ok {
			r.prices = append(r.prices, priceText(p.v))
		}
	}
	for _, d := range append(slices.Clone(bids), asks...) {
		r.levels = append(r.levels, priceText(d.Price))
	}
	for _, f := range e.log.top {
		r.largest = append(r.largest, f.Taker.String()+" "+f.Maker.String()+" "+priceText(f.Price))
	}
	for _, b := range s.Bars {
		r.bars = append(r.bars, priceText(b.Open)+priceText(b.High)+priceText(b.Low)+priceText(b.Close))
	}
	r.accounts = s.Accounts[:min(10, len(s.Accounts))]
	for _, n := range e.rejects {
		if n > 0 {
			r.rejectsFound++
		}
	}
	return r
}

func depthTable(w *bufio.Writer, bids, asks []Depth) {
	fmt.Fprintf(w, "%10s %6s %12s | %-12s %10s %6s\n", "bid qty", "orders", "bid", "ask", "ask qty", "orders")
	rows := max(len(bids), len(asks))
	if rows == 0 {
		fmt.Fprintf(w, "%20s%s%21s\n", "", "(the book is empty)", "")
	}
	for i := range rows {
		if i < len(bids) {
			fmt.Fprintf(w, "%10d %6d %12s", bids[i].Qty, bids[i].Orders, priceText(bids[i].Price))
		} else {
			fmt.Fprintf(w, "%30s", "")
		}
		w.WriteString(" | ")
		if i < len(asks) {
			fmt.Fprintf(w, "%-12s %10d %6d", priceText(asks[i].Price), asks[i].Qty, asks[i].Orders)
		}
		w.WriteString("\n")
	}
}

func fillsTable(w *bufio.Writer, title string, fills []Fill) {
	fmt.Fprintf(w, "%s\n", title)
	if len(fills) == 0 {
		w.WriteString("  (none)\n")
		return
	}
	for _, f := range fills {
		fmt.Fprintf(w, "  %10d  %-4s %7d @ %10s  taker %-9s maker %s\n", f.Ts, f.Side, f.Qty, priceText(f.Price), f.Taker, f.Maker)
	}
}

func line(w *bufio.Writer, label string, v any) { fmt.Fprintf(w, "%-18s%12v\n", label, v) }

func textReport(w *bufio.Writer, e *Engine, s *Summary, bids, asks []Depth, sum string) {
	t := &e.log
	line(w, "commands", e.events)
	line(w, "orders", e.orders.seq)
	line(w, "fills", len(t.fills))
	line(w, "cancels", e.cancels)
	line(w, "busts", t.busted)
	line(w, "live orders", e.orders.LiveCount())
	line(w, "stop queue entries", len(e.buyStops)+len(e.sellStops))
	for i, n := range e.rejects {
		if n > 0 {
			fmt.Fprintf(w, "rejected: %-14s%12d\n", rejectNames[i], n)
		}
	}
	line(w, "volume", t.traded)
	line(w, "vwap", s.Vwap.text())
	line(w, "vwap, buyers", s.BuyVwap.text())
	line(w, "vwap, sellers", s.SellVwap.text())
	line(w, "median fill", s.P50.text())
	line(w, "90th percentile", s.P90.text())
	line(w, "99th percentile", s.P99.text())
	line(w, "fills <= vwap", s.AtOrBelow)
	line(w, "last", optPrice{t.last, t.hasLast}.text())
	line(w, "traded prices", len(t.volume))
	w.WriteString("\n")
	depthTable(w, bids, asks)
	w.WriteString("\n")
	fillsTable(w, "largest fills", t.top)
	var recent []Fill
	for i := len(t.tape) - 1; i >= 0; i-- {
		recent = append(recent, t.tape[i])
	}
	fillsTable(w, "recent fills, newest first", recent)
	var busiest []Depth
	for p, q := range t.volume {
		busiest = append(busiest, Depth{Price: p, Qty: q})
	}
	sort.Slice(busiest, func(i, j int) bool {
		if busiest[i].Qty != busiest[j].Qty {
			return busiest[i].Qty > busiest[j].Qty
		}
		return busiest[i].Price < busiest[j].Price
	})
	w.WriteString("most traded prices\n")
	for _, d := range busiest[:min(3, len(busiest))] {
		fmt.Fprintf(w, "  %12s %10d\n", priceText(d.Price), d.Qty)
	}
	var resting []Order
	for i := range e.orders.pool.slots {
		if sl := &e.orders.pool.slots[i]; sl.used && e.orders.has(sl.o.Seq) {
			resting = append(resting, sl.o)
		}
	}
	sort.Slice(resting, func(i, j int) bool { return resting[i].Seq < resting[j].Seq })
	fmt.Fprintf(w, "oldest resting orders (%d live)\n", len(resting))
	for _, r := range resting[:min(3, len(resting))] {
		p := r.Price
		if r.Kind == Stop {
			p = r.Stop
		}
		fmt.Fprintf(w, "  %-9s %-4s %-9s %7d @ %10s\n", r.ID, r.Side, r.Kind, r.Left, priceText(p))
	}
	if len(s.Bars) > 0 {
		fmt.Fprintf(w, "bars, newest first\n%5s %10s %10s %10s %10s %10s\n", "#", "open", "high", "low", "close", "volume")
		for i := 0; i < len(s.Bars) && i < 10; i++ {
			b := s.Bars[len(s.Bars)-1-i]
			fmt.Fprintf(w, "%5d %10s %10s %10s %10s %10d\n", len(s.Bars)-i, priceText(b.Open), priceText(b.High), priceText(b.Low), priceText(b.Close), b.Volume)
		}
	}
	w.WriteString("fill sizes\n")
	for i, c := range s.Sizes {
		fmt.Fprintf(w, "  %-7s%9d\n", [7]string{"<=10", "<=50", "<=100", "<=500", "<=1000", "<=5000", ">5000"}[i], c)
	}
	fmt.Fprintf(w, "accounts by volume\n%4s %8s %10s %10s %7s\n", "rank", "account", "bought", "sold", "fills")
	for i, r := range s.Accounts[:min(10, len(s.Accounts))] {
		fmt.Fprintf(w, "%4d %8d %10d %10d %7d\n", i+1, r.Account, r.Bought, r.Sold, r.Fills)
	}
	fmt.Fprintf(w, "checksum %s\n", sum)
}

// ---- main

func main() { os.Exit(run()) }

func usage(msg string) int {
	fmt.Fprintf(os.Stderr, "error: %s\nusage: orderbook replay FEED [--quiet] [--snapshot N]\n", msg)
	return 2
}

func run() int {
	args := os.Args[1:]
	quiet, every, path := false, 10_000, ""
	if len(args) == 0 || args[0] != "replay" {
		return usage("the only command is replay")
	}
	for i := 1; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--quiet":
			quiet = true
		case a == "--snapshot" && i+1 < len(args):
			n, err := strconv.ParseInt(args[i+1], 10, 64)
			if err != nil || n < 0 || n > 1_000_000_000_000 {
				return usage("--snapshot does not take that value")
			}
			every = int(n)
			i++
		case !strings.HasPrefix(a, "--") && path == "":
			path = a
		default:
			return usage("unexpected argument " + a)
		}
	}
	if path == "" {
		return usage("replay needs a feed file")
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		fmt.Fprintf(os.Stderr, "error: cannot read %s\n", path)
		return 4
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read %s: %v\n", path, err)
		return 4
	}
	defer f.Close()
	e := newEngine(int(info.Size()/3)+2, every)
	r := csv.NewReader(bufio.NewReaderSize(f, 64*1024))
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			return 3
		}
		c, ok, err := parse(rec)
		if err != nil {
			line, _ := r.FieldPos(0)
			fmt.Fprintf(os.Stderr, "%s:%d: not a valid record\n", path, line)
			return 3
		}
		if ok {
			e.Submit(c)
		}
	}
	w := bufio.NewWriterSize(os.Stdout, 64*1024)
	defer w.Flush()
	s := summarize(e.log.fills, 1000)
	bids, asks := e.bids.Top(5), e.asks.Top(5)
	sum := fmt.Sprintf("%016x", e.Checksum())
	if quiet {
		fmt.Fprintf(w, "commands %d fills %d checksum %s\n", e.events, len(e.log.fills), sum)
	} else {
		textReport(w, e, &s, bids, asks, sum)
	}
	rv := buildReport(e, &s, bids, asks)
	snap := e.snap
	eb := e.bids.Close(e.orders)
	ea := e.asks.Close(e.orders)
	if !quiet {
		fmt.Fprintf(w, "last snapshot at command %d: %d live orders, %d fills\n", snap.Event, snap.Live, snap.Fills)
		fmt.Fprintf(w, "expired at close: %d bids (%d), %d asks (%d)\n", eb.Orders, eb.Qty, ea.Orders, ea.Qty)
	}
	// The report's values are built, as the dogfood builds its JSON
	// report; only its --json would write them.
	_ = rv
	return 0
}
