// Why: writing JSON encoders and decoders by hand for every struct is
// tedious and error-prone; `gen json.Codec(T)` writes them at compile time.
//
// What: Codec, the generator: for a struct or an enum T it emits
// `impl json.Json for T`, whose FormatJson writes T as compact JSON, and
// T.FromJson(a, v), which reads T back from a parsed Value.
//
// How: Codec walks T through the Gen API and emits quoted code. The emitted
// code calls only std.json's public names: Value's variants and accessors,
// Value's Format for strings, the Format of integers and floats, Mismatch
// and Error, so a codec adds no public helper to std.json. It writes them as
// json.X, literal names in its quotes, which resolve to this module's
// exported items (a module names itself by the last segment of its path);
// they come out pinned, so the user's module needs no import named json.
//
// Shapes:
// - a struct is an object of its exported fields, in declaration order, by
//   field name; FromJson ignores unknown members, takes the last of
//   duplicates, and fails with Mismatch on a missing field that is not ?T;
// - ?T is the value or null; FromJson takes a missing member or null as none;
// - str, bool, the integers (the signed ones and u8 to u32 through i64,
//   u64 and uint through u64, so every value written reads back), f32 and
//   f64 (a NaN or an infinity is written as null, which FromJson then
//   refuses), []T and List[T] as arrays, and any other struct or enum
//   through its own FormatJson and FromJson (its own gen line);
// - every field must be exported, since FromJson builds T from the JSON
//   alone; any other field, and a field of a type with no JSON form
//   (char, [N]T, references, json.Value itself), is E0903 at the field. A
//   struct or enum field whose type has no gen line of its own cannot be
//   told from the Gen API (Type.Methods answers only for interfaces), so
//   it is E0905 in the emitted code;
// - an enum is externally tagged, as serde's default: a variant with no
//   payload is the string "V", one with a payload {"V":{fields}}.
// Mismatch from FromJson carries offset -1: a Value keeps no offsets.

/// Codec writes, for the struct or enum t, `impl json.Json for T`, whose
/// FormatJson writes T as compact JSON, and `T.FromJson(a, v)`, which reads
/// T back from a parsed Value. Use it as `gen json.Codec(T)`.
///
/// A struct is an object of its exported fields, in declaration order,
/// keyed by field name. FromJson ignores unknown members, takes the last of
/// duplicates, and fails with Mismatch on a missing field that is not ?T. A
/// ?T is the value or null, and a missing member reads as none. str, bool,
/// the integers, f32, f64, []T and List[T] have their JSON forms; a NaN or
/// an infinity is written as null, which FromJson then refuses. A field of
/// another struct or enum type uses that type's own FormatJson and
/// FromJson, so it needs its own gen line (E0905 in the emitted code
/// without one). An enum is tagged by variant name: a variant with no
/// payload is the string "V", one with a payload {"V":{fields}}.
///
/// E0903 at a field that is not exported or whose type has no JSON form
/// (char, [N]T, references, json.Value). Runs at compile time; the emitted
/// FromJson allocates in its arena, FormatJson nothing. FromJson copies
/// every string it reads into a, so its result borrows a alone (`from a`)
/// and the parsed Value may end before it.
export Codec(g: mut Gen, t: Type) {
    if t.Kind() == TypeKind.Struct {
        CodecStruct(g, t)
        return
    }
    if t.Kind() == TypeKind.Enum {
        CodecEnum(g, t)
        return
    }
    g.FailOn(t, "json.Codec: only a struct or an enum can have a codec")
}

// CodecStruct: FormatJson writes {"f":v,...}; FromJson reads the members
// into one ?F local per field in one pass, then builds T.
CodecStruct(g: mut Gen, t: Type) {
    var enc := g.Block()
    var n := 0
    for f in t.Fields() {
        if !f.Exported() {
            g.FailAt(f, "json.Codec: FromJson builds T from its exported fields, so every field must be exported")
            return
        }
        if !Supported(f.Type()) {
            g.FailAt(f, NoForm(f.Type()))
            return
        }
        head := if n > 0 { "," } else { "{" }
        place := quote { (&self.$(f.Name())) }
        e := Enc(g, f.Type(), place)
        enc.Add(quote {
            try w.Str($(g.Str(KeyText(&*g, head, f.Name()))))
            $e
        })
        n += 1
    }
    close := if n == 0 { "{}" } else { "}" }
    enc.Add(quote {
        try w.Str($(g.Str(close)))
    })
    src := quote { v }
    pf := F32Helper(g, t.Fields())
    dec := DecFields(g, t.Fields(), src, t.Name(), pf)
    g.Emit(quote {
        impl json.Json for $(t.Name()) {
            FormatJson(&self, w: mut Writer): WriteError!void {
                $enc
            }
        }

        $(t.Name()).FromJson(a: &Arena, v: &json.Value): json.Error!$(t.Name()) from a {
            $dec
        }
    })
}

