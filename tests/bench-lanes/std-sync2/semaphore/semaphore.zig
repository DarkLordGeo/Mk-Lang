// semaphore, Zig, idiomatic std: the workload of README.md with
// std.Io.Semaphore (a mutex, a condition and a count; weight 1) and t OS
// threads (std.Thread; labelled threads). Each thread does n rounds of
// wait, a check that at most 4 hold one, post. Prints t * n and the
// violations. No libc. Usage: semaphore <n> <t>.
const std = @import("std");

const Shared = struct {
    sem: std.Io.Semaphore = .{ .permits = 4 },
    inuse: std.atomic.Value(i64) = .init(0),
    bad: std.atomic.Value(i64) = .init(0),
    done: std.atomic.Value(i64) = .init(0),
};

fn worker(s: *Shared, io: std.Io, n: usize) void {
    var k: i64 = 0;
    for (0..n) |_| {
        s.sem.waitUncancelable(io);
        if (s.inuse.fetchAdd(1, .seq_cst) + 1 > 4) _ = s.bad.fetchAdd(1, .monotonic);
        _ = s.inuse.fetchSub(1, .seq_cst);
        s.sem.post(io);
        k += 1;
    }
    _ = s.done.fetchAdd(k, .monotonic);
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
    try w.interface.print("{d} {d}\n", .{ s.done.load(.monotonic), s.bad.load(.monotonic) });
    try w.interface.flush();
}
