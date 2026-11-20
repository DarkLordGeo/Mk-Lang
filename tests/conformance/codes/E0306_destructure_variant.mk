// verdict: reject E0306
// note: an enum variant on the left of `:=`, even of a one-variant enum
enum One {
    Only(x: int)
}

Get(o: One): int {
    One.Only(x) := o // <- E0306
    return x
}
