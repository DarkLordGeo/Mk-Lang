// Why: the spawner's report decides whether a cell shows a peak or only a
// bound, so its parsing and recording are checked on every platform.
// What: TestSpawnReport and TestSpawnRecord.
// How: parses hand-written report lines and checks the recorded cells.

package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSpawnReport: how the Linux peak spawner's report is read (spawn.go),
// on every platform: a reading is kept only above the floor.
func TestSpawnReport(t *testing.T) {
	for _, tc := range []struct {
		in      string
		peak    uint64
		code    int
		state   string
		start   string
		success bool
	}{
		{"ok 9000 4000 0 exit status 0\n", 9000 * 1024, 0, "exit status 0", "", true},
		{"ok 4001 4000 2 exit status 2\n", 4001 * 1024, 2, "exit status 2", "", false},
		{"ok 4000 4000 0 exit status 0\n", 0, 0, "exit status 0", "", true}, // at the floor: not the program's
		{"ok 3000 4000 0 exit status 0\n", 0, 0, "exit status 0", "", true},
		{"ok 0 0 0 exit status 0\n", 0, 0, "exit status 0", "", true}, // no floor read: no reading
		{"ok 9000 4000 -1 signal: killed\n", 9000 * 1024, -1, "signal: killed", "", false},
		{"start fork/exec /x: no such file or directory\n", 0, 0, "", "fork/exec /x: no such file or directory", false},
	} {
		r, err := parseSpawnReport(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if r.peak() != tc.peak || r.Code != tc.code || r.State != tc.state || r.Start != tc.start || (tc.start == "" && r.success() != tc.success) {
			t.Errorf("%q: %+v, peak %d", tc.in, r, r.peak())
		}
	}
	for _, bad := range []string{
		"",
		"\n",
		"ok\n",
		"ok 1 2 0\n",
		"ok 1 2 0 \n",
		"ok x 2 0 exit status 0\n",
		"ok 1 -2 0 exit status 0\n",
		"ok 1 2 -2 exit status 0\n",
		"ok 1 2 0 exit status 0\nok 1 2 0 exit status 0\n",
		"start \n",
		"start\n",
		"what 1 2 0 exit status 0\n",
	} {
		if r, err := parseSpawnReport(bad); err == nil {
			t.Errorf("%q: read as %+v", bad, r)
		}
	}
}

// TestSpawnRecord: what a cell records of a report and how it prints:
// above the floor the reading is the peak, at or under it a bound printed "at
// most" the reading rounded up, with no floor read nothing; the raw reading
// and the floor are kept either way. A command field the spawner does not
// pass on is refused, and each spawner's command carries the program.
func TestSpawnRecord(t *testing.T) {
	for _, tc := range []struct {
		rep  spawnReport
		want memPeak
		text string
	}{
		{spawnReport{MaxrssKiB: 20480, FloorKiB: 1664}, memPeak{RSS: 20480 << 10, Maxrss: 20480 << 10, Floor: 1664 << 10, Spawner: "peakspawn"}, "20.0 MiB"},
		{spawnReport{MaxrssKiB: 1664, FloorKiB: 1664}, memPeak{Maxrss: 1664 << 10, Floor: 1664 << 10, Spawner: "peakspawn"}, "at most 1.7 MiB"},
		{spawnReport{MaxrssKiB: 4608, FloorKiB: 4608}, memPeak{Maxrss: 4608 << 10, Floor: 4608 << 10, Spawner: "peakspawn"}, "at most 4.5 MiB"},
		{spawnReport{MaxrssKiB: 1000, FloorKiB: 4608}, memPeak{Maxrss: 1000 << 10, Floor: 4608 << 10, Spawner: "peakspawn"}, "at most 1.0 MiB"},
		{spawnReport{}, memPeak{}, "-"},
		{spawnReport{MaxrssKiB: 5000}, memPeak{}, "-"},
	} {
		mp := tc.rep.memPeak("peakspawn")
		if mp != tc.want {
			t.Errorf("%+v: recorded %+v, want %+v", tc.rep, mp, tc.want)
		}
		c := &cell{}
		if mp != (memPeak{}) {
			c.Peak = &mp
		}
		if got := peakText(c); got != tc.text {
			t.Errorf("%+v: printed %q, want %q", tc.rep, got, tc.text)
		}
	}
	// A Windows peak prints as before.
	if got := peakText(&cell{Peak: &memPeak{RSS: 50 << 20, Commit: 25 << 20}}); got != "50.0 MiB (commit 25.0 MiB)" {
		t.Errorf("Windows peak: %q", got)
	}

	// The row's sentence names the spawners that read its cells, and a row
	// with no spawner's reading (Windows) has none.
	cs := []cell{{Peak: &memPeak{RSS: 1, Maxrss: 1, Floor: 1, Spawner: "peakspawn"}}, {Peak: &memPeak{Maxrss: 1, Floor: 2, Spawner: "tools/bench"}}, {}}
	if s := linuxPeakText(cs); !strings.Contains(s, "`peakspawn`, a minimal Go program; tools/bench itself, the fallback") || !strings.Contains(s, `"at most"`) {
		t.Errorf("Linux sentence: %q", s)
	}
	if s := linuxPeakText([]cell{{Peak: &memPeak{RSS: 5, Commit: 3}}, {}}); s != "" {
		t.Errorf("Windows row: %q", s)
	}

	ok := exec.Command("prog", "a")
	if err := spawnUnforwarded(ok); err != nil {
		t.Errorf("a plain command: %v", err)
	}
	for field, c := range map[string]*exec.Cmd{
		"SysProcAttr": {Path: "p", SysProcAttr: &syscall.SysProcAttr{}},
		"ExtraFiles":  {Path: "p", ExtraFiles: []*os.File{os.Stdin}},
		"Cancel":      exec.CommandContext(context.Background(), "p"),
		"WaitDelay":   {Path: "p", WaitDelay: time.Second},
		"Process":     {Path: "p", Process: &os.Process{Pid: 1}},
	} {
		if err := spawnUnforwarded(c); err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("%s: %v", field, err)
		}
	}

	prog := &exec.Cmd{Path: "/bin/prog", Args: []string{"argv0", "x"}, Env: []string{"A=1"}, Dir: "/d", Stdin: os.Stdin}
	min := peakSpawner{Path: "/w/peakspawn"}.command(prog)
	self := peakSpawner{Path: "/w/benchtool", Self: true}.command(prog)
	if strings.Join(min.Args, " ") != "/w/peakspawn /bin/prog argv0 x" || strings.Join(self.Args, " ") != "/w/benchtool "+spawnArg+" /bin/prog argv0 x" {
		t.Errorf("argv: %q, %q", min.Args, self.Args)
	}
	for _, c := range []*exec.Cmd{min, self} {
		if strings.Join(c.Env, " ") != "A=1" || c.Dir != "/d" || c.Stdin != os.Stdin {
			t.Errorf("%s: env %q, dir %q", c.Path, c.Env, c.Dir)
		}
	}
	if (peakSpawner{Path: "x"}).name() != "peakspawn" || (peakSpawner{Path: "x", Self: true}).name() != "tools/bench" {
		t.Error("spawner names")
	}
}
