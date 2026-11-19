// orderbook, Rust, std plus a hand CSV splitter: the task of README.md, the
// replay of Dogfoods/orderbook (`orderbook replay FEED [--quiet]
// [--snapshot N]`). The feed is read through a 64 KiB BufReader a line at a
// time (read_until into one reused Vec<u8>), each line checked by
// std::str::from_utf8 and split on commas by hand (the feed has no quoted
// field; a line holding a quote is refused with status 3), fields trimmed
// of ASCII white space, numbers parsed by str::parse::<i64>. Orders live in
// a slot pool (a Vec of slots, each with a generation, and a Vec of vacant
// slot indices used last in first out), named by (slot, generation)
// handles; a Vec<u64> bitset of live arrival numbers sized from the file (a
// third of its bytes, plus two); a HashMap from client id to handle and a
// HashSet of the ids that are done (std's RandomState, SipHash-1-3). Each
// side of the book is a BTreeMap from key (price times -1 for bids, times 1
// for asks) to a level, whose FIFO of handles is a VecDeque. Stops wait in
// two BinaryHeaps of Reverse((key, arrival number, handle)), the work queue
// is a VecDeque of commands. Trades are a Vec of every fill, a Vec of the
// five largest, an 8-fill VecDeque tape and a BTreeMap of volume by price.
// The checksum is FNV-1a over the bytes of 64-bit words, its multiply
// wrapping_mul; every other integer is i64 or usize, checked in rust-ovf,
// and no value of a feed overflows. After the replay, the statistics run
// whether or not --quiet is given: a sorted copy of the fill prices for
// percentiles, the VWAPs of all fills and of each aggressor side (a copy
// partitioned in place), bars of 1,000 fills (running totals, a short last
// bar merged), the fill-size histogram and the volume by account (a
// HashMap, then sort by account and a stable sort by volume); the depth of
// 5 levels a side; the report's values; then the close, which counts the
// live orders left in each side's queues. Without --quiet the program
// prints the dogfood's text report but its "engine heap" line, which reads
// the dogfood's own counting allocator.

use std::cmp::Reverse;
use std::collections::{BTreeMap, BinaryHeap, HashMap, HashSet, VecDeque};
use std::fmt;
use std::fs::File;
use std::io::{BufRead, BufReader, BufWriter, Write};
use std::process::ExitCode;

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Side {
    Buy,
    Sell,
}

impl fmt::Display for Side {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.pad(if *self == Side::Buy { "Buy" } else { "Sell" })
    }
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Kind {
    Limit,
    Market,
    Stop,
    StopLimit,
}

impl fmt::Display for Kind {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.pad(match self {
            Kind::Limit => "Limit",
            Kind::Market => "Market",
            Kind::Stop => "Stop",
            Kind::StopLimit => "StopLimit",
        })
    }
}

#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug, Default)]
struct ClientId {
    account: u32,
    serial: u32,
}

impl fmt::Display for ClientId {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.pad(&format!("{}/{}", self.account, self.serial))
    }
}

#[derive(Clone, Copy, Debug)]
struct Order {
    id: ClientId,
    seq: usize,
    side: Side,
    kind: Kind,
    price: i64,
    stop: i64,
    qty: i64,
    left: i64,
    ts: i64,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
struct Fill {
    taker: ClientId,
    maker: ClientId,
    taker_seq: usize,
    maker_seq: usize,
    side: Side,
    price: i64,
    qty: i64,
    ts: i64,
}

#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Debug)]
struct Handle {
    idx: u32,
    generation: u32,
}

#[derive(Clone, Copy, Debug)]
enum Command {
    Add { ts: i64, id: ClientId, side: Side, kind: Kind, qty: i64, price: i64, stop: i64 },
    Cancel { ts: i64, id: ClientId },
    Amend { ts: i64, id: ClientId, qty: i64, price: i64 },
    Halt { ts: i64, account: u32 },
    Resume { ts: i64, account: u32 },
    Bust { ts: i64 },
    Trigger { h: Handle },
}

const DUPLICATE: usize = 0;
const HALTED: usize = 1;
const UNKNOWN: usize = 2;
const ALREADY_DONE: usize = 3;
const NO_LIQUIDITY: usize = 4;
const BAD_QUANTITY: usize = 5;
const NOTHING_TO_BUST: usize = 6;
const REJECT_NAMES: [&str; 7] = ["duplicate id", "account halted", "unknown id", "already done", "no liquidity", "bad quantity", "nothing to bust"];

// ---- the slot pool

struct Slot {
    generation: u32,
    used: bool,
    o: Order,
}

const NO_ORDER: Order = Order { id: ClientId { account: 0, serial: 0 }, seq: 0, side: Side::Buy, kind: Kind::Limit, price: 0, stop: 0, qty: 0, left: 0, ts: 0 };

#[derive(Default)]
struct Pool {
    slots: Vec<Slot>,
    vacant: Vec<u32>,
}

impl Pool {
    fn add(&mut self, o: Order) -> Handle {
        if let Some(i) = self.vacant.pop() {
            let s = &mut self.slots[i as usize];
            s.used = true;
            s.o = o;
            return Handle { idx: i, generation: s.generation };
        }
        self.slots.push(Slot { generation: 0, used: true, o });
        Handle { idx: (self.slots.len() - 1) as u32, generation: 0 }
    }

