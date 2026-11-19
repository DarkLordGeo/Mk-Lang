// verdict: ok
// rule: D-904
// note: an extern var's name is its link symbol, used as written (a C binding's `extern int k_count;`), so the case rule does not apply, as for an extern function
import k

Run(): u32 {
    return k.Read()
}
