// verdict: ok
// rule: D-255
// note: Print and Arena.Fmt take no record when the string has no hole; the three-argument form with () is accepted too
Done(w: mut Writer, a: &Arena): WriteError|OutOfMemory!str from a {
    try Print(w, "done\n")
    try Print(w, "still done\n")
    _ = try a.Fmt("plain", ())
    return try a.Fmt("no holes")
}
