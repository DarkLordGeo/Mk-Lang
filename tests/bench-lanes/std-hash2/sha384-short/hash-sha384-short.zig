// hash-sha384-short, Zig: std's SHA-384 over 64-byte messages, the input and passes
// of mk/hash-sha384-short.mk's comment.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn digest(b: []const u8) u64 {
    var out: [48]u8 = undefined;
    std.crypto.hash.sha2.Sha384.hash(b, &out, .{});
    return std.mem.readInt(u64, out[0..8], .little);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const mib = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const n = mib * 1048576;
    const buf = try std.heap.page_allocator.alloc(u8, n);
    defer std.heap.page_allocator.free(buf);
    var i: usize = 0;
    while (i < n) : (i += 8) {
        st +%= 0x9e3779b97f4a7c15;
        std.mem.writeInt(u64, buf[i..][0..8], mix(st), .little);
    }
    var sum: u64 = 0;
    for (0..reps) |r| {
        var j: usize = 0;
        while (j < n) : (j += 64) {
            sum ^= digest(buf[j .. j + 64]);
        }
        buf[r % n] ^= @truncate(sum);
    }
    var wbuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &wbuf);
    try w.interface.print("{d}\n", .{sum});
    try w.interface.flush();
}
