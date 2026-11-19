// verdict: reject E0101
// note: foreign form, import a as b (E0101, not the E1222 of an infix as)
import std.fs as files // <- E0101

Zero(): int {
    return 0
}
