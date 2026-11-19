// Why: the methods every indexable type shares ([]T, [N]T, List, HeapList)
// are written once, against Slice[T], instead of once per container.
//
// What: Slice[T].Len, Get, GetMut, Swap, Reverse, Fill, CopyFrom, Has and
// Index, and the helper SwapAt.
//
// How: namespace functions instantiated over T only; each body sees self as
// []T or mut []T. Len is an intrinsic (RvLen on the slice). check gives
// implicit bounds: Has and Index need T: Eq, Fill and CopyFrom need T: Plain.
// Two unsafe blocks: SwapAt's bitwise exchange and CopyFrom's raw.Move.

import std.raw

/// The number of elements. O(1).
export Slice[T].Len(&self): int

/// some(&self[i]) when 0 <= i < Len(), else none; never panics. O(1);
/// allocates nothing.
export Slice[T].Get(&self, i: int): ?&T {
    if i < 0 || i >= self.Len() {
        return none
    }
    return some(&self.Items()[i])
}

/// Element i, writable, when 0 <= i < Len(), else none; never panics. O(1).
export Slice[T].GetMut(mut self, i: int): ?mut T {
    if i < 0 || i >= self.Len() {
        return none
    }
    s := self.ItemsMut()
    return some(mut s[i])
}

/// Exchanges elements i and j; i == j changes nothing. Panics (code 2)
/// unless both are in 0..Len(). O(1).
export Slice[T].Swap(mut self, i: int, j: int) {
    n := self.Len()
    if i < 0 || i >= n || j < 0 || j >= n {
        raw.Fail(2, "Slice.Swap: index out of range")
    }
    SwapAt(self.ItemsMut(), i, j)
}

/// Reverses the elements in place. O(Len()); allocates nothing.
export Slice[T].Reverse(mut self) {
    s := self.ItemsMut()
    var i := 0
    var j := s.Len() - 1
    for i < j {
        SwapAt(s, i, j)
        i += 1
        j -= 1
    }
}

/// Sets every element to x. O(Len()); allocates nothing.
///
/// Each element gets a copy of x, so T must be plain: a call with any other
/// T is E0701.
export Slice[T].Fill(mut self, x: T into self) {
    s := self.ItemsMut()
    for i in 0..s.Len() {
        s[i] = x
    }
}

/// Copies src's elements over self's. O(Len()); allocates nothing.
///
/// T must be plain: a call with any other T is E0701. The lengths must be
/// equal, else it panics (code 2) before any store. The copy is one block
/// move of the bytes, correct even when the two views overlap.
export Slice[T].CopyFrom(mut self, src: []T) *src into self {
    s := self.ItemsMut()
    n := s.Len()
    if src.Len() != n {
        raw.Fail(2, "Slice.CopyFrom: lengths differ")
    }
    unsafe "T is plain (CopyFrom's implicit bound), so n elements' bytes are their copy; s and src both hold n elements, compared above, and Move allows overlap" {
        raw.Move(raw.DataMut(s), raw.Data(src), n)
    }
}

/// Whether some element equals x; T needs Eq (E0701 at the call). O(Len());
/// allocates nothing.
export Slice[T].Has(&self, x: &T): bool {
    return self.Index(x).IsSome()
}

/// The index of the first element equal to x, or none; T needs Eq (E0701 at
/// the call). O(Len()) calls of Eq; allocates nothing. A u8 slice is
/// searched a word at a time, with vectors past the first 32 bytes, except
/// on x86_64-freestanding.
export Slice[T].Index(&self, x: &T): ?int {
    s := self.Items()
    if T is u8 {
        return IndexByteIn(s, *x, 0)
    }
    for i in 0..s.Len() {
        if s[i].Eq(x) {
            return some(i)
        }
    }
    return none
}

// SwapAt exchanges s[i] and s[j] bitwise, for any T; both indices are in
// range (the callers check, or derive them from Len).
SwapAt[T](s: mut []T, i: int, j: int) {
    if i == j {
        return
    }
    n := s.Len()
    if i < 0 || i >= n || j < 0 || j >= n {
        raw.Fail(2, "swap: index out of range")
    }
    unsafe "i and j are distinct in-range slots of s, each initialized" {
        p := raw.DataMut(s)
        a := raw.Offset(p, i)
        b := raw.Offset(p, j)
        t := raw.Read(a)
        raw.Write(a, raw.Read(b))
        raw.Write(b, t)
    }
}
