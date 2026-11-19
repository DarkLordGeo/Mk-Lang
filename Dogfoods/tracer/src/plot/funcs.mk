// Why: The plotter and the table mode share one list of math functions.
//
// What: The functions a plot expression may call, each in f64 and f32, grouped
// by signature.
//
// How: The expression parser looks names up here, and table mode runs every
// entry over its edge inputs.

import std.math

/// A function of one argument.
export struct Unary {
    export name: str
    export f: fn(f64): f64
    export g: fn(f32): f32
}

/// A function of two arguments.
export struct Binary {
    export name: str
    export f: fn(f64, f64): f64
    export g: fn(f32, f32): f32
}

/// A classifier, which an expression sees as 1 for true and 0 for false.
export struct Predicate {
    export name: str
    export f: fn(f64): bool
    export g: fn(f32): bool
}

/// The functions of one argument, in the order table mode prints them.
export const Unaries: []Unary = [
    Unary(name: "sqrt", f: math.Sqrt, g: math.SqrtF32),
    Unary(name: "floor", f: math.Floor, g: math.FloorF32),
    Unary(name: "ceil", f: math.Ceil, g: math.CeilF32),
    Unary(name: "round", f: math.Round, g: math.RoundF32),
    Unary(name: "roundeven", f: math.RoundEven, g: math.RoundEvenF32),
    Unary(name: "trunc", f: math.RoundToZero, g: math.RoundToZeroF32),
    Unary(name: "fract", f: math.Fract, g: math.FractF32),
    Unary(name: "exp", f: math.Exp, g: math.ExpF32),
    Unary(name: "exp2", f: math.Exp2, g: math.Exp2F32),
    Unary(name: "expm1", f: math.Expm1, g: math.Expm1F32),
    Unary(name: "log", f: math.Log, g: math.LogF32),
    Unary(name: "log2", f: math.Log2, g: math.Log2F32),
    Unary(name: "log10", f: math.Log10, g: math.Log10F32),
    Unary(name: "log1p", f: math.Log1p, g: math.Log1pF32),
    Unary(name: "sin", f: math.Sin, g: math.SinF32),
    Unary(name: "cos", f: math.Cos, g: math.CosF32),
    Unary(name: "tan", f: math.Tan, g: math.TanF32),
    Unary(name: "asin", f: math.Asin, g: math.AsinF32),
    Unary(name: "acos", f: math.Acos, g: math.AcosF32),
    Unary(name: "atan", f: math.Atan, g: math.AtanF32),
    Unary(name: "sinh", f: math.Sinh, g: math.SinhF32),
    Unary(name: "cosh", f: math.Cosh, g: math.CoshF32),
    Unary(name: "tanh", f: math.Tanh, g: math.TanhF32),
    Unary(name: "asinh", f: math.Asinh, g: math.AsinhF32),
    Unary(name: "acosh", f: math.Acosh, g: math.AcoshF32),
    Unary(name: "atanh", f: math.Atanh, g: math.AtanhF32),
    Unary(name: "cbrt", f: math.Cbrt, g: math.CbrtF32),
]

/// The functions of two arguments.
export const Binaries: []Binary = [
    Binary(name: "mod", f: math.Mod, g: math.ModF32),
    Binary(name: "pow", f: math.Pow, g: math.PowF32),
    Binary(name: "atan2", f: math.Atan2, g: math.Atan2F32),
    Binary(name: "hypot", f: math.Hypot, g: math.HypotF32),
    Binary(name: "copysign", f: math.CopySign, g: math.CopySignF32),
    Binary(name: "nextafter", f: math.NextAfter, g: math.NextAfterF32),
]

/// The classifiers.
export const Predicates: []Predicate = [
    Predicate(name: "isnan", f: math.IsNan, g: math.IsNanF32),
    Predicate(name: "isinf", f: math.IsInf, g: math.IsInfF32),
    Predicate(name: "isfinite", f: math.IsFinite, g: math.IsFiniteF32),
]

/// The named constants, in f64; f32 rounds each.
export struct Constant {
    export name: str
    export value: f64
}

/// The constants an expression may name.
export const Constants: []Constant = [
    Constant(name: "pi", value: math.Pi),
    Constant(name: "e", value: math.E),
    Constant(name: "ln2", value: math.Ln2),
    Constant(name: "sqrt2", value: math.Sqrt2),
    Constant(name: "inf", value: math.Inf),
    Constant(name: "nan", value: math.Nan),
]

/// What a name in an expression refers to.
export enum Callee {
    None
    Unary(index: int)
    Binary(index: int)
    Predicate(index: int)
    Fma
    LdExp
    FrExpFrac
    FrExpExp
    Constant(index: int)
}

/// Finds `name` among the functions and constants. O(number of names).
export Find(name: str): Callee {
    for i, u in Unaries {
        if u.name == name {
            return Callee.Unary(index: i)
        }
    }
    for i, b in Binaries {
        if b.name == name {
            return Callee.Binary(index: i)
        }
    }
    for i, p in Predicates {
        if p.name == name {
            return Callee.Predicate(index: i)
        }
    }
    for i, c in Constants {
        if c.name == name {
            return Callee.Constant(index: i)
        }
    }
    return match name {
        "fma" { Callee.Fma }
        "ldexp" { Callee.LdExp }
        "frexp" { Callee.FrExpFrac }
        "frexpexp" { Callee.FrExpExp }
        _ { Callee.None }
    }
}
