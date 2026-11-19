// verdict: reject E1109
// note: a library's export extern crosses the library boundary, so each parameter and its result need a C form: a slice, an optional, an error union or an enum has none; a reference to a C-form struct is a C pointer and has one; a function that is not exported stays inside the library
export struct Point {
    export x: i32
    export y: i32
}

export enum Mode {
    A
    B
}

export extern Sum(xs: []i32): i32 { // <- E1109
    return 0
}

export extern Find(p: &Point): ?i32 { // <- E1109
    return none
}

export extern Pick(m: Mode): i32 { // <- E1109
    return 0
}

export extern Norm(p: &Point, q: *Point): i64 {
    return i64(p.x) * i64(p.x) + i64(p.y) * i64(p.y)
}

Inside(xs: []i32): int {
    return xs.Len()
}
