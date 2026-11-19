// sortints, Mk: the reference algorithm of ../README.md. Go 1.27's
// pdqsortOrdered (slices/zsortordered.go) written out for []i64 and <, not a
// call of std's Slice[T].Sort (the same algorithm with a const comparator), so
// every column runs the same text. Indices are int; every s[i] is bounds
// checked, every integer + - * is checked (overflow class "never"). The fill
// uses the wrapping operators because splitmix64 wraps by design.
// Usage: sortints <n> <rounds> <seed>. Prints the checksum.

import std.atomic

// tools/bench flips this line in a copy for the counting build.
const BenchCount: bool = false
var NPdq: atomic.AtomicInt = atomic.AtomicInt.New(0)
var NIns: atomic.AtomicInt = atomic.AtomicInt.New(0)
var NShift: atomic.AtomicInt = atomic.AtomicInt.New(0)
var NHeap: atomic.AtomicInt = atomic.AtomicInt.New(0)
var NRev: atomic.AtomicInt = atomic.AtomicInt.New(0)
var NPart: atomic.AtomicInt = atomic.AtomicInt.New(0)
var NParteq: atomic.AtomicInt = atomic.AtomicInt.New(0)
var NPis: atomic.AtomicInt = atomic.AtomicInt.New(0)
var NBrk: atomic.AtomicInt = atomic.AtomicInt.New(0)

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

BitLen(n: int): int {
    var l := 0
    var x := Cast[u64](n)
    for x > 0 {
        l += 1
        x = x >> 1
    }
    return l
}

InsertionSort(s: mut []i64, a: int, b: int) {
    if BenchCount {
        _ = NIns.Add(1)
    }
    for i in a + 1..b {
        var j := i
        for j > a && s[j] < s[j - 1] {
            t := s[j]
            s[j] = s[j - 1]
            s[j - 1] = t
            if BenchCount {
                _ = NShift.Add(1)
            }
            j -= 1
        }
    }
}

SiftDown(s: mut []i64, lo: int, hi: int, first: int) {
    var root := lo
    for {
        var child := 2 * root + 1
        if child >= hi {
            return
        }
        if child + 1 < hi && s[first + child] < s[first + child + 1] {
            child += 1
        }
        if !(s[first + root] < s[first + child]) {
            return
        }
        t := s[first + root]
        s[first + root] = s[first + child]
        s[first + child] = t
        root = child
    }
}

HeapSort(s: mut []i64, a: int, b: int) {
    if BenchCount {
        _ = NHeap.Add(1)
    }
    first := a
    hi := b - a
    var i := (hi - 1) / 2
    for i >= 0 {
        SiftDown(s, i, hi, first)
        i -= 1
    }
    i = hi - 1
    for i >= 0 {
        t := s[first]
        s[first] = s[first + i]
        s[first + i] = t
        SiftDown(s, 0, i, first)
        i -= 1
    }
}

Median(s: mut []i64, a: int, b: int, c: int, swaps: mut int): int {
    var x := a
    var y := b
    var z := c
    if s[y] < s[x] {
        *swaps += 1
        t := x
        x = y
        y = t
    }
    if s[z] < s[y] {
        *swaps += 1
        t := y
        y = z
        z = t
    }
    if s[y] < s[x] {
        *swaps += 1
        t := x
        x = y
        y = t
    }
    return y
}

// ChoosePivot returns the pivot and sets hint: 0 unknown, 1 increasing, 2 decreasing.
ChoosePivot(s: mut []i64, a: int, b: int, hint: mut int): int {
    l := b - a
    var swaps := 0
    var i := a + l / 4 * 1
    var j := a + l / 4 * 2
    var k := a + l / 4 * 3
    if l >= 8 {
        if l >= 50 {
            i = Median(s, i - 1, i, i + 1, mut swaps)
            j = Median(s, j - 1, j, j + 1, mut swaps)
            k = Median(s, k - 1, k, k + 1, mut swaps)
        }
        j = Median(s, i, j, k, mut swaps)
    }
    if swaps == 0 {
        *hint = 1
    } else if swaps == 12 {
        *hint = 2
    } else {
        *hint = 0
    }
    return j
}

ReverseRange(s: mut []i64, a: int, b: int) {
    if BenchCount {
        _ = NRev.Add(1)
    }
    var i := a
    var j := b - 1
    for i < j {
        t := s[i]
        s[i] = s[j]
        s[j] = t
        i += 1
        j -= 1
    }
}

PartialInsertionSort(s: mut []i64, a: int, b: int): bool {
    if BenchCount {
        _ = NPis.Add(1)
    }
    var i := a + 1
    for _ in 0..5 {
        for i < b && !(s[i] < s[i - 1]) {
            i += 1
        }
        if i == b {
            return true
        }
        if b - a < 50 {
            return false
        }
        t := s[i]
        s[i] = s[i - 1]
        s[i - 1] = t
        if i - a >= 2 {
            var j := i - 1
            for j >= 1 {
                if !(s[j] < s[j - 1]) {
                    break
                }
                u := s[j]
                s[j] = s[j - 1]
                s[j - 1] = u
                j -= 1
            }
        }
        if b - i >= 2 {
            var j := i + 1
            for j < b {
                if !(s[j] < s[j - 1]) {
                    break
                }
                u := s[j]
                s[j] = s[j - 1]
                s[j - 1] = u
                j += 1
            }
        }
    }
    return false
}

