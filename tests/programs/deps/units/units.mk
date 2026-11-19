// The root module of the dependency "units": imported as `import units`.
export struct Length {
    export mm: i64
}

// A Length prints in metres with three decimals.
impl Format for Length {
    Format(&self, w: mut Writer): WriteError!void {
        m := self.mm / 1000
        rest := self.mm % 1000
        var pad := ""
        if rest < 10 {
            pad = "00"
        } else if rest < 100 {
            pad = "0"
        }
        try Print(w, "{m}.{pad}{rest} m", (m, pad, rest))
    }
}

export Sum(xs: []Length): Length {
    var t: i64 = 0
    for x in xs {
        t += x.mm
    }
    return Length(mm: t)
}
