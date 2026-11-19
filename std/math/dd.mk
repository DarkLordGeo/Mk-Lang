// Why: Sinh, Cosh, Tanh, Asinh, Acosh, Atanh and Atan2 cannot stay below
// 1 ulp with one f64 evaluation (msun's forms measured 1.03 to 1.44 ulps in
// TestSTDMATH_TrueError), so they need extra precision.
//
// What: the double-double kernels TwoSum, FastTwoSum and TwoProd, and the
// ExpDD, LogDD and AtanDD evaluations built on them, over the tables ExpTable
// (512 bytes) and LogTable (1,128 bytes).
//
// How: a value is hi + lo with |lo| <= ulp(hi) / 2, using Knuth's TwoSum
// and Dekker's TwoProd (Veltkamp's split), which are exact in IEEE f64
// arithmetic without a fused multiply-add. The compiler never contracts
// a * b + c, so the bits are the same on every target and in the evaluator.
// Each kernel's relative error is about 2^-62 or less, so rounding hi + lo
// once lands within 0.5 ulp plus about 2^-9 ulp. All O(1), no allocation,
// no panic.

// TwoSum is a + b as hi + lo exactly (Knuth), for any a and b whose sum
// does not overflow.
TwoSum(a: f64, b: f64): (hi: f64, lo: f64) {
    s := a + b
    bb := s - a
    return (hi: s, lo: (a - (s - bb)) + (b - bb))
}

// FastTwoSum is a + b as hi + lo exactly, for |a| >= |b| or a = 0
// (Dekker).
FastTwoSum(a: f64, b: f64): (hi: f64, lo: f64) {
    s := a + b
    return (hi: s, lo: b - (s - a))
}

// TwoProd is a * b as hi + lo exactly (Dekker, with Veltkamp's split into
// 26-bit halves), for |a| and |b| below 2^995 whose product neither
// overflows nor falls below 2^-969.
TwoProd(a: f64, b: f64): (hi: f64, lo: f64) {
    p := a * b
    ca := 134217729.0 * a
    ah := ca - (ca - a)
    al := a - ah
    cb := 134217729.0 * b
    bh := cb - (cb - b)
    bl := b - bh
    return (hi: p, lo: ((ah * bh - p) + ah * bl + al * bh) + al * bl)
}

// ExpDD's reduction: x = n ln2/32 + r. ExpL1 and ExpL2 have 37 bits, so n *
// ExpL1 and n * ExpL2 are exact for |n| < 2^16; the three together carry
// ln2/32 to 2^-180. ExpInvL is 32/ln2.
const ExpL1: f64 = f64.FromBits(0x3F962E42FEFA0000)   // 0.021660849392446835
const ExpL2: f64 = f64.FromBits(0x3D2CF79ABC9E0000)   // 5.1456092446457696e-14
const ExpL3: f64 = f64.FromBits(0x3ABD9CC01F97B57A)   // 9.568252300058288e-26
const ExpInvL: f64 = f64.FromBits(0x40471547652B82FE) // 46.16624130844683

// e^r - 1 - r - r^2/2 = r^3 (1/3! + r/4! + ... + r^4/7!) for |r| <= ln2/64.
const ExpC3: f64 = f64.FromBits(0x3FC5555555555555) // 1/3!
const ExpC4: f64 = f64.FromBits(0x3FA5555555555555) // 1/4!
const ExpC5: f64 = f64.FromBits(0x3F81111111111111) // 1/5!
const ExpC6: f64 = f64.FromBits(0x3F56C16C16C16C17) // 1/6!
const ExpC7: f64 = f64.FromBits(0x3F2A01A01A01A01A) // 1/7!

