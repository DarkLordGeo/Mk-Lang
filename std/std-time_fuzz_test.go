// Why: std.time's untrusted-input entry points (Zone.FromTzif and so
// Zone.Load, ParseRfc3339, ParseHttpDate, ParseDuration) must not crash or hang.
// What: one fuzz target each, and TestStdTime_TzifAdversarial.
// How: targets are seeded from the oracle's data and run through the
// long-running oracle program; the adversarial test truncates and corrupts
// every zone and compares the ones both std.time and Go accept.
//
//	go test ./std -run '^$' -fuzz FuzzStdTime_Tzif -fuzztime 5m -parallel 2

package std

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// time2TzifCheck runs FromTzif on data through the oracle: it fails t when
// the program does not answer, and when std.time and Go both accept data
// and disagree on an offset at the probe instants.
func time2TzifCheck(t testing.TB, data []byte) {
	probes := []int64{-1 << 62, -2e18, -1e18, 0, 1e18, 1.7e18, 2e18, 4e18, 1 << 62}
	zhex := hex.EncodeToString(data)
	if zhex == "" {
		zhex = "-"
	}
	ans := time2Ask(t, false, "Z "+zhex+" "+time2Ints(probes), 1+len(probes))
	if !strings.HasPrefix(ans[0], "ok") {
		if !strings.HasPrefix(ans[0], "E ") {
			t.Fatalf("FromTzif answered %q", ans[0])
		}
		return
	}
	rest := ans[1:]
	loc, err := time.LoadLocationFromTZData("z", data)
	if err != nil {
		return
	}
	for i, ns := range probes {
		tm := time.Unix(0, ns).In(loc)
		_, off := tm.Zone()
		// Before the first transition, or with none, Go applies its
		// lookupFirstZone heuristic and ignores the footer; std.time
		// follows RFC 8536 3.2 (time type 0, or the footer when there is
		// no transition). Real zones agree (ZonesAgainstGo).
		if start, _ := tm.ZoneBounds(); start.IsZero() {
			continue
		}
		if rest[i] != strconv.Itoa(off) {
			t.Errorf("FromTzif of %d bytes at %d: offset %s, Go %d", len(data), ns, rest[i], off)
		}
	}
}

func TestStdTime_TzifAdversarial(t *testing.T) {
	zones, _, _ := time2Zones(t)
	r := rand.New(rand.NewPCG(21, 22))
	n := 0
	for zi, z := range zones {
		if zi%7 != 0 {
			continue
		}
		// Every prefix of a few zones, a sample of prefixes of the rest.
		step := 1
		if zi%49 != 0 {
			step = 1 + len(z.data)/40
		}
		for k := 0; k < len(z.data); k += step {
			time2TzifCheck(t, z.data[:k])
			n++
		}
		for range 40 {
			b := append([]byte(nil), z.data...)
			b[r.IntN(len(b))] ^= byte(1 << r.IntN(8))
			time2TzifCheck(t, b)
			n++
		}
	}
	// Hostile headers: counts at their largest, a count past the data, and
	// a second header that disagrees with the first.
	base := zones[0].data
	for _, c := range []uint32{0xFFFFFFFF, 0x7FFFFFFF, uint32(len(base)), uint32(len(base) + 1), 1 << 20} {
		for field := range 6 {
			b := append([]byte(nil), base...)
			binary.BigEndian.PutUint32(b[20+4*field:], c)
			time2TzifCheck(t, b)
			n++
		}
	}
	for _, in := range time2TzifRegress {
		time2TzifCheck(t, []byte(in))
		n++
	}
	t.Logf("%d hostile TZif inputs: no crash, and every zone both read agrees with Go", n)
}

