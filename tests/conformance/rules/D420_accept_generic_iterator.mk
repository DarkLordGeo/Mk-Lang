// verdict: ok
// rule: D-420
// note: a type parameter iterates when a bound's interface has Next; a plain iterator is moved into the hidden var
interface Counter {
    Next(mut self): ?int
}

Sum[I: Counter + Plain](it: I): int {
    var s := 0
    for x in it {
        s += x
    }
    return s
}
