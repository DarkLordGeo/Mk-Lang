// notify, Zig, idiomatic std: the workload of README.md with a
// std.Io.Mutex guarding whose turn it is and one std.Io.Condition per
// side, over two OS threads (std.Thread; labelled threads). Each side, n
// times: lock, wait while it is not its turn, give the turn to the other,
// unlock, signal the other's Condition. Prints the total rounds (2 * n).
// No libc. Usage: notify <n>.
const std = @import("std");

const Shared = struct {
    mutex: std.Io.Mutex = .init,
    cv: [2]std.Io.Condition = .{ .init, .init },
    turn: usize = 0,
    total: std.atomic.Value(i64) = .init(0),
};

fn side(s: *Shared, io: std.Io, me: usize, n: usize) void {
    var k: i64 = 0;
    for (0..n) |_| {
        s.mutex.lockUncancelable(io);
        while (s.turn != me) s.cv[me].waitUncancelable(io, &s.mutex);
        s.turn = 1 - me;
        s.mutex.unlock(io);
        s.cv[1 - me].signal(io);
        k += 1;
    }
    _ = s.total.fetchAdd(k, .monotonic);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    var s: Shared = .{};
    var ths: [2]std.Thread = undefined;
    for (&ths, 0..) |*th, me| th.* = try std.Thread.spawn(.{}, side, .{ &s, init.io, me, n });
    for (ths) |th| th.join();
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{s.total.load(.monotonic)});
    try w.interface.flush();
}
