// notifypermit, Zig, idiomatic std: the workload of README.md with
// std.Io.Event, Zig std's set-and-wait flag, one per side, over two OS
// threads (std.Thread; labelled threads). notify = set the other's event;
// wait = wait on one's own, then reset it. The first side notifies then
// waits, the second waits then notifies, n times each. A side resets its
// event only after its own wait returned, and the other sets it again only
// after this side's next notify, so no reset races a wait, as Event.reset
// requires. Prints the total rounds (2 * n). No libc.
// Usage: notifypermit <n>.
const std = @import("std");

const Shared = struct {
    ev: [2]std.Io.Event = .{ .unset, .unset },
    total: std.atomic.Value(i64) = .init(0),
};

fn side(s: *Shared, io: std.Io, me: usize, n: usize) void {
    var k: i64 = 0;
    for (0..n) |_| {
        if (me == 0) {
            s.ev[1].set(io);
            s.ev[0].waitUncancelable(io);
            s.ev[0].reset();
        } else {
            s.ev[1].waitUncancelable(io);
            s.ev[1].reset();
            s.ev[0].set(io);
        }
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
