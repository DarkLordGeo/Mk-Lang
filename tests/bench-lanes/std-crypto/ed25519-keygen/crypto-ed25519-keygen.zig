// crypto-ed25519-keygen, Zig: std.crypto.sign.Ed25519
// KeyPair.generateDeterministic over the input of
// mk/crypto-ed25519-keygen.mk's comment.
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

const Ed25519 = std.crypto.sign.Ed25519;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    var st = try std.fmt.parseInt(u64, args[2], 10);
    const seeds = try std.heap.page_allocator.alloc(u8, 32 * n);
    defer std.heap.page_allocator.free(seeds);
    fill(&st, seeds);
    var sum: u64 = 0;
    for (0..n) |i| {
        const kp = try Ed25519.KeyPair.generateDeterministic(seeds[32 * i ..][0..32].*);
        const pk = kp.public_key.toBytes();
        sum ^= std.mem.readInt(u64, pk[0..8], .little) ^ std.mem.readInt(u64, pk[24..32], .little);
    }
    try print(init, sum);
}