    fn get(&self, h: Handle) -> Option<&Order> {
        match self.slots.get(h.idx as usize) {
            Some(s) if s.used && s.generation == h.generation => Some(&s.o),
            _ => None,
        }
    }

    fn get_mut(&mut self, h: Handle) -> Option<&mut Order> {
        match self.slots.get_mut(h.idx as usize) {
            Some(s) if s.used && s.generation == h.generation => Some(&mut s.o),
            _ => None,
        }
    }

    fn release(&mut self, i: usize) {
        let s = &mut self.slots[i];
        s.used = false;
        s.generation = s.generation.wrapping_add(1);
        s.o = NO_ORDER;
        self.vacant.push(i as u32);
    }

    fn free(&mut self, h: Handle) -> bool {
        if self.get(h).is_none() {
            return false;
        }
        self.release(h.idx as usize);
        true
    }
}

// ---- the order store

struct Orders {
    pool: Pool,
    flags: Vec<u64>,
    nbits: usize,
    index: HashMap<ClientId, Handle>,
    done: HashSet<ClientId>,
    seq: usize,
}

impl Orders {
    fn new(capacity: usize) -> Orders {
        Orders { pool: Pool::default(), flags: vec![0; capacity.div_ceil(64)], nbits: capacity, index: HashMap::new(), done: HashSet::new(), seq: 0 }
    }

    fn has(&self, seq: usize) -> bool {
        self.flags[seq >> 6] & (1u64 << (seq & 63)) != 0
    }

    fn set(&mut self, seq: usize) {
        assert!(seq < self.nbits, "orders past the capacity");
        self.flags[seq >> 6] |= 1u64 << (seq & 63);
    }

    fn clear(&mut self, seq: usize) {
        self.flags[seq >> 6] &= !(1u64 << (seq & 63));
    }

    fn live(&self, h: Handle) -> Option<&Order> {
        self.pool.get(h).filter(|o| self.has(o.seq))
    }

    fn is_live(&self, h: Handle) -> bool {
        self.live(h).is_some()
    }

    fn add(&mut self, mut o: Order) -> Handle {
        o.seq = self.seq;
        let h = self.pool.add(o);
        self.index.insert(o.id, h);
        self.set(o.seq);
        self.seq += 1;
        self.done.remove(&o.id);
        h
    }

    fn reissue(&mut self, mut o: Order) -> Handle {
        o.seq = self.seq;
        let h = self.pool.add(o);
        if let Some(old) = self.index.insert(o.id, h) {
            if let Some(prev) = self.pool.get(old) {
                let s = prev.seq;
                self.clear(s);
            }
        }
        self.set(o.seq);
        self.seq += 1;
        h
    }

    fn retire(&mut self, h: Handle) {
        let Some(o) = self.pool.get(h) else { return };
        let (seq, id) = (o.seq, o.id);
        self.clear(seq);
        self.index.remove(&id);
        self.done.insert(id);
    }

    fn live_count(&self) -> usize {
        let c: usize = self.flags.iter().map(|w| w.count_ones() as usize).sum();
        assert_eq!(c, self.index.len());
        c
    }

    fn sweep(&mut self) -> usize {
        let mut n = 0;
        for i in 0..self.pool.slots.len() {
            let s = &self.pool.slots[i];
            if s.used && !self.has(s.o.seq) {
                self.pool.release(i);
                n += 1;
            }
        }
        n
    }
}

// ---- one side of the book

struct Level {
    qty: i64,
    live: usize,
    orders: VecDeque<Handle>,
}

struct Ladder {
    levels: BTreeMap<i64, Level>,
    sign: i64,
}

#[derive(Clone, Copy, Debug)]
struct Depth {
    price: i64,
    qty: i64,
    orders: usize,
}

struct Expired {
    orders: usize,
    qty: i64,
}

const COMPACT_SLACK: usize = 8;

impl Ladder {
    fn new(side: Side) -> Ladder {
        Ladder { levels: BTreeMap::new(), sign: if side == Side::Buy { -1 } else { 1 } }
    }

    fn best(&self) -> Option<i64> {
        self.levels.keys().next().map(|k| k * self.sign)
    }

    fn crosses(&self, price: i64, limit: i64) -> bool {
        if self.sign > 0 { price <= limit } else { price >= limit }
    }

    fn rest(&mut self, price: i64, qty: i64, h: Handle) {
        let lvl = self.levels.entry(price * self.sign).or_insert_with(|| Level { qty: 0, live: 0, orders: VecDeque::new() });
        lvl.orders.push_back(h);
        lvl.qty += qty;
        lvl.live += 1;
    }

    fn withdraw(&mut self, price: i64, qty: i64, h: Handle) {
        let key = price * self.sign;
        let Some(lvl) = self.levels.get_mut(&key) else { return };
        lvl.qty -= qty;
        lvl.live -= 1;
        if lvl.orders.back() == Some(&h) {
            lvl.orders.pop_back();
        }
        if lvl.qty == 0 {
            self.levels.remove(&key);
        }
    }

    fn compact(&mut self, price: i64, book: &Orders) {
        let key = price * self.sign;
        let Some(old) = self.levels.get_mut(&key) else { return };
        if old.orders.len() <= 2 * old.live + COMPACT_SLACK {
            return;
        }
        let fresh: VecDeque<Handle> = old.orders.iter().copied().filter(|h| book.is_live(*h)).collect();
        old.orders = fresh;
    }

