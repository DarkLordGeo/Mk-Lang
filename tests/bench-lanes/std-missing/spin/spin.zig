// spin, Zig, idiomatic std: the workload of README.md with std.Io.Mutex
// (Zig's std has no spin lock; std.atomic has only the spin-loop hint) and
// t OS threads (std.Thread; labelled threads). Each thread takes the lock n
// times and adds one to the shared counter. Prints the counter. No libc.
// Usage: spin <n> <t>.
const std = @import("std");

const Shared = struct {
    m: std.Io.Mutex = .init,
    c: u64 = 0,
};

fn worker(s: *Shared, io: std.Io, n: usize) void {
    for (0..n) |_| {
        s.m.lockUncancelable(io);
        s.c += 1;
        s.m.unlock(io);
    }
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const t = try std.fmt.parseInt(usize, args[2], 10);
    var s: Shared = .{};
    const ths = try init.arena.allocator().alloc(std.Thread, t);
    for (ths) |*th| th.* = try std.Thread.spawn(.{}, worker, .{ &s, init.io, n });
    for (ths) |th| th.join();
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{s.c});
    try w.interface.flush();
}
