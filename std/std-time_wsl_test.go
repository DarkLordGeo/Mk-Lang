// Why: std.time reads zones differently on Linux (TZ, TZDIR, /usr/share/zoneinfo).
// What: TestStdTime_Linux, std.time on x86_64-linux through wsl.exe.
// How: the oracle program answers the Windows requests in one batch, then Zone.Load,
// Zone.Local from each form of TZ, and std.sim (MK_LINUX=1, tools/wsl-lock.ps1, -p 1).

package std

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// time2BuildLinux builds src for x86_64-linux (release) and returns the
// executable.
func time2BuildLinux(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	stdDir := time2Std(t, root, nil)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	out, status := mkCmd(t, root, stdDir, "build", "--release", "--target", "x86_64-linux", "-o", exe, "main.mk")
	if status != 0 {
		t.Fatalf("mk build for x86_64-linux: exit %d\n%s", status, out)
	}
	return exe
}

// time2WSL runs a Linux executable with env, args and stdin through
// wsl.exe, bounded by 5 minutes, and returns its output lines.
func time2WSL(t *testing.T, exe string, env []string, stdin string, args ...string) []string {
	t.Helper()
	wslGuard()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	argv := append([]string{"--cd", wslPath(filepath.Dir(exe)), "--", "env", "-u", "TZ", "-u", "TZDIR"}, env...)
	argv = append(argv, "./"+filepath.Base(exe))
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, "wsl.exe", argv...)
	cmd.WaitDelay = 10 * time.Second
	cmd.Stdin = strings.NewReader(stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		t.Fatalf("wsl.exe %q: %v\n%s", argv, err, e.String())
	}
	return strings.Split(strings.TrimRight(o.String(), "\n"), "\n")
}

