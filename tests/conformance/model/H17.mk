// verdict: reject E1003
// case: H17
// note: `Use(&s)` keeps `s` used, so no E0208 joins the E1003
Bad(io: Io) {
    s := io.Scope() // <- E1003
    Use(&s)
}
