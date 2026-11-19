// verdict: reject E0704
// note: a generated `impl` for an interface and a type the module declares neither of is an orphan, E0704 at the `gen` line, not wrapped as E0905; `Eq` is the prelude's and `Duration` is std's
import std.derive
import std.time

gen derive.Eq(time.Duration) // <- E0704
