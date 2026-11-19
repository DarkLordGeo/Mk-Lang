// verdict: reject E0101
// needs: types
// note: an iterator has no index form; the parser cannot tell an iterator from a slice, so check reports it
Count(text: str): int {
    var n := 0
    for i, line in text.Lines() { // <- E0101
        n += i + line.Len()
    }
    return n
}
