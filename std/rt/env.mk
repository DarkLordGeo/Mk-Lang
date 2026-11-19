// Why: std code such as term.UseColor must read one environment variable
// without a System or an arena, and get text that stays valid.
//
// What: Env, the value of one variable of the environment as the process
// started.
//
// How: it searches the snapshot rt decoded at start (DecodeEnv). Like Args,
// the text it returns lives until the process exits: DecodeEnv's blocks are
// never freed, and nothing in std writes Process.env after Start. It reads
// that snapshot, never the live OS block, which std itself changes: on
// Windows sys.Inherited removes MK_INHERIT from it
// (std/sys/windows/proc.mk).

import std.raw

/// Env is the value of the variable name in the environment as the process
/// started, or none: the first match wins, and on Windows the names compare
/// ASCII case-insensitively, as System.Env's do (std.core's EnvNameIs).
/// The text lives until exit. A caller sees it borrow name, the only
/// parameter that could supply it, so a literal name gives text no loan
/// holds. none before Start has decoded the environment.
/// O(total bytes of the names); allocates nothing.
export Env(name: str): ?str {
    if raw.IsNull(R()) {
        return none
    }
    unsafe "R() is not null, so Init ran and Proc() is the page it mapped, which lives until exit; DecodeEnv's blocks are never freed" {
        for v in (*Proc()).env {
            if EnvNameSame(v.name, name) {
                return some(v.value)
            }
        }
    }
    return none
}

// EnvNameSame is std.core's EnvNameIs, which is private there: names are
// equal on Linux, and equal with ASCII letters folded on Windows (a
// non-ASCII letter must match exactly).
EnvNameSame(a: str, b: str): bool {
    if Target.os != Os.Windows {
        return a == b
    }
    n := a.Len()
    if n != b.Len() {
        return false
    }
    x := a.AsBytes()
    y := b.AsBytes()
    for i in 0..n {
        if EnvUpper(x[i]) != EnvUpper(y[i]) {
            return false
        }
    }
    return true
}

// EnvUpper is c with an ASCII lowercase letter made uppercase.
EnvUpper(c: u8): u8 {
    if c >= b'a' && c <= b'z' {
        return c - 32
    }
    return c
}
