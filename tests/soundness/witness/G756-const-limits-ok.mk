// verdict: ok
// note: a precision witness for the const value rule (the fetch dogfood's transfer.Limits shape): the default client limits with the caller's decoders, returned `from decoders`. DefaultClientLimits' value reaches no memory that takes stores (proxy, allow and secure none, decoders empty), so the copy holds no such static, and the program is accepted. With the value rule cut (mutant M-D756-constvalue-precision) it is E0602 at the return
import std.http
import std.compress

Limits(decoders: []compress.Content): http.ClientLimits from decoders {
    var l := http.DefaultClientLimits
    l.decoders = decoders
    return l
}
