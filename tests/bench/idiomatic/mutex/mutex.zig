// mutex, Zig, idiomatic std: the task of README.md with a std.Io.Mutex
// (Zig 0.17's mutex, a futex word through std.Io) over t OS threads
// (std.Thread; labelled threads). Each thread does n times:
// lockUncancelable, add 1 to the shared count, unlock. Prints the count
// (t * n). No libc. Usage: mutex <n> <t>.
const std = @import("std");

const Shared = struct {
    mutex: std.Io.Mutex = .init,
    count: i64 = 0,
};

fn worker(s: *Shared, io: std.Io, n: usize) void {
    for (0..n) |_| {
        s.mutex.lockUncancelable(io);
        s.count += 1;
        s.mutex.unlock(io);
    }
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const t = try std.fmt.parseInt(usize, args[2], 10);
    if (t < 1 or t > 64) return error.Usage;
    var s: Shared = .{};
    var ths: [64]std.Thread = undefined;
    for (ths[0..t]) |*th| th.* = try std.Thread.spawn(.{}, worker, .{ &s, init.io, n });
    for (ths[0..t]) |th| th.join();
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{s.count});
    try w.interface.flush();
}
