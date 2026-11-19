// Why: a password hash that is subtly wrong still looks random, so only
// fixed outside values can prove it.
// What: blocks checking BLAKE2b and Argon2id against RFC and reference vectors.
// How: compares hex digests with values copied from the oracles below, and
// checks that the secret states are wiped.

// Test blocks for Argon2id and its BLAKE2b (argon2.mk, blake2b.mk).
// The oracles, each fetched or run on 2026-11-15, the values below copied
// from the generators' output, never typed:
// - RFC 7693 Appendix A's BLAKE2b-512("abc") (rfc7693.txt, sha256
//   c943754888364fe29bbd0bb3c71c6658ef392371497eaa4a9d9dde015b0721e5), and
//   Python 3.13's hashlib.blake2b for other lengths;
// - RFC 9106 section 5.3's Argon2id values: H_0, eight words of the memory
//   after each pass, and the tag (rfc9106.txt, sha256
//   855c06f060379e34285e83a217e9069b5c72e161a1e54df9af5cd88dbb231f31);
// - the reference implementation's Argon2id cases, phc-winner-argon2's
//   src/test.c (sha256
//   ec518844e823fb6a1bd38f79f2b5d1bbddf23f490c0a343c450aa8e3f5af5988);
// - golang.org/x/crypto v0.49.0's argon2.IDKey for what neither reaches: a
//   tag of 4, 64, 65, 100 and 1024 bytes, m not a multiple of 4p, segments
//   past 128 blocks with more than one lane, an empty password or salt, a
//   password longer than a BLAKE2b block, and the least memory.

import std.encoding

// TxA2Hex is b in lower-case hex, in a.
TxA2Hex(a: &Arena, b: []u8): str from a {
    return encoding.HexEncode(a, b).Must() // a test arena; OOM is a test failure
}

// TxB2 is the hex of BLAKE2b of data with an n-byte output.
TxB2(a: &Arena, data: []u8, n: int): str from a {
    var d: [64]u8 = [0; 64]
    B2Hash(mut d[0..n], data)
    return TxA2Hex(a, d[0..n])
}

// TxA2 is the hex of the n-byte Argon2id tag of pw and salt under c, with
// no secret and no data, or the name of the error.
TxA2(h: &Heap, a: &Arena, c: Argon2Cost, pw: []u8, salt: []u8, n: int): str from a {
    out := a.Slice[u8](n).Must() // a test arena
    Argon2id(h, c, pw, salt, "".AsBytes(), "".AsBytes(), mut out[..]) else e {
        return match e {
            BadLength { "BadLength" }
            BadCost { "BadCost" }
            OutOfMemory { "OutOfMemory" }
        }
    }
    return TxA2Hex(a, out[..])
}

// TxA2Ref is TxA2 for the reference implementation's cases: 2^logm KiB,
// one 32-byte tag.
TxA2Ref(sys: &System, t: int, logm: int, p: int, pw: str, salt: str): str from sys.arena {
    return TxA2(sys.heap, sys.arena, Argon2Cost(memory: 1 << logm, passes: t, lanes: p), pw.AsBytes(), salt.AsBytes(), 32)
}

// TxA2Why is the error Argon2id fails with for c and an n-byte tag, 0 <=
// n <= 64, "" when it succeeds, and "changed" when it fails having
// written to out.
TxA2Why(h: &Heap, c: Argon2Cost, n: int): str {
    var out: [64]u8 = [7; 64]
    Argon2id(h, c, "password".AsBytes(), "somesalt".AsBytes(), "".AsBytes(), "".AsBytes(), mut out[0..n]) else e {
        for i in 0..64 {
            if out[i] != 7 {
                return "changed"
            }
        }
        return match e {
            BadLength { "BadLength" }
            BadCost { "BadCost" }
            OutOfMemory { "OutOfMemory" }
        }
    }
    return ""
}

// TxA2Verify is Argon2idVerify of pw and salt under c with the tag in
// hex, or false on any error.
TxA2Verify(sys: &System, c: Argon2Cost, pw: str, salt: str, tag: str): bool {
    t := encoding.HexDecode(sys.arena, tag).Must() // the tests' hex is valid
    return Argon2idVerify(sys.heap, c, pw.AsBytes(), salt.AsBytes(), "".AsBytes(), "".AsBytes(), t) else {
        false
    }
}

