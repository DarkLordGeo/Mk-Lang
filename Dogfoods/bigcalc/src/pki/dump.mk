// Why: `cert der` shows the structure of any DER file.
//
// What: Dump, TagName and Primitive.
//
// How: one element a line, indented by depth, with the value of the
// primitive types a reader can use; nesting stops at MaxDepth.

import std.encoding

// The deepest nesting Dump prints, the bound std.encoding states for DER;
// certificates nest about eight deep.
const MaxDepth: int = 32

/// Dump prints every element of data and, inside a constructed one, its
/// children one level deeper. Fails with encoding's TooLarge past 32 levels
/// (the bound std's DER reader states) and with the DER's first fault. O(data); allocates the printed values in a.
export Dump(a: &Arena, out: mut Writer, data: []u8, depth: int): DerError|WriteError!void {
    if depth > MaxDepth {
        fail encoding.TooLarge
    }
    var rest := data
    for rest.Len() > 0 {
        (rest: next, value) := try encoding.DerRead(rest)
        rest = next
        for i in 0..depth {
            _ = i
            try out.Str("  ")
        }
        name := try TagName(a, &value)
        if value.constructed {
            try Print(out, "{name} ({n})\n", (name, n: value.content.Len()))
            try Dump(a, out, value.content, depth + 1)
            continue
        }
        try Primitive(a, out, &value, name)
    }
}

// The name of a tag: the universal types by name, others by class and number.
TagName(a: &Arena, v: &encoding.DerValue): OutOfMemory!str from a {
    tag := v.tag
    if v.class == 2 {
        return try a.Fmt("[{tag}]", (tag))
    }
    if v.class == 1 {
        return try a.Fmt("[APPLICATION {tag}]", (tag))
    }
    if v.class == 3 {
        return try a.Fmt("[PRIVATE {tag}]", (tag))
    }
    return match tag {
        1 { "BOOLEAN" }
        2 { "INTEGER" }
        3 { "BIT STRING" }
        4 { "OCTET STRING" }
        5 { "NULL" }
        6 { "OBJECT IDENTIFIER" }
        12 { "UTF8String" }
        16 { "SEQUENCE" }
        17 { "SET" }
        19 { "PrintableString" }
        22 { "IA5String" }
        23 { "UTCTime" }
        24 { "GeneralizedTime" }
        _ { try a.Fmt("UNIVERSAL {tag}", (tag)) }
    }
}

// One primitive element's line.
Primitive(a: &Arena, out: mut Writer, v: &encoding.DerValue, name: str): DerError|WriteError!void {
    n := v.content.Len()
    if v.class == 0 {
        match v.tag {
            1 {
                b := try v.Boolean()
                try Print(out, "{name} {b}\n", (name, b))
                return
            }
            2 {
                if n <= 16 {
                    value := try Signed(a, try v.Integer())
                    text := try value.Text(a, 10)
                    try Print(out, "{name} {text}\n", (name, text))
                    return
                }
            }
            5 {
                try Print(out, "{name}\n", (name))
                return
            }
            6 {
                id := try Oid(a, v.content)
                try Print(out, "{name} {id}\n", (name, id))
                return
            }
            12 | 19 | 22 | 23 | 24 {
                match str.From(v.content) {
                    ok(text) {
                        try Print(out, "{name} \"{text}\"\n", (name, text))
                        return
                    }
                    err(e) {
                        _ = e
                    }
                }
            }
            _ {}
        }
    }
    shown := if n > 16 { 16 } else { n }
    preview := try encoding.HexEncode(a, v.content[..shown])
    more := if n > 16 { "..." } else { "" }
    try Print(out, "{name} ({n}) {preview}{more}\n", (name, n, preview, more))
}
