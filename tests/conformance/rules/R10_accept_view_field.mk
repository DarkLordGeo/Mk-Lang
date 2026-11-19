// verdict: ok
// rule: R10
// note: the result refers to u8, which Doc's own bytes do not contain, so d stays free
struct Doc {
    text: str
    lines: [4]int
}

Doc.Title(&self): str {
    return self.text
}

Edit(d: mut Doc) {
    t := d.Title()
    d.lines[0] = 1
    Use(&t)
}
