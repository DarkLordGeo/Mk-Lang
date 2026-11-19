// cancelpoint, Zig: n uncontended lock and unlock pairs of a std.Io.Mutex
// (Zig 0.17: lock(io) is a cancellation point of std.Io, as Mk's Lock),
// the guarded value updated as x = x * 31 + i (wrapping). With expired 1
// an unrelated flag is set before the loop (Zig's cancellation belongs to
// a future, so nothing else can be expired beside this one); this loop
// never reads it. Prints n, x and the failed calls. Usage: cancelpoint <n>
// <expired>.
const std = @import("std");

var other = std.atomic.Value(bool).init(false);

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const expired = try std.fmt.parseInt(u32, args[2], 10);
    if (expired == 1) other.store(true, .seq_cst);
    var mu: std.Io.Mutex = .init;
    var x: u64 = 0;
    var fails: u64 = 0;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        mu.lock(init.io) catch {
            fails += 1;
            break;
        };
        x = x *% 31 +% i;
        mu.unlock(init.io);
    }
    var out: [96]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d} {d} {d}\n", .{ n, x, fails });
    try w.interface.flush();
}
