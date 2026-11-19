// verdict: reject E1101
// note: runtime.ForeignContext is unsafe only: it hands back the raw context pointer a foreign Io was made with
import std.runtime

Context(io: Io): *u8 {
    return runtime.ForeignContext(io) // <- E1101
}
