// Why: every command must map a compile result to the same exit status,
// and a child's death by a signal to the status a shell reports.
//
// What: ExitStatus and childStatus.
//
// How: one function that every command calls; it reads only the
// diagnostics' codes. childStatus reads the child's wait status.

package driver

import (
	"os/exec"
	"syscall"

	"mk/internal/diag"
)

// ExitStatus is the exit status of a compile result: 0 with no diagnostic, 3
// when any E0000 internal error is present, 2 when the report holds only
// E0001 toolchain errors, and 1 for any diagnostic of a user phase (parse to
// flow).
func ExitStatus(r diag.Report) int {
	if len(r.Diags) == 0 {
		return 0
	}
	onlyToolchain := true
	for _, e := range r.Diags {
		switch e.Diag.Code {
		case diag.E0000:
			return 3
		case diag.E0001:
		default:
			onlyToolchain = false
		}
	}
	if onlyToolchain {
		return 2
	}
	return 1
}

// childStatus is the exit status of a child that ran and failed: its exit
// code, or 128 plus the signal number when a signal killed it (139 for
// SIGSEGV, 143 for SIGTERM), as a shell reports it. Go's ExitCode is -1
// for a death by signal. A Windows child never dies by a signal, so there
// it is the exit code.
func childStatus(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ee.ExitCode()
}
