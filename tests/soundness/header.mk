// The soundness generator's helper header.
// The helpers of the loan-rule model in current
// syntax, as tests/conformance/model/ spells them, with i32 replaced by
// i64, then the helpers this generator adds. A generated program contains
// only the items it calls; the generator copies each one from this file
// between its "// item" markers, so this file and the generated text agree.
// The text is internal/corpus/soundshapes.go's soundHeader, and
// TestSoundGen_HeaderFile keeps the two equal.

// item Deep
struct Deep {
    a: &i64
    b: mut &i64
}

// item D2
struct D2 {
    a: &i64
    c: &Cell[&i64]
}

// item Sl
struct Sl {
    m: mut i64
}

// item Lx
struct Lx {
    src: &i64
    pos: i64
}

// item Put
Put(c: &Cell[&i64], v: &i64 into c) {
    c.Store(v)
}

// item SetRef
SetRef(r: mut &i64, v: &i64 into r) {
    *r = v
}

// item Deep.Mix
Deep.Mix(mut self) *self into self {
    *self.b = self.a
}

// item D2.Mix2
D2.Mix2(mut self) *self into self {
    self.c.Store(self.a)
}

// item Id
Id(x: &i64): &i64 {
    return x
}

// item Pick
Pick(var k: i64, a: &i64, b: &i64): &i64 from a, b {
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        return a
    }
    return b
}

// item Hold
Hold(c: &Cell[&i64]): &i64 from c {
    return c.Load()
}

// item PeekS
PeekS(s: &Sl): &i64 {
    return &*s.m
}

// item Cur
Cur(l: mut Lx): &i64 {
    return l.src
}

// item Src
Src(l: &Lx): &i64 from l.src {
    return l.src
}

// item Own
Own(l: &Lx): &i64 {
    return &l.pos
}

// item Bump
Bump(out: mut i64) {
    *out = *out +% 1
}

// item Sum
Sum(xs: []i64, out: mut i64) {
    for x in xs {
        *out = *out +% x
    }
}

// item Read
Read(x: &i64, out: mut i64) {
    *out = *out *% 31 +% *x
}

// item Spawner
Spawner(s: &Scope, x: mut i64 into s) {
    s.go Bump(x)
}

// item Two
Two(a: &i64, b: mut i64) {
    *b = *b *% 31 +% *a
}

// item Swap2
Swap2(a: mut &i64, b: mut &i64) *a into b, *b into a {
    t := *a
    *a = *b
    *b = t
}

// item Set2
Set2(p: mut Deep, v: &i64 into p) {
    *p.b = v
}

// item Find
Find(var k: i64, x: &i64): ?&i64 {
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        return some(x)
    }
    return none
}

// item Drop
Drop(a: &Arena, bp: Arena, n: i64): OutOfMemory!i64 {
    b := try a.Sub()
    b.Free()
    bp.Free()
    return n
}

// item Fold
Fold(xs: []i64, const f: fn(x: &i64): i64): i64 {
    var h: i64 = 0
    for i in 0..xs.Len() {
        h = h *% 31 +% f(&xs[i])
    }
    return h
}

// item FoldFwd
FoldFwd(xs: []i64, const f: fn(x: &i64): i64): i64 {
    return Fold(xs, f) *% 7
}

// item Pass
Pass(a: &i64, const f: fn(x: &i64): &i64): &i64 from a {
    return f(a)
}

// item Sl.Apply
Sl.Apply(mut self, const f: fn(x: &i64): i64) {
    v := f(&*self.m)
    *self.m = v
}

// item Twice
Twice[T](f: fn(T): T, x: T): T {
    return f(f(x))
}

// item Ident
Ident[T](x: T): T {
    return x
}

// item Via
Via[T](f: fn(x: &T): &T, x: &T): &T from x {
    return f(x)
}

// item IdR
IdR[T](x: &T): &T {
    return x
}

// item Bx
struct Bx[T: Plain] {
    v: T
}

// item Bx.Get
Bx[T].Get(&self): T {
    return self.v
}

// item ApplyB
ApplyB[T: Plain](f: fn(b: &Bx[T]): T, b: &Bx[T]): T {
    return f(b)
}

// item Ct
struct Ct {
    n: i64
}

// item Ct.Add
Ct.Add(mut self, d: i64) {
    self.n = self.n *% 31 +% d
}

// item It
struct It {
    xs: []i64
    i: int
}

// item It.Next
It.Next(mut self): ?(i: i64, x: &i64) from self.xs {
    if self.i >= self.xs.Len() {
        return none
    }
    j := self.i
    self.i += 1
    return some((i: i64(j), x: &self.xs[j]))
}

// item Miss
error Miss

// item Get
Get(var k: i64, x: &i64): Miss!&i64 {
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        return x
    }
    fail Miss
}

// item Fill
Fill(c: mut []i64, v: i64) {
    for x in mut c {
        *x = *x *% 31 +% v
    }
}

// item Halves
Halves(xs: mut []i64, i: int): (left: mut []i64, right: mut []i64) from xs {
    return xs.SplitAt(i)
}

// item Pair
Pair(xs: mut []i64, i: int, j: int): ?(a: mut i64, b: mut i64) from xs {
    return xs.GetPair(i, j)
}

