// mandelvec, Zig, SIMD: the kernel of README.md (same/mandelbrot's
// algorithm, L points at once) with @Vector(L, f64), L
// std.simd.suggestVectorLength(f64) at the build's fixed CPU target (2 at
// x86-64-v2, 4 at x86-64-v3) and no run-time dispatch. Each lane stops
// counting at its escape (@select), the loop ends when no lane is active
// (@reduce(.Or)) or at 50 iterations. Products and sums stay separate (Zig
// fuses only through @mulAdd). Usage: mandelvec <n>.
const std = @import("std");

const MaxIter = 50;
const L = std.simd.suggestVectorLength(f64) orelse 2;
const V = @Vector(L, f64);
const B = @Vector(L, bool);

fn point(cr: f64, ci: f64) i64 {
    var zr: f64 = 0.0;
    var zi: f64 = 0.0;
    var i: i64 = 0;
    while (i < MaxIter) {
        const tr = (zr * zr - zi * zi) + cr;
        const ti = (2.0 * zr) * zi + ci;
        zr = tr;
        zi = ti;
        if (zr * zr + zi * zi > 4.0) break;
        i += 1;
    }
    return i;
}

fn lanes(cr: V, ci: f64) i64 {
    const civ: V = @splat(ci);
    const two: V = @splat(2.0);
    const four: V = @splat(4.0);
    const no: B = @splat(false);
    var zr: V = @splat(0.0);
    var zi: V = @splat(0.0);
    var cnt: @Vector(L, i64) = @splat(0);
    var active: B = @splat(true);
    var i: usize = 0;
    while (i < MaxIter) : (i += 1) {
        const tr = (zr * zr - zi * zi) + cr;
        const ti = (two * zr) * zi + civ;
        zr = tr;
        zi = ti;
        const esc = zr * zr + zi * zi > four;
        active = @select(bool, esc, no, active);
        if (!@reduce(.Or, active)) break;
        cnt += @select(i64, active, @as(@Vector(L, i64), @splat(1)), @as(@Vector(L, i64), @splat(0)));
    }
    return @reduce(.Add, cnt);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const n = try std.fmt.parseInt(i64, args[1], 10);
    const fnn: f64 = @floatFromInt(n);
    var total: i64 = 0;
    var y: i64 = 0;
    while (y < n) : (y += 1) {
        const ci = (2.0 * @as(f64, @floatFromInt(y))) / fnn - 1.0;
        var x: i64 = 0;
        while (x + L <= n) : (x += L) {
            var crs: [L]f64 = undefined;
            for (&crs, 0..) |*c, l| c.* = (2.0 * @as(f64, @floatFromInt(x + @as(i64, @intCast(l))))) / fnn - 1.5;
            total += lanes(crs, ci);
        }
        while (x < n) : (x += 1) total += point((2.0 * @as(f64, @floatFromInt(x))) / fnn - 1.5, ci);
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
