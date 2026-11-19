// hash-sip13, Zig: std.hash.SipHash64(1, 3) under the key of 16 zero bytes
// (Rust DefaultHasher's key, and Mk's zero-filled HashKey), over the input
// and passes of mk/hash-sip13.mk's comment.
const std = @import("std");

const Sip13 = std.hash.SipHash64(1, 3);
const key: [16]u8 = @splat(0);

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
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
        const h: u64 = Sip13.toInt(buf, &key);
        sum ^= h;
        buf[r % n] ^= @truncate(h);
    }
    var wbuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &wbuf);
    try w.interface.print("{d}\n", .{sum});
    try w.interface.flush();
}