    fn close(&mut self, book: &Orders) -> Expired {
        let mut e = Expired { orders: 0, qty: 0 };
        for lvl in std::mem::take(&mut self.levels).into_values() {
            e.qty += lvl.qty;
            e.orders += lvl.orders.iter().filter(|h| book.is_live(**h)).count();
        }
        e
    }

    fn match_order(&mut self, book: &mut Orders, taker: Handle, now: i64, out: &mut Trades) {
        loop {
            let Some(t) = book.live(taker) else { return };
            if t.left == 0 {
                return;
            }
            let limited = t.kind == Kind::Limit || t.kind == Kind::StopLimit;
            let limit = t.price;
            let Some(&key) = self.levels.keys().next() else { return };
            if limited && !self.crosses(key * self.sign, limit) {
                return;
            }
            self.sweep(key, book, taker, now, out);
        }
    }

    fn sweep(&mut self, key: i64, book: &mut Orders, taker: Handle, now: i64, out: &mut Trades) {
        let price = key * self.sign;
        let Some(lvl) = self.levels.get_mut(&key) else { return };
        while lvl.qty > 0 {
            let Some(maker) = lvl.orders.pop_front() else { break };
            if !book.is_live(maker) || maker == taker {
                continue;
            }
            let t = *book.pool.get(taker).unwrap();
            let m = *book.pool.get(maker).unwrap();
            let q = t.left.min(m.left);
            let (tl, ml) = (t.left - q, m.left - q);
            book.pool.get_mut(taker).unwrap().left = tl;
            book.pool.get_mut(maker).unwrap().left = ml;
            lvl.qty -= q;
            out.record(Fill { taker: t.id, maker: m.id, taker_seq: t.seq, maker_seq: m.seq, side: t.side, price, qty: q, ts: now });
            if ml > 0 {
                lvl.orders.push_front(maker);
            } else {
                lvl.live -= 1;
                book.retire(maker);
            }
            if tl == 0 {
                break;
            }
        }
        if lvl.qty == 0 {
            self.levels.remove(&key);
        }
    }

    fn top(&self, n: usize) -> Vec<Depth> {
        self.levels.iter().take(n).map(|(k, l)| Depth { price: k * self.sign, qty: l.qty, orders: l.live }).collect()
    }

    fn quantity(&self) -> i64 {
        self.levels.values().map(|l| l.qty).sum()
    }
}

// ---- trades and the checksum

const TAPE_LENGTH: usize = 8;
const TOP_FILLS: usize = 5;
const FNV_BASIS: u64 = 0xcbf29ce484222325;
const FNV_PRIME: u64 = 0x100000001b3;

fn mix(mut h: u64, mut x: u64) -> u64 {
    for _ in 0..8 {
        h = (h ^ (x & 0xff)).wrapping_mul(FNV_PRIME);
        x >>= 8;
    }
    h
}

fn mix_int(h: u64, x: i64) -> u64 {
    mix(h, x as u64)
}

struct Trades {
    fills: Vec<Fill>,
    top: Vec<Fill>,
    tape: VecDeque<Fill>,
    volume: BTreeMap<i64, i64>,
    last: Option<i64>,
    traded: i64,
    notional: i64,
    busted: usize,
    checksum: u64,
}

impl Trades {
    fn record(&mut self, f: Fill) {
        self.fills.push(f);
        let at = self.top.partition_point(|x| x.qty >= f.qty);
        if at < TOP_FILLS {
            self.top.insert(at, f);
            self.top.truncate(TOP_FILLS);
        }
        self.tape.push_back(f);
        if self.tape.len() > TAPE_LENGTH {
            self.tape.pop_front();
        }
        *self.volume.entry(f.price).or_insert(0) += f.qty;
        self.last = Some(f.price);
        self.traded += f.qty;
        self.notional += f.qty * f.price;
        self.checksum = mix_int(mix_int(mix_int(mix(mix(self.checksum, f.taker_seq as u64), f.maker_seq as u64), f.price), f.qty), f.ts);
    }

    fn bust(&mut self) -> bool {
        let Some(f) = self.fills.pop() else { return false };
        self.tape.pop_back();
        if let Some(at) = self.top.iter().position(|x| *x == f) {
            self.top.remove(at);
        }
        if let Some(v) = self.volume.get_mut(&f.price) {
            *v -= f.qty;
            if *v == 0 {
                self.volume.remove(&f.price);
            }
        }
        self.traded -= f.qty;
        self.notional -= f.qty * f.price;
        self.busted += 1;
        self.checksum = mix(self.checksum, 0xb057);
        true
    }
}

// ---- the engine

#[derive(Clone, Copy, Default)]
struct Snapshot {
    event: usize,
    live: usize,
    fills: usize,
}

const SWEEP_EVERY: usize = 4096;

type StopQueue = BinaryHeap<Reverse<(i64, usize, Handle)>>;

struct Engine {
    bids: Ladder,
    asks: Ladder,
    orders: Orders,
    log: Trades,
    buy_stops: StopQueue,
    sell_stops: StopQueue,
    work: VecDeque<Command>,
    fired: Vec<Handle>,
    halted: HashSet<u32>,
    rejects: [usize; 7],
    snap: Snapshot,
    every: usize,
    now: i64,
    events: usize,
    cancels: usize,
    swept: usize,
}

impl Engine {
    fn new(capacity: usize, every: usize) -> Engine {
        Engine {
            bids: Ladder::new(Side::Buy),
            asks: Ladder::new(Side::Sell),
            orders: Orders::new(capacity),
            log: Trades { fills: Vec::new(), top: Vec::new(), tape: VecDeque::new(), volume: BTreeMap::new(), last: None, traded: 0, notional: 0, busted: 0, checksum: FNV_BASIS },
            buy_stops: BinaryHeap::new(),
            sell_stops: BinaryHeap::new(),
            work: VecDeque::new(),
            fired: Vec::new(),
            halted: HashSet::new(),
            rejects: [0; 7],
            snap: Snapshot::default(),
            every,
            now: 0,
            events: 0,
            cancels: 0,
            swept: 0,
        }
    }

