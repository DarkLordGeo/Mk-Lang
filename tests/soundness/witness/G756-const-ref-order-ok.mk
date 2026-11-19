// verdict: ok
// note: a precision witness for the const value rule: a body that takes `&http.DefaultClientLimits` before it returns a Client made from a copy of it. The static node of a const is made once per body, at its first sight; if `&` judged it by type, the copy would then hold a static that takes stores (E0602 at the return, where the copy alone is ok). A const is judged on its value either way, so the program is accepted. With the value rule cut (mutant M-D756-constvalue-precision) it is E0602 at the return
import std.http

F(h: &Heap): http.Client {
    p := &http.DefaultClientLimits
    _ = p
    return http.Client.New(h, http.DefaultClientLimits)
}
