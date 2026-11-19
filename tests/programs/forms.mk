// What a program prints through the forms of the reference for
// value arms, for over iterators, an error's
// own Format, mut methods through an index, the release protocol
// and the systems primitives, NaN payloads through Bits
// included. forms.out was written by a run and each line checked by hand against the
// reference; every target and profile prints it.
import std.mem

error NotFound(id: i64)
error Denied
error Busy(tries: int, wait: i64)

type LoadError = NotFound | Denied | Busy

Load(id: i64): LoadError!str {
    if id < 0 {
        fail Denied
    }
    if id == 0 {
        fail Busy(tries: 3, wait: -1)
    }
    fail NotFound(id: id)
}

// Sign is a value if chain in return position.
Sign(n: int): str {
    return if n < 0 { "negative" } else if n == 0 { "zero" } else { "positive" }
}

// Optional is a value if T is X bound to a local: none at T = int, some(v) otherwise.
Optional[T: Plain + Format](v: T): ?T {
    x: ?T = if T is int { none } else { some(v) }
    return x
}

// Either takes T from the arm that is not narrowed: w at T = int, v otherwise.
Either[T: Plain + Format](v: T, w: int): T {
    x := if T is int { w } else { v }
    return x
}

struct Countdown {
    n: int
}

Countdown.Next(mut self): ?int {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return some(self.n)
}

struct Counter {
    n: int
}

Counter.Bump(mut self) {
    self.n += 1
}

// A Slice[T] implementer that also has a Next: an index receiver goes through ItemsMut,
// and a for over it walks the elements, not Next's payloads.
struct Bank {
    cs: [3]Counter
}

impl Slice[Counter] for Bank {
    Items(&self): []Counter {
        return self.cs[0..]
    }

    ItemsMut(mut self): mut []Counter {
        return mut self.cs[0..]
    }
}

Bank.Next(mut self): ?str {
    return none
}

Arms(out: mut Writer): WriteError!void {
    for n in [-2, 0, 5] {
        s := Sign(n)
        try Print(out, "{n} is {s}\n", (n, s))
    }
    a := Optional(7)
    b := Optional(true)
    try Print(out, "optional {a} {b}\n", (a, b))
    c := Either(9, 4)
    d := Either(false, 4)
    try Print(out, "either {c} {d}\n", (c, d))
    width := "12x".ParseInt() else { 80 }
    try Print(out, "width {width}\n", (width))
}

Errors(out: mut Writer): WriteError!void {
    ids: [3]i64 = [-1, 0, 3]
    for id in ids {
        s := Load(id) else e {
            try Print(out, "load {id}: {e}\n", (id, e))
            continue
        }
        try Print(out, "load {id}: {s}\n", (id, s))
    }
}

Iterators(out: mut Writer, a: &Arena): WriteError|OutOfMemory!void {
    var letters := 0
    for w in "a bb  ccc".Words() {
        letters += w.Len()
    }
    var down := List[int].New(a)
    for k in Countdown(n: 3) {
        try down.Push(k)
    }
    var sum := 0
    for i, (a: x, b: y) in [(a: 1, b: 2), (a: 3, b: 4)] {
        sum += i * 100 + x * y
    }
    try Print(out, "letters {letters} down {down} sum {sum}\n", (letters, down: &down, sum))
}

Receivers(out: mut Writer): WriteError!void {
    var cs := [Counter(n: 0), Counter(n: 10)]
    cs[1].Bump()
    cs[1].Bump()
    cs[0].Bump()
    first := cs[0].n
    second := cs[1].n
    try Print(out, "counters {first} {second}\n", (first, second))
    var b := Bank(cs: [Counter(n: 0), Counter(n: 1), Counter(n: 2)])
    b[2].Bump()
    b[0].Bump()
    var walk := 0
    for i, c in b {
        walk += i * 10 + c.n
    }
    low := b[0].n
    high := b[2].n
    try Print(out, "bank {low} {high} walk {walk}\n", (low, high, walk))
}