test "BLAKE2b matches RFC 7693's BLAKE2b-512 of abc and hashlib's digests of other lengths, across block boundaries" (sys: &System) {
    a := sys.arena
    Assert(TxB2(a, "abc".AsBytes(), 64) == "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923")
    Assert(TxB2(a, "".AsBytes(), 64) == "786a02f742015903c6c6fd852552d272912f4740e15847618a86e217f71f5419d25e1031afee585313896444934eb04b903a685b1448b755d56f701afe9be2ce")
    Assert(TxB2(a, "abc".AsBytes(), 1) == "6b")
    Assert(TxB2(a, "abc".AsBytes(), 33) == "f7bb660ec10c1b537a53ff432791f8a34c09e9ecfca84288bba1ee39afec290d63")
    var b: [300]u8 = [0; 300]
    for i in 0..129 {
        b[i] = Trunc[u8](i)
    }
    // 128 bytes are one block, the last; the 129th starts a second.
    Assert(TxB2(a, b[0..128], 64) == "2319e3789c47e2daa5fe807f61bec2a1a6537fa03f19ff32e87eecbfd64b7e0e8ccff439ac333b040f19b0c4ddd11a61e24ac1fe0f10a039806c5dcc0da3d115")
    Assert(TxB2(a, b[0..129], 32) == "f7f3c46ba2564ff4c4c162da1f5b605f9f1c4aa6a20652a9f9a337c1a2f5b9c9")
    for i in 0..300 {
        b[i] = Trunc[u8](i * 7)
    }
    Assert(TxB2(a, b[..], 48) == "2d3d8e1cf78046219e26de4c7e08575192c2613152d12f7b0bec1576b5a074740948b5aaeea17367bd5932c9e5114540")
    // Absorbed in pieces, the digest is the same.
    var s := B2New(48)
    s.Bytes(b[0..1])
    s.Bytes(b[1..128])
    s.Bytes(b[128..256])
    s.Bytes(b[256..300])
    var d: [48]u8 = [0; 48]
    s.Sum(mut d[..])
    Assert(TxA2Hex(a, d[..]) == "2d3d8e1cf78046219e26de4c7e08575192c2613152d12f7b0bec1576b5a074740948b5aaeea17367bd5932c9e5114540")
}

// TxB2Zero reports whether every byte of s's chaining value and buffer is
// zero.
TxB2Zero(s: &B2b): bool {
    var acc: u8 = 0
    for x in s.h {
        acc = acc | x
    }
    for x in s.buf {
        acc = acc | x
    }
    return acc == 0
}

test "BLAKE2b's Sum wipes its state: the chaining value (H_0 in Argon2id) and the buffered input (its password and salt)" (sys: &System) {
    var b: [300]u8 = [0xA5; 300]
    // No input, part of a block, exactly one, one past it, and several.
    for n in [0, 3, 128, 129, 300] {
        for outlen in [32, 64] {
            var s := B2New(outlen)
            s.Bytes(b[0..n])
            var d: [64]u8 = [0; 64]
            s.Sum(mut d[0..outlen])
            Assert(TxB2Zero(&s))
        }
    }
}

// TxZero reports whether every byte of b is zero.
TxZero(b: []u8): bool {
    var acc: u8 = 0
    for x in b {
        acc = acc | x
    }
    return acc == 0
}

test "BLAKE2b's compression wipes its working vector, which follows the chaining value and the message words, after every block" (sys: &System) {
    var b: [300]u8 = [0xA5; 300]
    // One block compressed by Bytes (129 bytes), two (257), and the final
    // one by Sum.
    for n in [129, 257, 300] {
        var s := B2New(64)
        s.Bytes(b[0..n])
        Assert(TxZero(s.v[..]))
        var d: [64]u8 = [0; 64]
        s.Sum(mut d[..])
        Assert(TxZero(s.v[..]))
    }
}

