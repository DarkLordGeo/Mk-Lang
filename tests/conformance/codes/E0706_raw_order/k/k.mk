import std.raw

export Bad(): int {
    var w := raw.Atomic[int].New(0)
    unsafe "w is a local word" {
        w.StoreOrd(1, 5)
        w.StoreOrd(1, 1) // <- E0706
        _ = w.LoadOrd(2) // <- E0706
        _ = w.AddOrd(1, 5) // <- E0706
        _ = w.CasOrd(0, 1, 4, 2, false) // <- E0706
        raw.FenceOrd(0) // <- E0706
        raw.FenceOrd(6) // <- E0706
    }
    return w.LoadOrd(5)
}

// A module outside std that passes its own const order on restates raw's
// clause; only std's own forwarders state std.atomic's instead.
export Fwd(w: &raw.Atomic[int], const o: u8): int {
    var x := 0
    unsafe "w is the caller's word" {
        x = w.LoadOrd(o) // <- E0706
    }
    return x
}

export FwdStated(w: &raw.Atomic[int], const o: u8): int
    requires o == 0 || o == 1 || o == 4 || o == 5 {
    var x := 0
    unsafe "w is the caller's word" {
        x = w.LoadOrd(o)
    }
    return x
}
