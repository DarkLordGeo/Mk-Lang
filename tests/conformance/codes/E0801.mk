// verdict: reject E0801
error Missing
error Denied

Find(): Missing!int {
    fail Missing
}

Load(): Denied!int {
    n := try Find() // <- E0801
    return n
}
