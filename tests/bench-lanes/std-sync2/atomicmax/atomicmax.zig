// atomicmax, Zig, idiomatic std: the workload of README.md with two
// std.atomic.Value(i64) over t OS threads (std.Thread; labelled threads).
// Each thread takes a ticket n times with fetchAdd(1, .monotonic) and
// raises a shared maximum to it with a cmpxchgWeak(.monotonic, .monotonic)
// loop. Prints the counter and the maximum (t * n and t * n - 1). No libc.
// Usage: atomicmax <n> <t>.
const std = @import("std");

fn worker(c: *std.atomic.Value(i64), m: *std.atomic.Value(i64), n: usize) void {
    for (0..n) |_| {
        const x = c.fetchAdd(1, .monotonic);
        var cur = m.load(.monotonic);
        while (x > cur) {
            cur = m.cmpxchgWeak(cur, x, .monotonic, .monotonic) orelse break;
        }
    }
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const t = try std.fmt.parseInt(usize, args[2], 10);
    if (t > 64) return error.Usage;
    var c: std.atomic.Value(i64) = .init(0);
    var m: std.atomic.Value(i64) = .init(0);
    var ths: [64]std.Thread = undefined;
    for (ths[0..t]) |*th| th.* = try std.Thread.spawn(.{}, worker, .{ &c, &m, n });
    for (ths[0..t]) |th| th.join();
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d} {d}\n", .{ c.load(.monotonic), m.load(.monotonic) });
    try w.interface.flush();
}