test "Argon2id's H' wipes its chain: V_i and V_(i + 1), each 64 bytes of H_0's or the last blocks' hash" (sys: &System) {
    var input: [72]u8 = [0x5A; 72]
    // T of 65 (r = 1: V_1 only), 100 (r = 2: one step of the chain) and
    // 1024 (r = 30, as Argon2id's first blocks).
    for t in [65, 100, 1024] {
        var out: [1024]u8 = [0; 1024]
        var sc := A2Chain(v: [0; 64], w: [0; 64])
        HPrime(mut out[0..t], input[..], mut sc)
        Assert(TxZero(sc.v[..]))
        Assert(TxZero(sc.w[..]))
        Assert(!TxZero(out[0..t]))
    }
}

// TxA2Words reports whether the memory's block 0 words 0 to 3 and block
// 31 words 124 to 127 are w, in that order (RFC 9106 section 5.3's dump).
TxA2Words(mem: []u8, w: [8]u64): bool {
    var ok := true
    for k in 0..4 {
        ok = ok && Le64(mem, 8 * k) == w[k]
        ok = ok && Le64(mem, 31 * 1024 + 8 * (124 + k)) == w[4 + k]
    }
    return ok
}

test "Argon2id's H_0, the memory after each pass and the tag match RFC 9106 section 5.3" (sys: &System) {
    c := Argon2Cost(memory: 32, passes: 3, lanes: 4)
    var pw: [32]u8 = [1; 32]
    var salt: [16]u8 = [2; 16]
    var key: [8]u8 = [3; 8]
    var ad: [12]u8 = [4; 12]
    var h0: [72]u8 = [0; 72]
    A2H0(mut h0, c, pw[..], salt[..], key[..], ad[..], 32)
    Assert(TxA2Hex(sys.arena, h0[0..64]) == "2889de487eb42ae500c0007ed9252f1069eadec40d5765b485de6dc2437a67b8546a2f0acc1a0882db8fcf74714b472e94df421a5da1112ffa11434370a1e997")
    Assert(A2Blocks(c) == 32)
    mem := sys.arena.Slice[u8](1024 * A2Blocks(c)).Must() // a test arena
    A2Start(mut mem[..], c, mut h0)
    A2Pass(mut mem[..], c, 0)
    Assert(TxA2Words(mem[..], [0x6b2e09f10671bd43, 0xf69f5c27918a21be, 0xdea7810ea41290e1, 0x6787f7171870f893, 0x377fa81666dc7f2b, 0x50e586398a9c39c8, 0x6f732732a550924a, 0x81f88b28683ea8e5]))
    A2Pass(mut mem[..], c, 1)
    Assert(TxA2Words(mem[..], [0x3653ec9d01583df9, 0x69ef53a72d1e1fd3, 0x35635631744ab54f, 0x599512e96a37ab6e, 0x4d4b435cea35caa6, 0xc582210d99ad1359, 0xd087971b36fd6d77, 0xa55222a93754c692]))
    A2Pass(mut mem[..], c, 2)
    Assert(TxA2Words(mem[..], [0x942363968ce597a4, 0xa22448c0bdad5760, 0xa5f80662b6fa8748, 0xa0f9b9ce392f719f, 0xd723359b485f509b, 0xcb78824f42375111, 0x35bc8cc6e83b1875, 0x0b012846a40f346a]))
    var tag: [32]u8 = [0; 32]
    A2Tag(mem[..], c, mut tag[..])
    Assert(TxA2Hex(sys.arena, tag[..]) == "0d640df58d78766c08c037a34a8b53c9d01ef0452d75b65eb52520e96b01e659")
}