// 2^(j/32) for j in 0..32, as hi, lo bit pairs (math/big at 600 bits).
const ExpTable: [64]u64 = [
    0x3FF0000000000000, 0x0000000000000000,
    0x3FF059B0D3158574, 0x3C8D73E2A475B465,
    0x3FF0B5586CF9890F, 0x3C98A62E4ADC610B,
    0x3FF11301D0125B51, 0xBC96C51039449B3A,
    0x3FF172B83C7D517B, 0xBC819041B9D78A76,
    0x3FF1D4873168B9AA, 0x3C9E016E00A2643C,
    0x3FF2387A6E756238, 0x3C99B07EB6C70573,
    0x3FF29E9DF51FDEE1, 0x3C8612E8AFAD1255,
    0x3FF306FE0A31B715, 0x3C86F46AD23182E4,
    0x3FF371A7373AA9CB, 0xBC963AEABF42EAE2,
    0x3FF3DEA64C123422, 0x3C8ADA0911F09EBC,
    0x3FF44E086061892D, 0x3C489B7A04EF80D0,
    0x3FF4BFDAD5362A27, 0x3C7D4397AFEC42E2,
    0x3FF5342B569D4F82, 0xBC807ABE1DB13CAD,
    0x3FF5AB07DD485429, 0x3C96324C054647AD,
    0x3FF6247EB03A5585, 0xBC9383C17E40B497,
    0x3FF6A09E667F3BCD, 0xBC9BDD3413B26456,
    0x3FF71F75E8EC5F74, 0xBC816E4786887A99,
    0x3FF7A11473EB0187, 0xBC841577EE04992F,
    0x3FF82589994CCE13, 0xBC9D4C1DD41532D8,
    0x3FF8ACE5422AA0DB, 0x3C96E9F156864B27,
    0x3FF93737B0CDC5E5, 0xBC675FC781B57EBC,
    0x3FF9C49182A3F090, 0x3C7C7C46B071F2BE,
    0x3FFA5503B23E255D, 0xBC9D2F6EDB8D41E1,
    0x3FFAE89F995AD3AD, 0x3C97A1CD345DCC81,
    0x3FFB7F76F2FB5E47, 0xBC75584F7E54AC3B,
    0x3FFC199BDD85529C, 0x3C811065895048DD,
    0x3FFCB720DCEF9069, 0x3C7503CBD1E949DB,
    0x3FFD5818DCFBA487, 0x3C82ED02D75B3707,
    0x3FFDFC97337B9B5F, 0xBC91A5CD4F184B5C,
    0x3FFEA4AFA2A490DA, 0xBC9E9C23179C2893,
    0x3FFF50765B6E4540, 0x3C99D3E12DD8A18B,
]

// ExpDD is e^x as (hi + lo) * 2^k for |x| <= 1400, hi + lo in [0.98,
// 2.03], with a relative error below 2^-64: x = (32 k + j) ln2/32 + r,
// |r| <= ln2/64, e^r by its Taylor series to r^7 (truncation 2^-67) with
// its first two terms exact, times 2^(j/32) from ExpTable.
ExpDD(x: f64): (hi: f64, lo: f64, k: int) {
    // n = round(x 32/ln2), ties to even, in the low bits of nd0.
    nd0 := x * ExpInvL + ExpShift
    n := Trunc[int](nd0.Bits() -% ExpShift.Bits())
    j := n & 31
    nf := nd0 - ExpShift
    // x - n * ExpL1 is exact (Sterbenz), and so is each product.
    rs := TwoSum(x - nf * ExpL1, -(nf * ExpL2))
    rh := rs.hi
    rl := rs.lo - nf * ExpL3
    r2 := rh * rh
    q := r2 * ((0.5 + rh * ExpC3) + r2 * (ExpC4 + rh * ExpC5) + r2 * r2 * (ExpC6 + rh * ExpC7))
    e := FastTwoSum(rh, q)
    u := FastTwoSum(1.0, e.hi)
    ul := u.lo + (e.lo + (rl + rl * rh))
    th := f64.FromBits(ExpTable[2 * j])
    tl := f64.FromBits(ExpTable[2 * j + 1])
    p := TwoProd(th, u.hi)
    v := FastTwoSum(p.hi, p.lo + (th * ul + tl * u.hi))
    return (hi: v.hi, lo: v.lo, k: (n - j) / 32)
}

