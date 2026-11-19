// Why: a link tool that never starts (a command line over Windows' limit)
// must still give an E0001 with a stderr fact that says why; an empty one
// explains nothing.
// What: TestE0001_StartErrorNamed.
// How: feeds linkErr a ToolError that never started and one that ran, and
// checks the stderr fact of each.

package driver

import (
	"errors"
	"strings"
	"testing"

	"mk/internal/toolchain"
)

// TestE0001_StartErrorNamed: a tool that never started (a command line
// over Windows' limit) gives an E0001 whose stderr fact names Go's start
// error.
func TestE0001_StartErrorNamed(t *testing.T) {
	te := &toolchain.ToolError{Tool: "lld-link", Command: `lld-link.exe /out:a.exe a.o`, ExitCode: toolchain.ExitNotStarted,
		Err: errors.New(`fork/exec lld-link.exe: The filename or extension is too long.`)}
	d := linkErr(te, "")
	if got := fact(d, "stderr"); !strings.Contains(got, "could not start") || !strings.Contains(got, "too long") {
		t.Errorf("stderr fact %q does not name the start error", got)
	}
	// A tool that ran keeps its own stderr, even an empty one.
	ran := &toolchain.ToolError{Tool: "lld-link", Command: "x", ExitCode: 1, Stderr: "", Err: errors.New("exit status 1")}
	if got := fact(linkErr(ran, ""), "stderr"); got != "" {
		t.Errorf("a tool that ran: stderr fact %q, want empty", got)
	}
}
