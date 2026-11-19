// mappair, Zig, idiomatic std: the task of README.md with
// std.AutoHashMap(Pt, u64), Pt a struct of two u64 fields (its default
// hash, std.hash.autoHash over the fields into Wyhash with seed 0), over
// the page allocator through an arena per round.
const std = @import("std");

const Pt = struct { a: u64, b: u64 };

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn round(n: usize, seed: u64) !u64 {
    var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer arena.deinit();
    var m = std.AutoHashMap(Pt, u64).init(arena.allocator());
    var st = seed;
    var i: usize = 0;
    while (i < n) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        try m.put(.{ .a = mix(st), .b = st }, @intCast(i));
    }
    var sum: u64 = 0;
    st = seed;
    i = 0;
    while (i < n) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const k: Pt = .{ .a = mix(st), .b = st };
        if (m.get(k)) |v| sum +%= v;
        if (m.contains(.{ .a = mix(st), .b = st ^ 1 })) sum +%= 1;
        if (i % 2 == 0) _ = m.remove(k);
    }
    st = seed;
    i = 0;
    while (i < n) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        if (m.contains(.{ .a = mix(st), .b = st })) sum +%= 3;
    }
    return sum +% m.count();
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    const seed = try std.fmt.parseInt(u64, args[3], 10);
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) total +%= try round(n, seed +% r);
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