func TestStdTime_Linux(t *testing.T) {
	needWSL(t)
	zones, _, _ := time2Zones(t)
	r := rand.New(rand.NewPCG(1, 2))

	// The oracle program, in one batch: every zone's instants.
	exe := time2BuildLinux(t, time2Prog)
	var in strings.Builder
	var want []string
	for _, z := range zones {
		loc, err := time.LoadLocationFromTZData(z.name, z.data)
		if err != nil {
			t.Fatal(err)
		}
		inst := time2Instants(loc, r)
		for i := 0; i < len(inst); i += 2000 {
			batch := inst[i:min(i+2000, len(inst))]
			in.WriteString("I " + z.name + " " + time2Ints(batch) + "\n")
			for _, ns := range batch {
				want = append(want, time2Fields(time.Unix(0, ns).In(loc)))
			}
		}
	}
	in.WriteString("F America/New_York 2024 3 10 2 30 0 0\nF America/New_York 2024 11 3 1 30 0 0\nF UTC 2262 4 11 23 47 16 854775808\n")
	want = append(want, "E Skipped", "E Repeated", "E OutOfRange")
	got := time2WSL(t, exe, nil, in.String())
	if len(got) != len(want) {
		t.Fatalf("Linux oracle: %d answers for %d requests", len(got), len(want))
	}
	bad := 0
	for i := range want {
		if got[i] != want[i] {
			bad++
			if bad < 10 {
				t.Errorf("Linux answer %d: got %s, want %s", i, got[i], want[i])
			}
		}
	}
	t.Logf("x86_64-linux, release: %d zone instants and FromDate cases agree with Go", len(want)-bad)

	// Load from a TZDIR on the Windows drive, and from the system's
	// /usr/share/zoneinfo when TZDIR is unset.
	var paris []byte
	for _, z := range zones {
		if z.name == "Europe/Paris" {
			paris = z.data
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Test"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Test", "Paris"), paris, 0o644); err != nil {
		t.Fatal(err)
	}
	// A zone file Zone.Local cannot read: over the 1 MiB limit (a chmod
	// would not stop a root user or a file on the Windows drive).
	big := make([]byte, 2<<20)
	copy(big, paris)
	if err := os.WriteFile(filepath.Join(dir, "Test", "Big"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	tzdir := "TZDIR=" + wslPath(dir)
	lexe := time2BuildLinux(t, time2LoadProg)
	got = time2WSL(t, lexe, []string{tzdir}, "", "load:Test/Paris", "load:Test/Missing", "load:Test", "load:../Test/Paris", "db:Europe/Paris", "load:Test/Big")
	if w := []string{"ok 3600", "E NotFound", "E NotFound", "E BadName", "ok 3600", "E BadData"}; strings.Join(got, "|") != strings.Join(w, "|") {
		t.Errorf("Linux Load with TZDIR: got %q, want %q", got, w)
	}
	got = time2WSL(t, lexe, nil, "", "load:Europe/Paris", "load:Nowhere/Zone")
	if w := []string{"ok 3600", "E NotFound"}; strings.Join(got, "|") != strings.Join(w, "|") {
		t.Errorf("Linux Load from /usr/share/zoneinfo: got %q, want %q", got, w)
	}

	// Local from TZ in each form, against Go's offsets for the same rule.
	var inst strings.Builder
	var unix []int64
	for u := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC); u.Year() < 2040; u = u.Add(11*24*time.Hour + 5*time.Hour) {
		fmt.Fprintln(&inst, u.Unix())
		unix = append(unix, u.Unix())
	}
	ny, _ := time.LoadLocationFromTZData("America/New_York", time2ZoneData(zones, "America/New_York"))
	// A zone file that is missing or cannot be read is UTC: for TZ's zone
	// name TZ is then read as a POSIX rule ("JST-9" is +9).
	for _, c := range []struct {
		tz  string
		loc *time.Location
	}{
		{"TZ=Europe/Paris", mustLoc(t, zones, "Europe/Paris")},
		{"TZ=:America/New_York", ny},
		{"TZ=EST5EDT,M3.2.0,M11.1.0", time2RuleLoc("EST5EDT,M3.2.0,M11.1.0")},
		{"TZ='<+0330>-3:30'", time.FixedZone("", 3*3600+1800)},
		{"TZ=", time.UTC},
		{"TZ=Not/A_Zone", time.UTC},
		{"TZ=JST-9", time.FixedZone("", 9*3600)},
		{"TZ=:" + wslPath(filepath.Join(dir, "Test", "Big")), time.UTC},
		{tzdir + " TZ=Test/Big", time.UTC},
		{tzdir + " TZ=Test/Paris", mustLoc(t, zones, "Europe/Paris")},
	} {
		got = time2WSL(t, lexe, strings.Fields(c.tz), inst.String(), "local")
		if len(got) != len(unix) {
			t.Errorf("Local with %s: %d answers for %d instants: %q", c.tz, len(got), len(unix), got[:min(2, len(got))])
			continue
		}
		for i, u := range unix {
			_, off := time.Unix(u, 0).In(c.loc).Zone()
			if got[i] != fmt.Sprint(off) {
				t.Errorf("Local with %s at %d: %s, want %d", c.tz, u, got[i], off)
				break
			}
		}
	}

	// std.sim on Linux: the simulated clock, UTC, and no real TZDIR.
	// Cancellation on Linux: Load from /usr/share/zoneinfo and Local from TZ's
	// file stop at Canceled in a cancelled scope and read after it.
	got = time2WSL(t, lexe, []string{"TZ=Europe/Paris"}, "", "cancel")
	if len(got) != 3 || got[0] != "E Canceled" || got[1] != "E Canceled" || got[2] != "ok 3600" {
		t.Errorf("Linux cancel: got %q, want Canceled, Canceled, ok 3600", got)
	}
	// Under std.sim Load reads no file on Linux either, so it
	// goes to the embedded data with no cancel point, as on Windows.
	got = time2WSL(t, lexe, nil, "", "simcancel")
	if j := strings.Join(got, "|"); j != "yield E Canceled|ok 3600|ok 0|ok 3600" {
		t.Errorf("Linux simcancel: got %q", got)
	}
	t.Logf("Linux simcancel: %q", got)
	// LocalName on Linux: TZ's zone name (":" dropped) when Zone.Local
	// reads that zone's file; none for a POSIX rule, an empty TZ (UTC), a
	// name with no file ("JST-9" is a rule, "Not/A_Zone" UTC) or one it
	// cannot read; and with TZ unset the zone /etc/localtime links to, as
	// readlink reports it.
	for _, c := range []struct{ tz, want string }{
		{"TZ=Europe/Paris", "Europe/Paris"},
		{"TZ=:America/New_York", "America/New_York"},
		{"TZ=EST5EDT,M3.2.0,M11.1.0", "-"},
		{"TZ=", "-"},
		{"TZ=:", "-"},
		{"TZ=JST-9", "-"},
		{"TZ=Not/A_Zone", "-"},
		{tzdir + " TZ=Test/Paris", "Test/Paris"},
		{tzdir + " TZ=Test/Big", "-"},
	} {
		if got = time2WSL(t, lexe, strings.Fields(c.tz), "", "name"); len(got) != 1 || got[0] != c.want {
			t.Errorf("Linux LocalName with %s: got %q, want %q", c.tz, got, c.want)
		}
	}
	link := "-"
	wslGuard()
	if out, err := exec.Command("wsl.exe", "--", "readlink", "/etc/localtime").Output(); err == nil {
		if _, after, ok := strings.Cut(strings.TrimSpace(string(out)), "zoneinfo/"); ok {
			link = after
		}
	}
	if got = time2WSL(t, lexe, nil, "", "name"); len(got) != 1 || got[0] != link {
		t.Errorf("Linux LocalName from /etc/localtime: got %q, readlink gives %q", got, link)
	}
	t.Logf("Linux LocalName from /etc/localtime: %q", link)
	// time.Wall on the Linux clock, bracketed by date(1) in the same
	// wsl.exe call, since the distribution's clock may differ from this
	// host's by more than the slack.
	got = time2WSLShell(t, lexe, "date +%s%N; ./"+filepath.Base(lexe)+" wall; date +%s%N")
	if len(got) != 3 {
		t.Fatalf("Linux wall: %q", got)
	}
	before, err1 := strconv.ParseInt(got[0], 10, 64)
	after, err2 := strconv.ParseInt(got[2], 10, 64)
	if err1 != nil || err2 != nil {
		t.Fatalf("Linux wall: the date(1) bracket is not nanoseconds: %q", got)
	}
	time2CheckWall(t, got[1:2], before, after)
	got = time2WSL(t, lexe, []string{"TZDIR=" + wslPath(dir), "TZ=Europe/Paris"}, "", "sim")
	if len(got) != 4 || got[1] != "0" || got[2] != "E NoDatabase" || got[3] != "ok 3600" {
		t.Errorf("Linux std.sim: got %q", got)
	}
}

// time2WSLShell runs script with sh in the directory of exe through
// wsl.exe, TZ and TZDIR unset, bounded by 5 minutes, and returns its
// output lines.
func time2WSLShell(t *testing.T, exe, script string) []string {
	t.Helper()
	wslGuard()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	argv := []string{"--cd", wslPath(filepath.Dir(exe)), "--", "env", "-u", "TZ", "-u", "TZDIR", "sh", "-c", script}
	cmd := exec.CommandContext(ctx, "wsl.exe", argv...)
	cmd.WaitDelay = 10 * time.Second
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		t.Fatalf("wsl.exe %q: %v\n%s", argv, err, e.String())
	}
	return strings.Split(strings.TrimRight(o.String(), "\n"), "\n")
}

