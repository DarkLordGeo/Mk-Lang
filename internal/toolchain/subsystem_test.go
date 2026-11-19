// Why: a GUI program must link without a console window, and the choice
// must be refused where the format has no such flag.
// What: TestSubsystem, the link half of the subsystem setting.
// How: compares link lines for each subsystem value on COFF and ELF.

package toolchain

import (
	"reflect"
	"testing"
)

// TestSubsystem: a "windows" bin links COFF with /subsystem:windows and
// no console; "" and "console" keep /subsystem:console; ELF refuses
// "windows"; an unknown value is refused.
func TestSubsystem(t *testing.T) {
	out, o1 := absPath(t, "a.exe"), absPath(t, "1.o")
	for _, c := range []struct{ sub, want string }{{"", "/subsystem:console"}, {"console", "/subsystem:console"}, {"windows", "/subsystem:windows"}} {
		got, err := linkArgs(LinkJob{Format: COFF, Output: out, Objects: []string{o1}, Subsystem: c.sub})
		want := []string{"/nologo", "/nodefaultlib", "/entry:mk_start", c.want, "/osversion:10.0", "/machine:x64", "/Brepro", "/out:" + out, o1, "/opt:noicf"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Subsystem %q: %q, %v\nwant %q", c.sub, got, err, want)
		}
	}
	if _, err := linkArgs(LinkJob{Format: ELF, Output: out, Objects: []string{o1}, Subsystem: "windows"}); err == nil {
		t.Error("an ELF link accepted subsystem \"windows\"")
	}
	if got, err := linkArgs(LinkJob{Format: ELF, Output: out, Objects: []string{o1}, Subsystem: "console"}); err != nil {
		t.Errorf("an ELF link refused subsystem \"console\": %v (%q)", err, got)
	}
	if _, err := linkArgs(LinkJob{Format: COFF, Output: out, Objects: []string{o1}, Subsystem: "gui"}); err == nil {
		t.Error("an unknown subsystem was accepted")
	}
}
