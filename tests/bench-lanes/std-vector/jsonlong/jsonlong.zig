// jsonlong, Zig: std.json on one array of long strings
// built in the program from splitmix64(seed), as the Mk program builds it
// (README.md). Usage: jsonlong <mode> <strings> <reps> <seed>. parse runs
// std.json.parseFromSliceLeaky into std.json.Value reps times into one
// ArenaAllocator reset (retain_capacity) after each and sums the decoded
// string lengths; write parses once, then std.json.Stringify.valueAlloc
// (compact JSON) reps times into an ArenaAllocator reset after each, and
// sums the lengths. No libc.
const std = @import("std");

const Mix = struct {
    x: u64,
    fn next(m: *Mix) u64 {
        m.x +%= 0x9E3779B97F4A7C15;
        var z = m.x;
        z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
        z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
        return z ^ (z >> 31);
    }
};

// doc is the Mk program's Doc, byte for byte.
fn doc(a: std.mem.Allocator, strings: usize, seed: u64) ![]u8 {
    var m = Mix{ .x = seed };
    var t: std.ArrayList(u8) = .empty;
    try t.append(a, '[');
    var buf: [1100]u8 = undefined;
    for (0..strings) |k| {
        if (k > 0) try t.append(a, ',');
        const n: usize = 100 + @as(usize, @intCast(m.next() % 1000));
        var i: usize = 0;
        buf[i] = '"';
        i += 1;
        while (i < n) {
            const r = m.next();
            if (r % 256 == 0 and i + 2 < n) {
                buf[i] = '\\';
                buf[i + 1] = if ((r >> 8) % 2 == 0) 'n' else '"';
                i += 2;
            } else {
                var c: u8 = @intCast(0x20 + (r >> 16) % 95);
                if (c == '"' or c == '\\') c = 'a';
                buf[i] = c;
                i += 1;
            }
        }
        buf[n] = '"';
        try t.appendSlice(a, buf[0 .. n + 1]);
    }
    try t.append(a, ']');
    return t.items;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 5) return error.Usage;
    const pa = std.heap.page_allocator;
    const strings = try std.fmt.parseInt(usize, args[2], 10);
    const reps = try std.fmt.parseInt(usize, args[3], 10);
    const seed = try std.fmt.parseInt(u64, args[4], 10);
    var docArena = std.heap.ArenaAllocator.init(pa);
    defer docArena.deinit();
    const text = try doc(docArena.allocator(), strings, seed);
    var total: u64 = 0;
    var arena = std.heap.ArenaAllocator.init(pa);
    defer arena.deinit();
    if (std.mem.eql(u8, args[1], "parse")) {
        for (0..reps) |_| {
            const v = try std.json.parseFromSliceLeaky(std.json.Value, arena.allocator(), text, .{ .parse_numbers = false });
            for (v.array.items) |e| {
                switch (e) {
                    .string => |s| total +%= s.len,
                    else => {},
                }
            }
            _ = arena.reset(.retain_capacity);
        }
    } else {
        var tree = std.heap.ArenaAllocator.init(pa);
        defer tree.deinit();
        const v = try std.json.parseFromSliceLeaky(std.json.Value, tree.allocator(), text, .{ .parse_numbers = false });
        for (0..reps) |_| {
            const s = try std.json.Stringify.valueAlloc(arena.allocator(), v, .{});
            total +%= s.len;
            _ = arena.reset(.retain_capacity);
        }
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
