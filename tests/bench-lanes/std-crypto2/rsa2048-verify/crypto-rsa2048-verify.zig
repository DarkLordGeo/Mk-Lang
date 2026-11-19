// crypto-rsa2048-verify, Zig: std.crypto.Certificate.rsa (the RSA
// verification Zig's std ships for certificates) over the input of
// mk/crypto-rsa2048-verify.mk's comment: the same key and signatures,
// PKC840f5dac53106dd1f9c57219224cf51289290c42f20466875ba8e830ac5690e541536fcc8ab03b731f82bf66d83f194e7e180b3963ec7a2f3f7904a7ce49aed47da4d4b79421eaf937d301b3e696169297b797c32c076a12be4de0b58e003c5123051a84a10c62f8dac2f42a8640008eb3c7cccd6760ff5b51b689763922582845f048fb8150e5a7a6ca2eccc7bdc85349ad5b26c52137a79fa3fe5c29ab5cd7615013219c1941b6708e9c3c23feff5febaf0c8ebca5750b54e3e6e99a3e876b396f27860b7f3ec4e9191703c6332d944f6f69751167680c79c4f6b57f1cc8755d24b6ec158ccdbacdb23107a33cb6b332516c13274d1f9dccc21dced869e486v1_5Signature.verify and PSSSignature.verify with SHA-256. Zig's
// verify takes the message and hashes it, so a pass hands it the empty
// message, or on odd passes the one byte 0x01, where the other columns
// flip a digest byte; one SHA-256 compression per check, under 1 percent
// of an RSA-2048 verification. <keys> and <seed> are unused.
const std = @import("std");
const rsa = std.crypto.Certificate.rsa;
const Sha256 = std.crypto.hash.sha2.Sha256;

const modulus_hex = "a2b451a07d0aa5f96e455671513550514a8a5b462ebef717094fa1fee82224e637f9746d3f7cafd31878d80325b6ef5a1700f65903b469429e89d6eac8845097b5ab393189db92512ed8a7711a1253facd20f79c15e8247f3d3e42e46e48c98e254a2fe9765313a03eff8f17e1a029397a1fa26a8dce26f490ed81299615d9814c22da610428e09c7d9658594266f5c021d0fceca08d945a12be82de4d1ece6b4c03145b5d3495d4ed5411eb878daf05fd7afc3e09ada0f1126422f590975a1969816f48698bcbba1b4d9cae79d460d8f9f85e7975005d9bc22c4e5ac0f7c1a45d12569a62807d3b9a02e5a530e773066f453d1f5b4c2e9cf7820283f742b9d5";
const sig_pkcs1_hex = "840f5dac53106dd1f9c57219224cf51289290c42f20466875ba8e830ac5690e541536fcc8ab03b731f82bf66d83f194e7e180b3963ec7a2f3f7904a7ce49aed47da4d4b79421eaf937d301b3e696169297b797c32c076a12be4de0b58e003c5123051a84a10c62f8dac2f42a8640008eb3c7cccd6760ff5b51b689763922582845f048fb8150e5a7a6ca2eccc7bdc85349ad5b26c52137a79fa3fe5c29ab5cd7615013219c1941b6708e9c3c23feff5febaf0c8ebca5750b54e3e6e99a3e876b396f27860b7f3ec4e9191703c6332d944f6f69751167680c79c4f6b57f1cc8755d24b6ec158ccdbacdb23107a33cb6b332516c13274d1f9dccc21dced869e486";
const sig_pss_hex = "4f01e0c12b08625ecac89a69231906edf826380f37c959a96690d046316d68ffce9d5c471694fcebfc6b45534864689256e4fc81c78e583f675d0c94b449647451e81beff01a11a516d5e5ce3f1a910437cb8a3a5096b19fb15f4524a35b23d89cdba12cf5b71aac1047b28c562df7c5542c34ce23a182cf7e0e231934b17294799d44877a1d68ef1b8f073619b7618e6b7c22db20030d98cf591ffc3d4da5f58613ecd5ecfc3b40a1d02f40891ca43695cd4c088b05a8054c89c595a47e274816f35384226f74459ee63e25a1bfc03c360490552ec38343f8ace502f065303b00bc0ec320711b211fde92e57feb9013c3609342495ec0d7cabdec21e54acc38";

fn print(init: std.process.Init, sum: u64) !void {
    var wbuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &wbuf);
    try w.interface.print("{d}\n", .{sum});
    try w.interface.flush();
}
pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var n: [256]u8 = undefined;
    _ = try std.fmt.hexToBytes(&n, modulus_hex);
    var s1: [256]u8 = undefined;
    _ = try std.fmt.hexToBytes(&s1, sig_pkcs1_hex);
    var s2: [256]u8 = undefined;
    _ = try std.fmt.hexToBytes(&s2, sig_pss_hex);
    const e = [_]u8{ 0x01, 0x00, 0x01 };
    const key = try rsa.PublicKey.fromBytes(&e, &n);
    const odd = [_]u8{0x01};
    var sum: u64 = 0;
    for (0..reps) |r| {
        const msg: []const u8 = if (r & 1 == 1) &odd else &.{};
        if (rsa.PKCS1v1_5Signature.verify(256, &s1, msg, key, Sha256)) |_| {
            sum += 1;
        } else |_| {}
        if (rsa.PSSSignature.verify(256, &s2, msg, key, Sha256)) |_| {
            sum += 1;
        } else |_| {}
    }
    try print(init, sum);
}
