// Why: regression tests for std.time edge cases the oracle tests do not reach.
// What: Zone.Local on Windows, FromDate on 100,000 transitions, Zone.Load's
// peak commit, time.Wall, NoDatabase and RFC 850's two-digit year.
// How: each against a model: Windows' per-year zone records (not Go's
// time.Local, which applies the record in force to every year), a brute-force
// search, the file's size, Go's clock, a TZDIR of no directory, net/http.

package std

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// time2Pseudo is the local seconds of rule date d in year: d[0] set, that
// day; else the d[3]'th (5 the last) weekday d[2] of month d[1], at d's
// time, as Go's pseudoUnix computes it.
func time2Pseudo(year int, d [7]int) (int64, bool) {
	if d[1] < 1 || d[1] > 12 || d[2] > 6 || d[3] < 1 {
		return 0, false
	}
	if d[0] != 0 {
		if d[0] != year || d[3] > time.Date(year, time.Month(d[1])+1, 0, 0, 0, 0, 0, time.UTC).Day() {
			return 0, false
		}
		return time.Date(year, time.Month(d[1]), d[3], d[4], d[5], d[6], 0, time.UTC).Unix(), true
	}
	t := time.Date(year, time.Month(d[1]), 1, d[4], d[5], d[6], 0, time.UTC)
	day := 1 + (d[2]-int(t.Weekday())+7)%7
	if week := d[3] - 1; week < 4 {
		day += week * 7
	} else {
		day += 4 * 7
		if day > time.Date(year, time.Month(d[1])+1, 0, 0, 0, 0, 0, time.UTC).Day() {
			day -= 7
		}
	}
	return t.Unix() + int64(day-1)*86400, true
}

// time2RecordOffset is the offset of record z at instant ns of UTC year y:
// a record of recurring dates as Go's initLocalFromTZI reads it
// (time2GoTzi); one with an absolute date as the rule of that year alone,
// daylight time from the daylight date (local standard time) to the
// standard date (local daylight time), and the standard offset when a date
// is not one or is another year's.
func time2RecordOffset(z time2Tzi, y int, ns int64) int {
	if z.std[1] == 0 || (z.std[0] == 0 && z.dst[0] == 0) {
		return time2GoTzi(z, y, []int64{ns})[0]
	}
	stdOff := -int(z.bias+z.stdBias) * 60
	dstOff := -int(z.bias+z.dstBias) * 60
	start, ok1 := time2Pseudo(y, z.dst)
	end, ok2 := time2Pseudo(y, z.std)
	if !ok1 || !ok2 {
		return stdOff
	}
	start -= int64(stdOff)
	end -= int64(dstOff)
	u := time.Unix(0, ns).Unix()
	in := start <= u && u < end
	if start > end {
		in = u >= start || u < end
	}
	if in {
		return dstOff
	}
	return stdOff
}

// time2PerYear is Zone.Local on Windows as a model of its records: an
// instant of UTC year y takes y's record (recs[0] is first's; the years
// before first take it too) and the record in force, now, past the last
// year.
func time2PerYear(first int, recs []time2Tzi, now time2Tzi, inst []int64) []int {
	last := first + len(recs) - 1
	out := make([]int, len(inst))
	for k, ns := range inst {
		y := time.Unix(0, ns).UTC().Year()
		z := now
		switch {
		case y < first:
			z = recs[0]
		case y <= last:
			z = recs[y-first]
		}
		out[k] = time2RecordOffset(z, y, ns)
	}
	return out
}

