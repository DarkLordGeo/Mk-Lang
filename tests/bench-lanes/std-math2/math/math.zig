// math, Zig, idiomatic std: @exp, @log, @sin (Zig's own implementations,
// no C library), std.math.pow, atan2, sinh and cbrt, and @mulAdd (software
// at the x86-64-v2 target) over 4,096 inputs built from splitmix64 bits
// (README.md), reps passes, pass r starting at input r. The checksum adds
// bits >> 32 of each f64 result (bits >> 16 of each f32 result) with
// wrapping adds. One loop per function, each call direct.
// Usage: math <fn> <reps> <seed>.
const std = @import("std");

const N: usize = 4096;

fn mix(z0: u64) u64 {
    var z = z0;
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
    return z ^ (z >> 31);
}

fn f64of(z: u64, lo: i64, hi: i64, signed: bool) f64 {
    const e: u64 = @as(u64, @intCast(lo + 1023)) + (z >> 53) % @as(u64, @intCast(hi - lo + 1));
    const sign: u64 = if (signed) (z & 0x0010000000000000) << 11 else 0;
    return @bitCast(sign | e << 52 | (z & 0x000FFFFFFFFFFFFF));
}

fn f32of(z: u64, lo: i64, hi: i64, signed: bool) f32 {
    const e: u64 = @as(u64, @intCast(lo + 127)) + (z >> 53) % @as(u64, @intCast(hi - lo + 1));
    const sign: u64 = if (signed) (z & 0x0010000000000000) >> 21 else 0;
    return @bitCast(@as(u32, @truncate(sign | e << 23 | (z & 0x7FFFFF))));
}

fn bits(x: f64) u64 {
    return @bitCast(x);
}

fn bits32(x: f32) u64 {
    return @as(u32, @bitCast(x));
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const name = args[1];
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const eq = std.mem.eql;
    var lo: i64 = -10;
    var hi: i64 = 10;
    var sg = true; // atan2
    if (eq(u8, name, "exp") or eq(u8, name, "expf")) {
        lo = -10;
        hi = 5;
    } else if (eq(u8, name, "log") or eq(u8, name, "logf")) {
        lo = -100;
        hi = 100;
        sg = false;
    } else if (eq(u8, name, "sin") or eq(u8, name, "sinf")) {
        lo = -10;
        hi = 2;
    } else if (eq(u8, name, "sinlarge")) {
        lo = 20;
        hi = 60;
    } else if (eq(u8, name, "pow")) {
        lo = -4;
        hi = 3;
        sg = false;
    } else if (eq(u8, name, "sinh")) {
        lo = -10;
        hi = 4;
    } else if (eq(u8, name, "cbrt")) {
        lo = -200;
        hi = 200;
    } else if (eq(u8, name, "fma")) {
        lo = -5;
        hi = 5;
    }
    const f32row = eq(u8, name, "expf") or eq(u8, name, "logf") or eq(u8, name, "sinf");
    const pa = std.heap.page_allocator;
    const xs = try pa.alloc(f64, N);
    const ys = try pa.alloc(f64, N);
    const zs = try pa.alloc(f64, N);
    const fs = try pa.alloc(f32, N);
    for (0..N) |i| {
        st +%= 0x9E3779B97F4A7C15;
        const a = mix(st);
        st +%= 0x9E3779B97F4A7C15;
        const b = mix(st);
        st +%= 0x9E3779B97F4A7C15;
        const c = mix(st);
        xs[i] = f64of(a, lo, hi, sg);
        fs[i] = if (f32row) f32of(a, lo, hi, sg) else 0;
        ys[i] = if (eq(u8, name, "pow")) f64of(b, -4, 4, true) else f64of(b, lo, hi, sg);
        zs[i] = f64of(c, lo, hi, sg);
    }
    var acc: u64 = 0;
    for (0..reps) |r| {
        if (eq(u8, name, "exp")) {
            for (0..N) |i| acc +%= bits(@exp(xs[(i + r) & (N - 1)])) >> 32;
        } else if (eq(u8, name, "log")) {
            for (0..N) |i| acc +%= bits(@log(xs[(i + r) & (N - 1)])) >> 32;
        } else if (eq(u8, name, "sin") or eq(u8, name, "sinlarge")) {
            for (0..N) |i| acc +%= bits(@sin(xs[(i + r) & (N - 1)])) >> 32;
        } else if (eq(u8, name, "sinh")) {
            for (0..N) |i| acc +%= bits(std.math.sinh(xs[(i + r) & (N - 1)])) >> 32;
        } else if (eq(u8, name, "cbrt")) {
            for (0..N) |i| acc +%= bits(std.math.cbrt(xs[(i + r) & (N - 1)])) >> 32;
        } else if (eq(u8, name, "pow")) {
            for (0..N) |i| {
                const k = (i + r) & (N - 1);
                acc +%= bits(std.math.pow(f64, xs[k], ys[k])) >> 32;
            }
        } else if (eq(u8, name, "atan2")) {
            for (0..N) |i| {
                const k = (i + r) & (N - 1);
                acc +%= bits(std.math.atan2(ys[k], xs[k])) >> 32;
            }
        } else if (eq(u8, name, "fma")) {
            for (0..N) |i| {
                const k = (i + r) & (N - 1);
                acc +%= bits(@mulAdd(f64, xs[k], ys[k], zs[k])) >> 32;
            }
        } else if (eq(u8, name, "expf")) {
            for (0..N) |i| acc +%= bits32(@exp(fs[(i + r) & (N - 1)])) >> 16;
        } else if (eq(u8, name, "logf")) {
            for (0..N) |i| acc +%= bits32(@log(fs[(i + r) & (N - 1)])) >> 16;
        } else if (eq(u8, name, "sinf")) {
            for (0..N) |i| acc +%= bits32(@sin(fs[(i + r) & (N - 1)])) >> 16;
        }
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{acc});
    try w.interface.flush();
}
