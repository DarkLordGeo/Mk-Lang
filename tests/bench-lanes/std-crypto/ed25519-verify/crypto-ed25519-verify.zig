// crypto-ed25519-verify, Zig: std.crypto.sign.Ed25519 over the input of
// mk/crypto-ed25519-verify.mk's comment, the key from
// KeyPair.generateDeterministic. Signature.verifyStrict, the cofactorless
// equation as Mk and Go (Signature.verify is cofactored: [8S]B = [8]R +
// [8k]A, more work and a wider acceptance set; CRY-3). It still does more
// than Mk and Go: it decodes R (one square root), rejects an identity A or
// R and a non-canonical R before the equation, and compares points.
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
    var seed: [32]u8 = undefined;
    fill(&st, &seed);
    const kp = try Ed25519.KeyPair.generateDeterministic(seed);
    var msgs: [64 * 256]u8 = undefined;
    fill(&st, &msgs);
    var sigs: [256][64]u8 = undefined;
    var sum: u64 = 0;
    for (0..256) |i| {
        sigs[i] = (try kp.sign(msgs[64 * i .. 64 * i + 64], null)).toBytes();
        sum ^= std.mem.readInt(u64, sigs[i][0..8], .little);
    }
    sigs[255][32] ^= 1;
    var ok: u64 = 0;
    for (0..n) |i| {
        const j = i % 256;
        const sig = Ed25519.Signature.fromBytes(sigs[j]);
        if (sig.verifyStrict(msgs[64 * j .. 64 * j + 64], kp.public_key)) |_| {
            ok += 1;
        } else |_| {}
    }
    try print(init, sum ^ ok);
}
