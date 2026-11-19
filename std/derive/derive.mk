// Why: equality, hashing and formatting of a plain struct or enum are
// mechanical, and writing them by hand invites a missed field.
//
// What: the std generators derive.Eq, derive.Hash and derive.Format, with
// their enum forms EqEnum, HashEnum and FormatEnum and the helpers Pattern
// and Bound. They run at compile time only.
//
// How: each emits one impl over the fields in declaration order; Hash and
// Format print internal/gen/testdata/derive_hash.golden and
// derive_format.golden. Literal names in a quote (Eq, Hash, Hasher, Format,
// Writer, WriteError) resolve in this module's imports, then the prelude,
// never in this module's own items, so Eq inside the quotes is the prelude
// interface, not the generator below.
//
// Enums: one match on self with an arm per variant, in declaration
// order. A payload field f is bound by name to f1 (and, on other's side of
// Eq, f2): a name ending in a digit never equals self, other, h, w or
// another binding, so no payload can shadow the method's own parameters.
// Matching &self binds references, so a binding's Eq, Hash and Format are
// the field type's own (auto-deref), and Eq takes the other binding as is.
// The exact output for a sample enum is pinned by std/std-mem_derive_test.go
// (TestDeriveTargetsRun), which runs it natively.
//
// Any other kind fails the run at the type with E0903 (Gen.FailOn).
//
// Bound calls Must on the run's arena: its pages come from the evaluator,
// which never refuses one (it charges the bytes as fuel, so a runaway run
// ends in E0902 first), so Must cannot fire outside the soundness checker's
// injected allocation failures.

/// The generator behind `gen derive.Eq(T)`: it writes `impl Eq for T`,
/// whose Eq compares every field with the field type's own Eq, in
/// declaration order, and is true when all are equal. For an enum it
/// compares the variants first, then the payload fields of the matching
/// variant. Call it as `a.Eq(&b)`: `==` stays undefined on T.
///
/// E0903 at T when T is not a struct or an enum, or is an enum with no
/// variants. A field whose type has no Eq is E0905 at the gen line, and a
/// second Eq for T is E0704. Runs at compile time, O(fields); allocates
/// the emitted code in the run's arena. The emitted Eq allocates nothing.
export Eq(g: mut Gen, t: Type) {
    if t.Kind() == TypeKind.Enum {
        EqEnum(g, t)
        return
    }
    if t.Kind() != TypeKind.Struct {
        g.FailOn(t, "derive.Eq: only a struct or an enum can be derived")
        return
    }
    var body := g.Block()
    for f in t.Fields() {
        body.Add(quote {
            if !self.$(f.Name()).Eq(&other.$(f.Name())) {
                return false
            }
        })
    }
    g.Emit(quote {
        impl Eq for $(t.Name()) {
            Eq(&self, other: &$(t.Name())): bool {
                $body
                return true
            }
        }
    })
}

/// The generator behind `gen derive.Hash(T)`: it writes `impl Hash for T`,
/// whose Hash feeds every field to the Hasher through the field type's own
/// Hash, in declaration order, so two values that are Eq field by field
/// hash alike. For an enum it feeds the variant's index first, so .A and .B
/// with equal payloads hash differently. With derive.Eq, T can be a Map
/// key.
///
/// E0903 and E0905 as for Eq. Runs at compile time, O(fields); allocates
/// the emitted code in the run's arena. The emitted Hash allocates nothing.
export Hash(g: mut Gen, t: Type) {
    if t.Kind() == TypeKind.Enum {
        HashEnum(g, t)
        return
    }
    if t.Kind() != TypeKind.Struct {
        g.FailOn(t, "derive.Hash: only a struct or an enum can be derived")
        return
    }
    var body := g.Block()
    for f in t.Fields() {
        body.Add(quote {
            self.$(f.Name()).Hash(h)
        })
    }
    g.Emit(quote {
        impl Hash for $(t.Name()) {
            Hash(&self, h: mut Hasher) {
                $body
            }
        }
    })
}

/// The generator behind `gen derive.Format(T)`: it writes
/// `impl Format for T`, so `{}` prints a T. A struct prints as
/// `Name(f1: v1, f2: v2)`, or `Name()` with no fields; an enum value prints
/// as its variant's name, followed by `(f1: v1, ...)` when the variant has a
/// payload. Each value prints through its own Format, so a str field
/// prints as written, without quotes. This is the structural form of
/// printing: there is no debug format spec.
///
/// E0903 and E0905 as for Eq. Runs at compile time, O(fields); allocates
/// the emitted code in the run's arena. The emitted Format writes to its
/// Writer and fails only when the Writer does.
export Format(g: mut Gen, t: Type) {
    if t.Kind() == TypeKind.Enum {
        FormatEnum(g, t)
        return
    }
    if t.Kind() != TypeKind.Struct {
        g.FailOn(t, "derive.Format: only a struct or an enum can be derived")
        return
    }
    var body := g.Block()
    body.Add(quote {
        try w.Str($(g.Str(t.Text())))
        try w.Str("(")
    })
    for i, f in t.Fields() {
        if i > 0 {
            body.Add(quote {
                try w.Str(", ")
            })
        }
        body.Add(quote {
            try w.Str($(g.Str(f.Name())))
            try w.Str(": ")
            try self.$(f.Name()).Format(w)
        })
    }
    body.Add(quote {
        try w.Str(")")
    })
    g.Emit(quote {
        impl Format for $(t.Name()) {
            Format(&self, w: mut Writer): WriteError!void {
                $body
            }
        }
    })
}

