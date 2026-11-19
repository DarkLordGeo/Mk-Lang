// compress-adler32, Zig, idiomatic std: std.hash.Adler32.hash of a 50 MB log
// text, rounds times, round r over the text from byte r % 64 on (no cell
// writes to its buffer). The checksum adds the rounds' values.
// Usage: compress-adler32 <file> <rounds>.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const text = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(text);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    var total: u64 = 0;
    for (0..rounds) |r| {
        total +%= std.hash.Adler32.hash(text[r % 64 ..]);
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
