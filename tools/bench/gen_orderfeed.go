// Why: the app table's orderbook row (tests/bench/README.md
// "Applications", app/orderbook) replays a market-data feed through the
// orderbook dogfood and its four twins, and needs that feed generated.
//
// What: genOrderfeed, the "orderfeed" gen kind: a feed in
// Dogfoods/orderbook's format (`orderbook --help`).
//
// How: it is not a port of the dogfood's own `synth` (the README says why):
// every byte comes from splitmix(seed), and the mix is chosen so that a
// replay meets every command kind and every refusal reason.

package main

import (
	"bufio"
	"strconv"
)

// orderfeedAccounts is how many accounts send orders, and
// orderfeedRecent how many recent client ids the generator remembers for
// cancels, amends and reused ids.
const (
	orderfeedAccounts = 64
	orderfeedRecent   = 256
)

// orderfeed is the generator's state: the splitmix stream, the clock, the
// reference price in ticks, each account's last serial, and a ring of the
// ids issued last.
type orderfeed struct {
	g       splitmix
	ts      int64
	mid     int64
	serials [orderfeedAccounts]uint32
	recent  [orderfeedRecent][2]uint32
	seen    int
	line    []byte
}

func (f *orderfeed) below(n int) int { return int(f.g.next() % uint64(n)) }

// fresh is a new client id from a random account.
func (f *orderfeed) fresh() [2]uint32 {
	a := f.below(orderfeedAccounts)
	f.serials[a]++
	id := [2]uint32{uint32(a + 1), f.serials[a]}
	f.recent[f.seen%orderfeedRecent] = id
	f.seen++
	return id
}

// old is an id issued earlier: live, filled, cancelled or refused.
func (f *orderfeed) old() [2]uint32 {
	if f.seen == 0 {
		return [2]uint32{1, 1}
	}
	return f.recent[f.below(min(f.seen, orderfeedRecent))]
}

// qty is an order size: mostly 10 to 200, one in 50 a block of 25 times.
func (f *orderfeed) qty() int64 {
	q := int64(1+f.below(20)) * 10
	if f.below(50) == 0 {
		q *= 25
	}
	return q
}

func (f *orderfeed) side() byte {
	if f.below(2) == 0 {
		return 'B'
	}
	return 'S'
}

func (f *orderfeed) int(n int64) { f.line = strconv.AppendInt(f.line, n, 10) }

func (f *orderfeed) id(id [2]uint32) {
	f.line = append(f.line, ',')
	f.line = strconv.AppendUint(f.line, uint64(id[0]), 10)
	f.line = append(f.line, '/')
	f.line = strconv.AppendUint(f.line, uint64(id[1]), 10)
}

// price writes ticks as a price with two decimals, or, drawn from the
// stream, with one or none where the value allows: 10000 as 100, 100.0 or
// 100.00, and 10050 as 100.5 or 100.50.
func (f *orderfeed) price(ticks int64) {
	f.line = append(f.line, ',')
	f.int(ticks / 100)
	c := ticks % 100
	switch {
	case c == 0 && f.below(4) == 0:
		return
	case c == 0 && f.below(3) == 0:
		f.line = append(f.line, ".0"...)
		return
	case c%10 == 0 && f.below(2) == 0:
		f.line = append(f.line, '.', byte('0'+c/10))
		return
	}
	f.line = append(f.line, '.', byte('0'+c/10), byte('0'+c%10))
}

// head starts a command line: its type letter and timestamp.
func (f *orderfeed) head(kind byte) {
	f.line = append(f.line[:0], kind, ',')
	f.int(f.ts)
}

// order starts an order line with a fresh id, or with id when given.
func (f *orderfeed) order(kind byte, id *[2]uint32, side byte, qty int64) {
	f.head(kind)
	if id != nil {
		f.id(*id)
	} else {
		f.id(f.fresh())
	}
	f.line = append(f.line, ',', side, ',')
	f.int(qty)
}

