// Why: std.json needs one data model that Parse fills, Format writes and
// generated codecs read, without copying the parsed text.
//
// What: Value (the JSON value), its errors Syntax, Mismatch and Depth, the
// Error set, Member, the Json interface and Value's accessors Get, Int,
// Float and Str.
//
// How: a Value is a tree in the caller's arena. Its strings are views: Text
// and Number hold slices of the parsed text unless the string held an
// escape, in which case Text holds the decoded copy in the arena. Numbers
// stay text (RFC 8259 section 6 allows any precision), so Parse never rounds
// and the accessors decide how to read a number. No unsafe, so std.json is
// not in std/pkg.mk's unsafe_modules.

/// The text is not RFC 8259 JSON; offset is the first byte that cannot
/// continue it (the text's length when it ends too soon).
export error Syntax(offset: int)

/// A value does not have the shape FromJson reads; offset is -1, since a
/// Value keeps no offsets.
export error Mismatch(offset: int)

/// The text nests arrays and objects more than 512 deep; offset is the
/// opening bracket of the 513th.
export error Depth(offset: int)

/// Every way Parse and FromJson fail. Parse takes a str, which is valid
/// UTF-8, and refuses an escape that decodes to a lone surrogate with
/// Syntax at the escape, so the set has no Utf8Error.
export open type Error = Syntax | Mismatch | Depth | OutOfMemory

/// A JSON value, a tree in the caller's arena. Number keeps its text, and
/// Text is a view of the parsed text unless the string held an escape. A
/// Value formats as compact JSON, so `{v}` in a Print writes it; a Number
/// whose text is not a JSON number fails with WriteError.
export enum Value {
    /// null.
    Null
    /// true or false.
    Bool(b: bool)
    /// A number, kept as its text.
    Number(text: str)
    /// A string, its escapes decoded.
    Text(s: str)
    /// An array of values.
    Array(items: List[Value])
    /// An object's members.
    Object(members: List[Member])
}

/// One member of an Object: its key and its value.
export struct Member {
    /// The member's key.
    export key: str
    /// The member's value.
    export value: Value
}

/// Json is what gen json.Codec(T) implements: FormatJson writes T as compact
/// JSON, so generic code can encode any codec type.
export interface Json {
    FormatJson(&self, w: mut Writer): WriteError!void
}

/// Get is the value of the last member named key, when self is an Object;
/// none when there is no such member or self is not an Object. RFC 8259
/// leaves duplicate names to the reader; here the last one wins.
/// O(members), a scan from the end; allocates nothing.
export Value.Get(&self, key: str): ?&Value {
    match self {
        .Object(members) {
            ms := members.Items()
            var i := ms.Len()
            for i > 0 {
                i -= 1
                if ms[i].key == key {
                    return some(&ms[i].value)
                }
            }
            return none
        }
        _ {
            return none
        }
    }
}

/// Int is a Number whose text is an integer (no fraction or exponent) that
/// fits i64: "1.0" and "1e2" give none. None for any other variant.
/// O(text); allocates nothing.
export Value.Int(&self): ?i64 {
    return match self {
        .Number(text) {
            n := text.ParseInt() else { return none }
            some(n)
        }
        _ { none }
    }
}

/// Float is a Number's value as the nearest f64, correctly rounded; a
/// magnitude past f64's range gives none, a total underflow a zero with the
/// sign. None for any other variant. O(text); allocates nothing.
export Value.Float(&self): ?f64 {
    return match self {
        .Number(text) {
            x := text.ParseFloat() else { return none }
            some(x)
        }
        _ { none }
    }
}

/// Str is a Text's string, a view of the parsed text or of the arena; none
/// for any other variant. O(1); allocates nothing.
export Value.Str(&self): ?str {
    return match self {
        .Text(s) { some(*s) }
        _ { none }
    }
}
