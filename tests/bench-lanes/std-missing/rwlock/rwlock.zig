// rwlock, Zig, idiomatic std: the workload of README.md with std.Io.RwLock
// and t OS threads (std.Thread; labelled threads). Each thread does n
// operations on one shared pair: every tenth takes the write lock and adds
// one to both fields, the others take the read lock and count a pair whose
// fields differ. Prints the final first field and the mismatches. No libc.
// Usage: rwlock <n> <t>.
const std = @import("std");

const Pair = struct { a: u64 = 0, b: u64 = 0 };

const Shared = struct {
    l: std.Io.RwLock = .init,
    p: Pair = .{},
    bad: std.atomic.Value(i64) = .init(0),
};

fn worker(s: *Shared, io: std.Io, n: usize) void {
    var miss: i64 = 0;
    for (0..n) |i| {
        if (i % 10 == 0) {
            s.l.lockUncancelable(io);
            s.p.a += 1;
            s.p.b += 1;
            s.l.unlock(io);
        } else {
            s.l.lockSharedUncancelable(io);
            if (s.p.a != s.p.b) miss += 1;
            s.l.unlockShared(io);
        }
    }
    _ = s.bad.fetchAdd(miss, .monotonic);
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
    try w.interface.print("{d} {d}\n", .{ s.p.a, s.bad.load(.monotonic) });
    try w.interface.flush();
}
