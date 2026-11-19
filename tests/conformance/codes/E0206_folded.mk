// verdict: reject E0206
// note: two fields with one folded name, ignoring case and underscores
struct Span {
    line_no: int
    lineno: int // <- E0206
}
