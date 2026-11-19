// Why: every kvd subcommand takes flags, and each states which it accepts.
// What: ParseArgs, Args and Accepts, and the errors UnknownFlag, BadNumber
// and MissingValue (each naming the argument's index).
// How: after the subcommand, `--name value` flags, `--name` switches and
// plain arguments are taken in any order.

export error UnknownFlag(arg: int)
export error BadNumber(arg: int)
export error MissingValue(arg: int)

/// What can be wrong with a command line; `arg` is the argument's index.
type ArgError = UnknownFlag | BadNumber | MissingValue

/// The flags and plain arguments after the subcommand.
struct Args {
    flags: Map[str, str]
    plain: List[str]
}

/// The flags one subcommand takes: those that take a value, and switches.
struct Accepts {
    values: []str
    switches: []str
}

/// Splits `args` into flags and plain arguments, refusing a flag the
/// subcommand does not take. `--` ends the flags.
ParseArgs(a: &Arena, args: []str, accepts: &Accepts): ArgError|OutOfMemory!Args from a, args *args into a {
    var flags := Map[str, str].New(a)
    var plain := List[str].New(a)
    var i := 0
    var rest := false
    for i < args.Len() {
        arg := args[i]
        i += 1
        if rest || !arg.StartsWith("--") {
            try plain.Push(arg)
            continue
        }
        if arg == "--" {
            rest = true
            continue
        }
        if accepts.switches.Has(&arg) {
            try flags.Set(arg, "")
            continue
        }
        if !accepts.values.Has(&arg) {
            fail UnknownFlag(arg: i + 1)
        }
        if i >= args.Len() {
            fail MissingValue(arg: i + 1)
        }
        try flags.Set(arg, args[i])
        i += 1
    }
    return Args(flags: flags, plain: plain)
}

/// The value of `--name`, or `fallback` when it was not given.
Args.Str(&self, name: str, fallback: str): str from self, fallback {
    v := self.flags.Get(&name) else {
        return fallback
    }
    return *v
}

/// The value of `--name`, or `none` when it was not given.
Args.Get(&self, name: str): ?str from self {
    v := self.flags.Get(&name) else {
        return none
    }
    return some(*v)
}

/// Whether the switch `--name` was given.
Args.Has(&self, name: str): bool {
    return self.flags.Has(&name)
}

/// The value of `--name` as a number of at least `min`, or `fallback`.
Args.Int(&self, name: str, min: i64, fallback: i64): BadNumber!i64 {
    text := self.flags.Get(&name) else {
        return fallback
    }
    n := text.ParseInt() else {
        fail BadNumber(arg: 0)
    }
    if n < min {
        fail BadNumber(arg: 0)
    }
    return n
}

/// The plain argument at `i`.
Args.Arg(&self, i: int): ?str from self {
    if i >= self.plain.Len() {
        return none
    }
    return some(self.plain[i])
}

/// The plain arguments from `i` on.
Args.Rest(&self, i: int): []str from self {
    if i >= self.plain.Len() {
        return self.plain[self.plain.Len()..]
    }
    return self.plain[i..]
}
