// verdict: ok
// rule: D-454
// note: the accept twins of the D454_reject_allocator_field_* units: each function states the store it makes (*self into self, *xs into xs, *g into g, and s: D into s on a by-value s) and checks clean; the shapes are those units' (a spawned task, a slice element, an enum payload, a generic instance, by-value parameters, a Text field)
Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

struct Deep {
    a: &i32
    b: mut &i32
    l: List[i32]
}

Deep.Spawn(mut self, io: Io) *self into self {
    scope s := io.Scope() {
        s.go Put(mut self.a, mut self.b)
    }
}

Mix(xs: mut []Deep) *xs into xs {
    Put(mut xs[0].a, mut xs[0].b)
}

struct DeepText {
    a: &i32
    b: mut &i32
    t: Text
}

DeepText.Mix(mut self) *self into self {
    Put(mut self.a, mut self.b)
}

enum Kind {
    One(l: List[i32])
    Two
}

struct DeepKind {
    a: &i32
    b: mut &i32
    k: Kind
}

DeepKind.Mix(mut self) *self into self {
    Put(mut self.a, mut self.b)
}

struct Ctx {
    ar: &Arena
    name: str
}

struct G[T] {
    x: T
    a: &i32
    b: mut &i32
}

UseCtx(g: mut G[Ctx]) *g into g {
    Put(mut g.a, mut g.b)
}

struct In {
    a: &Arena
}

struct D1 {
    a: &i32
    b: mut &i32
    inn: In
}

F1(var s: D1 into s) {
    Put(mut s.a, mut s.b)
}

struct D2 {
    a: &i32
    b: mut &i32
    oa: ?&Arena
}

F2(var s: D2 into s) {
    Put(mut s.a, mut s.b)
}

G2(var s: D2 into s) {
    if a := s.oa {
        _ = a
        Put(mut s.a, mut s.b)
    }
}

struct D3 {
    a: &i32
    b: mut &i32
    as: [2]&Alloc
}

F3(var s: D3 into s) {
    x := s.as[0]
    _ = x
    Put(mut s.a, mut s.b)
}

enum E4 {
    A(ar: &Arena)
    B(n: i32)
}

struct D4 {
    a: &i32
    b: mut &i32
    e: E4
}

F4(var s: D4 into s) {
    Put(mut s.a, mut s.b)
}

view struct D5 {
    a: &i32
    b: mut &i32
    ar: &Arena
}

F5(var s: D5 into s) {
    Put(mut s.a, mut s.b)
}

struct D7 {
    a: &i32
    b: mut &i32
    l: List[i32]
}

F7(var s: D7 into s) {
    Put(mut s.a, mut s.b)
}

struct D8 {
    a: &i32
    b: mut &i32
    bx: Box[Arena]
}

F8(var s: D8 into s, h: &Heap) {
    Put(mut s.a, mut s.b)
    D8(a: _, b: _, bx: bx) := s
    ar := bx.Take(h.Alloc())
    ar.Free()
}

export Main(sys: &System): void {
}