Release(out: mut Writer, a: &Alloc): WriteError|OutOfMemory!void {
    var m := TextMap[HeapText].New(a)
    onerr m.Free(a)
    for round in 0..3 {
        var v := HeapText.New(a)
        onerr v.Free(a)
        try v.Push(a, "value ")
        try v.Push(a, if round == 2 { "two" } else { "one" })
        try m.Set(a, "key", v)
    }
    if v := m.Get("key") {
        text := v.Str()
        n := m.Len()
        try Print(out, "map {n} {text}\n", (n, text))
    }
    m.Free(a)
    var t := HeapText.New(a)
    onerr t.Free(a)
    try t.Push(a, "old")
    var fresh := HeapText.New(a)
    onerr fresh.Free(a)
    try fresh.Push(a, "new")
    old := Replace(mut t, fresh)
    onerr old.Free(a)
    was := old.Str()
    now := t.Str()
    try Print(out, "replace {was} {now}\n", (was, now))
    old.Free(a)
    t.Free(a)
}

// Bits and FromBits keep a NaN's payload, a signaling one's included, in the evaluator (these
// consts) and at run time (Primitives), and -0.0 keeps its sign bit.
const QuietBits: u64 = f64.FromBits(0x7ff8_0000_0000_0001).Bits()
const SignalBits: u32 = f32.FromBits(0xff80_0001).Bits()

Primitives(out: mut Writer, a: &Arena): WriteError|OutOfMemory!void {
    var x := 1
    var y := 2
    Swap(mut x, mut y)
    var xs := [1, 2, 3, 4, 5]
    (left, right) := xs.SplitAt(2)
    left[0] = right[0]
    same_none := xs.GetPair(1, 1).IsNone()
    far_none := xs.GetPair(0, 9).IsNone()
    if p := xs.GetPair(0, 4) {
        *p.a += *p.b
    }
    var lens := List[int].New(a)
    for c in xs.Chunks(2) {
        try lens.Push(c.Len())
    }
    for c in xs.ChunksMut(2) {
        c[0] = -c[0]
    }
    try Print(out, "swap {x} {y} pair {same_none} {far_none} chunks {lens} xs {xs}\n", (x, y, same_none, far_none, lens: &lens, xs: xs[0..5]))
    one := (1.0).Bits()
    back := f64.FromBits(one)
    half: f32 = 0.5
    half_bits := half.Bits()
    try Print(out, "bits {one:x} {back} {half_bits:x}\n", (one, back, half_bits))
    quiet := f64.FromBits(0x7ff8_0000_0000_0001).Bits()
    signal := f32.FromBits(0xff80_0001).Bits()
    neg := (-0.0).Bits()
    try Print(out, "nan {qc:x} {quiet:x} {sc:x} {signal:x} neg {neg:x}\n", (qc: QuietBits, quiet, sc: SignalBits, signal, neg))
}

Pool(out: mut Writer, a: &Arena): WriteError|OutOfMemory!void {
    var p := mem.Pool[int].New(a)
    h := try p.Add(10)
    k := try p.Add(20)
    _ = try p.Add(30)
    if q := p.GetPair(h, k) {
        *q.b += *q.a
    }
    var total := 0
    for (handle, value) in p.Iter() {
        _ = handle
        total += *value
    }
    dropped := p.Sweep(fn(g: mem.Handle, v: &int): bool {
        _ = g
        return *v != 30
    })
    left := p.Len()
    // h is live and k was swept: equal handles give none, and so does a stale one
    same := p.GetPair(h, h).IsNone()
    stale := p.GetPair(h, k).IsNone()
    gone := p.Get(k).IsNone()
    size := SizeOf[mem.Handle]()
    // h is live in p; another pool refuses it (its generations start at its own base)
    var other := mem.Pool[int].New(a)
    _ = try other.Add(1)
    foreign := other.Get(h).IsNone()
    try Print(out, "pool {total} dropped {dropped} left {left} same {same} stale {stale} gone {gone} handle {size} foreign {foreign}\n", (total, dropped, left, same, stale, gone, size, foreign))
}

