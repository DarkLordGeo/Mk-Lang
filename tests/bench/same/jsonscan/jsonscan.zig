// jsonscan, Zig: the reference algorithm of README.md.
// The file is read once with std.Io.Dir.readFileAlloc into a []u8 from
// std.heap.page_allocator, then scanned rounds times with b[i] (bounds
// checked in ReleaseSafe, unchecked in ReleaseFast). Indices are usize (the
// type Zig indexes with), the string length l is i64 like every column's
// (+ checked in ReleaseSafe, UB in ReleaseFast, never reached); the token hash is u64 with +% and *% (overflow class "wraps"). The
// if chain is in the README's order; no switch. No @setRuntimeSafety, no
// libc.
const std = @import("std");

fn scan(b: []const u8, h0: u64) u64 {
    var h = h0;
    const n = b.len;
    var i: usize = 0;
    while (i < n) {
        const c = b[i];
        if (c == ' ' or c == '\n' or c == '\r' or c == '\t') {
            i += 1;
        } else if (c == '"') {
            i += 1;
            var l: i64 = 0;
            while (i < n and b[i] != '"') {
                if (b[i] == '\\') i += 1;
                i += 1;
                l += 1;
            }
            i += 1;
            h = h *% 31 +% @as(u64, @bitCast(l));
        } else if (c == '-' or (c >= '0' and c <= '9')) {
            var neg: u64 = 0;
            if (c == '-') {
                neg = 1;
                i += 1;
            }
            var v: u64 = 0;
            while (i < n and ((b[i] >= '0' and b[i] <= '9') or b[i] == '.')) {
                if (b[i] != '.') v = v *% 10 +% @as(u64, b[i] - '0');
                i += 1;
            }
            h = h *% 31 +% (v *% 2 +% neg);
        } else if (c == 't') {
            h = h *% 31 +% 1;
            i += 4;
        } else if (c == 'f') {
            h = h *% 31 +% 2;
            i += 5;
        } else if (c == 'n') {
            h = h *% 31 +% 3;
            i += 4;
        } else {
            h = h *% 31 +% @as(u64, c);
            i += 1;
        }
    }
    return h;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const b = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(b);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    var total: u64 = 0;
    for (0..rounds) |_| {
        total = scan(b, total);
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
