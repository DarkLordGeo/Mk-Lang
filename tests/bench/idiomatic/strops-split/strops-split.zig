// strops-split, Zig, idiomatic std: the task of README.md with
// std.mem.splitScalar(u8, text, 'e') (empty pieces kept), std.mem.trim of
// " \t\r\n" on each piece, and std.mem.tokenizeAny over " \t\r\n" for the
// words. The file is read once with std.Io.Dir.readFileAlloc and checked
// with std.unicode.utf8ValidateSlice. Usage: strops-split <file> <rounds>.
const std = @import("std");

const ws = " \t\r\n";

fn round(text: []const u8) u64 {
    var total: u64 = 0;
    var h: u64 = 0xcbf29ce484222325;
    var pieces = std.mem.splitScalar(u8, text, 'e');
    while (pieces.next()) |piece| {
        const t = std.mem.trim(u8, piece, ws);
        total +%= 1;
        h = (h ^ @as(u64, t.len)) *% 0x100000001b3;
    }
    total +%= h;
    var words = std.mem.tokenizeAny(u8, text, ws);
    while (words.next()) |w| total +%= @as(u64, w.len) + 1;
    return total;
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
    while (r < rounds) : (r += 1) total +%= round(text);
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
