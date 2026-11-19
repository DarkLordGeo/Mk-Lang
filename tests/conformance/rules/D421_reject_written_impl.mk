// verdict: reject E0704
// rule: D-421
// note: the compiler provides an error's Format, so a written one is a second impl
error Denied

impl Format for Denied { // <- E0704
    Format(&self, w: mut Writer): WriteError!void {
        try w.Write("denied")
    }
}
