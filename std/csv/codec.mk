// Why: reading and writing a struct as CSV rows by hand is repetitive and
// easy to get wrong; a compile-time generator writes that code once.
//
// What: the generator Codec, its per-type emitters Enc, Dec and Read, and
// the helpers it emits into the user's module, EmitField, EmitGuard and
// EmitF32.
//
// How: for a struct T it emits, as json.Codec does for JSON,
//
//   T.FromCsv(a: &Arena, r: &csv.Record): ParseError|Utf8Error|OutOfMemory!T from a
//   T.WriteCsv(&self, w: mut Writer, o: &csv.Options): WriteError!void
//   T.WriteCsvHeader(w: mut Writer, o: &csv.Options): WriteError!void
//
// one record per value, T's exported fields in declaration order, one
// field each. Runs at compile time only.
//
// Shapes: str as it is; bool as true or false; the integers in decimal,
// read back through i64 (the signed ones, range-checked) or through u64
// from digits alone (the unsigned ones, range-checked; no sign, as
// strconv.ParseUint), so every value written reads back; f64 as Format
// writes it (shortest round trip, inf and nan included) and read by
// ParseFloat; f32 as its Format writes it and read to the correctly
// rounded f32 by an emitted helper (EmitF32, json.Codec's algorithm); ?T
// as T, or an empty field for none, and an empty field reads as none (so
// some("") of a ?str reads back as
// none). FromCsv fails with ParseError when the record has another field
// count or a field does not read as its type, with Utf8Error when a field
// is not UTF-8, and copies every str into a, so its result borrows a alone
// and the reader may move on. WriteCsv allocates nothing: it writes each
// field as csv.WriteRecord would, quoting only where RFC 4180 needs it,
// then CR LF. WriteCsv and WriteCsvHeader panic, as WriteRecord does, on
// options no reader can use (a delimiter or quote that is CR or LF, the
// two equal, a negative limit), and when T has a number field, which is
// written by its Format, also when the delimiter or quote is a byte a
// number's text can hold (digits, + - . e E n a i f): the program chose
// them. WriteCsvHeader writes the field names.
//
// Every field must be exported, since FromCsv builds T from the record
// alone; any other field, and a field of another type (char, a struct, a
// list), is E0903 at the field. The emitted code calls only std's public
// names and the prelude, through csv.X and simd.X literal names that
// resolve in this module's imports, plus the private helpers it emits
// into the user's module: the field writer, the guard of the options, and
// the f32 reader when T has an f32 field.

import std.simd

