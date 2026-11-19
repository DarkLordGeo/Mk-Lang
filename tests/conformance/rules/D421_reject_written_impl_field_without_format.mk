// verdict: reject E0704
// rule: D-421
// note: a written impl Format for an error is a second impl even when the provided one is dropped because a field has no Format
struct Raw {
    n: int
}

error Broken(r: Raw)

impl Format for Broken { // <- E0704
    Format(&self, w: mut Writer): WriteError!void {
        try w.Write("broken")
    }
}