// item Pieces
Pieces(xs: []i64, n: int): Chunks[i64] from xs {
    return xs.Chunks(n)
}

// item PiecesMut
PiecesMut(xs: mut []i64, n: int): ChunksMut[i64] from xs {
    return xs.ChunksMut(n)
}

// item Cr
struct Cr {
    r: &i64
}

// item Cr.Keep
Cr.Keep(mut self, v: &i64 into self) {
    self.r = v
}

// item Gr
struct Gr {
    rows: [2]Cr
}

// item Bf
struct Bf {
    buf: [3]i64
    i: int
}

// item Bf.Next
Bf.Next(mut self): ?&i64 from self {
    if self.i >= 3 {
        return none
    }
    j := self.i
    self.i += 1
    return some(&self.buf[j])
}

// item HalvesR
HalvesR(xs: mut []&i64, i: int): (left: mut []&i64, right: mut []&i64) from xs {
    return xs.SplitAt(i)
}

// item PairR
PairR(xs: mut []&i64, i: int, j: int): ?(a: mut &i64, b: mut &i64) from xs {
    return xs.GetPair(i, j)
}

// item PiecesMutR
PiecesMutR(xs: mut []&i64, n: int): ChunksMut[&i64] from xs {
    return xs.ChunksMut(n)
}

// item Fp
struct Fp {
    a: &i64
    b: &i64
}

// item FpMk
FpMk(x: &i64, y: &i64): Fp from (a: x, b: y) {
    return Fp(a: x, b: y)
}

// item FpSw
FpSw(x: &i64, y: &i64): Fp from (a: y, b: x) {
    var t := Fp(a: y, b: y)
    t.b = x
    return t
}

// item FpMix
FpMix(t: mut Fp) *t into t {
    t.a = t.b
}

// item FpShow
FpShow(t: &Fp): i64 {
    return *t.a +% *t.b
}

// item FpSet
FpSet(t: mut Fp, v: &i64 into t) {
    t.b = v
}

// item FpTake
FpTake(t: Fp): &i64 {
    return t.b
}

// item FpPick
FpPick(t: Fp, v: &i64): &i64 from t {
    var u := t
    u.a = v
    return u.b
}

// item Fp.Rot
Fp.Rot(mut self) *self into self {
    r := self.a
    self.a = self.b
    self.b = r
}

// item Fq
struct Fq {
    p: Fp
    c: &i64
}

// item Fw
struct Fw {
    a: &i64
    c: Cell[&i64]
}

// item Fw.Stow
Fw.Stow(&self, v: &i64 into self) {
    self.c.Store(v)
}

// item Fa
struct Fa {
    c: &Cell[&i64]
    s: &i64
}

// item FaStash
FaStash(p: &Fa, v: &i64 into p) {
    p.c.Store(v)
}

// item Fs
struct Fs {
    a: mut &i64
    b: &i64
}

// item Fh
struct Fh {
    s: Fs
}

// item FhPut
FhPut(l: mut Fh, x: Fs into l) {
    l.s = x
}

// item FpK
FpK(c: &i64 into x, x: &i64, y: &i64): Fp from (a: x, b: y) {
    return Fp(a: c, b: y)
}

// item FwMix
FwMix(w: &Fw) {
    w.c.Store(w.a)
}

// item Fr
struct Fr {
    p: mut &i64
    q: mut &i64
}

// item FrSet
FrSet(v: &i64 into w, w: mut Fr) {
    *w.q = v
}

// item StashL
StashL(l: &List[&Cell[&i64]], v: &i64 into l) {
    l[0].Store(v)
}

// item StashC2
StashC2(t: &Cell[?&Cell[&i64]], v: &i64 into t) {
    if inner := t.Load() {
        inner.Store(v)
    }
}

// item StashC3
StashC3(t: &Cell[?&Cell[?&Cell[&i64]]], v: &i64 into t) {
    if hc := t.Load() {
        if inner := hc.Load() {
            inner.Store(v)
        }
    }
}

// item Yes
Yes(): bool {
    return true
}

// item Kk
struct Kk {
    c: ?&Cell[&i64]
    n: ?&i64
}

// item Ks
struct Ks {
    name: &i64
    slot: &Cell[&i64]
}

// item Kn
struct Kn {
    s: &i64
    k: i64
}

// item KkName
KkName(k: mut Kk, s: &i64 into k) {
    k.n = some(s)
}

// item KkCell
KkCell(k: mut Kk, c: &Cell[&i64] into k) {
    k.c = some(c)
}

// item KkSlot
KkSlot(k: mut Kk, x: &Ks into k) {
    k.c = some(x.slot)
}

// item KkMid
KkMid(k: mut Kk, c: &Cell[&i64] into a, a: &i64 into k) {
    k.c = some(c)
}

// item KkStar
KkStar(k: mut Kk, q: &Kn, c: &Cell[&i64] into q) *q into k {
    k.c = some(c)
}

// item KkNm
KkNm(c: &Cell[&i64]): &i64 from c {
    return c.Load()
}

// item KkPoke
KkPoke(k: mut Kk, y: &i64 into k) {
    if c := k.c {
        c.Store(y)
    }
}

// item NewIn
NewIn(p: &Arena): OutOfMemory!Arena from p {
    return try Arena.NewIn(p.Alloc())
}