/// Codec writes, for the struct t, `T.FromCsv(a, r)`, which reads one
/// Record into a T, and `T.WriteCsv(w, o)` and `T.WriteCsvHeader(w, o)`,
/// which write a T, or the field names, as one record. Use it as
/// `gen csv.Codec(T)`. Each exported field is one field of the record, in
/// declaration order.
///
/// str is written as it is, bool as true or false, the integers in decimal,
/// f32 and f64 as their Format writes them (inf and nan included), and ?T
/// as T or an empty field for none; an empty field reads back as none, so
/// some("") of a ?str reads back as none. FromCsv fails with ParseError
/// when the record has another field count or a field does not read as its
/// type, and with Utf8Error when a field is not UTF-8. It copies every str
/// into a, so its result borrows a alone and the reader may move on.
/// WriteCsv quotes a field only where RFC 4180 needs it and ends the record
/// with CR LF. WriteCsv and WriteCsvHeader panic on options a reader
/// refuses (see Records.New), and, when T has a number field, on a
/// delimiter or quote that a number's text can hold (digits, + - . e E n a
/// i f).
///
/// E0903 at a field that is not exported or of another type (char, a
/// struct, a list), and at a struct with no field. Runs at compile time;
/// the emitted FromCsv allocates its strings in a, WriteCsv nothing.
export Codec(g: mut Gen, t: Type) {
    if t.Kind() != TypeKind.Struct {
        g.FailOn(t, "csv.Codec: only a struct has a CSV record form")
        return
    }
    fs := t.Fields()
    if fs.Len() == 0 {
        g.FailOn(t, "csv.Codec: a struct with no field has no CSV record")
        return
    }
    var numbers := false
    var floats := false
    for f in fs {
        if !f.Exported() {
            g.FailAt(f, "csv.Codec: FromCsv builds T from its exported fields, so every field must be exported")
            return
        }
        if !Supported(f.Type()) {
            g.FailAt(f, "csv.Codec: this field's type has no CSV form (str, bool, the integers, f32, f64, or ?T of one)")
            return
        }
        if IsNumber(Base(f.Type())) {
            numbers = true
        }
        if Base(f.Type()).Text() == "f32" {
            floats = true
        }
    }
    put := EmitField(g)
    guard := EmitGuard(g, numbers)
    pf := if floats { EmitF32(g) } else { quote {} }
    alone := fs.Len() == 1
    var enc := g.Block()
    var dec := g.Block()
    var names := g.Block()
    var args := quote {}
    var n := 0
    for f in fs {
        if n > 0 {
            enc.Add(quote {
                try w.Bytes(d[..])
            })
            names.Add(quote {
                try w.Bytes(d[..])
            })
            args = args.Then(quote { , })
        }
        place := quote { (&self.$(f.Name())) }
        enc.Add(Enc(g, f.Type(), place, put, alone))
        names.Add(quote {
            try $put(w, $(g.Str(f.Name())), o, $(Bool(alone)))
        })
        x := g.Fresh(f.Name())
        dec.Add(Dec(g, f.Type(), x, pf))
        args = args.Then(quote { $(f.Name()): $x })
        n += 1
    }
    // One field writes no delimiter, and an unused local is E0208.
    delim := if alone { quote {} } else { quote { var d: [1]u8 = [o.delimiter] } }
    pub := if t.Exported() { quote { export } } else { quote {} } // export as T is
    g.Emit(quote {
        $pub $(t.Name()).FromCsv(a: &Arena, r: &csv.Record): ParseError|Utf8Error|OutOfMemory!$(t.Name()) from a {
            var i := 0
            $dec
            if i != r.Len() {
                fail ParseError
            }
            return $(t.Name())($args)
        }

        $pub $(t.Name()).WriteCsv(&self, w: mut Writer, o: &csv.Options): WriteError!void {
            $guard(o)
            $delim
            $enc
            var crlf: [2]u8 = [0x0D, 0x0A]
            try w.Bytes(crlf[..])
        }

        $pub $(t.Name()).WriteCsvHeader(w: mut Writer, o: &csv.Options): WriteError!void {
            $guard(o)
            $delim
            $names
            var crlf: [2]u8 = [0x0D, 0x0A]
            try w.Bytes(crlf[..])
        }
    })
}

// Enc writes the value that e (a &ty expression) refers to as one field;
// put is the emitted field writer, alone whether it is the record's only
// field.
Enc(g: mut Gen, ty: Type, e: Code, put: Code, alone: bool): Code {
    if ty.Kind() == TypeKind.Optional {
        x := g.Fresh("x")
        inner := Enc(g, ty.Elem(), x, put, alone)
        return quote {
            match $e {
                some($x) {
                    $inner
                }
                none {
                    try $put(w, "", o, $(Bool(alone)))
                }
            }
        }
    }
    s := ty.Text()
    if s == "str" {
        return quote {
            try $put(w, *$e, o, $(Bool(alone)))
        }
    }
    if s == "bool" {
        return quote {
            try $put(w, if *$e { "true" } else { "false" }, o, false)
        }
    }
    return quote {
        try $e.Format(w)
    }
}

// Dec reads field i of r as ty into the local x, then counts it: after
// the code, x holds it or FromCsv has failed. pf is the gen line's f32
// reader (EmitF32), when T has an f32 field.
Dec(g: mut Gen, ty: Type, x: Code, pf: Code): Code {
    s := g.Fresh("s")
    v := Read(g, ty, s, pf)
    return quote {
        $s := r.Field(i) else {
            if i < r.Len() {
                fail Utf8Error
            }
            fail ParseError
        }
        $(v.code)
        $x := $(v.x)
        i += 1
    }
}

