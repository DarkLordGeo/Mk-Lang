// verdict: reject E1101
// note: runtime.AddFaultFilter is unsafe only: a filter edits registers and resumes code, so its install is an unsafe operation
import std.runtime

Pass(c: &runtime.FaultContext): runtime.FaultAction {
    return runtime.FaultAction.Pass
}

Install(): runtime.FilterId {
    return runtime.AddFaultFilter(Pass) // <- E1101
}
