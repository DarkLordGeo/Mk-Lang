// verdict: reject E0501
// case: H58
Two(xs: mut []i32) {
    a := xs.GetMut(0)
    b := xs.GetMut(1) // <- E0501
    Use(&a)
    Use(&b)
}
