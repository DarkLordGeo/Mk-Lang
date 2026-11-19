// Why: the words that print a value need one place for the output base and
// the other views of a number.
//
// What: PrintValue and Machine.Show.
//
// How: the top of the stack prints in the output base, or as an i64, an f64
// with its bits, bytes or 64-bit words.

import std.big
import std.encoding

/// PrintValue writes v in base and a newline: through Int.Format in base 10,
/// Int.Text otherwise. Allocates the text in a for a base other than 10.
export PrintValue(a: &Arena, out: mut Writer, v: &big.Int, base: int): OutOfMemory|WriteError!void {
    if base == 10 {
        try v.Format(out)
    } else {
        text := try v.Text(a, base)
        try out.Str(text)
    }
    try out.Str("\n")
}

// Runs tok when it is a printing word and reports whether it was one.
Machine.Show(&self, a: &Arena, out: mut Writer, stack: mut List[big.Int], tok: Token): Error!bool {
    c := tok.column
    base := self.settings.base
    match tok.text {
        "p" {
            try Need(stack.Len(), c, 1)
            try PrintValue(a, out, &stack[stack.Len() - 1], base)
        }
        "." {
            try Need(stack.Len(), c, 1)
            // Need checked the depth, so Pop is never none here
            try PrintValue(a, out, &stack.Pop().Must(), base)
        }
        "f" | "stack" {
            for i in 0..stack.Len() {
                try PrintValue(a, out, &stack[i], base)
            }
        }
        "i64" {
            try Need(stack.Len(), c, 1)
            if n := stack[stack.Len() - 1].ToI64() {
                try Print(out, "{n}\n", (n))
            } else {
                try Print(out, "does not fit in i64\n")
            }
        }
        "f64" {
            try Need(stack.Len(), c, 1)
            x := stack[stack.Len() - 1].ToF64()
            bits := x.Bits()
            try Print(out, "{x} (bits {bits:016x})\n", (x, bits))
        }
        "bytes" {
            try Need(stack.Len(), c, 1)
            raw := try stack[stack.Len() - 1].Bytes(a)
            hex := try encoding.HexEncode(a, raw)
            try Print(out, "bytes:{hex}\n", (hex))
        }
        "b64" {
            try Need(stack.Len(), c, 1)
            raw := try stack[stack.Len() - 1].Bytes(a)
            text := try encoding.Base64Encode(a, raw, encoding.Base64)
            try Print(out, "b64:{text}\n", (text))
        }
        "der" {
            // the DER INTEGER of the magnitude (X.690 8.3), as a key's modulus is written
            try Need(stack.Len(), c, 1)
            var w := encoding.DerWriter.New(a)
            try w.Integer(try stack[stack.Len() - 1].Bytes(a))
            // one primitive element and nothing left open: Bytes cannot fail
            der := w.Bytes().Must()
            hex := try encoding.HexEncode(a, der)
            try Print(out, "der:{hex}\n", (hex))
        }
        "words" {
            try Need(stack.Len(), c, 1)
            top := &stack[stack.Len() - 1]
            sign := if top.Sign() < 0 { "-" } else { "+" }
            try Print(out, "{sign}", (sign))
            for w in top.Words() {
                try Print(out, " {w:016x}", (w))
            }
            try out.Str("\n")
        }
        "vars" {
            for name in try self.Names(a) {
                v := try self.Recall(a, name) else {
                    continue
                }
                try Print(out, "{name} = ", (name))
                try PrintValue(a, out, &v, base)
            }
        }
        _ {
            return false
        }
    }
    return true
}