// Calc puts value arms where the functions above do not: a compound assignment
// whose arm assigns v first, a last line after statements, a match arm whose last line is
// a value if, and continue and break as never arms of a value if. Calc(3) is 411009:
// v = 10 then v += 1, w = 9 (k = 9 > 8), acc = 1 + 3 (2 and 4 continue, 5 breaks).
Calc(n: int): int {
    var v := 0
    v += if n > 2 {
        v = 10
        1
    } else {
        2
    }
    w := match n {
        0 { 100 }
        _ {
            k := n * 3
            if k > 8 { k } else { -k }
        }
    }
    var acc := 0
    for x in [1, 2, 3, 4, 5] {
        acc += if x % 2 == 0 { continue } else if x == 5 { break } else { x }
    }
    return v * 1000 + w + acc * 100000
}

const CalcAtThree: int = Calc(3)

// Clamp returns from an arm of a value if, and Parse fails from an arm of a value match:
// return and fail as never arms.
Clamp(n: int): int {
    m := if n > 100 { return 100 } else { n }
    return m + 1
}

Parse(n: int): Denied!int {
    v := match n {
        0 { fail Denied }
        _ { n * 2 }
    }
    return v
}

// SwapRefs forwards to the prelude's Swap. It is not named Swap: a module-level Swap hides
// the prelude's, and its body would call itself.
SwapRefs[T](a: mut T, b: mut T) *a into b, *b into a {
    Swap(a, b)
}

struct Span {
    lo: int
    hi: int
}

// Next returns *i and counts the call, so an index receiver that evaluated it twice shows.
Next(i: mut int): int {
    *i += 1
    return *i - 1
}

// Values runs Calc at n = 3 (no arguments) against the evaluator's Calc(3), then value
// arms as a constructor's field, a record literal's field and an array element, a linear
// value moved out of an if arm, and index receivers whose index has a side effect.
Values(out: mut Writer, a: &Alloc, n: int): WriteError|OutOfMemory!void {
    c := CalcAtThree
    r := Calc(n)
    s := Span(lo: if n > 2 { n } else { 0 }, hi: match n {
        3 { 30 }
        _ { -1 }
    })
    lo := s.lo
    hi := s.hi
    pair := (x: if n % 2 == 1 { "odd" } else { "even" }, y: n)
    px := pair.x
    xs := [if n > 2 { 7 } else { 8 }, 3]
    var cs := [Counter(n: 0), Counter(n: 0), Counter(n: 0)]
    var k := 0
    cs[Next(mut k)].Bump()
    cs[Next(mut k)].Bump()
    c0 := cs[0].n
    c1 := cs[1].n
    c2 := cs[2].n
    var made := if n > 2 {
        var t := HeapText.New(a)
        onerr t.Free(a)
        try t.Push(a, "big")
        t
    } else {
        HeapText.New(a)
    }
    onerr made.Free(a)
    text := made.Str()
    try Print(out, "values {c} {r} span {lo} {hi} pair {px} xs {xs} index {k} {c0} {c1} {c2} made {text}\n", (c, r, lo, hi, px, xs: xs[0..2], k, c0, c1, c2, text))
    made.Free(a)
    big := Clamp(n * 100)
    small := Clamp(n)
    parsed := Parse(n) else { -1 }
    refused := Parse(n - 3) else { -1 }
    var tally := 0
    tally += match n {
        3 { 5 }
        _ { 6 }
    }
    var p1 := 3
    var p2 := 4
    SwapRefs(mut p1, mut p2)
    try Print(out, "never {big} {small} {parsed} {refused} tally {tally} swaprefs {p1} {p2}\n", (big, small, parsed, refused, tally, p1, p2))
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    try Arms(mut out)
    try Errors(mut out)
    try Iterators(mut out, sys.arena)
    try Receivers(mut out)
    try Release(mut out, sys.heap.Alloc())
    try Primitives(mut out, sys.arena)
    try Pool(mut out, sys.arena)
    try Values(mut out, sys.heap.Alloc(), sys.args.Len() + 2)
}
