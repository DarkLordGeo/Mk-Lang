// verdict: reject E0604
// rule: EV531
// rule: R11
// note: H73 without its clause: the store into the parameter's list is not declared
Put(out: mut List[&i32], v: &i32) {
    out[0] = v // <- E0604
}
