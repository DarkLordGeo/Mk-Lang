// verdict: ok
// note: a view result takes a per-field clause as a struct does; what the view keeps outside its fields is its rest, which no clause names and which holds every source of the clause
view struct W {
    a: []u8
    b: []u8
}

F(x: []u8, y: []u8): W from (a: x, b: y) {
    return W(a: x, b: y)
}
