// Why: every command-line program parses flags and prints usage; writing
// that by hand per program is repetitive and drifts from the docs.
//
// What: the error set (Unknown, Missing, Invalid, Error) and the generator
// Args: `gen cli.Args(T)` emits, for a struct T,
//
//     T.Parse(a: &Arena, args: []str): cli.Error!T from a, args
//     T.Usage(w: mut Writer): WriteError!void
//
// How: both are built from T's fields and their `///` comments (Field.Doc).
// The grammar:
// - a `bool` field is a flag `--name` (or `--name=true`, `--name=false`),
//   an `int` or `str` field a required option `--name V` or `--name=V`, a
//   `?int` or `?str` field an optional one, and one `[]str` field the
//   positionals; the field `dry_run` is the option `--dry-run`;
// - `--` ends the options, `-` alone and any argument not starting with
//   `-` is a positional, `-x` is Unknown (no short options);
// - a value-taking option takes the next argument whatever it is, the last
//   of a repeated option wins, and parsing stops at the first error;
// - Unknown and Invalid carry the index into args, Missing the index of
//   the field (every field is exported, so among the exported fields); of
//   several missing options the first in declaration order is reported;
// - a `help: bool` field, when given, skips the required checks: Parse
//   returns T with help true and 0 or "" in each required option not
//   given, so `prog --help` reaches the program's Usage call; Unknown and
//   Invalid still stop it;
// - the str values are views of args; the positionals are a view of args
//   while they are one contiguous run, else one List[str] in a of copies
//   of them, so a holds no loan of args.
// Every field must be exported, since Parse builds T and a field has no
// default (as json.Codec); any other field, and a field of another
// type, is E0903 at the field. Runs at compile time only; the emitted code
// names this module's items as cli.X and calls only std.cli's public names.
//
// The text helpers push into the compile-time run's arena with Must: that
// arena fails only when the compiler itself is out of memory, which ends
// the build, so no input of the program reaches those failures.

/// An argument at this index of args names no option, or is a positional
/// and T takes none.
export error Unknown(arg: int)

/// The required option of the field at this index was not given.
export error Missing(field: int)

/// The argument at this index holds a value its option cannot take, or is
/// an option with no value at the end of args.
export error Invalid(arg: int)

/// Every way T.Parse fails.
export open type Error = Unknown | Missing | Invalid | OutOfMemory

// The field kinds of a cli.Args struct.
enum Kind {
    Flag
    IntOpt
    StrOpt
    MaybeInt
    MaybeStr
    Positionals
    Refused
}

