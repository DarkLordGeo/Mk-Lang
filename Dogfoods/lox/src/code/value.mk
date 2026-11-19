// Why: The VM moves values on every instruction, so a value must be small and
// plain.
//
// What: Value, ConstKey with its ordering KeyLess, and Constant.
//
// How: Anything larger than a number lives in the VM's object pool and the
// value holds its handle (Crafting Interpreters, chapter 18). Constants are
// keyed so that equal constants share one slot.

import std.derive
import std.mem

/// A Lox value. Strings, functions, natives and lists are objects named by a handle.
export enum Value {
    Nil
    Bool(b: bool)
    Num(n: f64)
    Obj(h: mem.Handle)
}

gen derive.Eq(Value)

/// Reports whether `v` is false in a condition: only `nil` and `false` are.
export Value.Falsey(self): bool {
    return match self {
        .Nil { true }
        .Bool(b) { !b }
        _ { false }
    }
}

/// The identity of a constant, so equal constants of one function share a slot. A number is
/// keyed by its bits, so 0 and -0 stay apart and every NaN payload is its own constant.
export enum ConstKey {
    Num(bits: u64)
    Str(text: str)
    Fun(index: int)
}

gen derive.Eq(ConstKey)
gen derive.Hash(ConstKey)
gen derive.Format(ConstKey)

/// The rank of a key's variant in the constant order.
Rank(k: &ConstKey): int {
    return match k {
        .Num { 0 }
        .Str { 1 }
        .Fun { 2 }
    }
}

/// A total order on keys: by variant, then by bits, text or index.
export KeyLess(a: &ConstKey, b: &ConstKey): bool {
    ra := Rank(a)
    rb := Rank(b)
    if ra != rb {
        return ra < rb
    }
    match a {
        .Num(bits: x) {
            if b2 := NumBits(b) {
                return *x < b2
            }
        }
        .Str(text: x) {
            if b2 := StrText(b) {
                return *x < b2
            }
        }
        .Fun(index: x) {
            if b2 := FunIndex(b) {
                return *x < b2
            }
        }
    }
    return false
}

NumBits(k: &ConstKey): ?u64 {
    match k {
        .Num(bits) { return some(*bits) }
        _ { return none }
    }
}

StrText(k: &ConstKey): ?str {
    match k {
        .Str(text) { return some(*text) }
        _ { return none }
    }
}

FunIndex(k: &ConstKey): ?int {
    match k {
        .Fun(index) { return some(*index) }
        _ { return none }
    }
}

/// A constant of a function. `object` caches the object made for a string or function
/// constant the first time it is loaded, so a loop that loads it makes one object; the VM
/// stores into it through a shared reference while the code is running.
export struct Constant {
    export key: ConstKey
    export object: Cell[?Value]
}
