// wordfreq, Zig, idiomatic std: the task of README.md. The file is read
// once with std.Io.Dir.readFileAlloc and checked with
// std.unicode.utf8ValidateSlice; each round splits it with
// std.mem.tokenizeAny over ASCII whitespace and counts every word in a
// std.StringHashMap(u64) (its default hash, std.hash.Wyhash with seed 0:
// fixed, no DoS resistance) through getOrPut, the keys slices of the text,
// over an ArenaAllocator the round frees; the map grows from empty. Then
// the checksum over the entries: sum of FNV-1a 64 of the word times its
// count, plus the number of distinct words, all wrapping. Usage: wordfreq
// <file> <rounds>.
const std = @import("std");

fn fnv(w: []const u8) u64 {
    var h: u64 = 0xcbf29ce484222325;
    for (w) |c| h = (h ^ c) *% 0x100000001b3;
    return h;
}

fn round(text: []const u8) !u64 {
    var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer arena.deinit();
    var m = std.StringHashMap(u64).init(arena.allocator());
    var words = std.mem.tokenizeAny(u8, text, " \t\n\r");
    while (words.next()) |w| {
        const e = try m.getOrPut(w);
        if (!e.found_existing) e.value_ptr.* = 0;
        e.value_ptr.* += 1;
    }
    var sum: u64 = 0;
    var it = m.iterator();
    while (it.next()) |e| sum +%= fnv(e.key_ptr.*) *% e.value_ptr.*;
    return sum +% m.count();
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
    while (r < rounds) : (r += 1) total +%= try round(text);
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