test "Argon2id and Argon2idVerify give RFC 9106's tag with a secret and associated data, and Verify refuses any other tag" (sys: &System) {
    c := Argon2Cost(memory: 32, passes: 3, lanes: 4)
    var pw: [32]u8 = [1; 32]
    var salt: [16]u8 = [2; 16]
    var key: [8]u8 = [3; 8]
    var ad: [12]u8 = [4; 12]
    var tag: [32]u8 = [0; 32]
    Argon2id(sys.heap, c, pw[..], salt[..], key[..], ad[..], mut tag[..]).Must() // the RFC's parameters are in range
    Assert(TxA2Hex(sys.arena, tag[..]) == "0d640df58d78766c08c037a34a8b53c9d01ef0452d75b65eb52520e96b01e659")
    Assert(Argon2idVerify(sys.heap, c, pw[..], salt[..], key[..], ad[..], tag[..]).Must())
    // A changed bit, a shorter tag, another secret, other data: false.
    tag[31] = tag[31] ^ 1
    Assert(!Argon2idVerify(sys.heap, c, pw[..], salt[..], key[..], ad[..], tag[..]).Must())
    tag[31] = tag[31] ^ 1
    Assert(!Argon2idVerify(sys.heap, c, pw[..], salt[..], key[..], ad[..], tag[0..31]).Must())
    Assert(!Argon2idVerify(sys.heap, c, pw[..], salt[..], key[0..7], ad[..], tag[..]).Must())
    Assert(!Argon2idVerify(sys.heap, c, pw[..], salt[..], key[..], "".AsBytes(), tag[..]).Must())
    Assert(Argon2idVerify(sys.heap, c, pw[..], salt[..], key[..], ad[..], tag[..]).Must())
}

test "Argon2id matches the reference implementation's 256 KiB cases" (sys: &System) {
    Assert(TxA2Ref(sys, 2, 8, 1, "password", "somesalt") == "9dfeb910e80bad0311fee20f9c0e2b12c17987b4cac90c2ef54d5b3021c68bfe")
    Assert(TxA2Ref(sys, 2, 8, 2, "password", "somesalt") == "6d093c501fd5999645e0ea3bf620d7b8be7fd2db59c20d9fff9539da2bf57037")
    c := Argon2Cost(memory: 256, passes: 2, lanes: 2)
    Assert(TxA2Verify(sys, c, "password", "somesalt", "6d093c501fd5999645e0ea3bf620d7b8be7fd2db59c20d9fff9539da2bf57037"))
    Assert(!TxA2Verify(sys, c, "passwore", "somesalt", "6d093c501fd5999645e0ea3bf620d7b8be7fd2db59c20d9fff9539da2bf57037"))
}

test "Argon2id matches the reference implementation's 64 MiB cases" (sys: &System) {
    Assert(TxA2Ref(sys, 2, 16, 1, "password", "somesalt") == "09316115d5cf24ed5a15a31a3ba326e5cf32edc24702987c02b6566f61913cf7")
    Assert(TxA2Ref(sys, 1, 16, 1, "password", "somesalt") == "f6a5adc1ba723dddef9b5ac1d464e180fcd9dffc9d1cbf76cca2fed795d9ca98")
    Assert(TxA2Ref(sys, 4, 16, 1, "password", "somesalt") == "9025d48e68ef7395cca9079da4c4ec3affb3c8911fe4f86d1a2520856f63172c")
    Assert(TxA2Ref(sys, 2, 16, 1, "differentpassword", "somesalt") == "0b84d652cf6b0c4beaef0dfe278ba6a80df6696281d7e0d2891b817d8c458fde")
    Assert(TxA2Ref(sys, 2, 16, 1, "password", "diffsalt") == "bdf32b05ccc42eb15d58fd19b1f856b113da1e9a5874fdcc544308565aa8141c")
}

test "Argon2id matches the reference implementation's 256 MiB case" (sys: &System) {
    Assert(TxA2Ref(sys, 2, 18, 1, "password", "somesalt") == "78fe1ec91fb3aa5657d72e710854e4c3d9b9198c742f9616c2f085bed95b2e8c")
}

