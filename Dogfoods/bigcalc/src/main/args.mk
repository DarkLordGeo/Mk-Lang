// Why: bigcalc's commands share one option syntax, with positional arguments
// mixed in.
//
// What: Args with Value, Has, Nth and Int, ParseArgs, and the option errors.
//
// How: `--name value`, `--name=value` and `--flag`, in any order among the
// positional arguments, with `--` ending the options.

struct Flag {
    name: str
    value: str
}

struct Args {
    flags: List[Flag]
    positional: List[str]
}

/// An option no subcommand takes; arg is its index in the arguments given.
error UnknownOption(arg: int)
/// An option that takes a value came last.
error MissingValue(arg: int)

type ArgError = UnknownOption | MissingValue | OutOfMemory

/// ParseArgs splits args into options and positional arguments. valued
/// lists the options that take a value, switches those that do not. O(n);
/// allocates the two lists in a.
ParseArgs(a: &Arena, args: []str, valued: []str, switches: []str): ArgError!Args from a, args *args into a {
    var flags := List[Flag].New(a)
    var positional := List[str].New(a)
    var i := 0
    var options := true
    for i < args.Len() {
        arg := args[i]
        if !options || !arg.StartsWith("--") {
            try positional.Push(arg)
            i += 1
            continue
        }
        if arg == "--" {
            options = false
            i += 1
            continue
        }
        var name := arg
        var value: ?str = none
        if eq := arg.Find("=") {
            name = arg[..eq]
            value = some(arg[eq + 1..])
        }
        if switches.Has(&name) && value.IsNone() {
            try flags.Push(Flag(name: name, value: ""))
            i += 1
            continue
        }
        if !valued.Has(&name) {
            fail UnknownOption(arg: i)
        }
        if v := value {
            try flags.Push(Flag(name: name, value: v))
            i += 1
            continue
        }
        if i + 1 >= args.Len() {
            fail MissingValue(arg: i)
        }
        try flags.Push(Flag(name: name, value: args[i + 1]))
        i += 2
    }
    return Args(flags: flags, positional: positional)
}

/// Value is the last value given for name, or none.
Args.Value(&self, name: str): ?str from self {
    var found: ?str = none
    for f in self.flags {
        if f.name == name {
            found = some(f.value)
        }
    }
    return found
}

/// Has reports whether the switch or option name was given.
Args.Has(&self, name: str): bool {
    for f in self.flags {
        if f.name == name {
            return true
        }
    }
    return false
}

/// Nth is the k-th value given for name, counting from 0, or none.
Args.Nth(&self, name: str, k: int): ?str from self {
    var seen := 0
    for f in self.flags {
        if f.name == name {
            if seen == k {
                return some(f.value)
            }
            seen += 1
        }
    }
    return none
}

/// An option whose value is not a number in its range.
error BadOptionValue

/// Int is the option name as an integer from lo to hi, or fallback when it
/// is absent. Fails with BadOptionValue otherwise.
Args.Int(&self, name: str, fallback: int, lo: int, hi: int): BadOptionValue!int {
    text := self.Value(name) else {
        return fallback
    }
    n := text.ParseInt() else {
        fail BadOptionValue
    }
    if n < Cast[i64](lo) || n > Cast[i64](hi) {
        fail BadOptionValue
    }
    return Cast[int](n)
}
