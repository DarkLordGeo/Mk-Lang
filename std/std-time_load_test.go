// Why: Zone.Load and Zone.Local touch the real file system and OS, which
// the in-memory oracle tests never do.
// What: TestStdTime_LoadFromDisk, TestStdTime_Sim and TestStdTime_CancelRepeatable.
// How: TZDIR trees on disk give found, BadName, NotFound, NoDatabase and BadData;
// under std.sim the clock starts at 2026-01-01T00:00:00Z, the zone is UTC, and no TZDIR is read.

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// time2LoadProg runs one request per argument: "load:NAME" and
// "db:NAME" (Zone.Load without and with tzdata.Database), "local" (the
// offsets of Zone.Local at the instants on stdin, one a line), "date:NAME"
// (Zone.Load of NAME, then FromDate of each "y m d h mi s" line on stdin:
// the UTC nanoseconds or the error), "wall" (time.Wall's nanoseconds and
// its UTC date), and "sim" (under a Sim: Wall's nanoseconds, Local's
// offset now, and Load of Europe/Paris without and with tzdata).
const time2LoadProg = `import std.sim
import std.time
import std.time.tzdata

Probe(sys: &System, out: mut Writer, z: &time.Zone): WriteError!void {
    d := time.FromUnix(1_700_000_000, 0).In(z)
    try Print(out, "ok {o}\n", (o: d.offset_seconds))
}

// LoadLine prints Load's outcome for name: "ok" and the offset, or the error.
LoadLine(sys: &System, out: mut Writer, io: Io, name: str, emb: ?time.Database): WriteError!void {
    z := time.Zone.Load(sys.arena, io, name, emb) else e {
        try Print(out, "E {e}\n", (e))
        return
    }
    try Probe(sys, out, &z)
}

// LocalLine prints Local's outcome: "ok" and the offset, or the error.
LocalLine(sys: &System, out: mut Writer, io: Io): WriteError!void {
    z := time.Zone.Local(sys.arena, io) else e {
        try Print(out, "E {e}\n", (e))
        return
    }
    try Probe(sys, out, &z)
}

export Main(sys: &System): WriteError|ReadError|OutOfMemory!void {
    var out := sys.Stdout()
    for arg in sys.args[1..] {
        if arg.StartsWith("load:") || arg.StartsWith("db:") {
            db := arg.StartsWith("db:")
            name := if db { arg[3..] } else { arg[5..] }
            emb := if db { some(tzdata.Database) } else { none }
            z := time.Zone.Load(sys.arena, sys.io, name, emb) else e {
                try Print(mut out, "E {e}\n", (e))
                continue
            }
            try Probe(sys, mut out, &z)
            continue
        }
        if arg == "local" {
            z := time.Zone.Local(sys.arena, sys.io) else e {
                try Print(mut out, "E {e}\n", (e))
                continue
            }
            var inp := sys.Stdin()
            for {
                line := try inp.Line(sys.arena) else {
                    break
                }
                n := line.ParseInt() else { 0 }
                d := time.FromUnix(n, 0).In(&z)
                try Print(mut out, "{o}\n", (o: d.offset_seconds))
            }
            continue
        }
        if arg.StartsWith("date:") {
            z := time.Zone.Load(sys.arena, sys.io, arg[5..], none) else e {
                try Print(mut out, "E {e}\n", (e))
                continue
            }
            var inp := sys.Stdin()
            for {
                line := try inp.Line(sys.arena) else {
                    break
                }
                var f: [6]i64 = [0; 6]
                var k := 0
                for w in line.Words() {
                    if k < 6 {
                        f[k] = w.ParseInt() else { 0 }
                        k += 1
                    }
                }
                t := time.FromDate(Trunc[i32](f[0]), Cast[int](f[1]), Cast[int](f[2]), Cast[int](f[3]), Cast[int](f[4]), Cast[int](f[5]), 0, &z) else e {
                    try Print(mut out, "E {e}\n", (e))
                    continue
                }
                try Print(mut out, "{t}\n", (t: t.UnixNanos()))
            }
            continue
        }
        if arg == "wall" {
            w := time.Wall(sys.io)
            utc := time.Zone.Utc()
            d := w.In(&utc)
            try Print(mut out, "{t} {y} {m} {d}\n", (t: w.UnixNanos(), y: d.year, m: d.month, d: d.day))
            continue
        }
        if arg == "name" {
            n := time.LocalName(sys.arena, sys.io) else { "-" }
            try Print(mut out, "{n}\n", (n))
            continue
        }
        if arg == "cancel" {
            // In a cancelled scope Load fails with Canceled when it
            // reaches a file; the same call after the scope succeeds.
            io := sys.io
            scope c := io.Scope() {
                c.Cancel()
                try LoadLine(sys, mut out, io, "Europe/Paris", none)
                try LocalLine(sys, mut out, io)
            }
            try LoadLine(sys, mut out, io, "Europe/Paris", none)
            continue
        }
        if arg == "simcancel" {
            var s := try sim.Sim.New(sys.arena, 7)
            defer s.Free()
            io := s.Io()
            scope c := io.Scope() {
                c.Cancel()
                // The control: a Sim cancel point in the same scope.
                io.Yield() else e {
                    try Print(mut out, "yield E {e}\n", (e))
                }
                try LoadLine(sys, mut out, io, "Europe/Paris", some(tzdata.Database))
                try LocalLine(sys, mut out, io)
            }
            try LoadLine(sys, mut out, io, "Europe/Paris", some(tzdata.Database))
            continue
        }
        if arg == "sim" {
            var s := try sim.Sim.New(sys.arena, 7)
            defer s.Free()
            io := s.Io()
            try Print(mut out, "{t}\n", (t: time.Wall(io).UnixNanos()))
            local := time.Zone.Local(sys.arena, io) else e {
                try Print(mut out, "E {e}\n", (e))
                continue
            }
            try Print(mut out, "{o}\n", (o: time.Wall(io).In(&local).offset_seconds))
            _ = time.Zone.Load(sys.arena, io, "Europe/Paris", none) else e {
                try Print(mut out, "E {e}\n", (e))
            }
            p := time.Zone.Load(sys.arena, io, "Europe/Paris", some(tzdata.Database)) else e {
                try Print(mut out, "E {e}\n", (e))
                continue
            }
            try Probe(sys, mut out, &p)
        }
    }
}
`

