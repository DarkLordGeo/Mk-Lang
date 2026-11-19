// verdict: ok
// note: a precision witness for the const value rule: http.DefaultClientLimits holds `proxy: ?&url.Url`, a reference to a view, so its type alone counts it as global memory that takes stores, and judged by type, returning a Client made from it under the automatic `from h` is E0602. Its evaluated value is none there and every other reference field is empty or none, so it reaches no memory that takes stores, and the program is accepted. With the value rule cut (mutant M-D756-constvalue-precision) it is E0602 at the return
import std.http

NewClient(h: &Heap): http.Client {
    return http.Client.New(h, http.DefaultClientLimits)
}
