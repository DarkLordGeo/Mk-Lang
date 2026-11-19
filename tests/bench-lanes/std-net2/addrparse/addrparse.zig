// addrparse, Zig 0.17, std.Io.net: IpAddress.format into a fixed writer and
// IpAddress.parseLiteral, round trips of splitmix64 addresses (README.md).
// Usage: addrparse <n>.
const std = @import("std");
const net = std.Io.net;

var s: u64 = 1;

fn next() u64 {
    s +%= 0x9E3779B97F4A7C15;
    var z = s;
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
    return z ^ (z >> 31);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    const n = try std.fmt.parseInt(u64, args[1], 10);
    var sum: u64 = 0;
    var tbuf: [80]u8 = undefined;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        const x = next();
        const port: u16 = @truncate((x >> 40) & 0xFFFF);
        var a: net.IpAddress = undefined;
        if (x & 1 == 0) {
            a = .{ .ip4 = .{ .bytes = .{ @truncate(x >> 8), @truncate(x >> 16), @truncate(x >> 24), @truncate(x >> 32) }, .port = port } };
        } else {
            const y = next();
            const z = next();
            const zero = (x >> 8) & (x >> 16);
            var b: [16]u8 = undefined;
            for (0..8) |k| {
                const w = if (k >= 4) z else y;
                var g = (w >> @intCast(16 * (k % 4))) & 0xFFFF;
                if ((zero >> @intCast(k)) & 1 == 1) g = 0;
                if (k == 0) g |= 0x2000;
                b[2 * k] = @truncate(g >> 8);
                b[2 * k + 1] = @truncate(g);
            }
            a = .{ .ip6 = .{ .bytes = b, .port = port, .flow = 0, .interface = .none } };
        }
        var w: std.Io.Writer = .fixed(&tbuf);
        try a.format(&w);
        const p = try net.IpAddress.parseLiteral(w.buffered());
        switch (p) {
            .ip4 => |v| {
                sum = sum *% 31 +% v.port;
                for (v.bytes) |c| sum = sum *% 31 +% c;
            },
            .ip6 => |v| {
                sum = sum *% 31 +% v.port;
                for (v.bytes) |c| sum = sum *% 31 +% c;
            },
        }
    }
    var wbuf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(init.io, &wbuf);
    try out.interface.print("{d} {d}\n", .{ n, sum });
    try out.interface.flush();
}
