// mandelbrot, Zig: the reference algorithm of README.md.
// Scalar f64, one point at a time, at most 50 iterations, the escape test
// after each step, the README's parenthesisation (no @setFloatMode, no
// @Vector). i64 counters: + checked in ReleaseSafe, UB in ReleaseFast.
const std = @import("std");

const max_iter: i64 = 50;

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
        while (x < n) : (x += 1) {
            const cr = (2.0 * @as(f64, @floatFromInt(x))) / fnn - 1.5;
            var zr: f64 = 0.0;
            var zi: f64 = 0.0;
            var i: i64 = 0;
            while (i < max_iter) {
                const tr = (zr * zr - zi * zi) + cr;
                const ti = (2.0 * zr) * zi + ci;
                zr = tr;
                zi = ti;
                if (zr * zr + zi * zi > 4.0) {
                    break;
                }
                i += 1;
            }
            total += i;
        }
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
