// strops-replace, Zig, idiomatic std: the task of README.md with
// std.mem.replaceOwned (non-overlapping, left to right, the output sized by
// replacementSize first) over std.heap.smp_allocator, three in turn on each
// line of std.mem.splitScalar, each result freed after use. The file is
// read once with std.Io.Dir.readFileAlloc and checked with
// std.unicode.utf8ValidateSlice. Usage: strops-replace <file> <rounds>.
const std = @import("std");

fn round(gpa: std.mem.Allocator, text: []const u8) !u64 {
    var h: u64 = 0xcbf29ce484222325;
    var n: u64 = 0;
    var lines = std.mem.splitScalar(u8, text, '\n');
    while (lines.next()) |line| {
        const a = try std.mem.replaceOwned(u8, gpa, line, "e", "EEE");
        defer gpa.free(a);
        const b = try std.mem.replaceOwned(u8, gpa, a, "s ", "S");
        defer gpa.free(b);
        const c = try std.mem.replaceOwned(u8, gpa, b, " a", "_A");
        defer gpa.free(c);
        for (c) |x| h = (h ^ x) *% 0x100000001b3;
        n +%= c.len;
    }
    return h +% n;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const text = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(text);
    if (!std.unicode.utf8ValidateSlice(text)) return error.InvalidUtf8;
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) total +%= try round(std.heap.smp_allocator, text);
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
