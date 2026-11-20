import std.cli
import std.csv
import std.json
import std.regex

export struct Pt {
    export x: int
    export y: int
}

gen json.Codec(Pt)
gen csv.Codec(Pt)

export struct Opts {
    /// Be loud.
    export loud: bool
}

gen cli.Args(Opts)

export struct Hex {
}

gen regex.Matcher(Hex, "[0-9a-f]+")

struct Hid {
    export n: int
}

gen json.Codec(Hid)

export Hidden(a: &Arena): bool {
    v := json.Parse(a, "{\"n\":1}") else {
        return false
    }
    h := Hid.FromJson(a, &v) else {
        return false
    }
    return h.n == 1
}
