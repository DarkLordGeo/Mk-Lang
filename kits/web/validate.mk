// Why: request structs need field checks (non-empty, bounds) that answer 422
// with a clear message; writing them by hand for every struct is repetitive.
//
// What: `gen web.Validate(T, rules)`, which reads T's fields and a rule text
// and emits T.Problem and T.Validate.
//
// How: the rule text is `field: rule, rule; field: rule`. The rules are
// `nonempty`, `min N` and `max N`: on a `str` field, nonempty asks for at
// least one byte and min and max bound its length in bytes; on an integer
// field, min and max bound its value. A rule naming no field of T, a rule
// a field's type does not take, a bound outside an integer field's range,
// or a malformed rule is a compile-time error at the `gen` line (E0903).
// Each rule becomes one comparison in the emitted code, in the order
// written.

import std.http

/// Emits, for the struct t and the rule text rules:
/// `T.Problem(&self): ?str`, the message of the first rule self breaks
/// ("qty: must be at most 1000"), or none; and
/// `T.Validate(&self): http.Error!void`, which fails with Status(422)
/// when Problem is not none. Both are O(rules) and allocate nothing; the
/// messages are static text. Runs at compile time.
export Validate(g: mut Gen, t: Type, rules: str) {
    if t.Kind() != TypeKind.Struct {
        g.FailOn(t, "web.Validate: only a struct's fields can be validated")
        return
    }
    a := g.Arena()
    var checks := g.Block()
    for clause in rules.Split(";") {
        c := clause.Trim()
        if c.Len() == 0 {
            continue
        }
        colon := c.Find(":") else {
            g.FailOn(t, "web.Validate: a clause is `field: rule, rule`, and this one has no `:`")
            return
        }
        name := c[..colon].Trim()
        var found := false
        for f in t.Fields() {
            if f.Name() != name {
                continue
            }
            found = true
            text := IsText(f.Type())
            if !text && !IsInteger(f.Type()) {
                g.FailAt(f, "web.Validate: only a str or an integer field takes rules")
                return
            }
            for r in c[colon + 1..].Split(",") {
                rule := r.Trim()
                if rule == "nonempty" {
                    if !text {
                        g.FailAt(f, "web.Validate: nonempty is a rule of a str field")
                        return
                    }
                    msg := g.Str(Message(a, name, "must not be empty", "", ""))
                    checks.Add(quote {
                        if self.$(g.Ident(name)).Len() == 0 {
                            return some($msg)
                        }
                    })
                    continue
                }
                var bound := ""
                var below := false
                if rule.StartsWith("min ") {
                    bound = rule[4..].Trim()
                    below = true
                } else if rule.StartsWith("max ") {
                    bound = rule[4..].Trim()
                } else {
                    g.FailAt(f, "web.Validate: a rule is nonempty, min N or max N")
                    return
                }
                n := bound.ParseInt() else {
                    g.FailAt(f, "web.Validate: min and max take a decimal integer")
                    return
                }
                if text && n < 0 {
                    g.FailAt(f, "web.Validate: a length bound is not negative")
                    return
                }
                if !text && !InRange(f.Type(), n) {
                    // Here, not as generated code that fails to check
                    // (E0905): the comparison's literal must fit the type.
                    g.FailAt(f, "web.Validate: min and max must lie in the field type's range")
                    return
                }
                what := if below { "must be at least " } else { "must be at most " }
                tail := if text { " bytes long" } else { "" }
                msg := g.Str(Message(a, name, what, bound, tail))
                if text && below {
                    checks.Add(quote {
                        if self.$(g.Ident(name)).Len() < $n {
                            return some($msg)
                        }
                    })
                } else if text {
                    checks.Add(quote {
                        if self.$(g.Ident(name)).Len() > $n {
                            return some($msg)
                        }
                    })
                } else if below {
                    checks.Add(quote {
                        if self.$(g.Ident(name)) < $n {
                            return some($msg)
                        }
                    })
                } else {
                    checks.Add(quote {
                        if self.$(g.Ident(name)) > $n {
                            return some($msg)
                        }
                    })
                }
            }
        }
        if !found {
            g.FailOn(t, "web.Validate: a clause names a field the struct does not have")
            return
        }
    }
    pub := if t.Exported() { quote { export } } else { quote {} } // export as T is
    g.Emit(quote {
        $pub $(t.Name()).Problem(&self): ?str {
            $checks
            return none
        }

        $pub $(t.Name()).Validate(&self): http.Error!void {
            if self.Problem().IsSome() {
                fail http.Status(code: 422)
            }
        }
    })
}

// Message is "<field>: <what><bound><tail>", in a.
Message(a: &Arena, field: str, what: str, bound: str, tail: str): str from a {
    m := a.Fmt("{field}: {what}{bound}{tail}", (field, what, bound, tail)) else {
        return ""
    }
    return m
}

// IsText: the field's type is str.
IsText(t: Type): bool {
    return t.Text() == "str"
}

// InRange: n is a value of integer type t (int, i64, uint and u64 take
// every int ParseInt gives at or above their least value).
InRange(t: Type, n: i64): bool {
    s := t.Text()
    if s == "i8" {
        return n >= -128 && n <= 127
    }
    if s == "i16" {
        return n >= -32768 && n <= 32767
    }
    if s == "i32" {
        return n >= -2147483648 && n <= 2147483647
    }
    if s == "u8" {
        return n >= 0 && n <= 255
    }
    if s == "u16" {
        return n >= 0 && n <= 65535
    }
    if s == "u32" {
        return n >= 0 && n <= 4294967295
    }
    if s == "uint" || s == "u64" {
        return n >= 0
    }
    return true
}

// IsInteger: the field's type is one of the integer types.
IsInteger(t: Type): bool {
    s := t.Text()
    return s == "int" || s == "uint" || s == "i8" || s == "i16" || s == "i32" || s == "i64" || s == "u8" || s == "u16" || s == "u32" || s == "u64"
}
