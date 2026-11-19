// Why: Reader.Of is a Reader over a byte slice, and its loan is what keeps
// the bytes from changing under it.
// What: mk check tests: the bytes cannot be written, or handed to the
// Reader's own Read, while it is live; a Reader over a local cannot escape.
// How: small programs checked for their diagnostics; the run-time side is
// std/core/tests_rw.mk's Reader.Of blocks (TestStdMkTests).

package std

import (
	"strings"
	"testing"
)

const core11ReaderOfLoans = `Escape(): Reader {
    var a: [4]u8 = [1, 2, 3, 4]
    return Reader.Of(a[..])
}

export Main(sys: &System): ReadError!void {
    var a: [4]u8 = [1, 2, 3, 4]
    var r := Reader.Of(a[..])
    a[0] = 9
    var d: [2]u8 = [0; 2]
    _ = try r.Read(mut d[..])
    var r2 := Reader.Of(a[..])
    _ = try r2.Read(mut a[0..2])
    var r3 := Reader.Of(a[..])
    _ = try r3.Read(mut d[..])
    a[1] = 7
}
`

// TestCore_ReaderOfHoldsItsLoan: the escape is E0603 (line 3), the write
// while r is live E0502 (line 9), and a Read into the Reader's own bytes
// E0501 (line 13); the write after r3's last use (line 17) is accepted, and
// std draws no diagnostic.
func TestCore_ReaderOfHoldsItsLoan(t *testing.T) {
	t.Parallel()
	out, _ := checkProgram(t, nil, core11ReaderOfLoans)
	if strings.Contains(out, "std/") {
		t.Errorf("std drew a diagnostic:\n%s", out)
	}
	want := []string{"3 E0603", "9 E0502", "13 E0501"}
	if got := diagCodes(out); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("want %q, got %q\n%s", want, got, out)
	}
}
