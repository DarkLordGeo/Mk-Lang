// Why: an illegal instruction in Mk code outside any std.vm code region must
// be reported as one, not as a memory fault at address 0.
// What: TestRt_IllegalInstructionReport on Main, a spawned task and a task
// under runtime.Dedicated.
// How: wants "panic: illegal instruction at 0x<pc>" (fault kind 3) and exit 101;
// Windows once passed kind 2, which printed "invalid memory access at 0x0...0".

package std

import (
	"runtime"
	"strings"
	"testing"
)

const rt10bIllegalSrc = `import std.runtime

// Ud runs ud2 in Mk code, outside any code region.
Ud() {
    unsafe "ud2 raises an illegal-instruction fault on purpose" {
        asm("ud2", options(noreturn, nostack))
    }
}

export Main(sys: &System): WriteError!void {
    mode := sys.Env("UD_MODE") else { "main" }
    if mode == "task" {
        scope s := sys.io.Scope() {
            s.go Ud()
        }
    } else if mode == "dedicated" {
        d := runtime.Dedicated(sys.io)
        scope s := d.Scope() {
            s.go Ud()
        }
    } else {
        Ud()
    }
    try sys.Print("survived\n", ())
}
`

func TestRt_IllegalInstructionReport(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("ud2 is x86_64's")
	}
	for _, profile := range []string{"dev", "release"} {
		exe := rt10bBuildPkg(t, "illegal", rt10bIllegalSrc, profile)
		for _, mode := range []string{"main", "task", "dedicated"} {
			out, status := rt10bRun(t, exe, []string{"UD_MODE=" + mode})
			if status != 101 || !strings.Contains(out, "panic: illegal instruction at 0x") || strings.Contains(out, "invalid memory access") {
				t.Errorf("%s, %s: exit %d, output %q; want 101 and \"panic: illegal instruction at 0x...\"", profile, mode, status, out)
			}
		}
	}
}