// log1p(r) - r + r^2/2 = r^3 (1/3 - r/4 + ... + r^8/11) for |r| <= 0.0112
// (truncation 2^-81).
const LogC3: f64 = f64.FromBits(0x3FD5555555555555)  // 1/3
const LogC4: f64 = f64.FromBits(0xBFD0000000000000)  // -1/4
const LogC5: f64 = f64.FromBits(0x3FC999999999999A)  // 1/5
const LogC6: f64 = f64.FromBits(0xBFC5555555555555)  // -1/6
const LogC7: f64 = f64.FromBits(0x3FC2492492492492)  // 1/7
const LogC8: f64 = f64.FromBits(0xBFC0000000000000)  // -1/8
const LogC9: f64 = f64.FromBits(0x3FBC71C71C71C71C)  // 1/9
const LogC10: f64 = f64.FromBits(0xBFB999999999999A) // -1/10
const LogC11: f64 = f64.FromBits(0x3FB745D1745D1746) // 1/11

// For i in 45..91: c, the f64 nearest 64 / i, then -ln(c) as hi, lo
// (math/big at 600 bits). Row 64 is c = 1 with -ln(c) = 0, so a logarithm
// near 1 never cancels.
const LogTable: [141]u64 = [
    0x3FF6C16C16C16C17, 0xBFD68AC83E9C6A15, 0x3C6ACD8A9145FF44,
    0x3FF642C8590B2164, 0xBFD522AE0738A3D7, 0xBC73840B263ACB43,
    0x3FF5C9882B931057, 0xBFD3C25277333183, 0xBC7152D81AF5713A,
    0x3FF5555555555555, 0xBFD269621134DB91, 0xBC7E0EFADD9DB02A,
    0x3FF4E5E0A72F0539, 0xBFD1178E8227E47A, 0xBC7B8CE2D07F1CB7,
    0x3FF47AE147AE147B, 0xBFCF991C6CB3B37A, 0xBC5ECCA0CDF30143,
    0x3FF4141414141414, 0xBFCD1037F2655E7B, 0x3C53F3ADB7B71CBC,
    0x3FF3B13B13B13B14, 0xBFCA93ED3C8AD9E5, 0xBC6BCAFA9DE97202,
    0x3FF3521CFB2B78C1, 0xBFC823C16551A3C0, 0xBC66DCD318F4187E,
    0x3FF2F684BDA12F68, 0xBFC5BF406B543DB0, 0x3C21F5B44C0DF7F7,
    0x3FF29E4129E4129E, 0xBFC365FCB0159014, 0xBC6BEA08D2DCA256,
    0x3FF2492492492492, 0xBFC1178E8227E47A, 0x3C50E63A5F01C693,
    0x3FF1F7047DC11F70, 0xBFBDA7276384469E, 0xBC5401FA71733017,
    0x3FF1A7B9611A7B96, 0xBFB9335E5D594988, 0x3C5478A85704CCB7,
    0x3FF15B1E5F75270D, 0xBFB4D3115D207EAC, 0xBC3DA7D0B1E10B2F,
    0x3FF1111111111111, 0xBFB08598B59E3A06, 0x3C5DD7009902BF32,
    0x3FF0C9714FBCDA3B, 0xBFA894AA149FB34B, 0x3C42BA0B44CFAEE5,
    0x3FF0842108421084, 0xBFA0415D89E74440, 0xBC4C05CF1D753621,
    0x3FF0410410410410, 0xBF90205658935837, 0xBC327C8E8416E717,
    0x3FF0000000000000, 0x0000000000000000, 0x0000000000000000,
    0x3FEF81F81F81F820, 0x3F8FC0A8B0FC03C4, 0xBC183092C5964281,
    0x3FEF07C1F07C1F08, 0x3F9F829B0E7832F8, 0x3C333E3F04F1EF25,
    0x3FEE9131ABF0B767, 0x3FA77458F632DCFF, 0x3C08D3CA87B92968,
    0x3FEE1E1E1E1E1E1E, 0x3FAF0A30C01162A8, 0x3C485F325C5BBACD,
    0x3FEDAE6076B981DB, 0x3FB341D7961BD1D0, 0xBC53599F227BECBB,
    0x3FED41D41D41D41D, 0x3FB6F0D28AE56B4E, 0xBC420DB323097324,
    0x3FECD85689039B0B, 0x3FBA926D3A4AD562, 0xBC4D7A16EAB1E2AD,
    0x3FEC71C71C71C71C, 0x3FBE27076E2AF2EA, 0xBC361578001E015A,
    0x3FEC0E070381C0E0, 0x3FC0D77E7CD08E5B, 0x3C69A5DC5E9030AD,
    0x3FEBACF914C1BAD0, 0x3FC29552F81FF521, 0x3C6301771C407DC0,
    0x3FEB4E81B4E81B4F, 0x3FC44D2B6CCB7D1C, 0x3C47D3D950F87E23,
    0x3FEAF286BCA1AF28, 0x3FC5FF3070A793D6, 0xBC5BC60EFAFC6F6C,
    0x3FEA98EF606A63BE, 0x3FC7AB890210D907, 0xBC61072534A57E7D,
    0x3FEA41A41A41A41A, 0x3FC9525A9CF456B6, 0xBC626FB3E2B1D1DA,
    0x3FE9EC8E951033D9, 0x3FCAF3C94E80BFF3, 0x3C6A3398064DF33E,
    0x3FE999999999999A, 0x3FCC8FF7C79A9A20, 0xBC64F689F8434011,
    0x3FE948B0FCD6E9E0, 0x3FCE27076E2AF2E8, 0xBC461578001E015E,
    0x3FE8F9C18F9C18FA, 0x3FCFB9186D5E3E29, 0x3C6355519B0DE535,
    0x3FE8ACB90F6BF3AA, 0x3FD0A324E27390E2, 0x3C7BDCFDE8061C03,
    0x3FE8618618618618, 0x3FD1675CABABA60F, 0x3C2CE63EAB883727,
    0x3FE8181818181818, 0x3FD22941FBCF7966, 0xBC5DBD7AC258A2BD,
    0x3FE7D05F417D05F4, 0x3FD2E8E2BAE11D31, 0xBC61E99B72BD7BF2,
    0x3FE78A4C8178A4C8, 0x3FD3A64C556945EA, 0x3C3CBCD735D03424,
    0x3FE745D1745D1746, 0x3FD4618BC21C5EC2, 0xBC27A42642661C62,
    0x3FE702E05C0B8170, 0x3FD51AAD872DF82E, 0xBC7D8DB0A7CC1543,
    0x3FE6C16C16C16C17, 0x3FD5D1BDBF5809CA, 0xBC77DC9C7C23801F,
    0x3FE6816816816817, 0x3FD686C81E9B14AD, 0x3C7710AF840538E3,
]

