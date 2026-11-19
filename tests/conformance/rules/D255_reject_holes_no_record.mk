// verdict: reject E0706
// rule: D-255
// note: a string with holes and no record is E0706, and so is a record with an unused field
Count(w: mut Writer, a: &Arena, n: int): WriteError|OutOfMemory!void {
    try Print(w, "{n} items\n")    // <- E0706
    _ = try a.Fmt("{n}")           // <- E0706
    try Print(w, "none\n", (n: n)) // <- E0706
}
