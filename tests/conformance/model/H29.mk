// verdict: reject E0402
// case: H29
// note: the model left E0402 unmarked; the checker puts it on the declaration of the linear local. `Fallible` has a body: a bodiless function outside std and the test prelude is E0101
Fallible(): OutOfMemory!void {
}

Early(): OutOfMemory!void {
    f := Acquire("a") // <- E0402
    try Fallible()
    f.Close()
}
