// keywords, Zig: README.md's keyword classifier, a chain of
// std.mem.eql(u8, w, "kw") tests against 16 literals per word, as a lexer's
// keyword match. The words are []const u8 views of one slice from
// std.heap.page_allocator, generated with a byte loop as Mk's; each word is
// text[a..b] (both offsets' bounds and the range checked in ReleaseSafe,
// unchecked in ReleaseFast; Zig has no UTF-8 boundary check and no
// validation pass). n, rounds, the offsets, the lengths and the loop
// indices are i64 (Mk's int), @intCast to usize where they index, and
// i % 1000 is @rem, a signed remainder as in Mk; the splitmix64 state, the
// classes and the checksum are u64 with +% and *% (overflow class "wraps").
// No @setRuntimeSafety, no libc.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

const keywords = [16][]const u8{ "if", "else", "for", "while", "return", "break", "continue", "fn", "let", "var", "const", "match", "struct", "enum", "import", "export" };

// classify is 1 + the index of the keyword w equals, or 0.
fn classify(w: []const u8) u64 {
    if (std.mem.eql(u8, w, "if")) return 1;
    if (std.mem.eql(u8, w, "else")) return 2;
    if (std.mem.eql(u8, w, "for")) return 3;
    if (std.mem.eql(u8, w, "while")) return 4;
    if (std.mem.eql(u8, w, "return")) return 5;
    if (std.mem.eql(u8, w, "break")) return 6;
    if (std.mem.eql(u8, w, "continue")) return 7;
    if (std.mem.eql(u8, w, "fn")) return 8;
    if (std.mem.eql(u8, w, "let")) return 9;
    if (std.mem.eql(u8, w, "var")) return 10;
    if (std.mem.eql(u8, w, "const")) return 11;
    if (std.mem.eql(u8, w, "match")) return 12;
    if (std.mem.eql(u8, w, "struct")) return 13;
    if (std.mem.eql(u8, w, "enum")) return 14;
    if (std.mem.eql(u8, w, "import")) return 15;
    if (std.mem.eql(u8, w, "export")) return 16;
    return 0;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(i64, args[1], 10);
    const rounds = try std.fmt.parseInt(i64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    const buf = try pa.alloc(u8, @intCast(n * 8));
    defer pa.free(buf);
    const offs = try pa.alloc(i64, @intCast(n + 1));
    defer pa.free(offs);
    var p: i64 = 0;
    var i: i64 = 0;
    while (i < n) : (i += 1) {
        offs[@intCast(i)] = p;
        st +%= 0x9e3779b97f4a7c15;
        const z = mix(st);
        const kind = z % 4;
        if (kind < 2) {
            const k = keywords[@intCast((z >> 8) % 16)];
            var j: usize = 0;
            while (j < k.len) : (j += 1) {
                buf[@intCast(p)] = k[j];
                p += 1;
            }
            if (kind == 1) {
                buf[@intCast(p - 1)] = @intCast(0x61 + (z >> 16) % 26);
            }
        } else {
            const l = 1 + @as(i64, @intCast((z >> 8) % 8));
            var j: i64 = 0;
            while (j < l) : (j += 1) {
                st +%= 0x9e3779b97f4a7c15;
                buf[@intCast(p)] = @intCast(0x61 + mix(st) % 26);
                p += 1;
            }
        }
    }
    offs[@intCast(n)] = p;
    const text = buf[0..@intCast(p)];
    var total: u64 = 0;
    var r: i64 = 0;
    while (r < rounds) : (r += 1) {
        i = 0;
        while (i < n) : (i += 1) {
            const w = text[@intCast(offs[@intCast(i)])..@intCast(offs[@intCast(i + 1)])];
            total +%= classify(w) *% @as(u64, @intCast(@rem(i, 1000) + 1));
        }
    }
    var out: [64]u8 = undefined;
    var wr = std.Io.File.stdout().writer(init.io, &out);
    try wr.interface.print("{d}\n", .{total});
    try wr.interface.flush();
}
