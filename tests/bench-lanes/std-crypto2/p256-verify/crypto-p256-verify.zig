// crypto-p256-verify, Zig: std.crypto.sign.ecdsa.EcdsaP256Sha256 over the
// input of mk/crypto-p256-verify.mk's comment. Each digest is signed once
// with signPrehashed (Zig's deterministic nonce mixes a noise block into
// RFC 6979's HMAC input, so r and s differ from the other columns', and
// the checksum counts verified signatures, not their bytes); each pass
// decodes the public key from SEC 1 (fromSec1) and the signature from DER
// (Signature.fromDer, as Mk and Go read DER each time) and checks it with
// verifyPrehashed, the digest's first byte flipped on odd passes.
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
const Scheme = std.crypto.sign.ecdsa.EcdsaP256Sha256;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const keys = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const a = init.arena.allocator();
    const secrets = try a.alloc(u8, 32 * keys);
    fill(&st, secrets);
    const digests = try a.alloc(u8, 32 * keys);
    fill(&st, digests);
    const pubs = try a.alloc([65]u8, keys);
    const sigs = try a.alloc([Scheme.Signature.der_encoded_length_max]u8, keys);
    const lens = try a.alloc(usize, keys);
    for (0..keys) |i| {
        secrets[32 * i] &= 0x7f;
        const kp = try Scheme.KeyPair.fromSecretKey(try Scheme.SecretKey.fromBytes(secrets[32 * i ..][0..32].*));
        pubs[i] = kp.public_key.toUncompressedSec1();
        const sig = try kp.signPrehashed(digests[32 * i ..][0..32].*, null);
        lens[i] = sig.toDer(&sigs[i]).len;
    }
    var sum: u64 = 0;
    for (0..reps) |r| {
        for (0..keys) |i| {
            const flip: u8 = @truncate(r & 1);
            digests[32 * i] ^= flip;
            const pk = try Scheme.PublicKey.fromSec1(&pubs[i]);
            const sig = try Scheme.Signature.fromDer(sigs[i][0..lens[i]]);
            if (sig.verifyPrehashed(digests[32 * i ..][0..32].*, pk)) |_| {
                sum += 1;
            } else |_| {}
            digests[32 * i] ^= flip;
        }
    }
    try print(init, sum);
}
