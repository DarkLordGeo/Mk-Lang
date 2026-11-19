// verdict: reject E0101
// note: foreign form, switch e {; hint "use match", its case and default lines are skipped with it
Name(n: int): str {
    switch n { // <- E0101
    case 1:
        return "one"
    default:
        return "many"
    }
}
