// Why: mk run's --timings lines must not end up before or inside the report.
// What: TestTimingsAfterReportForRun.
// How: runs a program that fails to import with --json --timings and checks
// that stderr starts with the JSON report.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTimingsAfterReportForRun: with --json, mk run's stderr
// starts with the one JSON document, then the stage lines; in text form
// the stage lines follow the summary line of the report.
func TestTimingsAfterReportForRun(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bad.mk")
	if err := os.WriteFile(file, []byte("import nothing.here\n\nexport Main(sys: &System) {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, errOut := mk("run", "--json", "--timings", file)
	if st != 1 || out != "" || !strings.HasPrefix(errOut, "{") {
		t.Fatalf("run --json --timings: status %d stdout %q stderr %q", st, out, errOut)
	}
	doc, rest, _ := strings.Cut(errOut, "\n")
	var v map[string]any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Errorf("first line of stderr is not the JSON document: %v\n%q", err, errOut)
	}
	if !strings.HasPrefix(rest, "stage ") {
		t.Errorf("stage lines do not follow the JSON document: %q", rest)
	}
	st, _, errOut = mk("run", "--timings", file)
	sum, stage := strings.Index(errOut, "1 error"), strings.Index(errOut, "stage ")
	if st != 1 || sum < 0 || stage < sum {
		t.Errorf("run --timings: status %d, summary at %d, first stage line at %d\n%s", st, sum, stage, errOut)
	}
}
