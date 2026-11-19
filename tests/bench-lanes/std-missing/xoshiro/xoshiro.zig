// xoshiro, Zig: xoshiro256** seeded with four SplitMix64 outputs and
// Lemire's bounded draw with a u128 product, the same steps as every other
// column of README.md. Wrapping operators (+%, *%) where the algorithm
// wraps, so the safe build (zig-safe) has nothing to trap on.
const std = @import("std");

fn splitmix(x: *u64) u64 {
    x.* +%= 0x9E3779B97F4A7C15;
    var z = x.*;
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
    return z ^ (z >> 31);
}

const X = struct {
    s: [4]u64,

    inline fn next(self: *X) u64 {
        const s = &self.s;
        const result = std.math.rotl(u64, s[1] *% 5, 7) *% 9;
        const t = s[1] << 17;
        s[2] ^= s[0];
        s[3] ^= s[1];
        s[1] ^= s[2];
        s[0] ^= s[3];
        s[2] ^= t;
        s[3] = std.math.rotl(u64, s[3], 45);
        return result;
    }

    inline fn below(self: *X, n: u64) u64 {
        var m: u128 = @as(u128, self.next()) * n;
        if (@as(u64, @truncate(m)) < n) {
            const t = (0 -% n) % n;
            while (@as(u64, @truncate(m)) < t) {
                m = @as(u128, self.next()) * n;
            }
        }
        return @truncate(m >> 64);
    }
};

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(i64, args[1], 10);
    var x = try std.fmt.parseInt(u64, args[2], 10);
    var g = X{ .s = undefined };
    for (0..4) |i| {
        g.s[i] = splitmix(&x);
    }
    var total: u64 = 0;
    var i: i64 = 0;
    while (i < n) : (i += 1) {
        total +%= g.next();
        total +%= g.below(1000);
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