/// The generator behind `gen cli.Args(T)`: it writes a command-line parser
/// and a usage printer for the struct T, from T's fields and their `///`
/// comments: `T.Parse(a: &Arena, args: []str): cli.Error!T from a, args`
/// and `T.Usage(w: mut Writer): WriteError!void`.
///
/// Each field is one argument. A bool field is a flag (`--name`, or
/// `--name=true` and `--name=false`); an int or str field is a required
/// option (`--name V` or `--name=V`); a ?int or ?str field is an optional
/// one; one []str field takes the positional arguments. The option's name
/// is the field's with `_` written `-`, so `dry_run` is `--dry-run`. Every
/// field must be exported; another field, or a field of another type, is
/// E0903 at that field.
///
/// Parse rules: `--` ends the options; `-` alone and any argument not
/// starting with `-` is a positional; there are no short options, so `-x`
/// is Unknown. An option that takes a value takes the next argument,
/// whatever it is. The last of a repeated option wins. Parse stops at the
/// first error. Unknown and Invalid carry the index into args, Missing the
/// index of the field among T's fields; of several missing options, the
/// first declared is reported. When T has a field `help: bool` that ends
/// true, Parse skips the required checks and gives 0 or "" to each
/// required option not given, so the program can print Usage; Unknown and
/// Invalid still fail. The str values are views of args, and so are the
/// positionals while they are one unbroken run; otherwise they are copied
/// into one list in a.
///
/// Usage writes an `Options:` list and an `Arguments:` list, leaving out an
/// empty one, with each field's `///` text, and marks a required option
/// `(required)`.
///
/// Fails the generator at the first field that cannot be an argument.
/// O(fields squared) at compile time (the help column); allocates in the
/// run's arena.
export Args(g: mut Gen, t: Type) {
    if t.Kind() != TypeKind.Struct {
        g.FailOn(t, "cli.Args: only a struct can describe command-line arguments")
        return
    }
    fs := t.Fields()
    // Every field is checked before anything is emitted.
    var npos := 0
    for f in fs {
        if !f.Exported() {
            g.FailAt(f, "cli.Args: Parse builds T from its fields, so every field must be exported")
            return
        }
        k := KindOf(f.Type())
        if k == Kind.Refused {
            g.FailAt(f, "cli.Args: a field must be bool (a flag), int or str (an option), ?int or ?str (an optional option), or []str (the positionals)")
            return
        }
        if k == Kind.Positionals {
            npos += 1
            if npos > 1 {
                g.FailAt(f, "cli.Args: only one []str field can take the positionals")
                return
            }
        }
    }
    EmitParse(g, t, fs, npos == 1)
    text := UsageText(&*g, fs)
    g.Emit(quote {
        $(t.Name()).Usage(w: mut Writer): WriteError!void {
            try w.Str($(g.Str(text)))
        }
    })
}

// KindOf classifies a field's type.
KindOf(ty: Type): Kind {
    k := ty.Kind()
    if k == TypeKind.Scalar {
        return match ty.Text() {
            "bool" { Kind.Flag }
            "int" { Kind.IntOpt }
            "str" { Kind.StrOpt }
            _ { Kind.Refused }
        }
    }
    if k == TypeKind.Optional {
        e := ty.Elem()
        if e.Kind() != TypeKind.Scalar {
            return Kind.Refused
        }
        return match e.Text() {
            "int" { Kind.MaybeInt }
            "str" { Kind.MaybeStr }
            _ { Kind.Refused }
        }
    }
    if k == TypeKind.Slice {
        e := ty.Elem()
        if e.Kind() == TypeKind.Scalar && e.Text() == "str" {
            return Kind.Positionals
        }
    }
    return Kind.Refused
}

