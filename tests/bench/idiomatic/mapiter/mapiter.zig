// mapiter, Zig, idiomatic std: the task of README.md with
// std.AutoHashMap(u64, u64) (its default integer hash) over
// std.heap.smp_allocator, filled once with put, then iterated <passes>
// times with iterator() and next(); the sum is order-free. Usage: mapiter
// <n> <passes> <seed>.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const passes = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    var m = std.AutoHashMap(u64, u64).init(std.heap.smp_allocator);
    defer m.deinit();
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        try m.put(mix(st), i);
    }
    var total: u64 = 0;
    var p: u64 = 0;
    while (p < passes) : (p += 1) {
        var it = m.iterator();
        while (it.next()) |e| total +%= e.key_ptr.* *% (e.value_ptr.* +% 1);
        total +%= m.count();
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
