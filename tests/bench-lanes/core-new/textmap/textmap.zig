// textmap, Zig, idiomatic std: the task of README.md with
// std.StringHashMap(u64) (wyhash with seed 0: fixed, no DoS resistance)
// through an arena per round over the page allocator. getOrPut finds or
// claims the slot; a new key is duplicated into the arena, as TextMap
// copies only a new key.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn digits(buf: *[20]u8, x: u64) usize {
    var i: usize = buf.len - 1;
    var v = x;
    buf[i] = @as(u8, @intCast(v % 10)) + '0';
    v /= 10;
    while (v > 0) {
        i -= 1;
        buf[i] = @as(u8, @intCast(v % 10)) + '0';
        v /= 10;
    }
    return i;
}

fn round(n: usize, seed: u64) !u64 {
    var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var m = std.StringHashMap(u64).init(a);
    const u: u64 = @intCast(n / 4 + 1);
    var buf: [20]u8 = undefined;
    var st = seed;
    var i: usize = 0;
    while (i < n) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const p = digits(&buf, mix(st) % u);
        const gop = try m.getOrPut(buf[p..]);
        if (!gop.found_existing) gop.key_ptr.* = try a.dupe(u8, buf[p..]);
        gop.value_ptr.* = @intCast(i);
    }
    var sum: u64 = 0;
    st = seed ^ 0x5555555555555555;
    i = 0;
    while (i < n) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const p = digits(&buf, mix(st) % u);
        const gop = try m.getOrPut(buf[p..]);
        if (gop.found_existing) {
            sum +%= gop.value_ptr.*;
        } else {
            gop.key_ptr.* = try a.dupe(u8, buf[p..]);
            sum +%= 1;
        }
        gop.value_ptr.* = @intCast(i);
    }
    return sum +% m.count();
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    const seed = try std.fmt.parseInt(u64, args[3], 10);
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) total +%= try round(n, seed +% r);
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