// CodecEnum: one match arm per variant each way.
CodecEnum(g: mut Gen, t: Type) {
    vs := t.Variants()
    if vs.Len() == 0 {
        g.FailOn(t, "json.Codec: an enum with no variants has no values to encode")
        return
    }
    var arms := g.Block()
    var units := g.Block()
    var tagged := g.Block()
    // Every variant's fields are checked before anything is emitted; the
    // f32 helper is emitted once when some field reads an f32.
    var any_f32 := false
    for v in vs {
        for f in v.Fields() {
            if !Supported(f.Type()) {
                g.FailAt(f, NoForm(f.Type()))
                return
            }
            if HasF32(f.Type()) {
                any_f32 = true
            }
        }
    }
    pf := if any_f32 { EmitF32(g) } else { quote {} }
    for v in vs {
        fs := v.Fields()
        if fs.Len() == 0 {
            arms.Add(quote {
                .$(v.Name()) {
                    try w.Str($(g.Str(Quoted(&*g, v.Name()))))
                }
            })
            units.Add(quote {
                $(g.Str(v.Name())) {
                    return $(t.Name()).$(v.Name())
                }
            })
            continue
        }
        var body := g.Block()
        var binds := quote {}
        var i := 0
        for f in fs {
            b := g.Fresh(f.Name())
            if i > 0 {
                binds = binds.Then(quote { , })
            }
            binds = binds.Then(quote { $(f.Name()): $b })
            e := Enc(g, f.Type(), b)
            // The first key carries the tag: {"V":{"f":
            key := if i == 0 { TagKey(&*g, v.Name(), f.Name()) } else { KeyText(&*g, ",", f.Name()) }
            body.Add(quote {
                try w.Str($(g.Str(key)))
                $e
            })
            i += 1
        }
        body.Add(quote {
            try w.Str("}}")
        })
        arms.Add(quote {
            .$(v.Name())($binds) {
                $body
            }
        })
        src := quote { (&p.value) }
        make := quote { $(t.Name()).$(v.Name()) }
        dec := DecFields(g, fs, src, make, pf)
        tagged.Add(quote {
            $(g.Str(v.Name())) {
                $dec
            }
        })
    }
    g.Emit(quote {
        impl json.Json for $(t.Name()) {
            FormatJson(&self, w: mut Writer): WriteError!void {
                match self {
                    $arms
                }
            }
        }

        $(t.Name()).FromJson(a: &Arena, v: &json.Value): json.Error!$(t.Name()) from a {
            match v {
                .Text(s) {
                    match *s {
                        $units
                        _ {}
                    }
                }
                .Object(members) {
                    ms := members.Items()
                    if ms.Len() == 1 {
                        p := &ms[0]
                        match p.key {
                            $tagged
                            _ {}
                        }
                    }
                }
                _ {}
            }
            fail json.Mismatch(offset: -1)
        }
    })
}

// DecFields reads the object src (a &json.Value expression) into one local
// per field, then returns make(field: local, ...): for a struct, T itself;
// for an enum variant, T.V. Every field is exported (CodecStruct refuses
// any other, and a variant's fields all are). With no field the object's
// members are not read, so none is bound (an unused binding is E0208 in
// the emitted code).
DecFields(g: mut Gen, fs: []Field, src: Code, make: Code, pf: Code): Code {
    mems := g.Fresh("members")
    ms := g.Fresh("ms")
    k := g.Fresh("k")
    mv := quote { (&$ms[$k].value) }
    var decls := g.Block()
    var arms := g.Block()
    var checks := g.Block()
    var args := quote {}
    var n := 0
    for f in fs {
        x := g.Fresh(f.Name())
        ft := f.Type()
        if n > 0 {
            args = args.Then(quote { , })
        }
        if ft.Kind() == TypeKind.Optional {
            // ?U: none when missing or null.
            decls.Add(quote {
                var $x: $(ft.Name()) = none
            })
            d := Dec(g, ft, mv, pf)
            arms.Add(quote {
                $(g.Str(f.Name())) {
                    $(d.code)
                    $x = $(d.x)
                }
            })
            args = args.Then(quote { $(f.Name()): $x })
        } else {
            decls.Add(quote {
                var $x: ?$(ft.Name()) = none
            })
            d := Dec(g, ft, mv, pf)
            arms.Add(quote {
                $(g.Str(f.Name())) {
                    $(d.code)
                    $x = some($(d.x))
                }
            })
            v := g.Fresh(f.Name())
            checks.Add(quote {
                $v := $x else { fail json.Mismatch(offset: -1) }
            })
            args = args.Then(quote { $(f.Name()): $v })
        }
        n += 1
    }
    if n == 0 {
        return quote {
            match $src {
                .Object(members: _) {}
                _ {
                    fail json.Mismatch(offset: -1)
                }
            }
            return $make($args)
        }
    }
    return quote {
        $decls
        match $src {
            .Object(members: $mems) {
                $ms := $mems.Items()
                for $k in 0..$ms.Len() {
                    match $ms[$k].key {
                        $arms
                        _ {}
                    }
                }
            }
            _ {
                fail json.Mismatch(offset: -1)
            }
        }
        $checks
        return $make($args)
    }
}

