// crypto-x25519, Zig: one X25519 key exchange per step as
// mk/crypto-x25519.mk's comment says: std.crypto.dh.X25519.recoverPublicKey
// (the ladder on the base point) and X25519.scalarmult (the ladder on u),
// two ladders, as Mk and Go.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    var st = try std.fmt.parseInt(u64, args[2], 10);
    var k: [32]u8 = undefined;
    for (0..4) |w| {
        st +%= 0x9e3779b97f4a7c15;
        std.mem.writeInt(u64, k[8 * w ..][0..8], mix(st), .little);
    }
    var u: [32]u8 = @splat(0);
    u[0] = 9;
    var acc: u64 = 0;
    for (0..n) |_| {
        const pk = std.crypto.dh.X25519.recoverPublicKey(k);
        acc ^= std.mem.readInt(u64, pk[0..8], .little);
        const r = try std.crypto.dh.X25519.scalarmult(k, u);
        u = k;
        k = r;
    }
    var wbuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &wbuf);
    try w.interface.print("{d}\n", .{std.mem.readInt(u64, k[0..8], .little) ^ acc});
    try w.interface.flush();
}
