// verdict: reject E0802
// note: a dropped E!T is E0802, not E0209 (catalogue E0209)
error Bad

Step(): Bad!void {
}

Run() {
    Step() // <- E0802
}