// Read turns the str local s into ty: after code, x holds it or the
// method has failed with ParseError (or OutOfMemory).
Read(g: mut Gen, ty: Type, s: Code, pf: Code): (code: Code, x: Code) {
    x := g.Fresh("x")
    if ty.Kind() == TypeKind.Optional {
        inner := Read(g, ty.Elem(), s, pf)
        return (code: quote {
            $x: $(ty.Name()) = if $s.Len() == 0 {
                none
            } else {
                $(inner.code)
                some($(inner.x))
            }
        }, x: x)
    }
    t := ty.Text()
    if t == "str" {
        return (code: quote {
            $x := try a.Dup($s)
        }, x: x)
    }
    if t == "bool" {
        return (code: quote {
            $x := match $s {
                "true" { true }
                "false" { false }
                _ { fail ParseError }
            }
        }, x: x)
    }
    if t == "f64" {
        return (code: quote {
            $x := try $s.ParseFloat()
        }, x: x)
    }
    if t == "f32" {
        // The text's correctly rounded f32 (EmitF32), not Cast[f32] of
        // the f64, which would round twice (csv-11).
        return (code: quote {
            $x := $pf($s) else {
                fail ParseError
            }
        }, x: x)
    }
    if t == "u8" || t == "u16" || t == "u32" || t == "u64" || t == "uint" {
        // Decimal digits only, no sign, at most 2^64 - 1, as
        // strconv.ParseUint(s, 10, bits) reads them: through u64, so every
        // value written reads back, then range-checked for the narrower
        // types (csv-9: through ParseInt, "-0" and "+1" read).
        n := g.Fresh("n")
        top := if t == "u8" {
            quote { $n > 255 }
        } else if t == "u16" {
            quote { $n > 65535 }
        } else if t == "u32" {
            quote { $n > 4294967295 }
        } else {
            quote { false }
        }
        b := g.Fresh("b")
        k := g.Fresh("k")
        dg := g.Fresh("d")
        return (code: quote {
            $b := $s.AsBytes()
            if $b.Len() == 0 {
                fail ParseError
            }
            var $n: u64 = 0
            for $k in 0..$b.Len() {
                if $b[$k] < 0x30 || $b[$k] > 0x39 {
                    fail ParseError
                }
                $dg := u64($b[$k] - 0x30)
                if $n > 1844674407370955161 || ($n == 1844674407370955161 && $dg > 5) {
                    fail ParseError
                }
                $n = $n * 10 + $dg
            }
            if $top {
                fail ParseError
            }
            $x := Cast[$(ty.Name())]($n)
        }, x: x)
    }
    // A signed type: through i64, then range-checked.
    n := g.Fresh("n")
    check := if t == "i8" {
        quote { $n < -128 || $n > 127 }
    } else if t == "i16" {
        quote { $n < -32768 || $n > 32767 }
    } else if t == "i32" {
        quote { $n < -2147483648 || $n > 2147483647 }
    } else {
        quote { false }
    }
    return (code: quote {
        $n := try $s.ParseInt()
        if $check {
            fail ParseError
        }
        $x := Cast[$(ty.Name())]($n)
    }, x: x)
}

// EmitField emits, once per gen line, the private field writer
// F(w, s, o, alone): s as csv.WriteRecord writes one field, quoted when it
// holds the delimiter, the quote, CR or LF, or when it is empty and alone
// in its record; and returns its name.
EmitField(g: mut Gen): Code {
    f := g.Fresh("CsvField")
    g.Emit(quote {
        $(f)(w: mut Writer, s: str, o: &csv.Options, alone: bool): WriteError!void {
            b := s.AsBytes()
            var stops: [3]u8 = [o.delimiter, o.quote_byte, 0x0A]
            var q: [1]u8 = [o.quote_byte]
            if !(alone && b.Len() == 0) && simd.IndexAny(b, stops[..]).IsNone() && simd.IndexByte(b, 0x0D).IsNone() {
                try w.Bytes(b)
                return
            }
            try w.Bytes(q[..])
            var at := 0
            for at < b.Len() {
                k := simd.IndexByte(b[at..], o.quote_byte) else {
                    try w.Bytes(b[at..])
                    break
                }
                try w.Bytes(b[at..at + k + 1])
                try w.Bytes(q[..])
                at += k + 1
            }
            try w.Bytes(q[..])
        }
    })
    return f
}

