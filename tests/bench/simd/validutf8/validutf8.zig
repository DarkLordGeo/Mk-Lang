// validutf8, Zig, SIMD: the kernel of README.md with @Vector(W, u8), W
// std.simd.suggestVectorLength(u8) at the build's fixed CPU target (16 at
// x86-64-v2, 32 at x86-64-v3) and no run-time dispatch (B16): the
// Keiser-Lemire validator (Keiser and Lemire, 2021; the algorithm of
// simdjson and simdutf) in portable vectors. Zig has no run-time byte
// shuffle on @Vector (@shuffle takes a comptime mask), so each 16-entry
// nibble lookup is a chain of compare-and-select over the table's runs of
// equal entries (5, 7 and 4 selects); the shifts by one, two and three
// bytes are @shuffle with comptime masks, the third and fourth byte check
// is saturating subtraction (-|), and 64-byte chunks with an all-ASCII skip
// as in the C and Rust cells. std.unicode.utf8ValidateSlice is scalar after
// an ASCII prefix; this row is about each language's SIMD layer. The buffer
// starts on a 64-byte boundary, as in every column (BS-4). Usage:
// validutf8 <size> <reps> <seed>.
const std = @import("std");

const W = std.simd.suggestVectorLength(u8) orelse 16;
const V = @Vector(W, u8);
const NV = 64 / W;

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

// The 16 encodings of README.md; entry k is 1 + k / 4 bytes long.
const enc = [16][4]u8{
    .{ 0x61, 0, 0, 0 },       .{ 0x20, 0, 0, 0 },       .{ 0x30, 0, 0, 0 },       .{ 0x7E, 0, 0, 0 },
    .{ 0xC2, 0x80, 0, 0 },    .{ 0xDF, 0xBF, 0, 0 },    .{ 0xC3, 0xA9, 0, 0 },    .{ 0xD0, 0x96, 0, 0 },
    .{ 0xE0, 0xA0, 0x80, 0 }, .{ 0xED, 0x9F, 0xBF, 0 }, .{ 0xEE, 0x80, 0x80, 0 }, .{ 0xE4, 0xB8, 0x96, 0 },
    .{ 0xF0, 0x90, 0x80, 0x80 }, .{ 0xF4, 0x8F, 0xBF, 0xBF }, .{ 0xF0, 0x9F, 0x98, 0x80 }, .{ 0xF3, 0xA0, 0x80, 0x80 },
};

const too_short: u8 = 1 << 0;
const too_long: u8 = 1 << 1;
const overlong_3: u8 = 1 << 2;
const too_large: u8 = 1 << 3;
const surrogate: u8 = 1 << 4;
const overlong_2: u8 = 1 << 5;
const too_large_1000: u8 = 1 << 6;
const overlong_4: u8 = 1 << 6;
const two_conts: u8 = 1 << 7;
const carry: u8 = too_short | too_long | two_conts;

// The tables by the high nibble of the previous byte, its low nibble, and
// the high nibble of the byte.
const t1h = [16]u8{
    too_long,                 too_long,  too_long,                           too_long,                                          too_long, too_long, too_long, too_long,
    two_conts,                two_conts, two_conts,                          two_conts,
    too_short | overlong_2,   too_short, too_short | overlong_3 | surrogate, too_short | too_large | too_large_1000 | overlong_4,
};
const t1l = [16]u8{
    carry | overlong_3 | overlong_2 | overlong_4,
    carry | overlong_2,
    carry,
    carry,
    carry | too_large,
    carry | too_large | too_large_1000,
    carry | too_large | too_large_1000,
    carry | too_large | too_large_1000,
    carry | too_large | too_large_1000,
    carry | too_large | too_large_1000,
    carry | too_large | too_large_1000,
    carry | too_large | too_large_1000,
    carry | too_large | too_large_1000,
    carry | too_large | too_large_1000 | surrogate,
    carry | too_large | too_large_1000,
    carry | too_large | too_large_1000,
};
const t2h = [16]u8{
    too_short, too_short, too_short, too_short, too_short, too_short, too_short, too_short,
    too_long | overlong_2 | two_conts | overlong_3 | too_large_1000 | overlong_4,
    too_long | overlong_2 | two_conts | overlong_3 | too_large,
    too_long | overlong_2 | two_conts | surrogate | too_large,
    too_long | overlong_2 | two_conts | surrogate | too_large,
    too_short, too_short, too_short, too_short,
};

fn splat(b: u8) V {
    return @splat(b);
}

