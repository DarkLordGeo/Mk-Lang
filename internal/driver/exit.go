// Why: every command must map a compile result to the same exit status.
//
// What: ExitStatus.
//
// How: one function that every command calls; it reads only the
// diagnostics' codes.

package driver

import "mk/internal/diag"

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
