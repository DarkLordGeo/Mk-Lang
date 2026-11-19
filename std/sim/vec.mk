// Why: the simulated world lives on the runtime heap, outside any arena,
// and holds many growable tables inside its own plain records, so it needs
// a growable array that is plain, not linear.
//
// What: Vec[T], a plain growable array of plain values (Empty, Push,
// Reserve, Get, Set, Free and the rest), and AppendBytes.
//
// How: doubling growth on rt.HeapAlloc, explicit Free; details follow.
//
// Vec[T] is a raw array of plain values: doubling growth (4, 8, 16, ...: amortized O(1) push, Rust's RawVec
// and Go's append), a bounds panic on a bad index, and no hidden
// allocation: Push and Reserve are the only calls that allocate, each
// through rt.HeapAlloc, and Free gives the block back. A Vec is plain, not
// linear, so the world holds many inside its own plain records (a node's
// pages, a connection end's bytes) and frees them explicitly: the release
// protocol's Free, FreeEach and Replace are for linear values and do not
// apply here.

import std.raw
import std.rt

struct Vec[T: Plain] {
    p: *T
    n: int
    cap: int
}

// MaxVecBytes bounds a Vec's byte size: past it a growth fails as out of
// memory instead of overflowing the size computation.
const MaxVecBytes: int = 0x1000_0000_0000

Vec[T].Empty(): Vec[T] {
    return Vec[T](p: raw.Null[T](), n: 0, cap: 0)
}

Vec[T].Len(&self): int {
    return self.n
}

// CheckIndex panics (code 3, the bounds code) when i is outside 0..n.
Vec[T].CheckIndex(&self, i: int) {
    if i < 0 || i >= self.n {
        raw.Fail(3, "std.sim: index out of range")
    }
}

Vec[T].Get(&self, i: int): T {
    self.CheckIndex(i)
    unsafe "0 <= i < n, and the first n slots are initialized" {
        return raw.Read(raw.Offset(self.p, i))
    }
}

Vec[T].Set(mut self, i: int, x: T) {
    self.CheckIndex(i)
    unsafe "0 <= i < n; T is plain, so the old value needs no drop" {
        raw.Write(raw.Offset(self.p, i), x)
    }
}

Vec[T].Mut(mut self, i: int): mut T {
    self.CheckIndex(i)
    unsafe "0 <= i < n; the slot lives in self's block until the next growth, which needs mut self" {
        return mut *raw.Offset(self.p, i)
    }
}

// Reserve makes room for at least need values; false when the heap is
// exhausted or the size would pass MaxVecBytes (nothing changed then).
Vec[T].Reserve(mut self, need: int): bool {
    if need <= self.cap {
        return true
    }
    var c := if self.cap > 0 { self.cap * 2 } else { 4 }
    if c < need {
        c = need
    }
    size := SizeOf[T]()
    if size > 0 && c > MaxVecBytes / size {
        return false
    }
    q := rt.HeapAlloc(c * size, 8)
    if raw.IsNull(q) {
        return false
    }
    unsafe "q is a fresh block of c slots; the old block holds n initialized plain values" {
        nq := raw.PtrCast[u8, T](q)
        if self.n > 0 {
            raw.Copy(nq, self.p, self.n)
        }
        if !raw.IsNull(self.p) {
            rt.HeapFree(raw.PtrCast[T, u8](self.p), self.cap * size, 8)
        }
        self.p = nq
    }
    self.cap = c
    return true
}

// Push appends x; false when out of memory (nothing changed).
Vec[T].Push(mut self, x: T): bool {
    if !self.Reserve(self.n + 1) {
        return false
    }
    unsafe "n < cap after Reserve" {
        raw.Write(raw.Offset(self.p, self.n), x)
    }
    self.n += 1
    return true
}

// Truncate keeps the first m values (0 <= m <= n).
Vec[T].Truncate(mut self, m: int) {
    if m < 0 || m > self.n {
        raw.Fail(3, "std.sim: truncate out of range")
    }
    self.n = m
}

// InsertAt puts x at index i (0 <= i <= n), moving i..n up by one:
// O(n - i); false when out of memory, nothing changed.
Vec[T].InsertAt(mut self, i: int, x: T): bool {
    if i < 0 || i > self.n {
        raw.Fail(3, "std.sim: insert out of range")
    }
    if !self.Reserve(self.n + 1) {
        return false
    }
    unsafe "n < cap after Reserve; slots i..n move up by one" {
        if i < self.n {
            raw.Move(raw.Offset(self.p, i + 1), raw.Offset(self.p, i), self.n - i)
        }
        raw.Write(raw.Offset(self.p, i), x)
    }
    self.n += 1
    return true
}

// RemoveAt deletes value i, keeping the order of the rest: O(n - i).
Vec[T].RemoveAt(mut self, i: int): T {
    x := self.Get(i)
    unsafe "0 <= i < n; slots i+1..n move down by one" {
        if i + 1 < self.n {
            raw.Move(raw.Offset(self.p, i), raw.Offset(self.p, i + 1), self.n - i - 1)
        }
    }
    self.n -= 1
    return x
}

// View is the first n values, alive while self is not changed.
Vec[T].View(&self): []T {
    unsafe "the first n slots are initialized, and self's block outlives the loan; an empty view uses an aligned dangling pointer, never read" {
        if self.n == 0 {
            return raw.Slice(raw.FromAddr[T](Cast[uint](AlignOf[T]())), 0)
        }
        return raw.Slice(self.p, self.n)
    }
}

Vec[T].Free(mut self) {
    if !raw.IsNull(self.p) {
        unsafe "p is this Vec's block of cap slots" {
            rt.HeapFree(raw.PtrCast[T, u8](self.p), self.cap * SizeOf[T](), 8)
        }
    }
    self.p = raw.Null[T]()
    self.n = 0
    self.cap = 0
}

// AppendBytes adds data to a byte Vec; false when out of memory.
AppendBytes(v: mut Vec[u8], data: []u8): bool {
    if data.Len() == 0 {
        return true
    }
    if !v.Reserve(v.n + data.Len()) {
        return false
    }
    unsafe "n + data.Len() <= cap after Reserve" {
        raw.Copy(raw.Offset(v.p, v.n), raw.Data(data), data.Len())
    }
    v.n += data.Len()
    return true
}

// DropFront deletes the first k values (0 <= k <= n), keeping the order of
// the rest: one move of n - k values.
Vec[T].DropFront(mut self, k: int) {
    if k < 0 || k > self.n {
        raw.Fail(3, "std.sim: drop out of range")
    }
    if k == 0 {
        return
    }
    unsafe "0 < k <= n; slots k..n move to the start" {
        if k < self.n {
            raw.Move(self.p, raw.Offset(self.p, k), self.n - k)
        }
    }
    self.n -= k
}
