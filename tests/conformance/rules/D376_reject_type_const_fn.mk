// verdict: reject E0705
// rule: D-376
// note: a struct's const parameter of fn type is refused at its type, since no argument could instantiate it
struct Box[const f: fn(i64): i64] { // <- E0705
    x: i64
}
