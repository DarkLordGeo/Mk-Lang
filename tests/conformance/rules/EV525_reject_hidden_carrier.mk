// verdict: reject E0410
// rule: EV525
// note: the private `name` makes `Holder` a carrier, so an exported declaration must say `view`
export struct Holder { // <- E0410
    name: str
}
