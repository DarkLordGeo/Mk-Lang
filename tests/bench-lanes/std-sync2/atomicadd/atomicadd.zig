// atomicadd, Zig, idiomatic std: the workload of README.md with
// std.atomic.Value(i64) over t OS threads (std.Thread; labelled threads).
// Each thread does n fetchAdd(1, .seq_cst) on one shared counter (seq_cst,
// as Mk's AtomicInt; on x86-64 every order of an RMW is the same
// lock-prefixed instruction). Prints the counter (t * n). No libc.
// Usage: atomicadd <n> <t>.
const std = @import("std");

fn worker(c: *std.atomic.Value(i64), n: usize) void {
    for (0..n) |_| _ = c.fetchAdd(1, .seq_cst);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const t = try std.fmt.parseInt(usize, args[2], 10);
    if (t > 64) return error.Usage;
    var c: std.atomic.Value(i64) = .init(0);
    var ths: [64]std.Thread = undefined;
    for (ths[0..t]) |*th| th.* = try std.Thread.spawn(.{}, worker, .{ &c, n });
    for (ths[0..t]) |th| th.join();
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{c.load(.seq_cst)});
    try w.interface.flush();
}
