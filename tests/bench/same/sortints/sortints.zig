// sortints, Zig: the reference algorithm of README.md.
// Go 1.27's pdqsortOrdered (slices/zsortordered.go) written out for []i64 and
// <, not std.sort.pdq: the same functions, constants and loop structure as
// every other column. The slice comes from std.heap.page_allocator; indices
// are usize (no subtraction in the algorithm goes below zero). Every s[i] is
// bounds checked in ReleaseSafe and unchecked in ReleaseFast; the checksum's
// + and * are checked in ReleaseSafe, UB in ReleaseFast (overflow class
// "never"). The fill uses +% and *% because splitmix64 wraps by design. No
// @setRuntimeSafety, no libc.
const std = @import("std");

// tools/bench flips this line in a copy for the counting build.
const bench_count = false;
var n_pdq: u64 = 0;
var n_ins: u64 = 0;
var n_shift: u64 = 0;
var n_heap: u64 = 0;
var n_rev: u64 = 0;
var n_part: u64 = 0;
var n_parteq: u64 = 0;
var n_pis: u64 = 0;
var n_brk: u64 = 0;

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn bitlen(n: usize) u6 {
    var l: u6 = 0;
    var x: u64 = n;
    while (x > 0) : (x >>= 1) {
        l += 1;
    }
    return l;
}

fn insertionSort(s: []i64, a: usize, b: usize) void {
    if (bench_count) n_ins += 1;
    var i = a + 1;
    while (i < b) : (i += 1) {
        var j = i;
        while (j > a and s[j] < s[j - 1]) : (j -= 1) {
            const t = s[j];
            s[j] = s[j - 1];
            s[j - 1] = t;
            if (bench_count) n_shift += 1;
        }
    }
}

fn siftDown(s: []i64, lo: usize, hi: usize, first: usize) void {
    var root = lo;
    while (true) {
        var child = 2 * root + 1;
        if (child >= hi) return;
        if (child + 1 < hi and s[first + child] < s[first + child + 1]) {
            child += 1;
        }
        if (!(s[first + root] < s[first + child])) return;
        const t = s[first + root];
        s[first + root] = s[first + child];
        s[first + child] = t;
        root = child;
    }
}

fn heapSort(s: []i64, a: usize, b: usize) void {
    if (bench_count) n_heap += 1;
    const first = a;
    const hi = b - a;
    var i = (hi - 1) / 2;
    while (true) {
        siftDown(s, i, hi, first);
        if (i == 0) break;
        i -= 1;
    }
    i = hi - 1;
    while (true) {
        const t = s[first];
        s[first] = s[first + i];
        s[first + i] = t;
        siftDown(s, 0, i, first);
        if (i == 0) break;
        i -= 1;
    }
}

fn median(s: []i64, a: usize, b: usize, c: usize, swaps: *i64) usize {
    var x = a;
    var y = b;
    var z = c;
    if (s[y] < s[x]) {
        swaps.* += 1;
        const t = x;
        x = y;
        y = t;
    }
    if (s[z] < s[y]) {
        swaps.* += 1;
        const t = y;
        y = z;
        z = t;
    }
    if (s[y] < s[x]) {
        swaps.* += 1;
        const t = x;
        x = y;
        y = t;
    }
    return y;
}

const Pivot = struct { pivot: usize, hint: i64 };

// choosePivot returns the pivot and the hint: 0 unknown, 1 increasing, 2 decreasing.
fn choosePivot(s: []i64, a: usize, b: usize) Pivot {
    const l = b - a;
    var swaps: i64 = 0;
    var i = a + l / 4 * 1;
    var j = a + l / 4 * 2;
    var k = a + l / 4 * 3;
    if (l >= 8) {
        if (l >= 50) {
            i = median(s, i - 1, i, i + 1, &swaps);
            j = median(s, j - 1, j, j + 1, &swaps);
            k = median(s, k - 1, k, k + 1, &swaps);
        }
        j = median(s, i, j, k, &swaps);
    }
    if (swaps == 0) return .{ .pivot = j, .hint = 1 };
    if (swaps == 12) return .{ .pivot = j, .hint = 2 };
    return .{ .pivot = j, .hint = 0 };
}

fn reverseRange(s: []i64, a: usize, b: usize) void {
    if (bench_count) n_rev += 1;
    var i = a;
    var j = b - 1;
    while (i < j) {
        const t = s[i];
        s[i] = s[j];
        s[j] = t;
        i += 1;
        j -= 1;
    }
}

fn partialInsertionSort(s: []i64, a: usize, b: usize) bool {
    if (bench_count) n_pis += 1;
    var i = a + 1;
    var step: usize = 0;
    while (step < 5) : (step += 1) {
        while (i < b and !(s[i] < s[i - 1])) {
            i += 1;
        }
        if (i == b) return true;
        if (b - a < 50) return false;
        const t = s[i];
        s[i] = s[i - 1];
        s[i - 1] = t;
        if (i - a >= 2) {
            var j = i - 1;
            while (j >= 1) : (j -= 1) {
                if (!(s[j] < s[j - 1])) break;
                const u = s[j];
                s[j] = s[j - 1];
                s[j - 1] = u;
            }
        }
        if (b - i >= 2) {
            var j = i + 1;
            while (j < b) : (j += 1) {
                if (!(s[j] < s[j - 1])) break;
                const u = s[j];
                s[j] = s[j - 1];
                s[j - 1] = u;
            }
        }
    }
    return false;
}