    fn submit(&mut self, c: Command) {
        self.work.push_back(c);
        while let Some(c) = self.work.pop_front() {
            self.apply(c);
        }
        self.events += 1;
        if self.events % SWEEP_EVERY == 0 {
            self.swept += self.orders.sweep();
        }
        if self.every > 0 && self.events % self.every == 0 {
            self.snap = Snapshot { event: self.events, live: self.orders.live_count(), fills: self.log.fills.len() };
        }
    }

    fn apply(&mut self, c: Command) {
        match c {
            Command::Add { ts, id, side, kind, qty, price, stop } => {
                self.now = ts;
                self.add(Order { id, seq: 0, side, kind, price, stop, qty, left: qty, ts });
            }
            Command::Cancel { ts, id } => {
                self.now = ts;
                self.cancel(id);
            }
            Command::Amend { ts, id, qty, price } => {
                self.now = ts;
                self.amend(ts, id, qty, price);
            }
            Command::Halt { ts, account } => {
                self.now = ts;
                self.halted.insert(account);
            }
            Command::Resume { ts, account } => {
                self.now = ts;
                self.halted.remove(&account);
            }
            Command::Bust { ts } => {
                self.now = ts;
                if !self.log.bust() {
                    self.refuse(NOTHING_TO_BUST);
                }
            }
            Command::Trigger { h } => self.fire(h),
        }
    }

    fn refuse(&mut self, code: usize) {
        self.rejects[code] += 1;
        self.log.checksum = mix(self.log.checksum, 0x7e00 + code as u64);
    }

    fn add(&mut self, o: Order) {
        if o.qty <= 0 || (o.kind != Kind::Market && o.kind != Kind::Stop && o.price <= 0) {
            return self.refuse(BAD_QUANTITY);
        }
        if self.halted.contains(&o.id.account) {
            return self.refuse(HALTED);
        }
        if self.orders.index.contains_key(&o.id) {
            return self.refuse(DUPLICATE);
        }
        let h = self.orders.add(o);
        if o.kind == Kind::Stop || o.kind == Kind::StopLimit {
            return self.arm(h);
        }
        self.execute(h);
    }

    fn arm(&mut self, h: Handle) {
        let Some(&o) = self.orders.live(h) else { return };
        if let Some(last) = self.log.last {
            let reached = if o.side == Side::Buy { last >= o.stop } else { last <= o.stop };
            if reached {
                return self.fire(h);
            }
        }
        if o.side == Side::Buy {
            self.buy_stops.push(Reverse((o.stop, o.seq, h)));
        } else {
            self.sell_stops.push(Reverse((-o.stop, o.seq, h)));
        }
    }

    fn fire(&mut self, h: Handle) {
        if !self.orders.is_live(h) {
            return;
        }
        let o = self.orders.pool.get_mut(h).unwrap();
        o.kind = if o.kind == Kind::StopLimit { Kind::Limit } else { Kind::Market };
        self.execute(h);
    }

    fn execute(&mut self, h: Handle) {
        let before = self.log.fills.len();
        let Some(side) = self.orders.live(h).map(|o| o.side) else { return };
        if side == Side::Buy {
            self.asks.match_order(&mut self.orders, h, self.now, &mut self.log);
        } else {
            self.bids.match_order(&mut self.orders, h, self.now, &mut self.log);
        }
        let Some(&o) = self.orders.live(h) else { return };
        if o.left == 0 {
            self.orders.retire(h);
        } else if o.kind == Kind::Limit {
            if side == Side::Buy {
                self.bids.rest(o.price, o.left, h);
            } else {
                self.asks.rest(o.price, o.left, h);
            }
        } else {
            // The rest of a market order is cancelled.
            self.orders.retire(h);
            if self.log.fills.len() == before {
                self.orders.pool.free(h);
                self.refuse(NO_LIQUIDITY);
            }
        }
        if self.log.fills.len() > before {
            self.triggers();
        }
    }

    fn triggers(&mut self) {
        let Some(last) = self.log.last else { return };
        while let Some(&Reverse((key, _, h))) = self.buy_stops.peek() {
            if key > last {
                break;
            }
            self.buy_stops.pop();
            if self.orders.is_live(h) {
                self.fired.push(h);
            }
        }
        while let Some(&Reverse((key, _, h))) = self.sell_stops.peek() {
            if key > -last {
                break;
            }
            self.sell_stops.pop();
            if self.orders.is_live(h) {
                self.fired.push(h);
            }
        }
        for &h in self.fired.iter().rev() {
            self.work.push_front(Command::Trigger { h });
        }
        self.fired.clear();
    }

