// crypto-p256-ecdh, Zig: std.crypto.ecc.P256 over the input of
// mk/crypto-p256-ecdh.mk's comment: each pass decodes the peer's SEC 1
// point (fromSec1, which checks it is on the curve), multiplies it by the
// secret (mul, constant time) and takes x of the affine result.
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
const P256 = std.crypto.ecc.P256;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const keys = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const a = init.arena.allocator();
    const secrets = try a.alloc(u8, 32 * keys);
    fill(&st, secrets);
    const pubs = try a.alloc(u8, 65 * keys);
    for (0..keys) |i| {
        secrets[32 * i] &= 0x7f;
        const p = try P256.basePoint.mul(secrets[32 * i ..][0..32].*, .big);
        pubs[65 * i ..][0..65].* = p.toUncompressedSec1();
    }
    var sum: u64 = 0;
    for (0..reps) |_| {
        for (0..keys) |i| {
            const j = (i + 1) % keys;
            const peer = try P256.fromSec1(pubs[65 * j .. 65 * j + 65]);
            const q = try peer.mul(secrets[32 * i ..][0..32].*, .big);
            const x = q.affineCoordinates().x.toBytes(.big);
            sum = sum *% 31 +% std.mem.readInt(u64, x[0..8], .little);
        }
    }
    try print(init, sum);
}