test "Argon2id matches x/crypto's argon2.IDKey on tag lengths, memory sizes and inputs the RFC's vectors do not reach" (sys: &System) {
    h := sys.heap
    a := sys.arena
    pw := "password".AsBytes()
    salt := "somesalt".AsBytes()
    Assert(TxA2(h, a, Argon2Cost(memory: 64, passes: 1, lanes: 1), pw, salt, 4) == "3dbf4e40")
    Assert(TxA2(h, a, Argon2Cost(memory: 64, passes: 2, lanes: 1), pw, salt, 64) == "3af1343719195f0aec85c38a855a9b7737bd6037bef7af654a0457ccecec1ed53f5f7a043e2a705432c959819f6ac62d88b570d9f767603c9c0aa39ce339ec1f")
    Assert(TxA2(h, a, Argon2Cost(memory: 64, passes: 2, lanes: 1), pw, salt, 65) == "59128b0a371abe556663c18010f357609184488d5d37c45b9f6e107e326c46b1c8180dc57002e6eb56b576cb6defae622457b45efcafb78b56b2d814a3baecb80d")
    Assert(TxA2(h, a, Argon2Cost(memory: 64, passes: 2, lanes: 2), pw, salt, 100) == "9e1aabbad29c004e3b0d760b8b6cfeb6ea4f061bf94781c4dcd2b7a981b129e4af08296207efb3c70b9c3f31fada12f649d690aa4f232e94cb1c718f1c99b076d11262baeca847b91bfb385e06cd339955de1a11dd1a62016d9996a12eba40a1649f1fcc")
    Assert(TxA2(h, a, Argon2Cost(memory: 32, passes: 1, lanes: 1), pw, salt, 1024) == "0e25de350255a67ee24ab36f3a30a716f8c5250d7deca02c8bae2f1b35ac85456d09a6205be083948c5c30c0c0aceb17ad23d479a92f78f94a3377fc0c9a9ba877e44048cf8db7eda78a0d020efe55e4df025234a0cfa13808e1234a2d7ebf480d049e7ee2d25a46abc08eb53358d5f03e8d7278c1666116e6d3ef2f680194716d9f271927a9677a950615f7175e32601be91f91f97a651f8254b58f9dfefe9a808f82273746b6ae70b1d4a87bebf4c19f337123010c0bd90a5127c6a119800ac306de375f0e5df0cabf46a20ad752e4ea72226f405e8b5798ce62e20b98ff23e02513d19d60de42ae52b6a16b891b9a4abfa828464cf256f58d11a28abac2d6ee1db7e2d4a08cd07835c399f1a2456c31919846e252d6088c62baf7f77a986b809436c7c55dc921983de86a6f6492ffaeaaad1503196fe66963b3ab3655492dcd07df09b576939e574b12aaea22fbc15d84bedf967969cef616c9f153358b7669be9467caa367ef92de855104b1cac27b3725b3b1b1e72c0cd73fb8ebbba86ab131830f5a77d8d61af3b5894061ed2aae76b65475127a9dd4724fee1f4058705704dc1126d18ac44b7ae81d68b92a357e4955a5bb30f9c485bd60df9162331bf2bf0f2c21b8a370b76be2d53adb052ae818e05965bb7f3e3d11002cb00327249873e85e99f8d3fc2b1ec7b5e5d158897437074c37b3125b2d0676b7c90bc2ee1c959bd69b50f5336094e1378f52097661738b39ac28f15b871ac9164bdb2f0a24abec5a26d823f3c2d1a839927041dc0fbfe70e69414b5ea29c822cc22f17dbedc83ec0852dbe7e1974d91e099aded264828dfbeac665465e0e9b11c45e409d16d2a266448feed91e24f11e2c118ad71fd08f7162840a9792b3158032f48ac14918703e94c8587872d876147c24b7381b61fcb39054e5fd445305b770c337c5b799c727d59ce0119e33661306ddb73387380e196e28f243c23c1d61efa060732c1bedb2b68b024bcea219e9aa22ef9a65184fdaf9d3d8ebdd142793c00dabf2f12db2b2261cb99a201e5873e8eb3b9f957c70d769f3204b60ec76bedbe147ce28d1350e336017afe3d36cc0d3e994d095beaa19973622f633f346c6e77bb34193e1ac23feb57f79b3d64840556b7244082a0b490305a22e0d85e8de5012e905aa8fd2ca4e3639826ba009240841d5bea97829b4f040c5012cfac72599f064decc61f9711f26e47105028e7ed3064080e3be6b3853e6b4328b1702592b78e0b2449b1c837fa646232a4b74559db1585c2fa15b119c6e317125a338f12aee9b371e5287137ee149f78db2e3d5800b3359f2a421dc1a8f0376b3da997de5533869bad4f89e18a6ae577e730629337cf38783395faaec3299cd8b60ca9f96fd06a30a691d55a137548ea90f3d4346f411da0db0d4a2392fb66c097f95c32abf15fc")
    // m' is 36 of 37 blocks: m rounds down to a multiple of 4p.
    Assert(TxA2(h, a, Argon2Cost(memory: 37, passes: 2, lanes: 3), pw, salt, 32) == "fd3d6c0350c90b38be1da55d3387c3da995b683542cf1de6af4cb06f0cbd1188")
    // Segments of 133 and 256 blocks make more than one address block.
    Assert(TxA2(h, a, Argon2Cost(memory: 1600, passes: 3, lanes: 3), pw, salt, 32) == "204ff6a5a9063ddbc5daefc37472cfa7feaf18ade902dedbe7992655a32be4c6")
    Assert(TxA2(h, a, Argon2Cost(memory: 1024, passes: 1, lanes: 1), pw, salt, 32) == "c8e9aedc956f6a7dff0a4d42940df628623f328ea1235005abac933c57093e23")
    Assert(TxA2(h, a, Argon2Cost(memory: 64, passes: 2, lanes: 2), "".AsBytes(), "saltsalt".AsBytes(), 32) == "d57ddd4c03f56671240add35bf603d2ff52c0950939caa0dc5029a29c5d22701")
    Assert(TxA2(h, a, Argon2Cost(memory: 16, passes: 1, lanes: 2), pw, "".AsBytes(), 32) == "9341020c45f32a7d47b7f7ca515f794f7973bed350586ba1d314123861e5ff3c")
    var long: [300]u8 = [0; 300]
    for i in 0..300 {
        long[i] = Trunc[u8](i * 7)
    }
    Assert(TxA2(h, a, Argon2Cost(memory: 32, passes: 1, lanes: 4), long[..], salt, 32) == "677c900ac32f3ed533107531385df19e1c5c853f2e708604abbc1753e72824f9")
    // The least memory RFC 9106 allows, 8 KiB a lane.
    Assert(TxA2(h, a, Argon2Cost(memory: 8, passes: 1, lanes: 1), pw, salt, 32) == "f137f8e186a403a679ccd0606e5ab5dcdafe43c1640855ac8c6e33e9bd63eeb3")
}