// EmitParse emits T.Parse: one pass over args, one local per field, a match
// on the option name, then T's literal, whose required fields fail with
// Missing when their option was not given.
EmitParse(g: mut Gen, t: Type, fs: []Field, takes_pos: bool) {
    i := g.Fresh("i")
    arg := g.Fresh("arg")
    key := g.Fresh("key")
    val := g.Fresh("val")
    has := g.Fresh("has")
    eq := g.Fresh("eq")
    rest := g.Fresh("rest")
    p0 := g.Fresh("p0")
    pn := g.Fresh("pn")
    pl := g.Fresh("pl")
    spilled := g.Fresh("spilled")
    pos := g.Fresh("pos")
    k2 := g.Fresh("k")
    done := g.Fresh("done")
    var decls := g.Block()
    var arms := g.Block()
    var lit := quote {}
    var n := 0
    var narms := 0
    // A `help: bool` field, when T has one: given, it skips the required
    // checks, so `prog --help` reaches the program. Its local is named
    // first, since a required option before it in T refers to it.
    var has_help := false
    for f in fs {
        if f.Name() == "help" && KindOf(f.Type()) == Kind.Flag {
            has_help = true
        }
    }
    help := g.Fresh("help")
    for f in fs {
        k := KindOf(f.Type())
        x := if has_help && f.Name() == "help" && k == Kind.Flag { help } else { g.Fresh(f.Name()) }
        name := g.Str(OptionName(&*g, f.Name()))
        if n > 0 {
            lit = lit.Then(quote { , })
        }
        if k == Kind.Flag {
            decls.Add(quote {
                var $x := false
            })
            arms.Add(quote {
                $name {
                    $x = if !$has || $val == "true" {
                        true
                    } else if $val == "false" {
                        false
                    } else {
                        fail cli.Invalid(arg: $i)
                    }
                }
            })
            lit = lit.Then(quote { $(f.Name()): $x })
            narms += 1
        } else if k == Kind.Positionals {
            lit = lit.Then(quote { $(f.Name()): $pos })
        } else {
            // After the value is taken, i is its index, `=` form or not.
            var take := quote {
                $x = some($val)
            }
            if k == Kind.IntOpt || k == Kind.MaybeInt {
                v := g.Fresh("v")
                take = quote {
                    $v := $val.ParseInt() else {
                        fail cli.Invalid(arg: $i)
                    }
                    $x = some(Cast[int]($v))
                }
                decls.Add(quote {
                    var $x: ?int = none
                })
            } else {
                decls.Add(quote {
                    var $x: ?str = none
                })
            }
            arms.Add(quote {
                $name {
                    if !$has {
                        if $i + 1 == args.Len() {
                            fail cli.Invalid(arg: $i)
                        }
                        $i += 1
                        $val = args[$i]
                    }
                    $take
                }
            })
            // A required option's else block fails with Missing, or gives
            // its zero under --help. The literal's fields are evaluated in
            // order, so the first missing field is the one reported.
            if k == Kind.MaybeInt || k == Kind.MaybeStr {
                lit = lit.Then(quote { $(f.Name()): $x })
            } else if !has_help {
                lit = lit.Then(quote { $(f.Name()): $x else { fail cli.Missing(field: $(n)) } })
            } else {
                zero := if k == Kind.IntOpt { quote { 0 } } else { quote { "" } }
                lit = lit.Then(quote { $(f.Name()): $x else { if $help { $zero } else { fail cli.Missing(field: $(n)) } } })
            }
            narms += 1
        }
        n += 1
    }
    // A positional: the run args[p0..p0 + pn] while it is contiguous, else
    // the list pl, which takes copies of the run at the first gap: a view
    // of args stored into a List in a would store args's loans into a,
    // which T.Parse's signature does not declare.
    var on_pos := quote {
        fail cli.Unknown(arg: $i)
    }
    var pos_end := quote {}
    if takes_pos {
        decls.Add(quote {
            var $p0 := 0
            var $pn := 0
            var $pl := List[str].New(a)
            var $spilled := false
        })
        on_pos = quote {
            if $spilled {
                try $pl.Push(try a.Dup($arg))
            } else if $pn == 0 {
                $p0 = $i
                $pn = 1
            } else if $p0 + $pn == $i {
                $pn += 1
            } else {
                for $k2 in $p0..$p0 + $pn {
                    try $pl.Push(try a.Dup(args[$k2]))
                }
                try $pl.Push(try a.Dup($arg))
                $spilled = true
            }
            $i += 1
            continue
        }
        pos_end = quote {
            var $pos: []str = args[$p0..$p0 + $pn]
            if $spilled {
                $done := $pl.Done()
                $pos = $done[..]
            }
        }
    }
    // The option name and its `=` value; with no option field every name
    // is Unknown.
    var named := quote {
        fail cli.Unknown(arg: $i)
    }
    if narms > 0 {
        named = quote {
            var $key := $arg[2..]
            var $val := ""
            var $has := false
            if $eq := $key.Find("=") {
                $val = $key[$eq + 1..]
                $key = $key[0..$eq]
                $has = true
            }
            match $key {
                $arms
                _ {
                    fail cli.Unknown(arg: $i)
                }
            }
        }
    }
    g.Emit(quote {
        $(t.Name()).Parse(a: &Arena, args: []str): cli.Error!$(t.Name()) from a, args {
            $decls
            var $rest := false
            var $i := 0
            for $i < args.Len() {
                $arg := args[$i]
                if $rest || $arg.Len() < 2 || $arg.Byte(0) != 0x2D {
                    $on_pos
                }
                if $arg.Byte(1) != 0x2D {
                    fail cli.Unknown(arg: $i)
                }
                if $arg.Len() == 2 {
                    $rest = true
                    $i += 1
                    continue
                }
                $named
                $i += 1
            }
            $pos_end
            return $(t.Name())($lit)
        }
    })
}

