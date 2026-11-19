// strops-find, Zig, idiomatic std: the task of README.md with
// std.mem.findPos (indexOfPos: the first match at or after p), over the
// lines of std.mem.splitScalar(u8, text, '\n'). The file is read once with
// std.Io.Dir.readFileAlloc and checked with std.unicode.utf8ValidateSlice.
// Usage: strops-find <file> <rounds>.
const std = @import("std");

const needles = [_][]const u8{ "the", "ing", "qzx", "e a", "tion", "s an d", "abcdefgh", "xylophone quartz" };

fn round(text: []const u8) u64 {
    var total: u64 = 0;
    var lines = std.mem.splitScalar(u8, text, '\n');
    while (lines.next()) |line| {
        for (needles) |nd| {
            var p: usize = 0;
            while (std.mem.findPos(u8, line, p, nd)) |q| {
                total +%= @as(u64, q) + 1;
                p = q + nd.len;
            }
        }
    }
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
