// Why: the one-argument f32 transcendental functions must be correctly
// rounded, which rounding an f64 result once does not always give.
//
// What: ExpF32, Exp2F32, Expm1F32, LogF32, Log2F32, Log10F32, Log1pF32,
// the trig, inverse trig, hyperbolic and inverse hyperbolic F32 forms,
// CbrtF32, and their tables of exceptions. The two-argument forms are in
// mid32.mk.
//
// How: each widens to f64 (exact), calls the f64 function, and rounds once
// to f32 (LogF32, SinF32 and CosF32 first try fast32.mk's fast path). The
// f64 result is within 2^-29 of an f32 ulp, so the f32 is correct except
// close to a midpoint; the run over every f32 input
// (TestSTDMATH_Exhaustive) finds those, and each is answered from a table
// of exceptions, as CORE-MATH's f32 routines do. An overflow is an
// infinity, never Cast's panic. Each is O(1) and allocates nothing.

// The inputs where the f64 path rounds to the wrong f32, each packed as its
// bits << 32 | the correctly rounded result's bits: every one the run over
// all 2^32 inputs found (TestSTDMATH_Exhaustive), each checked at 512 bits
// (TestSTDMATH_ExceptionsAreCorrect), so with them each F32 form is
// correctly rounded. A change to the f64 function reruns that test,
// which finds the new set.
const Log1pExceptions: [9]u64 = [0x35400003353FFFFF, 0x3710001B370FFFF3, 0x3EFD81AD3ECDEEE1, 0x41078FEB400FE5E7, 0x65D890D34254D1F9, 0x6F31A8EC42845A89, 0xB53FFFFDB5400001, 0xB70FFFE5B710000D, 0xBB0EC8C4BB0EF0A5]
const Exp2Exceptions: [2]u64 = [0x3B429D373F804385, 0xBCF3A9373F7AC6B1]
const LogExceptions: [5]u64 = [0x3C413D3AC08E158F, 0x41178FEB400FE5E7, 0x4C5D65A5418F034B, 0x65D890D34254D1F9, 0x6F31A8EC42845A89]
const Log10Exceptions: [1]u64 = [0x0EFEEE7AC1E99D23]
const SinExceptions: [2]u64 = [0x46199998BEB1FA5D, 0xC61999983EB1FA5D]
const CosExceptions: [4]u64 = [0x5F18B8783F7F14BB, 0x6115CB113F78142F, 0xDF18B8783F7F14BB, 0xE115CB113F78142F]
const AcosExceptions: [2]u64 = [0x328885A33FC90FDB, 0x398262223FC907B5]
const AtanExceptions: [2]u64 = [0x3D8D6B233D8D31C3, 0xBD8D6B23BD8D31C3]
const SinhExceptions: [2]u64 = [0x3A1285FF3A1285FF, 0xBA1285FFBA1285FF]
const AsinhExceptions: [8]u64 = [0x4BDD65A5418F034B, 0x5E68984E422E4A21, 0x655890D34254D1F9, 0xCBDD65A5C18F034B, 0xDE68984EC22E4A21, 0xE55890D3C254D1F9, 0x6EB1A8EC42845A89, 0xEEB1A8ECC2845A89]
const AcoshExceptions: [3]u64 = [0x5E68984E422E4A21, 0x655890D34254D1F9, 0x6EB1A8EC42845A89]

// Corrected is r, or the correctly rounded result when x is in table.
Corrected(x: f32, r: f32, table: []u64): f32 {
    b := u64(x.Bits())
    for i in 0..table.Len() {
        e := table[i]
        if e >> 32 == b {
            return f32.FromBits(Trunc[u32](e))
        }
    }
    return r
}

/// ExpF32 is Exp for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export ExpF32(x: f32): f32 {
    if (x.Bits() & 0x7FFFFFFF) -% 0x32800000 < 0x42B00000 - 0x32800000 {
        // 2^-26 <= |x| < 88: Exp's kernel needs no special case, and the
        // result is below the largest f32, so Cast rounds it once. The
        // kernel is ExpCore(f64(x), 0.0)'s steps (exptab.mk), written here
        // so they are not a call, without its |x| >= 512 branch, which
        // cannot be taken: the same bits (tests_fast32.mk).
        xd := f64(x)
        kd0 := xd * ExpInvLn2N + ExpShift
        ki := kd0.Bits()
        kd := kd0 - ExpShift
        r := (xd - kd * ExpLn2HiN) - kd * ExpLn2LoN + 0.0
        i := Trunc[int](ki & 127) * 2
        tail := f64.FromBits(ExpT[i])
        sv := f64.FromBits(ExpT[i + 1] +% (ki << 45))
        r2 := r * r
        tmp := tail + r + r2 * (ExpQ2 + r * ExpQ3) + r2 * r2 * (ExpQ4 + r * ExpQ5)
        return Cast[f32](sv + sv * tmp)
    }
    return Narrow(Exp(f64(x)))
}

/// Exp2F32 is Exp2 for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export Exp2F32(x: f32): f32 {
    return Corrected(x, Narrow(Exp2(f64(x))), Exp2Exceptions[..])
}

/// Expm1F32 is Expm1 for f32, correctly rounded for every one of its
/// 2^32 inputs. O(1); allocates nothing.
export Expm1F32(x: f32): f32 {
    return Narrow(Expm1(f64(x)))
}