// EmitGuard emits, once per gen line, the private G(o) that WriteCsv and
// WriteCsvHeader call first, and returns its name. It panics, with the
// messages of csv.WriteRecord's own check (CheckOptions, which is private
// to std.csv, so its body is emitted here), on options no reader can use:
// a delimiter or quote that is CR or LF, the two equal, a negative
// field_bytes or record_fields below 1 (csv-2). When the struct has a
// number field it also panics when the delimiter or the quote is a byte of
// a number's text, since numbers are written bare.
EmitGuard(g: mut Gen, numbers: bool): Code {
    f := g.Fresh("CsvOptions")
    digits := if numbers {
        quote {
            digits := "0123456789+-.eEnaif".AsBytes()
            var both: [2]u8 = [o.delimiter, o.quote_byte]
            if simd.IndexAny(digits, both[..]).IsSome() {
                Panic("csv.Codec: WriteCsv writes numbers bare, so the delimiter and the quote cannot be a byte of a number")
            }
        }
    } else {
        quote {}
    }
    g.Emit(quote {
        $(f)(o: &csv.Options) {
            if o.delimiter == 0x0A || o.delimiter == 0x0D || o.quote_byte == 0x0A || o.quote_byte == 0x0D {
                Panic("csv: the delimiter and the quote cannot be CR or LF")
            }
            if o.delimiter == o.quote_byte {
                Panic("csv: the delimiter and the quote must differ")
            }
            if o.field_bytes < 0 || o.record_fields < 1 {
                Panic("csv: field_bytes must be at least 0 and record_fields at least 1")
            }
            $digits
        }
    })
    return f
}