    fn done_or_unknown(&mut self, id: ClientId) {
        let code = if self.orders.done.contains(&id) { ALREADY_DONE } else { UNKNOWN };
        self.refuse(code);
    }

    fn cancel(&mut self, id: ClientId) {
        let Some(&h) = self.orders.index.get(&id) else { return self.done_or_unknown(id) };
        if let Some(&o) = self.orders.live(h) {
            self.orders.retire(h);
            if o.kind == Kind::Limit {
                let l = if o.side == Side::Buy { &mut self.bids } else { &mut self.asks };
                l.withdraw(o.price, o.left, h);
                l.compact(o.price, &self.orders);
            }
        }
        self.cancels += 1;
    }

    fn amend(&mut self, ts: i64, id: ClientId, qty: i64, price: i64) {
        let Some(&h) = self.orders.index.get(&id) else { return self.done_or_unknown(id) };
        if qty <= 0 || price <= 0 {
            return self.refuse(BAD_QUANTITY);
        }
        let Some(&old) = self.orders.live(h) else { return };
        if old.kind == Kind::Limit {
            if old.side == Side::Buy {
                self.bids.withdraw(old.price, old.left, h);
            } else {
                self.asks.withdraw(old.price, old.left, h);
            }
        }
        // An amended order is a new arrival.
        let mut o = old;
        o.qty = qty;
        o.left = qty;
        if o.kind == Kind::Stop {
            o.stop = price;
        } else {
            o.price = price;
        }
        o.ts = ts;
        let h2 = self.orders.reissue(o);
        if o.kind == Kind::Stop || o.kind == Kind::StopLimit {
            return self.arm(h2);
        }
        self.execute(h2);
    }

