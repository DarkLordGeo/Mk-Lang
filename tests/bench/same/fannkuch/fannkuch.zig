// fannkuch, Zig: the reference algorithm of README.md (the Benchmarks Game's
// fannkuch-redux, sequential). Three []i64 of n from std.heap.page_allocator
// indexed perm[i] (an i64 index converted with @intCast, itself checked in
// ReleaseSafe), the same loops and next-permutation step as every other
// column. + and - checked in ReleaseSafe, UB in ReleaseFast. No libc.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const n = try std.fmt.parseInt(i64, args[1], 10);
    const nu: usize = @intCast(n);
    const pa = std.heap.page_allocator;
    const perm = try pa.alloc(i64, nu);
    const perm1 = try pa.alloc(i64, nu);
    const count = try pa.alloc(i64, nu);
    for (0..nu) |i| {
        perm1[i] = @intCast(i);
    }
    var r: i64 = n;
    var perm_count: i64 = 0;
    var checksum: i64 = 0;
    var max_flips: i64 = 0;
    var done = false;
    while (!done) {
        while (r != 1) {
            count[@intCast(r - 1)] = r;
            r -= 1;
        }
        for (0..nu) |i| {
            perm[i] = perm1[i];
        }
        var flips: i64 = 0;
        var k = perm[0];
        while (k != 0) {
            var i: i64 = 0;
            var j: i64 = k;
            while (i < j) {
                const t = perm[@intCast(i)];
                perm[@intCast(i)] = perm[@intCast(j)];
                perm[@intCast(j)] = t;
                i += 1;
                j -= 1;
            }
            flips += 1;
            k = perm[0];
        }
        if (flips > max_flips) {
            max_flips = flips;
        }
        if (@mod(perm_count, 2) == 0) {
            checksum += flips;
        } else {
            checksum -= flips;
        }
        while (true) {
            if (r == n) {
                done = true;
                break;
            }
            const perm0 = perm1[0];
            for (0..@intCast(r)) |i| {
                perm1[i] = perm1[i + 1];
            }
            perm1[@intCast(r)] = perm0;
            count[@intCast(r)] -= 1;
            if (count[@intCast(r)] > 0) {
                break;
            }
            r += 1;
        }
        perm_count += 1;
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{checksum * 1000 + max_flips});
    try w.interface.flush();
}
