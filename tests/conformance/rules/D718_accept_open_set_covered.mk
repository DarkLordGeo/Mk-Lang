// verdict: ok
// rule: D-718
// note: outside std an open set's future members are covered by `_` or a binding at any depth: `err(_)` under `E!T`, `_` on `Target.os` (Os is open); a set the package declares open is matched as written inside its package
import std.fs

open enum Mode {
    Read
    Write
}

Bits(m: Mode): int {
    return match m {
        .Read { 4 }
        .Write { 2 }
    }
}

Size(r: fs.Error!int): int {
    return match r {
        ok(n) { n }
        err(fs.NotFound) { -1 }
        err(_) { -2 }
    }
}

Native(): bool {
    return match Target.os {
        .Windows { true }
        .Linux { true }
        _ { false }
    }
}