// OptionName is the field's name with each `_` as `-`, in the run's arena.
OptionName(g: &Gen, field: str): str from g {
    a := g.Arena()
    var t := Text.New(a)
    var from := 0
    for k in 0..field.Len() {
        if field.Byte(k) == 0x5F {
            t.Push(field[from..k]).Must()
            t.Push("-").Must()
            from = k + 1
        }
    }
    t.Push(field[from..field.Len()]).Must()
    return a.Dup(t.Str()).Must()
}

// LeftCell is a field's entry in the left column of Usage.
LeftCell(g: &Gen, f: &Field): str from g {
    a := g.Arena()
    k := KindOf(f.Type())
    var t := Text.New(a)
    if k == Kind.Positionals {
        t.Push("<").Must()
        t.Push(OptionName(g, f.Name())).Must()
        t.Push(">...").Must()
        return a.Dup(t.Str()).Must()
    }
    t.Push("--").Must()
    t.Push(OptionName(g, f.Name())).Must()
    if k == Kind.IntOpt || k == Kind.MaybeInt {
        t.Push(" <int>").Must()
    } else if k == Kind.StrOpt || k == Kind.MaybeStr {
        t.Push(" <str>").Must()
    }
    return a.Dup(t.Str()).Must()
}

// UsageText is the whole Usage output, built at compile time: an
// "Options:" section for the flags and options and an "Arguments:" section
// for the positionals, each left out when empty; the help column is the
// widest left cell plus two.
UsageText(g: &Gen, fs: []Field): str from g {
    a := g.Arena()
    var width := 0
    var nopt := 0
    for f in fs {
        w := LeftCell(g, &f).Len()
        if w > width {
            width = w
        }
        if KindOf(f.Type()) != Kind.Positionals {
            nopt += 1
        }
    }
    var t := Text.New(a)
    if nopt > 0 {
        t.Push("Options:\n").Must()
        for f in fs {
            if KindOf(f.Type()) != Kind.Positionals {
                Entry(mut t, g, &f, width + 4)
            }
        }
    }
    if nopt < fs.Len() {
        t.Push("Arguments:\n").Must()
        for f in fs {
            if KindOf(f.Type()) == Kind.Positionals {
                Entry(mut t, g, &f, width + 4)
            }
        }
    }
    return a.Dup(t.Str()).Must()
}

// Entry writes one field's lines: two spaces, the left cell, the help from
// column col with each further help line indented to col, and
// " (required)" after a required option's help. The help is the field's
// `///` comment.
Entry(t: mut Text, g: &Gen, f: &Field, col: int) {
    cell := LeftCell(g, f)
    t.Push("  ").Must()
    t.Push(cell).Must()
    k := KindOf(f.Type())
    doc := f.Doc()
    help := if k != Kind.IntOpt && k != Kind.StrOpt {
        doc
    } else if doc == "" {
        "(required)"
    } else {
        g.Arena().Fmt("{doc} (required)", (doc)).Must()
    }
    if help == "" {
        t.Push("\n").Must()
        return
    }
    var pad := col - 2 - cell.Len()
    var first := true
    for line in help.Split("\n") {
        if !first {
            t.Push("\n").Must()
            pad = col
        }
        for _ in 0..pad {
            t.Push(" ").Must()
        }
        t.Push(line).Must()
        first = false
    }
    t.Push("\n").Must()
}