    fn checksum(&self) -> u64 {
        let mut h = self.log.checksum;
        h = mix_int(h, self.bids.best().unwrap_or(0));
        h = mix_int(h, self.asks.best().unwrap_or(0));
        h = mix_int(h, self.bids.quantity());
        h = mix_int(h, self.asks.quantity());
        mix(h, self.orders.live_count() as u64)
    }
}

// ---- the feed

fn digits(s: &str) -> Option<i64> {
    if s.is_empty() || s.len() > 15 {
        return None;
    }
    let mut n: i64 = 0;
    for b in s.bytes() {
        if !b.is_ascii_digit() {
            return None;
        }
        n = n * 10 + (b - b'0') as i64;
    }
    Some(n)
}

fn parse_price(s: &str) -> Option<i64> {
    let (whole, frac) = match s.find('.') {
        Some(d) => (&s[..d], Some(&s[d + 1..])),
        None => (s, None),
    };
    let w = digits(whole)?;
    let mut c = 0;
    if let Some(f) = frac {
        if f.is_empty() || f.len() > 2 {
            return None;
        }
        c = digits(f)?;
        if f.len() == 1 {
            c *= 10;
        }
    }
    if w > 9_999_999_999_999 {
        return None;
    }
    Some(w * 100 + c)
}

fn account(s: &str) -> Option<u32> {
    let n: i64 = s.parse().ok()?;
    if !(0..=0xffff_ffff).contains(&n) {
        return None;
    }
    Some(n as u32)
}

fn client_id(s: &str) -> Option<ClientId> {
    let (a, b) = s.split_once('/')?;
    Some(ClientId { account: account(a)?, serial: account(b)? })
}

/// The command in one line's fields: Ok(None) for a comment or a blank
/// line, Err for a record that is not a command.
fn parse(line: &str) -> Result<Option<Command>, ()> {
    let mut cols = [""; 7];
    let mut count = 0;
    for (i, f) in line.split(',').enumerate() {
        let f = f.trim_matches(|c: char| c.is_ascii_whitespace());
        if i == 0 && f.starts_with('#') {
            return Ok(None);
        }
        if i < 7 {
            cols[i] = f;
        }
        count += 1;
    }
    if count == 1 && cols[0].is_empty() {
        return Ok(None);
    }
    let want = match cols[0] {
        "X" => 2,
        "C" | "H" | "R" => 3,
        "M" | "A" => 5,
        "N" | "S" => 6,
        "T" => 7,
        _ => return Err(()),
    };
    if count != want {
        return Err(());
    }
    let ts: i64 = cols[1].parse().map_err(|_| ())?;
    let kind = cols[0];
    if kind == "X" {
        return Ok(Some(Command::Bust { ts }));
    }
    if kind == "H" || kind == "R" {
        let account = account(cols[2]).ok_or(())?;
        return Ok(Some(if kind == "H" { Command::Halt { ts, account } } else { Command::Resume { ts, account } }));
    }
    let id = client_id(cols[2]).ok_or(())?;
    if kind == "C" {
        return Ok(Some(Command::Cancel { ts, id }));
    }
    if kind == "A" {
        let qty: i64 = cols[3].parse().map_err(|_| ())?;
        let price = parse_price(cols[4]).ok_or(())?;
        return Ok(Some(Command::Amend { ts, id, qty, price }));
    }
    let side = match cols[3] {
        "B" => Side::Buy,
        "S" => Side::Sell,
        _ => return Err(()),
    };
    let qty: i64 = cols[4].parse().map_err(|_| ())?;
    let (k, price, stop) = match kind {
        "N" => (Kind::Limit, parse_price(cols[5]).ok_or(())?, 0),
        "M" => (Kind::Market, 0, 0),
        "S" => (Kind::Stop, 0, parse_price(cols[5]).ok_or(())?),
        _ => {
            let stop = parse_price(cols[5]).ok_or(())?;
            (Kind::StopLimit, parse_price(cols[6]).ok_or(())?, stop)
        }
    };
    Ok(Some(Command::Add { ts, id, side, kind: k, qty, price, stop }))
}

// ---- the statistics

#[derive(Clone, Copy)]
struct Bar {
    open: i64,
    high: i64,
    low: i64,
    close: i64,
    volume: i64,
    trades: usize,
}

#[derive(Clone, Copy)]
struct AccountRow {
    account: u32,
    bought: i64,
    sold: i64,
    fills: usize,
}

struct Summary {
    p50: Option<i64>,
    p90: Option<i64>,
    p99: Option<i64>,
    vwap: Option<i64>,
    buy_vwap: Option<i64>,
    sell_vwap: Option<i64>,
    at_or_below: usize,
    bars: Vec<Bar>,
    sizes: [usize; 7],
    accounts: Vec<AccountRow>,
}

fn percentile(sorted: &[i64], p: usize) -> Option<i64> {
    if sorted.is_empty() {
        return None;
    }
    let rank = (sorted.len() * p + 99) / 100;
    Some(sorted[rank.saturating_sub(1)])
}

fn vwap(fills: &[Fill]) -> Option<i64> {
    let (mut q, mut n) = (0i64, 0i64);
    for f in fills {
        q += f.qty;
        n += f.qty * f.price;
    }
    if q == 0 { None } else { Some(n / q) }
}

const SIZE_LIMITS: [i64; 6] = [10, 50, 100, 500, 1000, 5000];

fn summarize(fills: &[Fill], per_bar: usize) -> Summary {
    let prices: Vec<i64> = fills.iter().map(|f| f.price).collect();
    let mut sorted = prices.clone();
    sorted.sort_unstable();
    let v = vwap(fills);
    let at_or_below = match v {
        Some(v) => sorted.partition_point(|x| *x <= v),
        None => 0,
    };
    let mut xs = fills.to_vec();
    let mut cut = 0;
    for i in 0..xs.len() {
        if xs[i].side == Side::Buy {
            xs.swap(i, cut);
            cut += 1;
        }
    }
    let (left, right) = xs.split_at(cut);
    let mut sizes = [0usize; 7];
    for f in fills {
        let mut class = 0;
        for l in SIZE_LIMITS {
            if f.qty <= l {
                break;
            }
            class += 1;
        }
        sizes[class] += 1;
    }
    Summary {
        p50: percentile(&sorted, 50),
        p90: percentile(&sorted, 90),
        p99: percentile(&sorted, 99),
        vwap: v,
        buy_vwap: vwap(left),
        sell_vwap: vwap(right),
        at_or_below,
        bars: bars(fills, per_bar),
        sizes,
        accounts: accounts(fills),
    }
}

fn bars(fills: &[Fill], per: usize) -> Vec<Bar> {
    let mut out = Vec::new();
    if fills.is_empty() || per < 1 {
        return out;
    }
    let mut volumes: Vec<i64> = fills.iter().map(|f| f.qty).collect();
    for chunk in volumes.chunks_mut(per) {
        let mut sum = 0;
        for x in chunk.iter_mut() {
            sum += *x;
            *x = sum;
        }
    }
    let mut at = 0;
    for c in fills.chunks(per) {
        at += c.len();
        let mut b = Bar { open: c[0].price, high: c[0].price, low: c[0].price, close: c[c.len() - 1].price, volume: volumes[at - 1], trades: c.len() };
        for f in c {
            b.high = b.high.max(f.price);
            b.low = b.low.min(f.price);
        }
        out.push(b);
    }
    let n = out.len();
    if n >= 2 && out[n - 1].trades * 2 < per {
        let last = out.pop().unwrap();
        let prev = &mut out[n - 2];
        prev.high = prev.high.max(last.high);
        prev.low = prev.low.min(last.low);
        prev.close = last.close;
        prev.volume += last.volume;
        prev.trades += last.trades;
    }
    out
}

fn accounts(fills: &[Fill]) -> Vec<AccountRow> {
    let mut by: HashMap<u32, AccountRow> = HashMap::new();
    for f in fills {
        let (buyer, seller) = if f.side == Side::Buy { (f.taker.account, f.maker.account) } else { (f.maker.account, f.taker.account) };
        let b = by.entry(buyer).or_insert(AccountRow { account: buyer, bought: 0, sold: 0, fills: 0 });
        b.bought += f.qty;
        b.fills += 1;
        let s = by.entry(seller).or_insert(AccountRow { account: seller, bought: 0, sold: 0, fills: 0 });
        s.sold += f.qty;
        s.fills += 1;
    }
    let mut rows: Vec<AccountRow> = by.into_values().collect();
    rows.sort_unstable_by_key(|r| r.account);
    rows.sort_by(|x, y| (y.bought + y.sold).cmp(&(x.bought + x.sold)));
    rows
}

// ---- the report

fn price_text(t: i64) -> String {
    let sign = if t < 0 { "-" } else { "" };
    let t = t.abs();
    format!("{}{}.{:02}", sign, t / 100, t % 100)
}

fn opt_text(p: Option<i64>) -> String {
    p.map_or_else(|| "-".to_string(), price_text)
}

/// What the dogfood's JSON report holds, built whether or not it is
/// written, as the dogfood builds it.
struct ReportValues {
    prices: Vec<String>,
    levels: Vec<String>,
    largest: Vec<String>,
    bars: Vec<String>,
    accounts: Vec<AccountRow>,
}

fn build_report(e: &Engine, s: &Summary, bids: &[Depth], asks: &[Depth]) -> ReportValues {
    let prices = [s.vwap, s.buy_vwap, s.sell_vwap, s.p50, s.p99, e.log.last, e.bids.best(), e.asks.best()].iter().flatten().map(|p| price_text(*p)).collect();
    let levels = bids.iter().chain(asks).map(|d| price_text(d.price)).collect();
    let largest = e.log.top.iter().map(|f| format!("{} {} {}", f.taker, f.maker, price_text(f.price))).collect();
    let bars = s.bars.iter().map(|b| price_text(b.open) + &price_text(b.high) + &price_text(b.low) + &price_text(b.close)).collect();
    ReportValues { prices, levels, largest, bars, accounts: s.accounts.iter().take(10).copied().collect() }
}

fn line<W: Write, V: fmt::Display>(w: &mut W, label: &str, v: V) -> std::io::Result<()> {
    writeln!(w, "{:<18}{:>12}", label, v)
}

fn depth_table<W: Write>(w: &mut W, bids: &[Depth], asks: &[Depth]) -> std::io::Result<()> {
    writeln!(w, "{:>10} {:>6} {:>12} | {:<12} {:>10} {:>6}", "bid qty", "orders", "bid", "ask", "ask qty", "orders")?;
    let rows = bids.len().max(asks.len());
    if rows == 0 {
        writeln!(w, "{:^60}", "(the book is empty)")?;
    }
    for i in 0..rows {
        match bids.get(i) {
            Some(b) => write!(w, "{:>10} {:>6} {:>12}", b.qty, b.orders, price_text(b.price))?,
            None => write!(w, "{:30}", "")?,
        }
        write!(w, " | ")?;
        if let Some(a) = asks.get(i) {
            write!(w, "{:<12} {:>10} {:>6}", price_text(a.price), a.qty, a.orders)?;
        }
        writeln!(w)?;
    }
    Ok(())
}

fn fills_table<'a, W: Write, I: Iterator<Item = &'a Fill>>(w: &mut W, title: &str, mut fills: std::iter::Peekable<I>) -> std::io::Result<()> {
    writeln!(w, "{}", title)?;
    if fills.peek().is_none() {
        return writeln!(w, "  (none)");
    }
    for f in fills {
        writeln!(w, "  {:>10}  {:<4} {:>7} @ {:>10}  taker {:<9} maker {}", f.ts, f.side, f.qty, price_text(f.price), f.taker, f.maker)?;
    }
    Ok(())
}

fn text_report<W: Write>(w: &mut W, e: &Engine, s: &Summary, bids: &[Depth], asks: &[Depth], sum: &str) -> std::io::Result<()> {
    let t = &e.log;
    line(w, "commands", e.events)?;
    line(w, "orders", e.orders.seq)?;
    line(w, "fills", t.fills.len())?;
    line(w, "cancels", e.cancels)?;
    line(w, "busts", t.busted)?;
    line(w, "live orders", e.orders.live_count())?;
    line(w, "stop queue entries", e.buy_stops.len() + e.sell_stops.len())?;
    for (i, n) in e.rejects.iter().enumerate() {
        if *n > 0 {
            writeln!(w, "rejected: {:<14}{:>12}", REJECT_NAMES[i], n)?;
        }
    }
    line(w, "volume", t.traded)?;
    line(w, "vwap", opt_text(s.vwap))?;
    line(w, "vwap, buyers", opt_text(s.buy_vwap))?;
    line(w, "vwap, sellers", opt_text(s.sell_vwap))?;
    line(w, "median fill", opt_text(s.p50))?;
    line(w, "90th percentile", opt_text(s.p90))?;
    line(w, "99th percentile", opt_text(s.p99))?;
    line(w, "fills <= vwap", s.at_or_below)?;
    line(w, "last", opt_text(t.last))?;
    line(w, "traded prices", t.volume.len())?;
    writeln!(w)?;
    depth_table(w, bids, asks)?;
    writeln!(w)?;
    fills_table(w, "largest fills", t.top.iter().peekable())?;
    fills_table(w, "recent fills, newest first", t.tape.iter().rev().peekable())?;
    let mut busiest: Vec<(i64, i64)> = t.volume.iter().map(|(p, q)| (*p, *q)).collect();
    busiest.sort_by(|x, y| y.1.cmp(&x.1));
    writeln!(w, "most traded prices")?;
    for (p, q) in busiest.iter().take(3) {
        writeln!(w, "  {:>12} {:>10}", price_text(*p), q)?;
    }
    let mut resting: Vec<Order> = e.orders.pool.slots.iter().filter(|s| s.used && e.orders.has(s.o.seq)).map(|s| s.o).collect();
    resting.sort_unstable_by_key(|o| o.seq);
    writeln!(w, "oldest resting orders ({} live)", resting.len())?;
    for r in resting.iter().take(3) {
        let p = if r.kind == Kind::Stop { r.stop } else { r.price };
        writeln!(w, "  {:<9} {:<4} {:<9} {:>7} @ {:>10}", r.id, r.side, r.kind, r.left, price_text(p))?;
    }
    if !s.bars.is_empty() {
        writeln!(w, "bars, newest first\n{:>5} {:>10} {:>10} {:>10} {:>10} {:>10}", "#", "open", "high", "low", "close", "volume")?;
        for (i, b) in s.bars.iter().rev().take(10).enumerate() {
            writeln!(w, "{:>5} {:>10} {:>10} {:>10} {:>10} {:>10}", s.bars.len() - i, price_text(b.open), price_text(b.high), price_text(b.low), price_text(b.close), b.volume)?;
        }
    }
    writeln!(w, "fill sizes")?;
    let labels = ["<=10", "<=50", "<=100", "<=500", "<=1000", "<=5000", ">5000"];
    for (i, c) in s.sizes.iter().enumerate() {
        writeln!(w, "  {:<7}{:>9}", labels[i], c)?;
    }
    writeln!(w, "accounts by volume\n{:>4} {:>8} {:>10} {:>10} {:>7}", "rank", "account", "bought", "sold", "fills")?;
    for (i, r) in s.accounts.iter().take(10).enumerate() {
        writeln!(w, "{:>4} {:>8} {:>10} {:>10} {:>7}", i + 1, r.account, r.bought, r.sold, r.fills)?;
    }
    writeln!(w, "checksum {}", sum)
}

// ---- main

fn usage(msg: &str) -> ExitCode {
    eprintln!("error: {}\nusage: orderbook replay FEED [--quiet] [--snapshot N]", msg);
    ExitCode::from(2)
}

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.first().map(String::as_str) != Some("replay") {
        return usage("the only command is replay");
    }
    let (mut quiet, mut every, mut path) = (false, 10_000usize, None::<String>);
    let mut i = 1;
    while i < args.len() {
        let a = &args[i];
        if a == "--quiet" {
            quiet = true;
        } else if a == "--snapshot" && i + 1 < args.len() {
            match args[i + 1].parse::<i64>() {
                Ok(n) if (0..=1_000_000_000_000).contains(&n) => every = n as usize,
                _ => return usage("--snapshot does not take that value"),
            }
            i += 1;
        } else if !a.starts_with("--") && path.is_none() {
            path = Some(a.clone());
        } else {
            return usage(&format!("unexpected argument {}", a));
        }
        i += 1;
    }
    let Some(path) = path else { return usage("replay needs a feed file") };
    let file = match File::open(&path) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("error: cannot read {}: {}", path, e);
            return ExitCode::from(4);
        }
    };
    let size = match file.metadata() {
        Ok(m) if m.is_file() => m.len() as usize,
        _ => {
            eprintln!("error: cannot read {}", path);
            return ExitCode::from(4);
        }
    };
    let mut e = Engine::new(size / 3 + 2, every);
    let mut r = BufReader::with_capacity(64 * 1024, file);
    let mut buf: Vec<u8> = Vec::with_capacity(512);
    let mut n = 0;
    loop {
        buf.clear();
        match r.read_until(b'\n', &mut buf) {
            Ok(0) => break,
            Ok(_) => {}
            Err(err) => {
                eprintln!("error: cannot read {}: {}", path, err);
                return ExitCode::from(4);
            }
        }
        n += 1;
        let mut l = &buf[..];
        if l.last() == Some(&b'\n') {
            l = &l[..l.len() - 1];
        }
        if l.last() == Some(&b'\r') {
            l = &l[..l.len() - 1];
        }
        let cmd = match std::str::from_utf8(l) {
            Ok(s) if !s.contains('"') => parse(s),
            _ => Err(()),
        };
        match cmd {
            Ok(Some(c)) => e.submit(c),
            Ok(None) => {}
            Err(()) => {
                eprintln!("{}:{}: not a valid record", path, n);
                return ExitCode::from(3);
            }
        }
    }
    let out = std::io::stdout();
    let mut w = BufWriter::with_capacity(64 * 1024, out.lock());
    let s = summarize(&e.log.fills, 1000);
    let (bids, asks) = (e.bids.top(5), e.asks.top(5));
    let sum = format!("{:016x}", e.checksum());
    let res = if quiet {
        writeln!(w, "commands {} fills {} checksum {}", e.events, e.log.fills.len(), sum)
    } else {
        text_report(&mut w, &e, &s, &bids, &asks, &sum)
    };
    // The report's values are built, as the dogfood builds its JSON report;
    // only its --json would write them.
    let rv = build_report(&e, &s, &bids, &asks);
    let snap = e.snap;
    let eb = e.bids.close(&e.orders);
    let ea = e.asks.close(&e.orders);
    let res = res.and_then(|_| {
        if !quiet {
            writeln!(w, "last snapshot at command {}: {} live orders, {} fills", snap.event, snap.live, snap.fills)?;
            writeln!(w, "expired at close: {} bids ({}), {} asks ({})", eb.orders, eb.qty, ea.orders, ea.qty)?;
        }
        w.flush()
    });
    drop((rv.prices, rv.levels, rv.largest, rv.bars, rv.accounts));
    if res.is_err() {
        return ExitCode::from(4);
    }
    ExitCode::SUCCESS
}
