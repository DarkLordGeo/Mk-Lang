// closuresort, Mk, the context-struct form's sort: std's Slice[T].SortWith
// as it stood before std dropped it for capturing literals (an earlier
// std/core/core_sort.mk): Go's pdqsort_func with the comparator's context passed as an
// explicit argument to every call. The body is copied unchanged from that
// file (its Pdq*W functions and the two helpers they share with Sort),
// with two edits a package outside std needs: SwapAt is Slice.Swap (std's
// SwapAt is a std-internal bitwise exchange behind the same bounds check),
// and BitLen counts the length's bits with a loop once per sort (std's uses
// raw.Clz64, and std.raw is banned outside std). Today's Slice[T].Sort is
// the same pdqsort body without the context (the copied text and std's
// Pdq functions differ only in the ctx argument).

const PdqMaxInsertion = 12
const PdqShortestNinther = 50
const PdqMaxSwaps = 12
const PdqMaxSteps = 5
const PdqShortestShifting = 50

export SortWith[T, C](s: mut []T, ctx: &C, const less: fn(&C, &T, &T): bool) {
    n := s.Len()
    PdqW(s, 0, n, BitLen(n), ctx, less)
}

// BitLen is Go's bits.Len(uint(n)) for n >= 0.
BitLen(n: int): int {
    var k := 0
    var m := n
    for m > 0 {
        k += 1
        m = m >> 1
    }
    return k
}

SwapAt[T](s: mut []T, i: int, j: int) {
    s.Swap(i, j)
}

PdqReverse[T](s: mut []T, a: int, b: int) {
    var i := a
    var j := b - 1
    for i < j {
        SwapAt(s, i, j)
        i += 1
        j -= 1
    }
}

// PdqBreakPatterns is Go's breakPatterns_func: it swaps idx, idx + 1 and
// idx + 2, which are mid - 1, mid and mid + 1 for mid = a + length/4*2
// (idx - 1 + i would give a different permutation from Go's).
PdqBreakPatterns[T](s: mut []T, a: int, b: int) {
    length := b - a
    if length < 8 {
        return
    }
    var r := Trunc[u64](length)
    modulus := Trunc[u64](1) << BitLen(length)
    idx := a + (length / 4) * 2 - 1
    for i in 0..3 {
        r = r ^ (r << 13)
        r = r ^ (r >> 7)
        r = r ^ (r << 17)
        var other := Trunc[int](r & (modulus - 1))
        if other >= length {
            other -= length
        }
        SwapAt(s, idx + i, a + other)
    }
}

PdqInsertionW[T, C](s: mut []T, a: int, b: int, ctx: &C, const less: fn(&C, &T, &T): bool) {
    for i in a + 1..b {
        var j := i
        for j > a && less(ctx, &s[j], &s[j - 1]) {
            SwapAt(s, j, j - 1)
            j -= 1
        }
    }
}

PdqSiftDownW[T, C](s: mut []T, lo: int, hi: int, first: int, ctx: &C, const less: fn(&C, &T, &T): bool) {
    var root := lo
    for {
        var child := 2 * root + 1
        if child >= hi {
            return
        }
        if child + 1 < hi && less(ctx, &s[first + child], &s[first + child + 1]) {
            child += 1
        }
        if !less(ctx, &s[first + root], &s[first + child]) {
            return
        }
        SwapAt(s, first + root, first + child)
        root = child
    }
}

PdqHeapSortW[T, C](s: mut []T, a: int, b: int, ctx: &C, const less: fn(&C, &T, &T): bool) {
    first := a
    hi := b - a
    var i := (hi - 1) / 2
    for i >= 0 {
        PdqSiftDownW(s, i, hi, first, ctx, less)
        i -= 1
    }
    i = hi - 1
    for i >= 0 {
        SwapAt(s, first, first + i)
        PdqSiftDownW(s, 0, i, first, ctx, less)
        i -= 1
    }
}

// PdqMedian orders a, b, c by less as Go's median_func does, counting the
// swaps, and returns the middle index.
PdqMedianW[T, C](s: mut []T, a: int, b: int, c: int, swaps: mut int, ctx: &C, const less: fn(&C, &T, &T): bool): int {
    var x := a
    var y := b
    var z := c
    if less(ctx, &s[y], &s[x]) {
        *swaps += 1
        t := x
        x = y
        y = t
    }
    if less(ctx, &s[z], &s[y]) {
        *swaps += 1
        t := y
        y = z
        z = t
    }
    if less(ctx, &s[y], &s[x]) {
        *swaps += 1
        t := x
        x = y
        y = t
    }
    return y
}