// next writes one feed line, with its newline, into f.line.
func (f *orderfeed) next() {
	f.ts += int64(1 + f.below(3))
	if f.below(64) == 0 {
		// A slow random walk of the reference price.
		f.mid = max(f.mid+int64(f.below(7))-3, 1000)
	}
	roll := f.below(1000)
	switch {
	case roll < 520: // a limit order near the reference price
		s := f.side()
		off := int64(f.below(12))
		p := f.mid - 2 + off
		if s == 'B' {
			p = f.mid + 2 - off
		}
		f.order('N', nil, s, f.qty())
		f.price(p)
	case roll < 580: // a market order
		f.order('M', nil, f.side(), f.qty())
	case roll < 770: // a cancel of an earlier id, live or not
		f.head('C')
		f.id(f.old())
	case roll < 800: // an amend of an earlier id: a limit's new limit, a stop's new trigger
		f.head('A')
		f.id(f.old())
		f.line = append(f.line, ',')
		f.int(f.qty())
		f.price(f.mid + int64(f.below(9)) - 4)
	case roll < 850: // a stop order
		s := f.side()
		gap := int64(3 + f.below(8))
		stop := f.mid - gap
		if s == 'B' {
			stop = f.mid + gap
		}
		f.order('S', nil, s, f.qty())
		f.price(stop)
	case roll < 880: // a stop-limit order
		s := f.side()
		gap := int64(3 + f.below(8))
		stop, limit := f.mid-gap, f.mid-gap-2
		if s == 'B' {
			stop, limit = f.mid+gap, f.mid+gap+2
		}
		f.order('T', nil, s, f.qty())
		f.price(stop)
		f.price(limit)
	case roll < 930: // a limit order far from the reference price
		s := f.side()
		off := int64(20 + f.below(200))
		p := f.mid + off
		if s == 'B' {
			p = f.mid - off
		}
		f.order('N', nil, s, f.qty())
		f.price(p)
	case roll < 940: // a bust of the last trade
		f.head('X')
	case roll < 960: // a halt, rarely, or a resume
		halt := roll < 942 && f.below(4) == 0
		f.head('R')
		if halt {
			f.line[0] = 'H'
		}
		f.line = append(f.line, ',')
		f.int(int64(1 + f.below(orderfeedAccounts)))
	case roll < 975: // a limit order under an earlier id: refused while it is live, a reuse once it is done
		id := f.old()
		s := f.side()
		f.order('N', &id, s, f.qty())
		f.price(f.mid + int64(f.below(5)) - 2)
	case roll < 982: // a bad quantity or price
		switch f.below(3) {
		case 0:
			f.order('N', nil, f.side(), 0)
			f.price(f.mid)
		case 1:
			f.order('N', nil, f.side(), f.qty())
			f.price(0)
		default:
			f.head('A')
			f.id(f.old())
			f.line = append(f.line, ",0"...)
			f.price(f.mid)
		}
	case roll < 988: // a cancel of an id never issued
		a := f.below(orderfeedAccounts)
		f.head('C')
		f.id([2]uint32{uint32(a + 1), f.serials[a] + 1000000})
	case roll < 989: // a sweep: a market order large enough to empty a side and fire its stops
		f.order('M', nil, f.side(), int64(5000+f.below(20000)))
	default: // a comment, which the parser skips
		f.line = append(f.line[:0], "# orderfeed: note "...)
		f.int(f.ts)
	}
	f.line = append(f.line, '\n')
}

// genOrderfeed writes the "orderfeed" kind: a feed of commands until about
// spec.Bytes bytes, drawn from splitmix(seed). It opens with a comment,
// a bust (refused: nothing to bust) and a market order (refused: no
// liquidity), both on an empty book. Then each line is, by a roll of 1,000:
//
//	520 a limit order N within 2 ticks of the reference price on its side and 9 beyond
//	 60 a market order M
//	190 a cancel C of one of the last 256 ids issued
//	 30 an amend A of one of them, the new price within 4 ticks of the reference
//	 50 a stop order S, 3 to 10 ticks away from the reference price
//	 30 a stop-limit order T, the same, its limit 2 ticks beyond the stop
//	 50 a limit order N 20 to 219 ticks away, behind the market
//	 10 a bust X
//	 20 a halt H (one in four of the first two) or a resume R of an account
//	 15 a limit order N under one of the last 256 ids (a duplicate while live)
//	  7 a quantity of 0, a price of 0 or an amend to a quantity of 0
//	  6 a cancel of an id never issued
//	  1 a market order of 5,000 to 24,999, which may sweep a side
//	 11 a comment
//
// Sizes are 10 to 200 in tens, one in 50 of them times 25. Ids are
// account/serial over 64 accounts. Prices are written with two decimals,
// or with one or none where the value allows. The timestamps rise by 1
// to 3 a line, and the reference price walks by -3 to 3 ticks one line in
// 64, never below 1,000. The lines end in LF and hold no quote.
func genOrderfeed(w *bufio.Writer, spec dataSpec) error {
	f := &orderfeed{g: splitmix{spec.Seed}, ts: 34_200_000, mid: 10_000}
	var n int64
	put := func(s []byte) error {
		n += int64(len(s))
		_, err := w.Write(s)
		return err
	}
	f.line = append(f.line[:0], "# orderfeed seed "...)
	f.line = strconv.AppendUint(f.line, spec.Seed, 10)
	f.line = append(f.line, '\n')
	if err := put(f.line); err != nil {
		return err
	}
	f.ts++
	f.head('X')
	f.line = append(f.line, '\n')
	if err := put(f.line); err != nil {
		return err
	}
	f.ts++
	f.order('M', nil, 'B', 100)
	f.line = append(f.line, '\n')
	if err := put(f.line); err != nil {
		return err
	}
	for n < spec.Bytes {
		f.next()
		if err := put(f.line); err != nil {
			return err
		}
	}
	return nil
}