// Enc writes the value that e (a &ty expression) refers to; w is the
// emitted method's writer.
Enc(g: mut Gen, ty: Type, e: Code): Code {
    k := ty.Kind()
    if k == TypeKind.Scalar {
        s := ty.Text()
        if s == "str" {
            return quote {
                try json.Value.Text(s: *$e).Format(w)
            }
        }
        if s == "bool" {
            return quote {
                try w.Str(if *$e { "true" } else { "false" })
            }
        }
        if s == "f32" || s == "f64" {
            // NaN - NaN and inf - inf are NaN; JSON has neither.
            return quote {
                if *$e - *$e != 0.0 {
                    try w.Str("null")
                } else {
                    try $e.Format(w)
                }
            }
        }
        return quote {
            try $e.Format(w)
        }
    }
    if k == TypeKind.Optional {
        x := g.Fresh("x")
        inner := Enc(g, ty.Elem(), x)
        return quote {
            match $e {
                some($x) {
                    $inner
                }
                none {
                    try w.Str("null")
                }
            }
        }
    }
    if k == TypeKind.Slice || IsList(ty) {
        xs := g.Fresh("xs")
        i := g.Fresh("i")
        item := quote { (&$xs[$i]) }
        inner := Enc(g, ElemOf(ty), item)
        seq := if k == TypeKind.Slice { quote { *$e } } else { quote { $e.Items() } }
        return quote {
            $xs := $seq
            try w.Str("[")
            for $i in 0..$xs.Len() {
                if $i > 0 {
                    try w.Str(",")
                }
                $inner
            }
            try w.Str("]")
        }
    }
    return quote {
        try $e.FormatJson(w)
    }
}

