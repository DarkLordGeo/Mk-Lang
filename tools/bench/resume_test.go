// Why: a long publish run that dies should not have to time every cell
// again, but a stale raw export must never be reused.
// What: TestResumeRaw, a test of resumeRaw.
// How: writes raw hyperfine exports beside a fake program and checks which
// ones are taken.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestResumeRaw: publish -resume reuses a dead run's raw export only when
// it names the same program and arguments, holds the run count, and is newer
// than the program.
func TestResumeRaw(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "go.exe")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{filepath.Join(dir, "data", "in.log"), "22"}
	built := time.Now().Add(-time.Hour)
	os.Chtimes(exe, built, built)

	export := func(name, command string, n int, at time.Time) string {
		type meas struct {
			Wall struct {
				Value float64 `json:"value"`
				Unit  string  `json:"unit"`
			} `json:"time_wall_clock"`
		}
		var ms []meas
		for i := range n {
			var m meas
			m.Wall.Value = 1 + float64(i)/100
			m.Wall.Unit = "second"
			ms = append(ms, m)
		}
		doc := map[string]any{"results": []any{map[string]any{"command": command, "measurements": ms}}}
		b, _ := json.Marshal(doc)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, at, at)
		return p
	}
	words := []string{quoteArg(exe)}
	for _, a := range args {
		words = append(words, quoteArg(a))
	}
	cmd := strings.Join(words, " ")
	later := built.Add(30 * time.Minute)

	if ts, ok := resumeRaw(export("ok.json", cmd, 10, later), exe, args, 10); !ok || len(ts) != 10 || ts[0] != 1 {
		t.Fatalf("a matching export was not reused: %v %v", ts, ok)
	}
	if _, ok := resumeRaw(export("rerun.json", cmd, 30, later), exe, args, 30); !ok {
		t.Fatal("a matching rerun export (30 runs) was not reused")
	}
	for _, c := range []struct {
		why  string
		path string
		runs int
	}{
		{"older than the program", export("old.json", cmd, 10, built.Add(-time.Minute)), 10},
		{"another run count", export("count.json", cmd, 9, later), 10},
		{"other arguments", export("args.json", strings.Replace(cmd, "22", "23", 1), 10, later), 10},
		{"another program", export("prog.json", strings.Replace(cmd, "go.exe", "mk.exe", 1), 10, later), 10},
		{"missing", filepath.Join(dir, "none.json"), 10},
	} {
		if _, ok := resumeRaw(c.path, exe, args, c.runs); ok {
			t.Errorf("%s: reused", c.why)
		}
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{"), 0o644)
	os.Chtimes(bad, later, later)
	if _, ok := resumeRaw(bad, exe, args, 10); ok {
		t.Error("a half-written export was reused")
	}
}
