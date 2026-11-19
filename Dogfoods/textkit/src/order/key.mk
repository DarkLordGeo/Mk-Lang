// Why: sort and uniq compare lines by a key.
// What: Keying, KeyOf and the numeric key forms.
// How: a field, folded or read as a number.

import std.text

/// How a line's sort and comparison key is taken from it.
export struct Keying {
    /// The field to compare, from 1; 0 is the whole line
    export field: int
    /// The field separator; none splits at whitespace
    export separator: ?str
    /// Compare case-folded keys
    export fold: bool
}

/// The key of `line`: its field `k.field`, folded when `k.fold` asks.
/// O(n); allocates the folded key in `a`.
export KeyOf(a: &Arena, line: str, k: &Keying): OutOfMemory!str from a, line, k.separator {
    field := if k.field > 0 { Field(line, k.field, k.separator) } else { line }
    if k.fold {
        return try text.FoldCase(a, field)
    }
    return field
}

// Split holds one loan set for its text and its separator, so a piece holds both
/// Field `n` of `line`, from 1, split at `separator` or at whitespace, or
/// `""` when the line has fewer fields.
export Field(line: str, n: int, separator: ?str): str from line, separator {
    var i := 0
    if sep := separator {
        for piece in line.Split(sep) {
            i += 1
            if i == n {
                return piece
            }
        }
        return ""
    }
    for word in line.Words() {
        i += 1
        if i == n {
            return word
        }
    }
    return ""
}

/// A key read as a number: exactly as an integer when it is one, else as a
/// float, else not a number.
export enum Number {
    Whole(n: i64)
    Real(x: f64)
    None
}

/// The number `key` spells, ignoring spaces around it.
export NumberOf(key: str): Number {
    s := key.Trim()
    match s.ParseInt() {
        ok(n) {
            return Number.Whole(n: n)
        }
        err(_) {}
    }
    x := s.ParseFloat() else {
        return Number.None
    }
    // a NaN has no place in an order, so it sorts with the words
    if x != x {
        return Number.None
    }
    return Number.Real(x: x)
}

/// Whether `a` orders before `b`: words before numbers, and numbers by
/// value, an integer exactly against an integer.
export NumberBefore(a: &Number, b: &Number): bool {
    match *a {
        .None {
            return match *b {
                .None { false }
                _ { true }
            }
        }
        .Whole(n) {
            match *b {
                .None { return false }
                .Whole(n: m) { return n < m }
                .Real(x) { return Cast[f64](n) < x }
            }
        }
        .Real(x) {
            match *b {
                .None { return false }
                .Whole(n) { return x < Cast[f64](n) }
                .Real(x: y) { return x < y }
            }
        }
    }
}