// LogDD is ln(xh + xl) as hi + lo for a normal xh > 0 and |xl| <= ulp(xh),
// with a relative error below 2^-62: xh = 2^k m, m in [sqrt2/2, sqrt2), c
// from LogTable's row nearest m, r = m c - 1 + (xl / 2^k) c as rh + rl
// with m c exact by TwoProd (|r| <= 0.0112), and ln = k ln2 - ln(c) +
// log1p(r) with log1p's r^2 term exact.
LogDD(xh: f64, xl: f64): (hi: f64, lo: f64) {
    b := xh.Bits()
    var k := Trunc[int](b >> 52) - 1023
    var mb := (b & FracMask64) | 0x3FF0000000000000
    if mb > 0x3FF6A09E667F3BCD {
        mb = mb - 0x0010000000000000 // m / 2, so m < sqrt2
        k = k + 1
    }
    m := f64.FromBits(mb)
    row := 3 * (Cast[int](m * 64.0 + 0.5) - 45)
    c := f64.FromBits(LogTable[row])
    p := TwoProd(m, c)
    // p.hi - 1 is exact (Sterbenz).
    rs := TwoSum(p.hi - 1.0, p.lo + LdExp(xl, -k) * c)
    rh := rs.hi
    rl := rs.lo
    sq := TwoProd(rh, rh)
    poly := rh * sq.hi * (LogC3 + rh * (LogC4 + rh * (LogC5 + rh * (LogC6 + rh * (LogC7 + rh * (LogC8 + rh * (LogC9 + rh * (LogC10 + rh * LogC11))))))))
    a := FastTwoSum(rh, -0.5 * sq.hi)
    kf := Cast[f64](k)
    // k * Ln2Hi is exact: Ln2Hi has 32 bits and |k| <= 1024.
    s1 := TwoSum(kf * Ln2Hi, f64.FromBits(LogTable[row + 1]))
    s2 := TwoSum(s1.hi, a.hi)
    tail := a.lo + ((-0.5 * sq.lo) + poly + (rl - rl * rh))
    lo := s1.lo + s2.lo + tail + (f64.FromBits(LogTable[row + 2]) + kf * Ln2Lo)
    return FastTwoSum(s2.hi, lo)
}

