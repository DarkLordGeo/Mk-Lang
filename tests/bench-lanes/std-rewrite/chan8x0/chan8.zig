// chan8, Zig, idiomatic std: 8 OS threads (std.Thread) each send 0..n-1 on
// one std.Io.Queue(i64) whose buffer holds c values (c = 0: no buffer, so a
// put waits for a getter, which takes the value from the parked putter),
// and 8 threads sum what they get until the queue is closed and drained;
// prints the total (8 * n * (n - 1) / 2). std.Io.Queue is Zig's std
// channel: many producers, many consumers, blocking putOne and getOne,
// close, and getters served first. The Io is the process's std.Io.Threaded
// instance. No libc. Usage: chan8 <n> <c>.
const std = @import("std");

const Q = std.Io.Queue(i64);

fn sender(q: *Q, io: std.Io, n: i64) void {
    var i: i64 = 0;
    while (i < n) : (i += 1) {
        q.putOneUncancelable(io, i) catch unreachable; // closed only after every sender ends
    }
}

fn receiver(q: *Q, io: std.Io, total: *std.atomic.Value(i64)) void {
    var k: i64 = 0;
    while (true) {
        const x = q.getOneUncancelable(io) catch break; // error.Closed: closed and drained
        k += x;
    }
    _ = total.fetchAdd(k, .monotonic);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(i64, args[1], 10);
    const c = try std.fmt.parseInt(usize, args[2], 10);
    const a = init.arena.allocator();
    const buf = try a.alloc(i64, c);
    var q = Q.init(buf);
    var total = std.atomic.Value(i64).init(0);
    var rs: [8]std.Thread = undefined;
    var ss: [8]std.Thread = undefined;
    for (&rs) |*th| th.* = try std.Thread.spawn(.{}, receiver, .{ &q, init.io, &total });
    for (&ss) |*th| th.* = try std.Thread.spawn(.{}, sender, .{ &q, init.io, n });
    for (ss) |th| th.join();
    q.close(init.io);
    for (rs) |th| th.join();
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total.load(.monotonic)});
    try w.interface.flush();
}