// EqEnum: for each variant V, `.V(..) { match other { .V(..) { compare;
// return true } _ { return false } } }`; the `_` arm is left out when V is
// the only variant, since it could never match.
EqEnum(g: mut Gen, t: Type) {
    vs := t.Variants()
    if vs.Len() == 0 {
        g.FailOn(t, "derive.Eq: an enum with no variants has no values to compare")
        return
    }
    var arms := g.Block()
    for v in vs {
        var cmp := g.Block()
        for f in v.Fields() {
            cmp.Add(quote {
                if !$(Bound(&*g, f, 1)).Eq($(Bound(&*g, f, 2))) {
                    return false
                }
            })
        }
        cmp.Add(quote {
            return true
        })
        var inner := g.Block()
        inner.Add(quote {
            $(Pattern(&*g, v, 2)) {
                $cmp
            }
        })
        if vs.Len() > 1 {
            inner.Add(quote {
                _ {
                    return false
                }
            })
        }
        arms.Add(quote {
            $(Pattern(&*g, v, 1)) {
                match other {
                    $inner
                }
            }
        })
    }
    g.Emit(quote {
        impl Eq for $(t.Name()) {
            Eq(&self, other: &$(t.Name())): bool {
                match self {
                    $arms
                }
            }
        }
    })
}

// HashEnum: for each variant, its index through h.U64, then each payload
// field's Hash. An enum with no variants is E0903 at the type, as in EqEnum:
// an empty `match self` would be generated-invalid (E0905), blaming std.
HashEnum(g: mut Gen, t: Type) {
    vs := t.Variants()
    if vs.Len() == 0 {
        g.FailOn(t, "derive.Hash: an enum with no variants has no values to hash")
        return
    }
    var arms := g.Block()
    for i, v in vs {
        var body := g.Block()
        body.Add(quote {
            h.U64($i)
        })
        for f in v.Fields() {
            body.Add(quote {
                $(Bound(&*g, f, 1)).Hash(h)
            })
        }
        arms.Add(quote {
            $(Pattern(&*g, v, 1)) {
                $body
            }
        })
    }
    g.Emit(quote {
        impl Hash for $(t.Name()) {
            Hash(&self, h: mut Hasher) {
                match self {
                    $arms
                }
            }
        }
    })
}

// FormatEnum: for each variant, its name, then (f: v, ...) when it has a
// payload, as Format writes a struct. An enum with no variants is E0903 at
// the type, as in EqEnum.
FormatEnum(g: mut Gen, t: Type) {
    vs := t.Variants()
    if vs.Len() == 0 {
        g.FailOn(t, "derive.Format: an enum with no variants has no values to format")
        return
    }
    var arms := g.Block()
    for v in vs {
        var body := g.Block()
        body.Add(quote {
            try w.Str($(g.Str(v.Name())))
        })
        fs := v.Fields()
        if fs.Len() > 0 {
            body.Add(quote {
                try w.Str("(")
            })
            for i, f in fs {
                if i > 0 {
                    body.Add(quote {
                        try w.Str(", ")
                    })
                }
                body.Add(quote {
                    try w.Str($(g.Str(f.Name())))
                    try w.Str(": ")
                    try $(Bound(&*g, f, 1)).Format(w)
                })
            }
            body.Add(quote {
                try w.Str(")")
            })
        }
        arms.Add(quote {
            $(Pattern(&*g, v, 1)) {
                $body
            }
        })
    }
    g.Emit(quote {
        impl Format for $(t.Name()) {
            Format(&self, w: mut Writer): WriteError!void {
                match self {
                    $arms
                }
            }
        }
    })
}

// Pattern is `.V(f: f1, g: g1)` for variant v on side 1 (`f2` on side 2), or
// `.V` when v has no payload.
Pattern(g: &Gen, v: Variant, side: int): Code {
    fs := v.Fields()
    if fs.Len() == 0 {
        return quote { .$(v.Name()) }
    }
    var binds := quote { $(fs[0].Name()): $(Bound(g, fs[0], side)) }
    for i in 1..fs.Len() {
        binds = binds.Then(quote { , $(fs[i].Name()): $(Bound(g, fs[i], side)) })
    }
    return quote { .$(v.Name())($binds) }
}

// Bound is the name payload field f is bound to on a side (1 or 2): f's name
// followed by the digit, in the run's arena. Built with a Text, not
// Arena.Fmt("{name}{side}"): the evaluator refuses a format string with an
// empty literal piece (E0901, pointer-bytes in Writer.Bytes: lower gives the
// empty piece the dangling pointer 1).
Bound(g: &Gen, f: Field, side: int): str {
    a := g.Arena()
    var t := Text.New(a)
    // TestDeriveEnumFormsDeclared pins the statement form below.
    t.Push(f.Name()).Must()
    if side == 1 {
        t.Push("1").Must()
    } else {
        t.Push("2").Must()
    }
    return a.Dup(t.Str()).Must()
}
