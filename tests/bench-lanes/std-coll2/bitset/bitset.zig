// bitset, Zig, std.DynamicBitSet: the workload of README.md. Indices are
// usize: + checked in ReleaseSafe, UB in ReleaseFast. No libc.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    var b = try std.DynamicBitSet.initEmpty(std.heap.page_allocator, n);
    defer b.deinit();
    var total: usize = 0;
    for (0..rounds) |r| {
        var i = r % 3;
        while (i < n) : (i += 3) b.set(i);
        i = r % 7;
        while (i < n) : (i += 7) b.unset(i);
        total += b.count();
    }
    var sum: u64 = 0;
    for (0..n) |i| {
        if (b.isSet(i)) sum +%= i;
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d} {d}\n", .{ total, sum });
    try w.interface.flush();
}