fn breakPatterns(s: []i64, a: usize, b: usize) void {
    if (bench_count) n_brk += 1;
    const length = b - a;
    if (length < 8) return;
    var r: u64 = length;
    const modulus = @as(u64, 1) << bitlen(length);
    const idx = a + (length / 4) * 2 - 1;
    var i: usize = 0;
    while (i < 3) : (i += 1) {
        r ^= r << 13;
        r ^= r >> 7;
        r ^= r << 17;
        var other: usize = @intCast(r & (modulus - 1));
        if (other >= length) other -= length;
        const t = s[idx + i];
        s[idx + i] = s[a + other];
        s[a + other] = t;
    }
}

const Mid = struct { mid: usize, already: bool };

fn partition(s: []i64, a: usize, b: usize, pivot: usize) Mid {
    if (bench_count) n_part += 1;
    var t = s[a];
    s[a] = s[pivot];
    s[pivot] = t;
    var i = a + 1;
    var j = b - 1;
    while (i <= j and s[i] < s[a]) i += 1;
    while (i <= j and !(s[j] < s[a])) j -= 1;
    if (i > j) {
        t = s[j];
        s[j] = s[a];
        s[a] = t;
        return .{ .mid = j, .already = true };
    }
    t = s[i];
    s[i] = s[j];
    s[j] = t;
    i += 1;
    j -= 1;
    while (true) {
        while (i <= j and s[i] < s[a]) i += 1;
        while (i <= j and !(s[j] < s[a])) j -= 1;
        if (i > j) break;
        t = s[i];
        s[i] = s[j];
        s[j] = t;
        i += 1;
        j -= 1;
    }
    t = s[j];
    s[j] = s[a];
    s[a] = t;
    return .{ .mid = j, .already = false };
}

fn partitionEqual(s: []i64, a: usize, b: usize, pivot: usize) usize {
    if (bench_count) n_parteq += 1;
    var t = s[a];
    s[a] = s[pivot];
    s[pivot] = t;
    var i = a + 1;
    var j = b - 1;
    while (true) {
        while (i <= j and !(s[a] < s[i])) i += 1;
        while (i <= j and s[a] < s[j]) j -= 1;
        if (i > j) break;
        t = s[i];
        s[i] = s[j];
        s[j] = t;
        i += 1;
        j -= 1;
    }
    return i;
}

fn pdqsort(s: []i64, a0: usize, b0: usize, limit0: usize) void {
    var a = a0;
    var b = b0;
    var limit = limit0;
    var balanced = true;
    var partitioned = true;
    while (true) {
        const length = b - a;
        if (bench_count) n_pdq += 1;
        if (length <= 12) {
            insertionSort(s, a, b);
            return;
        }
        if (limit == 0) {
            heapSort(s, a, b);
            return;
        }
        if (!balanced) {
            breakPatterns(s, a, b);
            limit -= 1;
        }
        var pv = choosePivot(s, a, b);
        if (pv.hint == 2) {
            reverseRange(s, a, b);
            pv.pivot = (b - 1) - (pv.pivot - a);
            pv.hint = 1;
        }
        if (balanced and partitioned and pv.hint == 1) {
            if (partialInsertionSort(s, a, b)) return;
        }
        if (a > 0 and !(s[a - 1] < s[pv.pivot])) {
            a = partitionEqual(s, a, b, pv.pivot);
            continue;
        }
        const m = partition(s, a, b, pv.pivot);
        partitioned = m.already;
        const left = m.mid - a;
        const right = b - m.mid;
        const threshold = length / 8;
        if (left < right) {
            balanced = left >= threshold;
            pdqsort(s, a, m.mid, limit);
            a = m.mid + 1;
        } else {
            balanced = right >= threshold;
            pdqsort(s, m.mid + 1, b, limit);
            b = m.mid;
        }
    }
}

fn check(s: []i64) i64 {
    var sum: i64 = 0;
    for (0..s.len) |i| {
        if (i > 0 and s[i - 1] > s[i]) return -1;
        sum += s[i] * @as(i64, @intCast(i % 1000));
    }
    return sum;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const xs = try std.heap.page_allocator.alloc(i64, n);
    defer std.heap.page_allocator.free(xs);
    var total: i64 = 0;
    for (0..rounds) |_| {
        for (0..4) |p| {
            for (0..n) |i| {
                if (p == 0) {
                    st +%= 0x9e3779b97f4a7c15;
                    xs[i] = @intCast(mix(st) & 0xFFFFF);
                } else if (p == 1) {
                    xs[i] = @intCast(n - i);
                } else if (p == 2) {
                    xs[i] = @intCast(i % 1000);
                } else {
                    st +%= 0x9e3779b97f4a7c15;
                    xs[i] = @intCast(mix(st) >> 60);
                }
            }
            pdqsort(xs, 0, n, bitlen(n));
            total += check(xs);
        }
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    if (bench_count) try w.interface.print("counts pdq={d} ins={d} shift={d} heap={d} rev={d} part={d} parteq={d} pis={d} brk={d}\n", .{ n_pdq, n_ins, n_shift, n_heap, n_rev, n_part, n_parteq, n_pis, n_brk });
    try w.interface.flush();
}