test "Argon2id refuses a cost or a tag length outside RFC 9106's limits, writing nothing" (sys: &System) {
    h := sys.heap
    Assert(TxA2Why(h, Argon2Cost(memory: 8, passes: 1, lanes: 1), 4) == "")
    Assert(TxA2Why(h, Argon2Cost(memory: 8, passes: 1, lanes: 1), 3) == "BadLength")
    Assert(TxA2Why(h, Argon2Cost(memory: 8, passes: 1, lanes: 1), 0) == "BadLength")
    Assert(TxA2Why(h, Argon2Cost(memory: 64, passes: 1, lanes: 0), 32) == "BadCost")
    Assert(TxA2Why(h, Argon2Cost(memory: 64, passes: 1, lanes: -1), 32) == "BadCost")
    Assert(TxA2Why(h, Argon2Cost(memory: 8 * 0x1000000, passes: 1, lanes: 0x1000000), 32) == "BadCost")
    Assert(TxA2Why(h, Argon2Cost(memory: 64, passes: 0, lanes: 1), 32) == "BadCost")
    Assert(TxA2Why(h, Argon2Cost(memory: 64, passes: 0x100000000, lanes: 1), 32) == "BadCost")
    Assert(TxA2Why(h, Argon2Cost(memory: 7, passes: 1, lanes: 1), 32) == "BadCost")
    Assert(TxA2Why(h, Argon2Cost(memory: 15, passes: 1, lanes: 2), 32) == "BadCost")
    Assert(TxA2Why(h, Argon2Cost(memory: 0x100000000, passes: 1, lanes: 1), 32) == "BadCost")
    // A bad cost is reported before a bad length.
    Assert(TxA2Why(h, Argon2Cost(memory: 7, passes: 1, lanes: 1), 3) == "BadCost")
    var tag: [3]u8 = [0; 3]
    c := Argon2Cost(memory: 8, passes: 1, lanes: 1)
    _ = Argon2idVerify(h, c, "password".AsBytes(), "somesalt".AsBytes(), "".AsBytes(), "".AsBytes(), tag[..]) else e {
        match e {
            BadLength {}
            BadCost { Assert(false) }
            OutOfMemory { Assert(false) }
        }
        return
    }
    Assert(false)
}
