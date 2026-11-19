// big-factorial, Zig, idiomatic std: the Mk row's loop with
// std.math.big.int (Managed): for i from 2 to n, acc = acc * (i + seed mod
// 7) (mul by a one-limb value), then acc's decimal text (toString); prints
// the text's length times 31 plus the sum of its digits times their
// 1-based position, wrapping. Every value uses init.gpa, Zig's
// default-selected general purpose allocator (std.process.Init).
// Usage: big-factorial <n> <seed>.
const std = @import("std");
const Managed = std.math.big.int.Managed;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const seed = try std.fmt.parseInt(u64, args[2], 10);
    const a = init.gpa;
    var acc = try Managed.initSet(a, 1);
    defer acc.deinit();
    var f = try Managed.init(a);
    defer f.deinit();
    var t = try Managed.init(a);
    defer t.deinit();
    var i: u64 = 2;
    while (i <= n) : (i += 1) {
        try f.set(i + seed % 7);
        try t.mul(&acc, &f);
        acc.swap(&t);
    }
    const text = try acc.toString(a, 10, .lower);
    defer a.free(text);
    var sum: u64 = @as(u64, text.len) *% 31;
    for (text, 0..) |ch, k| {
        sum +%= @as(u64, ch - '0') *% @as(u64, k + 1);
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{sum});
    try w.interface.flush();
}