// Dec reads the value src (a &json.Value expression) as ty: after code,
// the local x holds it, or the method has failed with Mismatch (or
// OutOfMemory, or a nested FromJson's error). For ?U, x is the ?U.
Dec(g: mut Gen, ty: Type, src: Code, pf: Code): (code: Code, x: Code) {
    x := g.Fresh("x")
    k := ty.Kind()
    if k == TypeKind.Scalar {
        s := ty.Text()
        if s == "str" {
            // A match, not Value.Str: the borrow checker refuses the
            // accessor's result pushed into a List in a loop. The text is
            // copied into a (Arena.Dup), so the value FromJson builds
            // holds no loan of the parsed tree: a str pushed into a List
            // made in a would otherwise store a loan of v into what v's
            // own Lists reach. So FromJson is declared `from a`:
            // a List of a record holding a str needs it, since the nested
            // FromJson's result is pushed into a List made in a.
            return (code: quote {
                $x := match $src {
                    .Text(s) {
                        try a.Dup(*s)
                    }
                    _ {
                        fail json.Mismatch(offset: -1)
                    }
                }
            }, x: x)
        }
        if s == "bool" {
            return (code: quote {
                $x := match $src {
                    .Bool(b) {
                        *b
                    }
                    _ {
                        fail json.Mismatch(offset: -1)
                    }
                }
            }, x: x)
        }
        if s == "f64" {
            return (code: quote {
                $x := $src.Float() else { fail json.Mismatch(offset: -1) }
            }, x: x)
        }
        if s == "f32" {
            // The text's correctly rounded f32, through the helper of this
            // gen line (EmitF32), not Cast[f32] of the f64.
            t := g.Fresh("text")
            return (code: quote {
                $x := match $src {
                    .Number(text: $t) {
                        $pf(*$t) else { fail json.Mismatch(offset: -1) }
                    }
                    _ {
                        fail json.Mismatch(offset: -1)
                    }
                }
            }, x: x)
        }
        n := g.Fresh("n")
        if s == "u64" || s == "uint" {
            // Through u64, so every value FormatJson writes reads back, as
            // Go's encoding/json does: the text is decimal digits
            // only (no sign, fraction or exponent), at most 2^64 - 1, as
            // Go's strconv.ParseUint(text, 10, 64) reads it ("007", which
            // only a Number made by hand can hold, is 7).
            t := g.Fresh("text")
            tb := g.Fresh("b")
            i := g.Fresh("i")
            d := g.Fresh("d")
            good := g.Fresh("good")
            return (code: quote {
                var $n: u64 = 0
                var $good := false
                match $src {
                    .Number(text: $t) {
                        $tb := $t.AsBytes()
                        $good = $tb.Len() > 0
                        for $i in 0..$tb.Len() {
                            if $tb[$i] < 0x30 || $tb[$i] > 0x39 {
                                $good = false
                                break
                            }
                            $d := u64($tb[$i] - 0x30)
                            if $n > 1844674407370955161 || ($n == 1844674407370955161 && $d > 5) {
                                $good = false
                                break
                            }
                            $n = $n * 10 + $d
                        }
                    }
                    _ {}
                }
                if !$good {
                    fail json.Mismatch(offset: -1)
                }
                $x := Cast[$(ty.Name())]($n)
            }, x: x)
        }
        // A signed or narrower unsigned type: through i64, then
        // range-checked.
        check := if s == "i8" {
            quote { $n < -128 || $n > 127 }
        } else if s == "i16" {
            quote { $n < -32768 || $n > 32767 }
        } else if s == "i32" {
            quote { $n < -2147483648 || $n > 2147483647 }
        } else if s == "u8" {
            quote { $n < 0 || $n > 255 }
        } else if s == "u16" {
            quote { $n < 0 || $n > 65535 }
        } else if s == "u32" {
            quote { $n < 0 || $n > 4294967295 }
        } else {
            quote { false }
        }
        return (code: quote {
            $n := $src.Int() else { fail json.Mismatch(offset: -1) }
            if $check {
                fail json.Mismatch(offset: -1)
            }
            $x := Cast[$(ty.Name())]($n)
        }, x: x)
    }
    if k == TypeKind.Optional {
        d := Dec(g, ty.Elem(), src, pf)
        return (code: quote {
            $x: $(ty.Name()) = match $src {
                .Null { none }
                _ {
                    $(d.code)
                    some($(d.x))
                }
            }
        }, x: x)
    }
    if k == TypeKind.Slice || IsList(ty) {
        l := g.Fresh("l")
        arr := g.Fresh("arr")
        it := g.Fresh("items")
        i := g.Fresh("i")
        item := quote { (&$it[$i]) }
        d := Dec(g, ElemOf(ty), item, pf)
        var make := quote {
            var $l := $(ty.Name()).New(a)
        }
        var done := quote {
            $x := $l
        }
        if k == TypeKind.Slice {
            make = quote {
                var $l := List[$(ty.Elem().Name())].New(a)
            }
            m := g.Fresh("m")
            done = quote {
                $m := $l.Done()
                $x := $m[..]
            }
        }
        return (code: quote {
            $make
            match $src {
                .Array(items: $arr) {
                    $it := $arr.Items()
                    for $i in 0..$it.Len() {
                        $(d.code)
                        try $l.Push($(d.x))
                    }
                }
                _ {
                    fail json.Mismatch(offset: -1)
                }
            }
            $done
        }, x: x)
    }
    return (code: quote {
        $x := try $(ty.Name()).FromJson(a, $src)
    }, x: x)
}

// HasF32 reports whether reading ty reads an f32 itself (through ?, []
// or List; a nested codec type reads its own).
HasF32(ty: Type): bool {
    k := ty.Kind()
    if k == TypeKind.Scalar {
        return ty.Text() == "f32"
    }
    if k == TypeKind.Optional || k == TypeKind.Slice {
        return HasF32(ty.Elem())
    }
    if IsList(ty) {
        return HasF32(ElemOf(ty))
    }
    return false
}

// F32Helper emits the f32 helper when a field of fs needs it and returns
// its name, else an empty Code that no emitted line splices.
F32Helper(g: mut Gen, fs: []Field): Code {
    for f in fs {
        if HasF32(f.Type()) {
            return EmitF32(g)
        }
    }
    return quote {}
}

