// verdict: reject E1101
// note: mem.Custom is unsafe only, and naming it as a fn value is refused as a call is
import std.mem

Name(): int {
    f := mem.Custom // <- E1101
    _ = f
    return 0
}
