// verdict: ok
// note: a precision witness for the const value rule: k's const D of a struct with `p: ?&url.Url`, set to none, copied and returned `from d`. Its value reaches nothing, so D takes no store, and the program is accepted. With the value rule cut (mutant M-D756-constvalue-precision) it is E0602 at the return. A const of the body's own module is still judged by its type (the same program in one module is E0602): its value is written only after the module's bodies are borrow-checked
import k

F(d: []u8): k.L from d {
    var l := k.D
    l.d = d
    return l
}

export Main(sys: &System): WriteError!void {
    var b: [2]u8 = [1, 2]
    l := F(b[..])
    try sys.Print("{n}\n", (n: l.d.Len()))
}