// time2ZoneData is the bytes of the zone name of the pinned database.
func time2ZoneData(zones []time2Zone, name string) []byte {
	for _, z := range zones {
		if z.name == name {
			return z.data
		}
	}
	return nil
}

func mustLoc(t *testing.T, zones []time2Zone, name string) *time.Location {
	loc, err := time.LoadLocationFromTZData(name, time2ZoneData(zones, name))
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// time2RuleLoc is Go's zone for a POSIX rule string alone: a TZif v2 with
// no transitions, one type and the rule as its footer. Go applies the
// footer only after the last transition, so the file has one transition at
// the start of 1900.
func time2RuleLoc(rule string) *time.Location {
	var b bytes.Buffer
	hdr := func(timecnt, typecnt, charcnt int) {
		b.WriteString("TZif2")
		b.Write(make([]byte, 15))
		for _, c := range []int{0, 0, 0, timecnt, typecnt, charcnt} {
			b.Write([]byte{byte(c >> 24), byte(c >> 16), byte(c >> 8), byte(c)})
		}
	}
	hdr(0, 1, 1)
	b.Write([]byte{0, 0, 0, 0, 0, 0, 0})
	hdr(1, 1, 4)
	t0 := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	for s := 56; s >= 0; s -= 8 {
		b.WriteByte(byte(t0 >> s))
	}
	b.WriteByte(0)
	b.Write([]byte{0xFF, 0xFF, 0xB9, 0xB0, 0, 0})
	b.WriteString("EST\x00")
	b.WriteString("\n" + rule + "\n")
	loc, err := time.LoadLocationFromTZData("rule", b.Bytes())
	if err != nil {
		panic(err)
	}
	return loc
}