/// LogF32 is Log for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export LogF32(x: f32): f32 {
    if x.Bits() -% 0x00800000 < 0x7F000000 {
        // x positive, normal and finite: the fast kernel, when its rounding
        // test passes (fast32.mk). y is ln x for ix the bits of x widened
        // to f64, with a relative error below 2^-43. x = 2^k z with z in
        // [0.6875, 1.375) in row i of PowLogT (pow.mk), whose invc has at
        // most 8 significant bits: z has 24, so r = z invc - 1 is exact,
        // and |r| <= 2^-7 (TestSTDMATH_Tables). ln x = k ln2 - ln invc +
        // log1p(r), where k ln2 - ln invc is exact as in PowLog, and
        // log1p(r) is Taylor's series to r^6, truncated below |r|^7 / 7 <=
        // 2^-51.8. The two rows beside 1 have invc = 1, so there ln x =
        // log1p(r) with a relative truncation below 2^-44.8; every other
        // row has |ln x| >= ln(1 + 2^-8) > 2^-8.01, so the absolute 2^-51.8
        // is below 2^-43.7 relative, and the four roundings of the sum and
        // the table's tail add less than 2^-50. Written here, not called.
        ix := f64(x).Bits()
        tmp := ix -% LogOff
        i := Trunc[int]((tmp >> 45) & 127) * 3
        k := Trunc[i64](tmp) >> 52
        z := f64.FromBits(ix -% (tmp & 0xFFF0000000000000))
        r := z * f64.FromBits(PowLogT[i]) - 1.0
        kd := Cast[f64](k)
        hi := kd * LogLn2Hi + f64.FromBits(PowLogT[i + 1])
        lo := kd * LogLn2Lo + f64.FromBits(PowLogT[i + 2])
        r2 := r * r
        p := r2 * (LogA2 + r * LogA3 + r2 * (LogA4 + r * LogA5 + r2 * LogA6))
        y := (hi + r) + (lo + p)
        if Rounds32(y, LogFast32Ulps) {
            return Cast[f32](y)
        }
    }
    return Corrected(x, Narrow(Log(f64(x))), LogExceptions[..])
}

/// Log2F32 is Log2 for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export Log2F32(x: f32): f32 {
    return Narrow(Log2(f64(x)))
}

/// Log10F32 is Log10 for f32, correctly rounded for every one of its
/// 2^32 inputs. O(1); allocates nothing.
export Log10F32(x: f32): f32 {
    return Corrected(x, Narrow(Log10(f64(x))), Log10Exceptions[..])
}

/// Log1pF32 is Log1p for f32, correctly rounded for every one of its
/// 2^32 inputs. O(1); allocates nothing.
export Log1pF32(x: f32): f32 {
    return Corrected(x, Narrow(Log1p(f64(x))), Log1pExceptions[..])
}

/// SinF32 is Sin for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export SinF32(x: f32): f32 {
    if (x.Bits() & 0x7FFFFFFF) -% 0x03800000 < 0x49800000 - 0x03800000 {
        // 2^-120 <= |x| < 2^20: SinCosFast32, when its rounding test passes
        // (fast32.mk).
        y := SinCosFast32(f64(x), false)
        if Rounds32(y, SinCosFast32Ulps) {
            return Cast[f32](y)
        }
    }
    return Corrected(x, Narrow(Sin(f64(x))), SinExceptions[..])
}

/// CosF32 is Cos for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export CosF32(x: f32): f32 {
    if x.Bits() & 0x7FFFFFFF < 0x49800000 {
        // |x| < 2^20: SinCosFast32, when its rounding test passes (fast32.mk).
        y := SinCosFast32(f64(x), true)
        if Rounds32(y, SinCosFast32Ulps) {
            return Cast[f32](y)
        }
    }
    return Corrected(x, Narrow(Cos(f64(x))), CosExceptions[..])
}

/// TanF32 is Tan for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export TanF32(x: f32): f32 {
    return Narrow(Tan(f64(x)))
}

/// AsinF32 is Asin for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export AsinF32(x: f32): f32 {
    return Narrow(Asin(f64(x)))
}

/// AcosF32 is Acos for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export AcosF32(x: f32): f32 {
    return Corrected(x, Narrow(Acos(f64(x))), AcosExceptions[..])
}

/// AtanF32 is Atan for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export AtanF32(x: f32): f32 {
    return Corrected(x, Narrow(Atan(f64(x))), AtanExceptions[..])
}

/// SinhF32 is Sinh for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export SinhF32(x: f32): f32 {
    return Corrected(x, Narrow(Sinh(f64(x))), SinhExceptions[..])
}

/// CoshF32 is Cosh for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export CoshF32(x: f32): f32 {
    return Narrow(Cosh(f64(x)))
}

/// TanhF32 is Tanh for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export TanhF32(x: f32): f32 {
    return Narrow(Tanh(f64(x)))
}

/// AsinhF32 is Asinh for f32, correctly rounded for every one of its
/// 2^32 inputs. O(1); allocates nothing.
export AsinhF32(x: f32): f32 {
    return Corrected(x, Narrow(Asinh(f64(x))), AsinhExceptions[..])
}

/// AcoshF32 is Acosh for f32, correctly rounded for every one of its
/// 2^32 inputs. O(1); allocates nothing.
export AcoshF32(x: f32): f32 {
    return Corrected(x, Narrow(Acosh(f64(x))), AcoshExceptions[..])
}

/// AtanhF32 is Atanh for f32, correctly rounded for every one of its
/// 2^32 inputs. O(1); allocates nothing.
export AtanhF32(x: f32): f32 {
    return Narrow(Atanh(f64(x)))
}

/// CbrtF32 is Cbrt for f32, correctly rounded for every one of its 2^32 inputs.
/// O(1); allocates nothing.
export CbrtF32(x: f32): f32 {
    return Narrow(Cbrt(f64(x)))
}