// PdqPivot is Go's choosePivot_func: the pivot index, and the hint 0
// unknown, 1 increasing, 2 decreasing.
PdqPivotW[T, C](s: mut []T, a: int, b: int, hint: mut int, ctx: &C, const less: fn(&C, &T, &T): bool): int {
    l := b - a
    var swaps := 0
    var i := a + l / 4 * 1
    var j := a + l / 4 * 2
    var k := a + l / 4 * 3
    if l >= 8 {
        if l >= PdqShortestNinther {
            i = PdqMedianW(s, i - 1, i, i + 1, mut swaps, ctx, less)
            j = PdqMedianW(s, j - 1, j, j + 1, mut swaps, ctx, less)
            k = PdqMedianW(s, k - 1, k, k + 1, mut swaps, ctx, less)
        }
        j = PdqMedianW(s, i, j, k, mut swaps, ctx, less)
    }
    if swaps == 0 {
        *hint = 1
    } else if swaps == PdqMaxSwaps {
        *hint = 2
    } else {
        *hint = 0
    }
    return j
}

PdqPartialInsertionW[T, C](s: mut []T, a: int, b: int, ctx: &C, const less: fn(&C, &T, &T): bool): bool {
    var i := a + 1
    for _ in 0..PdqMaxSteps {
        for i < b && !less(ctx, &s[i], &s[i - 1]) {
            i += 1
        }
        if i == b {
            return true
        }
        if b - a < PdqShortestShifting {
            return false
        }
        SwapAt(s, i, i - 1)
        if i - a >= 2 {
            var j := i - 1
            for j >= 1 {
                if !less(ctx, &s[j], &s[j - 1]) {
                    break
                }
                SwapAt(s, j, j - 1)
                j -= 1
            }
        }
        if b - i >= 2 {
            var j := i + 1
            for j < b {
                if !less(ctx, &s[j], &s[j - 1]) {
                    break
                }
                SwapAt(s, j, j - 1)
                j += 1
            }
        }
    }
    return false
}

// PdqPartition is Go's partition_func; alreadyPartitioned is set when no
// element moved.
PdqPartitionW[T, C](s: mut []T, a: int, b: int, pivot: int, already: mut bool, ctx: &C, const less: fn(&C, &T, &T): bool): int {
    SwapAt(s, a, pivot)
    var i := a + 1
    var j := b - 1
    for i <= j && less(ctx, &s[i], &s[a]) {
        i += 1
    }
    for i <= j && !less(ctx, &s[j], &s[a]) {
        j -= 1
    }
    if i > j {
        SwapAt(s, j, a)
        *already = true
        return j
    }
    SwapAt(s, i, j)
    i += 1
    j -= 1
    for {
        for i <= j && less(ctx, &s[i], &s[a]) {
            i += 1
        }
        for i <= j && !less(ctx, &s[j], &s[a]) {
            j -= 1
        }
        if i > j {
            break
        }
        SwapAt(s, i, j)
        i += 1
        j -= 1
    }
    SwapAt(s, j, a)
    *already = false
    return j
}

PdqPartitionEqualW[T, C](s: mut []T, a: int, b: int, pivot: int, ctx: &C, const less: fn(&C, &T, &T): bool): int {
    SwapAt(s, a, pivot)
    var i := a + 1
    var j := b - 1
    for {
        for i <= j && !less(ctx, &s[a], &s[i]) {
            i += 1
        }
        for i <= j && less(ctx, &s[a], &s[j]) {
            j -= 1
        }
        if i > j {
            break
        }
        SwapAt(s, i, j)
        i += 1
        j -= 1
    }
    return i
}

PdqW[T, C](s: mut []T, a0: int, b0: int, limit0: int, ctx: &C, const less: fn(&C, &T, &T): bool) {
    var a := a0
    var b := b0
    var limit := limit0
    var balanced := true
    var partitioned := true
    for {
        length := b - a
        if length <= PdqMaxInsertion {
            PdqInsertionW(s, a, b, ctx, less)
            return
        }
        if limit == 0 {
            PdqHeapSortW(s, a, b, ctx, less)
            return
        }
        if !balanced {
            PdqBreakPatterns(s, a, b)
            limit -= 1
        }
        var hint := 0
        var pivot := PdqPivotW(s, a, b, mut hint, ctx, less)
        if hint == 2 {
            PdqReverse(s, a, b)
            pivot = (b - 1) - (pivot - a)
            hint = 1
        }
        if balanced && partitioned && hint == 1 {
            if PdqPartialInsertionW(s, a, b, ctx, less) {
                return
            }
        }
        if a > 0 && !less(ctx, &s[a - 1], &s[pivot]) {
            a = PdqPartitionEqualW(s, a, b, pivot, ctx, less)
            continue
        }
        var already := false
        mid := PdqPartitionW(s, a, b, pivot, mut already, ctx, less)
        partitioned = already
        left := mid - a
        right := b - mid
        threshold := length / 8
        if left < right {
            balanced = left >= threshold
            PdqW(s, a, mid, limit, ctx, less)
            a = mid + 1
        } else {
            balanced = right >= threshold
            PdqW(s, mid + 1, b, limit, ctx, less)
            b = mid
        }
    }
}