var time2LoadExe string

// time2LoadRun runs the program with args, env added and stdin.
func time2LoadRun(t *testing.T, env []string, stdin string, args ...string) []string {
	t.Helper()
	if time2LoadExe == "" {
		time2LoadExe = time2Build(t, nil, time2LoadProg)
		dir, err := os.MkdirTemp(os.Getenv("TMP"), "time2load")
		if err != nil {
			t.Fatal(err)
		}
		keep := filepath.Join(dir, filepath.Base(time2LoadExe))
		b, err := os.ReadFile(time2LoadExe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keep, b, 0o755); err != nil {
			t.Fatal(err)
		}
		time2LoadExe = keep
	}
	cmd := exec.Command(time2LoadExe, args...)
	var keepEnv []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "TZDIR=") && !strings.HasPrefix(strings.ToUpper(kv), "TZ=") {
			keepEnv = append(keepEnv, kv)
		}
	}
	cmd.Env = append(keepEnv, env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.Split(strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n")
}

func TestStdTime_LoadFromDisk(t *testing.T) {
	zones, _, _ := time2Zones(t)
	var paris []byte
	for _, z := range zones {
		if z.name == "Europe/Paris" {
			paris = z.data
		}
	}
	dir := t.TempDir()
	write := func(name string, data []byte) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Test/Paris", paris)
	write("Test/Garbage", []byte("not a zone"))
	big := make([]byte, 1<<20+1)
	copy(big, paris)
	write("Test/Big", big)
	limit := append([]byte(nil), paris...)
	write("Test/Exact", limit)
	long := strings.Repeat("a", 256)
	cases := []struct{ arg, want string }{
		{"load:Test/Paris", "ok 3600"},
		{"load:Test/Exact", "ok 3600"},
		{"load:Test/Missing", "E NotFound"},
		{"db:Test/Missing", "E NotFound"},
		{"load:Test/Garbage", "E BadData"},
		{"load:Test/Big", "E BadData"},
		{"load:Test", "E NotFound"},
		{"db:Europe/Paris", "ok 3600"},
		{"load:../Test/Paris", "E BadName"},
		{"load:Test/./Paris", "E BadName"},
		{"load:Test//Paris", "E BadName"},
		{"load:/Test/Paris", "E BadName"},
		{"load:Test/Paris/", "E BadName"},
		{"load:Test\\Paris", "E BadName"},
		{"load:Test/Pa ris", "E BadName"},
		{"load:" + long, "E BadName"},
		{"load:" + long[:255], "E NotFound"},
	}
	var args []string
	for _, c := range cases {
		args = append(args, c.arg)
	}
	got := time2LoadRun(t, []string{"TZDIR=" + dir}, "", args...)
	for i, c := range cases {
		if i >= len(got) || got[i] != c.want {
			t.Errorf("%.40s with TZDIR: got %q, want %q", c.arg, got[min(i, len(got)-1)], c.want)
		}
	}
	// No TZDIR: Windows has nowhere to look; Linux looks in
	// /usr/share/zoneinfo.
	got = time2LoadRun(t, nil, "", "load:Test/Paris", "db:Test/Paris", "db:Europe/Paris", "load:")
	want := []string{"E NoDatabase", "E NotFound", "ok 3600", "E BadName"}
	if runtime.GOOS != "windows" {
		want[0] = "E NotFound"
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("without TZDIR: got %q, want %q", got, want)
	}
}

func TestStdTime_Sim(t *testing.T) {
	got := time2LoadRun(t, []string{"TZDIR=" + t.TempDir(), "TZ=Europe/Paris"}, "", "sim")
	// The simulated clock starts at 2026-01-01T00:00:00Z on every seed and
	// has moved by the Sim's own steps when Wall reads it.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	if len(got) > 0 {
		var w int64
		fmt.Sscan(got[0], &w)
		if w < start || w > start+int64(time.Millisecond) {
			t.Errorf("Wall under std.sim: %d, want 2026-01-01T00:00:00Z (%d) or just after", w, start)
		}
		got = got[1:]
	}
	// The Sim's disk is empty, so no place to look is a directory:
	// NoDatabase on both targets.
	want := []string{"0", "E NoDatabase", "ok 3600"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("under std.sim: got %q, want %q", got, want)
	}
}

func TestStdTime_CancelRepeatable(t *testing.T) {
	zones, _, _ := time2Zones(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Europe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Europe", "Paris"), time2ZoneData(zones, "Europe/Paris"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Natively: Load reaches TZDIR's file and fails with Canceled, then the
	// same call outside the scope reads it (Load is repeatable). On
	// Windows Local makes no cancellable call (the OS's zone is an inline
	// call), so it answers in the cancelled scope too.
	got := time2LoadRun(t, []string{"TZDIR=" + dir}, "", "cancel")
	if len(got) != 3 || got[0] != "E Canceled" || got[2] != "ok 3600" {
		t.Errorf("cancel: got %q, want Canceled, Local's answer, then ok 3600", got)
	}
	if runtime.GOOS == "windows" && !strings.HasPrefix(got[1], "ok ") {
		t.Errorf("cancel: Local on Windows answered %q", got[1])
	}
	// Under std.sim Load reads no file on any target, so it goes to the
	// embedded database (no cancel point), and Local is UTC with no call
	// (the same Sim operations on every target).
	got = time2LoadRun(t, nil, "", "simcancel")
	want := []string{"yield E Canceled", "ok 3600", "ok 0", "ok 3600"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("simcancel: got %q, want %q", got, want)
	}
}
