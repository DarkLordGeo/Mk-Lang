// The sanitize row (README.md): the same loop in Zig.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn gen(b: []u8, n: i64) usize {
    var st: u64 = 1;
    var o: usize = 0;
    var i: i64 = 0;
    while (i < n) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const z = mix(st);
        const k = z % 64;
        const v: u8 = @intCast((z >> 8) % 95);
        const w: u8 = @intCast((z >> 8) % 32);
        if (k < 48) {
            b[o] = 0x20 + v;
            o += 1;
        } else if (k < 52) {
            b[o] = 0x0a;
            o += 1;
        } else if (k < 56) {
            b[o] = 0xc3;
            b[o + 1] = 0xa9;
            o += 2;
        } else if (k < 58) {
            b[o] = 0x1b;
            o += 1;
        } else if (k < 60) {
            b[o] = w;
            o += 1;
        } else if (k < 62) {
            b[o] = 0xc2;
            b[o + 1] = 0x80 + w;
            o += 2;
        } else {
            b[o] = 0xe2;
            b[o + 1] = 0x82;
            b[o + 2] = 0xac;
            o += 3;
        }
    }
    return o;
}

// sanitize writes s to out with C0 controls but LF and tab, DEL and C1
// controls replaced by U+FFFD, each safe run copied whole; the length.
fn sanitize(out: []u8, s: []const u8) usize {
    var o: usize = 0;
    var run: usize = 0;
    var i: usize = 0;
    while (i < s.len) {
        const c = s[i];
        var bad: usize = 0;
        if ((c < 0x20 and c != 0x0a and c != 0x09) or c == 0x7f) {
            bad = 1;
        } else if (c == 0xc2 and i + 1 < s.len and s[i + 1] >= 0x80 and s[i + 1] <= 0x9f) {
            bad = 2;
        }
        if (bad == 0) {
            i += 1;
            continue;
        }
        @memcpy(out[o .. o + (i - run)], s[run..i]);
        o += i - run;
        out[o] = 0xef;
        out[o + 1] = 0xbf;
        out[o + 2] = 0xbd;
        o += 3;
        i += bad;
        run = i;
    }
    @memcpy(out[o .. o + (s.len - run)], s[run..]);
    return o + s.len - run;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    const n = try std.fmt.parseInt(i64, args[1], 10);
    const rounds = try std.fmt.parseInt(i64, args[2], 10);
    const alloc = std.heap.page_allocator;
    // Both buffers zeroed, as every column allocates them.
    const b = try alloc.alloc(u8, @intCast(3 * n));
    @memset(b, 0);
    const out = try alloc.alloc(u8, @intCast(9 * n));
    @memset(out, 0);
    const len = gen(b, n);
    var total: u64 = 0;
    var r: i64 = 0;
    while (r < rounds) : (r += 1) {
        const m = sanitize(out, b[0..len]);
        total +%= m;
        total +%= out[@intCast(@mod(r * 7919, @as(i64, @intCast(m))))];
    }
    var obuf: [64]u8 = undefined;
    var wr = std.Io.File.stdout().writer(init.io, &obuf);
    try wr.interface.print("{d}\n", .{total});
    try wr.interface.flush();
}