// AtanDD is atan(th + tl) as hi + lo for th in [2^-62, 2^62] and |tl| <=
// ulp(th): msun's reduction (s_atan.c) to |z| < 0.44 against atan(0.5),
// atan(1), atan(1.5) and pi/2, with the numerator of z exact and z carried
// as zh + zl, then msun's polynomial for the cubic and higher terms.
AtanDD(th: f64, tl: f64): (hi: f64, lo: f64) {
    if th < 0.4375 {
        // The general path's id = -1 with its divisions by 1.0 taken out,
        // the same bits for th > 0 (Atan2Core's quotient): zh = th / 1.0 =
        // th; TwoProd(th, 1.0) splits 1.0 into 1.0 and +0, so p.hi = th and
        // p.lo = ((ah - th) + ah * +0 + al) + al * +0 = (-al + al) = +0
        // (+0 also when al = 0); then zl = ((th - th) - +0 + tl - th *
        // 0.0) / 1.0 is tl, but +0 for tl = -0: tl + 0.0.
        zl0 := tl + 0.0
        z0 := th * th
        w0 := z0 * z0
        s10 := z0 * (AT0 + w0 * (AT2 + w0 * (AT4 + w0 * (AT6 + w0 * (AT8 + w0 * AT10)))))
        s20 := w0 * (AT1 + w0 * (AT3 + w0 * (AT5 + w0 * (AT7 + w0 * AT9))))
        poly0 := th * (s10 + s20)
        corr0 := zl0 / (1.0 + z0)
        a0 := FastTwoSum(th, -poly0)
        return FastTwoSum(a0.hi, a0.lo + corr0)
    }
    var id := -1
    var nh := th
    var nl := tl
    var dh := 1.0
    var dl := 0.0
    if th >= 0.4375 {
        if th < 0.6875 {
            id = 0 // z = (2t - 1) / (2 + t), 2 th - 1 exact
            nh = 2.0 * th - 1.0
            nl = 2.0 * tl
            d := TwoSum(2.0, th)
            dh = d.hi
            dl = d.lo + tl
        } else if th < 1.1875 {
            id = 1 // z = (t - 1) / (t + 1), th - 1 exact
            nh = th - 1.0
            d := TwoSum(th, 1.0)
            dh = d.hi
            dl = d.lo + tl
        } else if th < 2.4375 {
            id = 2 // z = (t - 1.5) / (1 + 1.5 t), th - 1.5 exact
            nh = th - 1.5
            p := TwoProd(1.5, th)
            d := TwoSum(1.0, p.hi)
            dh = d.hi
            dl = d.lo + p.lo + 1.5 * tl
        } else {
            id = 3 // z = -1 / t
            nh = -1.0
            nl = 0.0
            dh = th
            dl = tl
        }
    }
    zh := nh / dh
    p := TwoProd(zh, dh)
    // nh - p.hi is exact (Sterbenz).
    zl := (((nh - p.hi) - p.lo) + nl - zh * dl) / dh
    z := zh * zh
    w := z * z
    s1 := z * (AT0 + w * (AT2 + w * (AT4 + w * (AT6 + w * (AT8 + w * AT10)))))
    s2 := w * (AT1 + w * (AT3 + w * (AT5 + w * (AT7 + w * AT9))))
    poly := zh * (s1 + s2)
    corr := zl / (1.0 + z)
    if id < 0 {
        a := FastTwoSum(zh, -poly)
        return FastTwoSum(a.hi, a.lo + corr)
    }
    s := TwoSum(AtanHi[id], zh)
    return FastTwoSum(s.hi, s.lo + ((AtanLo[id] - poly) + corr))
}