// time2NyRecords is New York's own record for each year 1970 to 2026, as
// GetTimeZoneInformationForYear gives a zone's yearly rules: the last
// Sunday of April to the last of October; 1974 and 1975's dates, which
// were no rule, as absolute dates; the first Sunday of April from 1987;
// the second Sunday of March to the first of November from 2007.
func time2NyRecords() []time2Tzi {
	ny := func(dst, std [7]int) time2Tzi { return time2Tzi{bias: 300, dstBias: -60, std: std, dst: dst} }
	var recs []time2Tzi
	for y := 1970; y <= 2026; y++ {
		switch {
		case y == 1974:
			recs = append(recs, ny([7]int{1974, 1, 0, 6, 2, 0, 0}, [7]int{1974, 10, 0, 27, 2, 0, 0}))
		case y == 1975:
			recs = append(recs, ny([7]int{1975, 2, 0, 23, 2, 0, 0}, [7]int{1975, 10, 0, 26, 2, 0, 0}))
		case y < 1987:
			recs = append(recs, ny([7]int{0, 4, 0, 5, 2, 0, 0}, [7]int{0, 10, 0, 5, 2, 0, 0}))
		case y < 2007:
			recs = append(recs, ny([7]int{0, 4, 0, 1, 2, 0, 0}, [7]int{0, 10, 0, 5, 2, 0, 0}))
		default:
			recs = append(recs, ny([7]int{0, 3, 0, 2, 2, 0, 0}, [7]int{0, 11, 0, 1, 2, 0, 0}))
		}
	}
	return recs
}

// The per-year model is itself checked: over New York's records it is the
// IANA zone at every transition of 1970 to 2100 and either side, where one
// record for every year (Go's time.Local on Windows) is not.
func TestStdTime_PerYearModelAgainstIana(t *testing.T) {
	zones, _, _ := time2Zones(t)
	ny := mustLoc(t, zones, "America/New_York")
	recs := time2NyRecords()
	inst := time2TransBetween(ny, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	model := time2PerYear(1970, recs, recs[len(recs)-1], inst)
	single := time2GoTzi(recs[len(recs)-1], 2026, inst)
	bad, singleBad := 0, 0
	for k, ns := range inst {
		_, off := time.Unix(0, ns).In(ny).Zone()
		if model[k] != off {
			if bad++; bad <= 10 {
				t.Errorf("model at %s: %d, IANA %d", time.Unix(0, ns).UTC().Format(time.RFC3339), model[k], off)
			}
		}
		if single[k] != off {
			singleBad++
		}
	}
	if singleBad == 0 {
		t.Error("one record for every year agrees with IANA too: the model check shows nothing")
	}
	t.Logf("per-year model: %d instants agree with IANA; one record for every year differs at %d", len(inst)-bad, singleBad)
}

// TestStdTime_LocalAgainstGo: on Windows, Zone.Local against the per-year
// model over the records kernel32 gives this host (the same calls
// Zone.Local makes), so the test holds on a host of any zone; elsewhere,
// against Go's time.Local, which reads TZ and /etc/localtime as Zone.Local
// does.
func TestStdTime_LocalAgainstGo(t *testing.T) {
	var in strings.Builder
	var inst []int64
	for u := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC); u.Year() < 2040; u = u.Add(7*24*time.Hour + 3*time.Hour) {
		fmt.Fprintln(&in, u.Unix())
		inst = append(inst, u.UnixNano())
	}
	var want []int
	oracle := "Go's time.Local"
	if runtime.GOOS == "windows" {
		recs, now, ok := time2HostRecords(1970, time.Now().UTC().Year())
		if !ok {
			t.Fatal("GetDynamicTimeZoneInformation or GetTimeZoneInformationForYear failed")
		}
		want = time2PerYear(1970, recs, now, inst)
		oracle = "the per-year model of this host's records"
	} else {
		for _, ns := range inst {
			_, off := time.Unix(0, ns).In(time.Local).Zone()
			want = append(want, off)
		}
	}
	got := time2LoadRun(t, nil, in.String(), "local")
	if len(got) != len(want) {
		t.Fatalf("Local: %d answers for %d instants: %q", len(got), len(want), got[:min(3, len(got))])
	}
	bad := 0
	for i := range want {
		if got[i] != strconv.Itoa(want[i]) {
			if bad++; bad < 5 {
				t.Errorf("Local at %s: offset %s, %s %d", time.Unix(0, inst[i]).UTC().Format(time.RFC3339), got[i], oracle, want[i])
			}
		}
	}
	t.Logf("Zone.Local agrees with %s at %d instants of 2000 to 2040", oracle, len(want)-bad)
}