// lookup is t[idx] for idx in 0..15: a select at the start of each run of
// equal entries, in ascending order, so the last run whose start is at most
// idx wins.
fn lookup(idx: V, comptime t: [16]u8) V {
    var r: V = splat(t[0]);
    inline for (1..16) |k| {
        if (t[k] != t[k - 1]) {
            r = @select(u8, idx >= splat(k), splat(t[k]), r);
        }
    }
    return r;
}

fn shr4(x: V) V {
    return x >> @as(@Vector(W, u3), @splat(4));
}

// prev is the input shifted by n bytes, the gap filled from the end of the
// previous vector.
fn prev(comptime n: usize, cur: V, before: V) V {
    const mask = comptime blk: {
        var m: [W]i32 = undefined;
        for (0..W) |j| {
            m[j] = if (j >= n) @intCast(j - n) else ~@as(i32, @intCast(W - n + j));
        }
        break :blk m;
    };
    return @shuffle(u8, cur, before, mask);
}

const maxv: V = blk: {
    var m: [W]u8 = @splat(0xFF);
    m[W - 3] = 0xF0 - 1;
    m[W - 2] = 0xE0 - 1;
    m[W - 1] = 0xC0 - 1;
    break :blk m;
};

const State = struct {
    err: V = splat(0),
    prev: V = splat(0),
    prev_incomplete: V = splat(0),
};

// check is the error vector of one input vector against the one before.
fn check(input: V, before: V) V {
    const p1 = prev(1, input, before);
    const sc = lookup(shr4(p1), t1h) & lookup(p1 & splat(0x0F), t1l) & lookup(shr4(input), t2h);
    const p2 = prev(2, input, before);
    const p3 = prev(3, input, before);
    const must23 = (p2 -| splat(0xE0 - 0x80)) | (p3 -| splat(0xF0 - 0x80));
    return (must23 & splat(0x80)) ^ sc;
}

fn chunk(s: *State, p: []const u8, i: usize) void {
    var v: [NV]V = undefined;
    var all = splat(0);
    inline for (0..NV) |j| {
        v[j] = p[i + j * W ..][0..W].*;
        all |= v[j];
    }
    if (@reduce(.Or, all) & 0x80 == 0) {
        s.err |= s.prev_incomplete;
        return;
    }
    var before = s.prev;
    inline for (0..NV) |j| {
        s.err |= check(v[j], before);
        before = v[j];
    }
    s.prev_incomplete = v[NV - 1] -| maxv;
    s.prev = v[NV - 1];
}

fn validUtf8(b: []const u8) bool {
    var s: State = .{};
    const n = b.len;
    var i: usize = 0;
    while (i + 64 <= n) : (i += 64) {
        chunk(&s, b, i);
    }
    if (i < n) {
        var tail: [64]u8 = @splat(0);
        @memcpy(tail[0 .. n - i], b[i..]);
        chunk(&s, &tail, 0);
    }
    s.err |= s.prev_incomplete;
    return @reduce(.Or, s.err) == 0;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    const blen: usize = if (n < 65536) n else 65536;
    const blk = try pa.alloc(u8, blen);
    defer pa.free(blk);
    // BS-4: the buffer starts on a 64-byte boundary in every column.
    const buf = try pa.alignedAlloc(u8, .@"64", n);
    defer pa.free(buf);
    if (@intFromPtr(buf.ptr) % 64 != 0) {
        std.debug.print("validutf8: buffer not 64-byte aligned\n", .{});
        std.process.exit(3);
    }
    var p: usize = 0;
    while (true) {
        st +%= 0x9e3779b97f4a7c15;
        const k: usize = @intCast(mix(st) & 15);
        const l = 1 + k / 4;
        if (p + l > blen) break;
        @memcpy(blk[p .. p + l], enc[k][0..l]);
        p += l;
    }
    @memset(blk[p..], 0x61);
    var i: usize = 0;
    while (i + blen <= n) : (i += blen) {
        @memcpy(buf[i .. i + blen], blk);
    }
    @memset(buf[i..], 0x61);
    const span: u64 = if (n < 64) n else 64;
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < reps) : (r += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const z = mix(st);
        const pos = n - 1 - @as(usize, @intCast((z >> 1) % span));
        const old = buf[pos];
        if (z & 1 == 1) buf[pos] = @truncate(z >> 56);
        total = total *% 31 +% @intFromBool(validUtf8(buf));
        buf[pos] = old;
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