// EmitF32 emits, once per gen line whose struct has an f32 field, a
// private function F(t: str): ?f32 into the user's module: the f32
// nearest to the number t, ties to even, or none past f32's range, as Go's
// strconv.ParseFloat(t, 32); inf, -inf and nan read as themselves. It is
// json.Codec's helper (std/json/codec.mk, EmitF32, which documents the
// algorithm: one correctly rounded ParseFloat, and an exact decimal
// comparison only when the f64 lands on an f32 midpoint, after Boldo and
// Melquiond), emitted here too because no public std function parses an
// f32 and the emitted code reaches only std's public names. O(len(t))
// but for the midpoint case, O(len(t) + 131 * 176); allocates nothing.
EmitF32(g: mut Gen): Code {
    f := g.Fresh("CsvF32")
    c := g.Fresh("CsvF32Cmp")
    g.Emit(quote {
        $(f)(t: str): ?f32 {
            d := t.ParseFloat() else {
                return none
            }
            neg := d < 0.0
            ad := if neg { -d } else { d }
            // inf, -inf and nan, which WriteCsv writes for an f32 that
            // holds one, read back as themselves.
            if ad - ad != 0.0 {
                return some(Cast[f32](d))
            }
            var r: f32 = 0.0
            if ad > 3.4028235677973366e38 {
                return none
            }
            if ad == 3.4028235677973366e38 {
                if $(c)(t, ad) >= 0 {
                    return none
                }
                r = Cast[f32](3.4028234663852886e38)
            } else {
                r = Cast[f32](ad)
                back := Cast[f64](r)
                if back != ad {
                    other := 2.0 * ad - back
                    if other < 3.4028235677973366e38 && Cast[f64](Cast[f32](other)) == other && (back + other) * 0.5 == ad {
                        k := $(c)(t, ad)
                        if k != 0 {
                            pick := if (k > 0) == (other > back) { other } else { back }
                            r = Cast[f32](pick)
                        }
                    }
                }
            }
            return some(if neg { -r } else { r })
        }

        $(c)(t: str, m: f64): int {
            var x := m
            var e := 0
            for x >= 67108864.0 {
                x = x * 0.5
                e += 1
            }
            for x < 33554432.0 {
                x = x * 2.0
                e -= 1
            }
            var dg: [160]u8 = [0; 160]
            var nd := 0
            var v := Cast[u64](x)
            for v > 0 {
                dg[nd] = Trunc[u8](v % 10)
                v = v / 10
                nd += 1
            }
            var steps := e
            var mul: u64 = 2
            if e < 0 {
                steps = -e
                mul = 5
            }
            for _ in 0..steps {
                var carry: u64 = 0
                for i in 0..nd {
                    p := u64(dg[i]) * mul + carry
                    dg[i] = Trunc[u8](p % 10)
                    carry = p / 10
                }
                for carry > 0 {
                    dg[nd] = Trunc[u8](carry % 10)
                    carry = carry / 10
                    nd += 1
                }
            }
            mdp := if e < 0 { nd + e } else { nd }
            var lo := 0
            for dg[lo] == 0 {
                lo += 1
            }
            b := t.AsBytes()
            n := b.Len()
            var i := 0
            if i < n && (b[i] == 0x2D || b[i] == 0x2B) {
                i += 1
            }
            var j := i
            for j < n && (b[j] | 0x20) != 0x65 {
                j += 1
            }
            var ex := 0
            if j < n {
                var q := j + 1
                var eneg := false
                if q < n && (b[q] == 0x2D || b[q] == 0x2B) {
                    eneg = b[q] == 0x2D
                    q += 1
                }
                for q < n {
                    if ex < 100000000 {
                        ex = ex * 10 + Cast[int](b[q] - 0x30)
                    }
                    q += 1
                }
                if eneg {
                    ex = -ex
                }
            }
            var xdp := 0
            var seen := false
            var dot := false
            var first := j
            for p in i..j {
                ch := b[p]
                if ch == 0x2E {
                    dot = true
                    continue
                }
                if !seen {
                    if ch == 0x30 {
                        if dot {
                            xdp -= 1
                        }
                        continue
                    }
                    seen = true
                    first = p
                }
                if !dot {
                    xdp += 1
                }
            }
            if !seen {
                return -1
            }
            xdp += ex
            if xdp != mdp {
                return if xdp > mdp { 1 } else { -1 }
            }
            var at := nd - 1
            for p in first..j {
                ch := b[p]
                if ch == 0x2E {
                    continue
                }
                dx := ch - 0x30
                if at < lo {
                    if dx != 0 {
                        return 1
                    }
                    continue
                }
                if dx != dg[at] {
                    return if dx > dg[at] { 1 } else { -1 }
                }
                at -= 1
            }
            if at >= lo {
                return -1
            }
            return 0
        }
    })
    return f
}

// Supported reports whether ty has a CSV field form.
Supported(ty: Type): bool {
    b := Base(ty)
    if b.Kind() != TypeKind.Scalar {
        return false
    }
    s := b.Text()
    return s == "str" || s == "bool" || IsNumber(b)
}

// Base is U of ?U, else ty itself. A ??U is not supported: Supported sees
// its ?U base.
Base(ty: Type): Type {
    if ty.Kind() == TypeKind.Optional {
        return ty.Elem()
    }
    return ty
}

// IsNumber reports whether ty is an integer type, f32 or f64.
IsNumber(ty: Type): bool {
    if ty.Kind() != TypeKind.Scalar {
        return false
    }
    s := ty.Text()
    return s == "i8" || s == "i16" || s == "i32" || s == "i64" || s == "int" || s == "u8" || s == "u16" || s == "u32" || s == "u64" || s == "uint" || s == "f32" || s == "f64"
}

// Bool is the literal true or false.
Bool(b: bool): Code {
    if b {
        return quote { true }
    }
    return quote { false }
}
