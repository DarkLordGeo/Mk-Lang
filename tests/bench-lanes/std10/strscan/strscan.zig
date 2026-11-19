// strscan, Zig, idiomatic std: std.mem.findScalarLast (variant
// lastindexbyte), std.unicode.calcUtf16LeLen (variant utf16len) and
// std.unicode.wtf8ValidateSlice (variant validwtf8) over the text of
// README.md, reps calls, call r on the text from offset r mod 64. The
// checksum adds each answer (a bool as 0 or 1) and the offset. Zig's std
// has no all-ASCII test of a slice (std.ascii.isAscii takes one byte;
// skipped, bench.json).
// Usage: strscan <variant> <size> <reps> <seed>.
const std = @import("std");

fn mix(z0: u64) u64 {
    var z = z0;
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
    return z ^ (z >> 31);
}

fn fill(buf: []u8, ascii: bool, wtf: bool, seed: u64) void {
    const n = buf.len;
    var st = seed;
    var i: usize = 0;
    while (i < n and i < 128) : (i += 1) buf[i] = 'a' + @as(u8, @intCast(i % 26));
    while (i < n) {
        st +%= 0x9E3779B97F4A7C15;
        const z = mix(st);
        const p: u64 = if (ascii) 0 else z % 8;
        if (p < 5 or i + 4 > n) {
            buf[i] = 'a' + @as(u8, @intCast((z >> 8) % 26));
            i += 1;
        } else if (p == 5) {
            @memcpy(buf[i .. i + 2], &[_]u8{ 0xC3, 0xA9 });
            i += 2;
        } else if (p == 6) {
            @memcpy(buf[i .. i + 3], &[_]u8{ 0xE2, 0x82, 0xAC });
            i += 3;
        } else if (wtf and (z >> 16) % 4 == 0) {
            @memcpy(buf[i .. i + 3], &[_]u8{ 0xED, 0xA0, 0x80 });
            i += 3;
        } else {
            @memcpy(buf[i .. i + 4], &[_]u8{ 0xF0, 0x9F, 0x98, 0x80 });
            i += 4;
        }
    }
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 5) return error.Usage;
    const name = args[1];
    const eq = std.mem.eql;
    const n = try std.fmt.parseInt(usize, args[2], 10);
    const reps = try std.fmt.parseInt(usize, args[3], 10);
    const seed = try std.fmt.parseInt(u64, args[4], 10);
    const buf = try init.arena.allocator().alloc(u8, n);
    fill(buf, eq(u8, name, "lastindexbyte"), eq(u8, name, "validwtf8"), seed);
    var acc: i64 = 0;
    if (eq(u8, name, "lastindexbyte")) {
        buf[100] = 0xFF;
        for (0..reps) |r| {
            const off = r % 64;
            const k: i64 = if (std.mem.findScalarLast(u8, buf[off..], 0xFF)) |k| @intCast(k) else -1;
            acc += k + @as(i64, @intCast(off));
        }
    } else if (eq(u8, name, "utf16len")) {
        for (0..reps) |r| {
            const off = r % 64;
            acc += @as(i64, @intCast(try std.unicode.calcUtf16LeLen(buf[off..]))) + @as(i64, @intCast(off));
        }
    } else if (eq(u8, name, "validwtf8")) {
        for (0..reps) |r| {
            const off = r % 64;
            acc += @as(i64, @intFromBool(std.unicode.wtf8ValidateSlice(buf[off..]))) + @as(i64, @intCast(off));
        }
    } else return error.Usage;
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{acc});
    try w.interface.flush();
}