// time2TzifRegress are inputs the fuzzer found: a first transition to a
// daylight type, where Go's lookupFirstZone and RFC 8536 3.2 differ before
// the first transition.
var time2TzifRegress = []string{
	"TZif2000000000000000\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00000000TZif2000000000000000\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00J\x00\x00\x00\a\x00\x00\x00\x1c\xff\xff\xff\xff0000\xff\xff\xff\xff1000\xff\xff\xff\xff1001\x00\x00\x00\x00\x04000\x00\x00\x00\x00\x05000\x00\x00\x00\x00\x06000\x00\x00\x00\x00\a000\x00\x00\x00\x00\b000\x00\x00\x00\x00\t000\x00\x00\x00\x00\n000\x00\x00\x00\x00\n001\x00\x00\x00\x00\v000\x00\x00\x00\x00\f000\x00\x00\x00\x00\r000\x00\x00\x00\x00\x0e000\x00\x00\x00\x00\x0f000\x00\x00\x00\x00\x10000\x00\x00\x00\x00\x11000\x00\x00\x00\x00\x12000\x00\x00\x00\x00\x13000\x00\x00\x00\x00\x14000\x00\x00\x00\x00\x15000\x00\x00\x00\x00\x16000\x00\x00\x00\x00\x17000\x00\x00\x00\x00\x18000\x00\x00\x00\x00\x19000\x00\x00\x00\x00\x1a000\x00\x00\x00\x00\x1a100\x00\x00\x00\x00\x1b000\x00\x00\x00\x00\x1c000\x00\x00\x00\x00\x1d000\x00\x00\x00\x00\x1e000\x00\x00\x00\x00\x1f000\x00\x00\x00\x00 000\x00\x00\x00\x00 001\x00\x00\x00\x00 002\x00\x00\x00\x00 007\x00\x00\x00\x00 008\x00\x00\x00\x00 009\x00\x00\x00\x00!000\x00\x00\x00\x00\"000\x00\x00\x00\x00\"001\x00\x00\x00\x00#000\x00\x00\x00\x00#001\x00\x00\x00\x00#002\x00\x00\x00\x00#007\x00\x00\x00\x00#008\x00\x00\x00\x00#009\x00\x00\x00\x00#00A\x00\x00\x00\x000000\x00\x00\x00\x000100\x00\x00\x00\x000101\x00\x00\x00\x000102\x00\x00\x00\x000107\x00\x00\x00\x000108\x00\x00\x00\x001000\x00\x00\x00\x001001\x00\x00\x00\x002000\x00\x00\x00\x007000\x00\x00\x00\x007010\x00\x00\x00\x007011\x00\x00\x00\x007012\x00\x00\x00\x007017\x00\x00\x00\x007018\x00\x00\x00\x007019\x00\x00\x00\x00701A\x00\x00\x00\x00701B\x00\x00\x00\x00701C\x00\x00\x00\x00701X\x00\x00\x00\x00701Y\x00\x00\x00\x00701Z\x00\x00\x00\x008000\x00\x00\x00\x00800100000000\x04\x01\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x00\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x05\x06\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x02\x03\x00\x0000\x00\x00\xff\xff00\x01\x04\xff\xff00\x00\b\xff\xff00\x01\f\xff\xff00\x01\x10\xff\xff00\x00\x14\xff\xff00\x01\x180000000000000000000000000000\n\n",
}

func FuzzStdTime_Tzif(f *testing.F) {
	zones, _, _ := time2Zones(f)
	for i, z := range zones {
		if i%10 == 0 {
			f.Add(z.data)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 24<<10 {
			return
		}
		time2TzifCheck(t, data)
	})
}

func FuzzStdTime_Rfc3339(f *testing.F) {
	for _, s := range []string{"1985-04-12T23:20:50.52Z", "1996-12-19T16:39:57-08:00", "1990-12-31T23:59:60Z", "1990-12-31T15:59:60-08:00", "1937-01-01T12:00:27.87+00:20", "2262-04-11T23:47:16.854775807Z"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 || strings.ContainsAny(s, "\n") {
			return
		}
		got := time2Ask(t, false, "P3 "+time2Hex(s), 1)[0]
		if got == "E" {
			return
		}
		if !time2IsRfc3339(s) {
			t.Fatalf("ParseRfc3339(%q) = %s, but it is not RFC 3339", s, got)
		}
		if gt, err := time.Parse(time.RFC3339Nano, s); err == nil && !strings.ContainsAny(s, "tz") {
			if want := strconv.FormatInt(gt.UnixNano(), 10); got != want {
				t.Fatalf("ParseRfc3339(%q) = %s, Go %s", s, got, want)
			}
		}
	})
}

func FuzzStdTime_HttpDate(f *testing.F) {
	for _, s := range []string{"Sun, 06 Nov 1994 08:49:37 GMT", "Sunday, 06-Nov-94 08:49:37 GMT", "Sun Nov  6 08:49:37 1994"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 || strings.ContainsAny(s, "\n") {
			return
		}
		got := time2Ask(t, false, "PH "+time2Hex(s), 1)[0]
		if got == "E" {
			return
		}
		gt, err := http.ParseTime(s)
		if err != nil {
			t.Fatalf("ParseHttpDate(%q) = %s; Go refuses: %v", s, got, err)
		}
		if want := strconv.FormatInt(gt.UnixNano(), 10); got != want {
			t.Fatalf("ParseHttpDate(%q) = %s, Go %s", s, got, want)
		}
	})
}

func FuzzStdTime_Duration(f *testing.F) {
	for _, s := range []string{"0", "1.5h", "-2h45m", "300ms", "1µs", "9223372036854775807ns", ".5s"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 || strings.ContainsAny(s, "\n") {
			return
		}
		got := time2Ask(t, false, "PD "+time2Hex(s), 1)[0]
		gd, err := time.ParseDuration(s)
		want := "E"
		if err == nil {
			want = fmt.Sprint(int64(gd))
		}
		if got != want {
			t.Fatalf("ParseDuration(%q): got %s, want %s", s, got, want)
		}
	})
}