BreakPatterns(s: mut []i64, a: int, b: int) {
    if BenchCount {
        _ = NBrk.Add(1)
    }
    length := b - a
    if length < 8 {
        return
    }
    var r := Cast[u64](length)
    modulus := Cast[u64](1) << BitLen(length)
    idx := a + (length / 4) * 2 - 1
    for i in 0..3 {
        r = r ^ (r << 13)
        r = r ^ (r >> 7)
        r = r ^ (r << 17)
        var other := Trunc[int](r & (modulus - 1))
        if other >= length {
            other -= length
        }
        t := s[idx + i]
        s[idx + i] = s[a + other]
        s[a + other] = t
    }
}

// Partition returns the new pivot index and sets already when no element moved.
Partition(s: mut []i64, a: int, b: int, pivot: int, already: mut bool): int {
    if BenchCount {
        _ = NPart.Add(1)
    }
    var t := s[a]
    s[a] = s[pivot]
    s[pivot] = t
    var i := a + 1
    var j := b - 1
    for i <= j && s[i] < s[a] {
        i += 1
    }
    for i <= j && !(s[j] < s[a]) {
        j -= 1
    }
    if i > j {
        t = s[j]
        s[j] = s[a]
        s[a] = t
        *already = true
        return j
    }
    t = s[i]
    s[i] = s[j]
    s[j] = t
    i += 1
    j -= 1
    for {
        for i <= j && s[i] < s[a] {
            i += 1
        }
        for i <= j && !(s[j] < s[a]) {
            j -= 1
        }
        if i > j {
            break
        }
        t = s[i]
        s[i] = s[j]
        s[j] = t
        i += 1
        j -= 1
    }
    t = s[j]
    s[j] = s[a]
    s[a] = t
    *already = false
    return j
}

PartitionEqual(s: mut []i64, a: int, b: int, pivot: int): int {
    if BenchCount {
        _ = NParteq.Add(1)
    }
    var t := s[a]
    s[a] = s[pivot]
    s[pivot] = t
    var i := a + 1
    var j := b - 1
    for {
        for i <= j && !(s[a] < s[i]) {
            i += 1
        }
        for i <= j && s[a] < s[j] {
            j -= 1
        }
        if i > j {
            break
        }
        t = s[i]
        s[i] = s[j]
        s[j] = t
        i += 1
        j -= 1
    }
    return i
}

Pdqsort(s: mut []i64, a0: int, b0: int, limit0: int) {
    var a := a0
    var b := b0
    var limit := limit0
    var balanced := true
    var partitioned := true
    for {
        length := b - a
        if BenchCount {
            _ = NPdq.Add(1)
        }
        if length <= 12 {
            InsertionSort(s, a, b)
            return
        }
        if limit == 0 {
            HeapSort(s, a, b)
            return
        }
        if !balanced {
            BreakPatterns(s, a, b)
            limit -= 1
        }
        var hint := 0
        var pivot := ChoosePivot(s, a, b, mut hint)
        if hint == 2 {
            ReverseRange(s, a, b)
            pivot = (b - 1) - (pivot - a)
            hint = 1
        }
        if balanced && partitioned && hint == 1 {
            if PartialInsertionSort(s, a, b) {
                return
            }
        }
        if a > 0 && !(s[a - 1] < s[pivot]) {
            a = PartitionEqual(s, a, b, pivot)
            continue
        }
        var already := false
        mid := Partition(s, a, b, pivot, mut already)
        partitioned = already
        left := mid - a
        right := b - mid
        threshold := length / 8
        if left < right {
            balanced = left >= threshold
            Pdqsort(s, a, mid, limit)
            a = mid + 1
        } else {
            balanced = right >= threshold
            Pdqsort(s, mid + 1, b, limit)
            b = mid
        }
    }
}

Check(s: []i64): i64 {
    var sum: i64 = 0
    n := s.Len()
    for i in 0..n {
        if i > 0 && s[i - 1] > s[i] {
            return -1
        }
        sum += s[i] * Cast[i64](i % 1000)
    }
    return sum
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    xs := try sys.arena.Slice[i64](n)
    var total: i64 = 0
    for _ in 0..rounds {
        for p in 0..4 {
            for i in 0..n {
                if p == 0 {
                    st = st +% 0x9e3779b97f4a7c15
                    xs[i] = Cast[i64](Mix(st) & 0xFFFFF)
                } else if p == 1 {
                    xs[i] = Cast[i64](n - i)
                } else if p == 2 {
                    xs[i] = Cast[i64](i % 1000)
                } else {
                    st = st +% 0x9e3779b97f4a7c15
                    xs[i] = Cast[i64](Mix(st) >> 60)
                }
            }
            Pdqsort(xs, 0, n, BitLen(n))
            total += Check(xs[..])
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
    if BenchCount {
        try Print(mut out, "counts pdq={pdq} ins={ins} shift={shift} heap={heap} rev={rev} part={part} parteq={parteq} pis={pis} brk={brk}\n", (pdq: NPdq.Load(), ins: NIns.Load(), shift: NShift.Load(), heap: NHeap.Load(), rev: NRev.Load(), part: NPart.Load(), parteq: NParteq.Load(), pis: NPis.Load(), brk: NBrk.Load()))
    }
}