// TestStdTime_LocalRecordsOfEastern: a host set to Eastern Standard Time,
// whose records changed in 2007, without changing this host's zone: the
// records kernel32 gives for that zone through TziTable, the table
// Zone.Local builds, against the per-year model, and the model against
// IANA's New York where the records claim the IANA rules.
func TestStdTime_LocalRecordsOfEastern(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("kernel32's zone records are Windows'")
	}
	last := time.Now().UTC().Year()
	recs, now, ok := time2ZoneRecords("Eastern Standard Time", 1970, last)
	if !ok {
		t.Skip("this system gives no records for Eastern Standard Time")
	}
	zones, _, _ := time2Zones(t)
	ny := mustLoc(t, zones, "America/New_York")
	inst := time2TransBetween(ny, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC))
	model := time2PerYear(1970, recs, now, inst)
	single := time2GoTzi(now, last, inst)
	ianaBad, singleBad := 0, 0
	for k, ns := range inst {
		_, off := time.Unix(0, ns).In(ny).Zone()
		if model[k] != off {
			ianaBad++
		}
		if single[k] != off {
			singleBad++
		}
	}
	t.Logf("Eastern's records 2000 to 2040: the per-year model differs from IANA at %d of %d instants, one record for every year (Go's time.Local) at %d", ianaBad, len(inst), singleBad)
	for _, release := range time2Builds {
		got := time2TziAsk(t, release, 1970, recs, now, inst)
		bad := 0
		for k, ns := range inst {
			if got[k] != strconv.Itoa(model[k]) {
				if bad++; bad <= 10 {
					t.Errorf("TziTable over Eastern's records at %s (release %v): %s, model %d", time.Unix(0, ns).UTC().Format(time.RFC3339), release, got[k], model[k])
				}
			}
		}
	}
}

// time2DenseZone is a version 2 TZif of n transitions one second apart
// from 2100-01-01 (past 32 bits, so the version 1 block has none), each to
// one of three offsets at random, the last to offset 0 as the footer is.
func time2DenseZone(n int, r *rand.Rand) (time2Tz, []int32) {
	offs := []int32{0, 3600, 5401}
	var z time2Tz
	for _, o := range offs {
		var ty [6]byte
		binary.BigEndian.PutUint32(ty[:], uint32(o))
		z.types = append(z.types, ty)
	}
	z.chars = []byte("ZZZ\x00")
	z.footer = "UTC0"
	start := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	for i := range n {
		z.trans = append(z.trans, start+int64(i))
		z.idx = append(z.idx, byte(r.IntN(3)))
	}
	z.idx[n-1] = 0
	return z, offs
}

// time2DenseShown is the brute-force answer for local second l: the
// instants l - o for each offset o whose period holds l - o.
func time2DenseShown(z time2Tz, offs []int32, l int64) []int64 {
	offAt := func(u int64) int32 {
		i, found := slices.BinarySearch(z.trans, u)
		if !found {
			i--
		}
		if i < 0 {
			return offs[0]
		}
		return offs[z.idx[i]]
	}
	var out []int64
	for _, o := range offs {
		if c := l - int64(o); offAt(c) == o {
			out = append(out, c)
		}
	}
	return out
}

