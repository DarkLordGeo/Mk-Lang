// verdict: ok
// rule: EV525
// note: an exported struct whose private fields make it a carrier or linear says so
export view struct Holder {
    name: str
}

export linear struct Owner {
    r: Res
}
