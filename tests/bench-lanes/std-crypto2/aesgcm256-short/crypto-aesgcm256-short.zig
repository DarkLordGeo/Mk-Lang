// crypto-aesgcm256-short, Zig: std.crypto.aead.aes_gcm.Aes256Gcm encrypt then
// decrypt over the input of mk/crypto-aesgcm256-short.mk's comment.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn fill(st: *u64, b: []u8) void {
    var i: usize = 0;
    while (i < b.len) {
        st.* +%= 0x9e3779b97f4a7c15;
        const x = mix(st.*);
        var k: usize = 0;
        while (k < 8 and i < b.len) : ({
            k += 1;
            i += 1;
        }) {
            b[i] = @truncate(x >> @intCast(8 * k));
        }
    }
}

fn print(init: std.process.Init, sum: u64) !void {
    var wbuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &wbuf);
    try w.interface.print("{d}\n", .{sum});
    try w.interface.flush();
}

const Aead = std.crypto.aead.aes_gcm.Aes256Gcm;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const mib = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const n = mib * 1048576;
    const buf = try std.heap.page_allocator.alloc(u8, n);
    defer std.heap.page_allocator.free(buf);
    fill(&st, buf);
    var key: [32]u8 = undefined;
    fill(&st, &key);
    var sealed: [64]u8 = undefined;
    var back: [64]u8 = undefined;
    const recs = n / 64;
    var nonce: [12]u8 = @splat(0);
    var aad: [13]u8 = @splat(0);
    var sum: u64 = 0;
    for (0..reps) |r| {
        for (0..recs) |j| {
            std.mem.writeInt(u64, nonce[0..8], @intCast(r * recs + j), .little);
            std.mem.writeInt(u64, aad[0..8], @intCast(j), .little);
            var tag: [16]u8 = undefined;
            Aead.encrypt(&sealed, &tag, buf[64 * j .. 64 * j + 64], &aad, nonce, key);
            sum ^= std.mem.readInt(u64, tag[0..8], .little);
            try Aead.decrypt(&back, &sealed, tag, &aad, nonce, key);
            sum ^= back[0];
        }
    }
    try print(init, sum);
}
