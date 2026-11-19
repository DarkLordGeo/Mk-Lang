// verdict: ok
// rule: R8
// note: the str read through &h holds what h held, a static string, not the loan on h
struct Holder {
    name: str
}

Name(h: &Holder): str {
    return h.name
}

Outlives(): str {
    h := Holder(name: "static")
    return Name(&h)
}