// TestStdTime_ResolveDenseZone: FromDate in a zone of 100,000 one-second
// periods (about 900 KB of TZif, under the 1 MiB limit) against the
// brute-force model, at local times before, inside and after the block.
// Zone.Resolve finds the first period by one binary search and walks the
// rest by index, O(log n + k); the elapsed time is logged, not asserted.
func TestStdTime_ResolveDenseZone(t *testing.T) {
	r := rand.New(rand.NewPCG(51, 52))
	const n = 100_000
	z, offs := time2DenseZone(n, r)
	data := z.encode('2')
	if len(data) > 1<<20 {
		t.Fatalf("the dense zone is %d bytes, over the limit", len(data))
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Dense"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dense", "Zone"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	start := z.trans[0]
	var locals []int64
	for _, d := range []int64{-90000, -3600, -1, 0, 1, 3600, 5401, n / 2, n - 5402, n - 1, n, n + 3600, n + 90000} {
		locals = append(locals, start+d)
	}
	for range 300 {
		locals = append(locals, start-7200+r.Int64N(n+14400))
	}
	var in strings.Builder
	var want []string
	counts := map[string]int{}
	for _, l := range locals {
		c := time.Unix(l, 0).UTC()
		fmt.Fprintf(&in, "%d %d %d %d %d %d\n", c.Year(), int(c.Month()), c.Day(), c.Hour(), c.Minute(), c.Second())
		switch s := time2DenseShown(z, offs, l); len(s) {
		case 0:
			want = append(want, "E Skipped")
		case 1:
			want = append(want, strconv.FormatInt(s[0]*1e9, 10))
		default:
			want = append(want, "E Repeated")
		}
		counts[want[len(want)-1][:1]]++
	}
	time2LoadRun(t, nil, "", "load:Missing/Zone")
	began := time.Now()
	got := time2LoadRun(t, []string{"TZDIR=" + dir}, in.String(), "date:Dense/Zone")
	took := time.Since(began)
	if len(got) != len(want) {
		t.Fatalf("FromDate in the dense zone: %d answers for %d local times: %q", len(got), len(want), got[:min(3, len(got))])
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("FromDate at local %d (block start %+d): %s, want %s", locals[i], locals[i]-start, got[i], want[i])
		}
	}
	t.Logf("dense zone: %d transitions, %d bytes; %d FromDate calls (outcomes by first letter %v) in one process, %v with the load", n, len(data), len(locals), counts, took)
}

// TestStdTime_LoadReadsTheFileSize: Zone.Load reads a zone file into a
// buffer of the file's size plus one byte, so loading a 1.1 KB zone
// raises the process's peak commit by far less than the 1 MiB limit.
// Windows only (the peak is PROCESS_MEMORY_COUNTERS.PeakPagefileUsage);
// each side is the least of three runs.
func TestStdTime_LoadReadsTheFileSize(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the peak commit is read on Windows")
	}
	zones, _, _ := time2Zones(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Test"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Test", "Paris"), time2ZoneData(zones, "Europe/Paris"), 0o644); err != nil {
		t.Fatal(err)
	}
	time2LoadRun(t, nil, "", "load:Missing/Zone")
	peak := func(arg string) uint64 {
		best := ^uint64(0)
		for range 3 {
			cmd := exec.Command(time2LoadExe, arg)
			cmd.Env = append(time2LoadEnv(), "TZDIR="+dir)
			best = min(best, time2PeakCommit(t, cmd))
		}
		return best
	}
	missing := peak("load:Test/Missing")
	found := peak("load:Test/Paris")
	if found > missing+256<<10 {
		t.Errorf("Load of a 1.1 KB zone: peak commit %d bytes, %d more than a Load that finds no file; want under 256 KiB more", found, found-missing)
	}
	t.Logf("peak commit: Load of Europe/Paris from TZDIR %d bytes, of a missing zone %d (difference %d)", found, missing, int64(found)-int64(missing))
}

// TestStdTime_WallAgainstGo: time.Wall on the native clock is Go's
// time.Now within 50 ms either side of the run, and its UTC date through
// Time.In is the date Go gives the same instant.
func TestStdTime_WallAgainstGo(t *testing.T) {
	time2LoadRun(t, nil, "", "load:Missing/Zone")
	before := time.Now()
	got := time2LoadRun(t, nil, "", "wall")
	after := time.Now()
	time2CheckWall(t, got, before.UnixNano(), after.UnixNano())
}