// EmitF32 emits, once per gen line that reads an f32, a private function
// F(t: str): ?f32 into the user's module: the f32 nearest to the number t,
// ties to even, or none past f32's range, as Go's strconv.ParseFloat(t, 32).
// The emitted code reaches only std.json's public names, and none of them
// parses an f32, so the helper is emitted.
//
// Algorithm: d := ParseFloat(t) is the correctly rounded f64. Rounding d to
// f32 is then also correct, except when d is exactly the midpoint m of two
// adjacent f32 values while t is not: an f32 midpoint has 25 significant
// bits, so it is an f64, and one strictly between t and d would be nearer
// t than d is. Only then does the helper decide by an exact comparison of
// t's decimal digits with m's (the second function: m's exact decimal, at
// most 131 digits, built by doubling or by multiplying by 5 in a stack
// buffer, against t's digit stream), as the round-to-odd argument of
// Boldo and Melquiond says a wider correctly rounded result needs. That
// case costs O(len(t) + 131 * 176) steps; every other number costs one
// ParseFloat and one compare. The largest midpoint, 2^128 - 2^103, rounds
// to an infinity, so a t at or above it is none and one below it is f32's
// largest value. NaN and the infinities, which only a Value made by hand
// can hold ("nan" is ParseFloat's), give none past the range test, or NaN.
EmitF32(g: mut Gen): Code {
    f := g.Fresh("JsonF32")
    c := g.Fresh("JsonF32Cmp")
    g.Emit(quote {
        $(f)(t: str): ?f32 {
            d := t.ParseFloat() else {
                return none
            }
            neg := d < 0.0
            ad := if neg { -d } else { d }
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

// Supported reports whether ty has a JSON form the codec writes.
Supported(ty: Type): bool {
    k := ty.Kind()
    if k == TypeKind.Scalar {
        return ty.Text() != "char" && ty.Text() != "void"
    }
    if k == TypeKind.Optional || k == TypeKind.Slice {
        return Supported(ty.Elem())
    }
    if IsList(ty) {
        return Supported(ElemOf(ty))
    }
    if IsValue(ty) {
        // json.Value has no FromJson: it is not plain (its Lists cannot be
        // copied out of the parsed tree), and std.json has no deep copy.
        return false
    }
    return k == TypeKind.Struct || k == TypeKind.Enum
}

// NoForm is the E0903 message for a field whose type has no JSON form.
NoForm(ty: Type): str {
    if HasValue(ty) {
        return "json.Codec: a json.Value field has no codec: a Value is not plain, so FromJson cannot copy it out of the parsed tree; keep the Value itself, or give the field a codec type"
    }
    return "json.Codec: this field's type has no JSON form (str, bool, integers, f32, f64, ?T, []T, List[T], or a struct or enum with its own codec)"
}

// HasValue reports whether ty is json.Value or holds one through ?, [] or
// List.
HasValue(ty: Type): bool {
    k := ty.Kind()
    if k == TypeKind.Optional || k == TypeKind.Slice {
        return HasValue(ty.Elem())
    }
    if IsList(ty) {
        return HasValue(ElemOf(ty))
    }
    return IsValue(ty)
}

// IsValue reports whether ty is json.Value, by its name and its variants:
// a Type names no module, and only an interface answers Methods, so a user
// enum named Value is told apart by its shape.
IsValue(ty: Type): bool {
    if ty.Kind() != TypeKind.Enum || ty.Text() != "Value" {
        return false
    }
    vs := ty.Variants()
    if vs.Len() != 6 {
        return false
    }
    return vs[0].Name() == "Null" && vs[1].Name() == "Bool" && vs[2].Name() == "Number" && vs[3].Name() == "Text" && vs[4].Name() == "Array" && vs[5].Name() == "Object"
}

// IsList reports whether ty is the prelude List[U].
IsList(ty: Type): bool {
    return ty.Kind() == TypeKind.Struct && ty.Args().Len() == 1 && ty.Text() == "List"
}

// ElemOf is U of []U or List[U].
ElemOf(ty: Type): Type {
    if ty.Kind() == TypeKind.Slice {
        return ty.Elem()
    }
    return ty.Args()[0]
}

// KeyText is head followed by "name": in the run's arena. KeyText, Quoted
// and TagKey end in Must: a generator has no error path, and its arena
// failing ends the expansion, which no input of the program reaches.
KeyText(g: &Gen, head: str, name: str): str from g {
    return g.Arena().Fmt("{head}\"{name}\":", (head, name)).Must()
}

// Quoted is "name" in the run's arena.
Quoted(g: &Gen, name: str): str from g {
    return g.Arena().Fmt("\"{name}\"", (name)).Must()
}

// TagKey is {"variant":{"field": in the run's arena.
TagKey(g: &Gen, variant: str, field: str): str from g {
    return g.Arena().Fmt("{{\"{variant}\":{{\"{field}\":", (variant, field)).Must()
}
