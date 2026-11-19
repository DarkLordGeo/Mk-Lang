// mapstr, Zig, idiomatic std: the task of README.md with
// std.StringHashMap(u64) (its default string hash, std.hash.Wyhash with
// seed 0) whose keys are slices of one generated buffer (no copy per key),
// over the page allocator through an arena per round.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn round(ks: []const []const u8, ms: []const []const u8, r: u64) !u64 {
    var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer arena.deinit();
    var m = std.StringHashMap(u64).init(arena.allocator());
    for (ks, 0..) |k, i| try m.put(k, @as(u64, @intCast(i)) +% r);
    var sum: u64 = 0;
    for (ks, 0..) |k, i| {
        if (m.get(k)) |v| sum +%= v;
        if (m.contains(ms[i])) sum +%= 1;
        if (i % 2 == 0) _ = m.remove(k);
    }
    for (ks) |k| {
        if (m.contains(k)) sum +%= 3;
    }
    return sum +% m.count();
}

pub fn main(init: std.process.Init) !void {
    const gpa = init.arena.allocator();
    const args = try init.minimal.args.toSlice(gpa);
    if (args.len != 6) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    const seed = try std.fmt.parseInt(u64, args[3], 10);
    const min_len = try std.fmt.parseInt(usize, args[4], 10);
    const max_len = try std.fmt.parseInt(usize, args[5], 10);
    var buf: std.ArrayList(u8) = .empty;
    var miss: std.ArrayList(u8) = .empty;
    var ends = try gpa.alloc(usize, n);
    var st = seed;
    const span: u64 = @intCast(max_len - min_len + 1);
    var i: usize = 0;
    while (i < n) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        var z = mix(st);
        const l = min_len + @as(usize, @intCast(z % span));
        var j: usize = 0;
        while (j < l) : (j += 1) {
            if (j % 8 == 0) z = mix(z +% @as(u64, @intCast(j)));
            const sh: u6 = @intCast(8 * (j % 8));
            var c: u8 = 'a' + @as(u8, @intCast(((z >> sh) & 0xFF) % 26));
            try buf.append(gpa, c);
            if (j == 0) c -= 'a' - 'A';
            try miss.append(gpa, c);
        }
        ends[i] = buf.items.len;
    }
    var ks = try gpa.alloc([]const u8, n);
    var ms = try gpa.alloc([]const u8, n);
    var start: usize = 0;
    for (ends, 0..) |e, k| {
        ks[k] = buf.items[start..e];
        ms[k] = miss.items[start..e];
        start = e;
    }
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) total +%= try round(ks, ms, r);
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