// time2CheckWall checks a "wall" answer against a bracket of Unix
// nanoseconds.
func time2CheckWall(t *testing.T, got []string, before, after int64) {
	t.Helper()
	var ns int64
	var y, m, d int
	if len(got) != 1 {
		t.Fatalf("wall: %q", got)
	}
	if _, err := fmt.Sscan(got[0], &ns, &y, &m, &d); err != nil {
		t.Fatalf("wall: %q: %v", got[0], err)
	}
	const slack = int64(50 * time.Millisecond)
	if ns < before-slack || ns > after+slack {
		t.Errorf("Wall: %d, outside Go's clock before and after the run, %d to %d", ns, before, after)
	}
	gy, gm, gd := time.Unix(0, ns).UTC().Date()
	if y != gy || m != int(gm) || d != gd {
		t.Errorf("Wall's UTC date: %d-%02d-%02d, Go gives %d-%02d-%02d for %d", y, m, d, gy, gm, gd, ns)
	}
	t.Logf("Wall %s, %.3f ms after the clock read before the run", time.Unix(0, ns).UTC().Format(time.RFC3339Nano), float64(ns-before)/1e6)
}

// TestStdTime_NoDatabaseWithoutDirectory: a TZDIR that is missing or is a
// file is nowhere to look, so with no embedded database Load fails with
// NoDatabase on Windows (Linux still has /usr/share/zoneinfo when it is a
// directory); an empty TZDIR directory is somewhere to look, so NotFound.
func TestStdTime_NoDatabaseWithoutDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	nowhere := "E NoDatabase"
	if runtime.GOOS != "windows" {
		if st, err := os.Stat("/usr/share/zoneinfo"); err == nil && st.IsDir() {
			nowhere = "E NotFound"
		}
	}
	for _, c := range []struct{ tzdir, want string }{
		{filepath.Join(dir, "absent"), nowhere},
		{file, nowhere},
		{empty, "E NotFound"},
	} {
		got := time2LoadRun(t, []string{"TZDIR=" + c.tzdir}, "", "load:Nowhere/Zone", "db:Europe/Paris", "db:Nowhere/Zone")
		if w := []string{c.want, "ok 3600", "E NotFound"}; strings.Join(got, "|") != strings.Join(w, "|") {
			t.Errorf("TZDIR=%s: got %q, want %q", c.tzdir, got, w)
		}
	}
}

// TestStdTime_HttpDateTwoDigitYear: RFC 850's two-digit year for every
// value 00 to 99 against net/http.ParseTime (Go's pivot: 69 to 99 in the
// 1900s), which ParseHttpDate's doc states in place of RFC 9110's 50-year
// rule.
func TestStdTime_HttpDateTwoDigitYear(t *testing.T) {
	for _, release := range time2Builds {
		for yy := range 100 {
			full := 2000 + yy
			if yy >= 69 {
				full = 1900 + yy
			}
			tm := time.Date(full, time.November, 6, 8, 49, 37, 0, time.UTC)
			s := tm.Format("Monday, 02-Jan-06 15:04:05 GMT")
			g, err := http.ParseTime(s)
			if err != nil || !g.Equal(tm) {
				t.Fatalf("net/http reads %q as %v (%v), want %v", s, g, err, tm)
			}
			got := time2Ask(t, release, "PH "+time2Hex(s), 1)[0]
			if want := strconv.FormatInt(tm.UnixNano(), 10); got != want {
				t.Errorf("ParseHttpDate(%q) (release %v): %s, want %s (%d)", s, release, got, want, full)
			}
		}
	}
}

// time2LoadEnv is this process's environment without TZ and TZDIR.
func time2LoadEnv() []string {
	var keep []string
	for _, kv := range os.Environ() {
		u := strings.ToUpper(kv)
		if !strings.HasPrefix(u, "TZDIR=") && !strings.HasPrefix(u, "TZ=") {
			keep = append(keep, kv)
		}
	}
	return keep
}
