// Why: std.time's zones, arithmetic and parsers are checked against an oracle,
// Go's time package with the same compiled zones.
// What: every zone over every transition of 1970 to 2100 and around it, FromDate,
// saturation, Format, ParseRfc3339, ParseHttpDate and ParseDuration.
// How: FromDate's Skipped and Repeated against a model over Go's offsets, saturation
// against math/big, parsers against time.Parse, net/http.ParseTime and time.ParseDuration.

package std

import (
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// time2Release is whether the tests also run the release build.
var time2Builds = []bool{false, true}

// time2Fields is a time's Date in the oracle server's form.
func time2Fields(t time.Time) string {
	_, off := t.Zone()
	return fmt.Sprintf("%d %d %d %d %d %d %d %d %d %d", t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), int(t.Weekday()), t.YearDay(), off)
}

// time2Instants are the instants a zone is checked at: each transition of
// 1970 to 2100 and the second and nanosecond either side, the ends of that
// span, and random instants over Time's whole range.
func time2Instants(loc *time.Location, r *rand.Rand) []int64 {
	start := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	stop := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	out := []int64{start.UnixNano(), stop.UnixNano(), math.MinInt64, math.MaxInt64}
	t := start.In(loc)
	for t.Before(stop) {
		_, end := t.ZoneBounds()
		if end.IsZero() || !end.After(t) {
			break
		}
		e := end.UnixNano()
		out = append(out, e-1, e, e+1, e-1e9, e+1e9)
		t = end
	}
	for range 64 {
		out = append(out, int64(r.Uint64()))
	}
	return out
}

func TestStdTime_ZonesAgainstGo(t *testing.T) {
	zones, _, _ := time2Zones(t)
	r := rand.New(rand.NewPCG(1, 2))
	checked := 0
	for _, release := range time2Builds {
		for _, z := range zones {
			loc, err := time.LoadLocationFromTZData(z.name, z.data)
			if err != nil {
				t.Fatalf("%s: Go: %v", z.name, err)
			}
			inst := time2Instants(loc, r)
			for i := 0; i < len(inst); i += 2000 {
				batch := inst[i:min(i+2000, len(inst))]
				got := time2Ask(t, release, "I "+z.name+" "+time2Ints(batch), len(batch))
				for k, ns := range batch {
					want := time2Fields(time.Unix(0, ns).In(loc))
					if got[k] != want {
						t.Errorf("%s at %d (release %v): got %s, want %s", z.name, ns, release, got[k], want)
					}
					checked++
				}
			}
		}
	}
	t.Logf("%d zones, %d instants, dev and release: all agree", len(zones), checked)
}

// time2Shown counts the distinct instants whose local time in loc is the
// civil fields given, by Go's offsets: an independent model of FromDate.
func time2Shown(loc *time.Location, y, mo, d, h, mi, s int) []int64 {
	local := time.Date(y, time.Month(mo), d, h, mi, s, 0, time.UTC).Unix()
	seen := map[int64]bool{}
	var out []int64
	// Every offset in force within two days of the local time.
	for u := local - 2*86400; u <= local+2*86400; u += 1800 {
		_, off := time.Unix(u, 0).In(loc).Zone()
		c := local - int64(off)
		if _, o := time.Unix(c, 0).In(loc).Zone(); o == off && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func TestStdTime_FromDateAgainstGo(t *testing.T) {
	zones, _, _ := time2Zones(t)
	r := rand.New(rand.NewPCG(3, 4))
	counts := map[string]int{}
	for _, z := range zones {
		loc, _ := time.LoadLocationFromTZData(z.name, z.data)
		// Local times at and around each transition of 1970 to 2040, and
		// at random.
		var locals []time.Time
		t0 := time.Date(1970, 1, 1, 0, 0, 0, 0, loc)
		stop := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
		for t0.Before(stop) && len(locals) < 400 {
			_, end := t0.ZoneBounds()
			if end.IsZero() || !end.After(t0) {
				break
			}
			before := end.Add(-time.Second).In(loc)
			after := end.In(loc)
			for _, x := range []time.Time{before, after, before.Add(-30 * time.Minute), after.Add(30 * time.Minute)} {
				locals = append(locals, x)
			}
			// The wall-clock times the transition skipped or repeated.
			_, o1 := before.Zone()
			_, o2 := after.Zone()
			mid := end.Unix() + int64(o1+o2)/2
			locals = append(locals, time.Unix(mid, 0).UTC())
			t0 = end
		}
		for range 8 {
			locals = append(locals, time.Unix(r.Int64N(4_000_000_000), 0).UTC())
		}
		for _, release := range time2Builds {
			for _, lt := range locals {
				y, mo, d := lt.Date()
				h, mi, s := lt.Clock()
				ns := r.IntN(1_000_000_000)
				got := time2Ask(t, release, fmt.Sprintf("F %s %d %d %d %d %d %d %d", z.name, y, int(mo), d, h, mi, s, ns), 1)[0]
				shown := time2Shown(loc, y, int(mo), d, h, mi, s)
				var want string
				switch len(shown) {
				case 0:
					want = "E Skipped"
				case 1:
					want = strconv.FormatInt(shown[0]*1e9+int64(ns), 10)
				default:
					want = "E Repeated"
				}
				if strings.HasPrefix(want, "E ") {
					counts[want[2:]]++
				} else {
					counts["ok"]++
				}
				if got != want {
					t.Errorf("%s FromDate(%d-%02d-%02d %02d:%02d:%02d.%09d) (release %v): got %s, want %s", z.name, y, mo, d, h, mi, s, ns, release, got, want)
				}
			}
		}
	}
	t.Logf("FromDate outcomes: %v", counts)
}

func TestStdTime_FromDateRange(t *testing.T) {
	cases := []struct {
		args, want string
	}{
		{"UTC 2024 2 29 0 0 0 0", "1709164800000000000"},
		{"UTC 2023 2 29 0 0 0 0", "E OutOfRange"},
		{"UTC 2024 13 1 0 0 0 0", "E OutOfRange"},
		{"UTC 2024 0 1 0 0 0 0", "E OutOfRange"},
		{"UTC 2024 1 1 24 0 0 0", "E OutOfRange"},
		{"UTC 2024 1 1 0 60 0 0", "E OutOfRange"},
		{"UTC 2024 1 1 0 0 60 0", "E OutOfRange"},
		{"UTC 2024 1 1 0 0 0 1000000000", "E OutOfRange"},
		{"UTC 2024 1 1 0 0 0 -1", "E OutOfRange"},
		{"UTC 2262 4 11 23 47 16 854775807", "9223372036854775807"},
		{"UTC 2262 4 11 23 47 16 854775808", "E OutOfRange"},
		{"UTC 1677 9 21 0 12 43 145224192", "-9223372036854775808"},
		{"UTC 1677 9 21 0 12 43 145224191", "E OutOfRange"},
		{"UTC 2147483647 12 31 23 59 59 0", "E OutOfRange"},
		{"UTC -2147483648 1 1 0 0 0 0", "E OutOfRange"},
		{"America/New_York 2024 3 10 2 30 0 0", "E Skipped"},
		{"America/New_York 2024 11 3 1 30 0 0", "E Repeated"},
		{"America/New_York 2024 11 3 2 30 0 0", "1730619000000000000"},
		{"Nowhere/Zone 2024 1 1 0 0 0 0", "E NotFound"},
	}
	for _, release := range time2Builds {
		for _, c := range cases {
			if got := time2Ask(t, release, "F "+c.args, 1)[0]; got != c.want {
				t.Errorf("FromDate %s (release %v): got %s, want %s", c.args, release, got, c.want)
			}
		}
	}
}

// time2Sat is x clamped to i64.
func time2Sat(x *big.Int) int64 {
	if x.Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return math.MaxInt64
	}
	if x.Cmp(big.NewInt(math.MinInt64)) < 0 {
		return math.MinInt64
	}
	return x.Int64()
}

func TestStdTime_SaturationAgainstBig(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	edge := []int64{0, 1, -1, 999_999_999, -999_999_999, 1e9, -1e9, 9_223_372_036, 9_223_372_037, -9_223_372_036, -9_223_372_037, -9_223_372_038, math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, math.MinInt64 + 1, 854_775_807, 145_224_192, -854_775_808}
	pick := func() int64 {
		if r.IntN(2) == 0 {
			return edge[r.IntN(len(edge))]
		}
		return int64(r.Uint64()) >> r.IntN(64)
	}
	g := big.NewInt(1e9)
	for _, release := range time2Builds {
		for range 3000 {
			a, b := pick(), pick()
			want := time2Sat(new(big.Int).Add(new(big.Int).Mul(big.NewInt(a), g), big.NewInt(b)))
			if got := time2Ask(t, release, fmt.Sprintf("U %d %d", a, b), 1)[0]; got != strconv.FormatInt(want, 10) {
				t.Fatalf("FromUnix(%d, %d): got %s, want %d", a, b, got, want)
			}
			sum := time2Sat(new(big.Int).Add(big.NewInt(a), big.NewInt(b)))
			if got := time2Ask(t, release, fmt.Sprintf("A %d %d", a, b), 1)[0]; got != strconv.FormatInt(sum, 10) {
				t.Fatalf("Add(%d, %d): got %s, want %d", a, b, got, sum)
			}
			diff := time2Sat(new(big.Int).Sub(big.NewInt(a), big.NewInt(b)))
			if got := time2Ask(t, release, fmt.Sprintf("S %d %d", a, b), 1)[0]; got != strconv.FormatInt(diff, 10) {
				t.Fatalf("Sub(%d, %d): got %s, want %d", a, b, got, diff)
			}
		}
	}
}

func TestStdTime_FormatAgainstGo(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	ts := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 1e9, 1_700_000_000_123_000_000, 951_782_400_000_000_000}
	for range 3000 {
		ts = append(ts, int64(r.Uint64()))
		ts = append(ts, r.Int64N(4e18)/1e9*1e9)
	}
	for _, release := range time2Builds {
		for _, ns := range ts {
			tm := time.Unix(0, ns).UTC()
			if got, want := time2Ask(t, release, fmt.Sprintf("FT %d", ns), 1)[0], tm.Format(time.RFC3339Nano); got != want {
				t.Fatalf("Format(%d): got %s, want %s", ns, got, want)
			}
			if got, want := time2Ask(t, release, fmt.Sprintf("FH %d", ns), 1)[0], tm.Format(http.TimeFormat); got != want {
				t.Fatalf("FormatHttpDate(%d): got %s, want %s", ns, got, want)
			}
		}
	}
}

// time2Mutate returns s with one random edit: a byte changed, removed,
// inserted or duplicated.
func time2Mutate(r *rand.Rand, s string) string {
	b := []byte(s)
	const alphabet = "0123456789:-+.TtZzGMT ,abcdefhmnsuµμ\x00\xff"
	switch k := r.IntN(4); {
	case len(b) == 0 || k == 0:
		i := r.IntN(len(b) + 1)
		return string(b[:i]) + string(alphabet[r.IntN(len(alphabet))]) + string(b[i:])
	case k == 1:
		i := r.IntN(len(b))
		return string(b[:i]) + string(b[i+1:])
	case k == 2:
		i := r.IntN(len(b))
		b[i] = alphabet[r.IntN(len(alphabet))]
		return string(b)
	default:
		i := r.IntN(len(b))
		return string(b[:i+1]) + string(b[i:])
	}
}

func TestStdTime_Rfc3339AgainstGo(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 10))
	seeds := []string{"2006-01-02T15:04:05Z", "1985-04-12T23:20:50.52Z", "1996-12-19T16:39:57-08:00", "1990-12-31T23:59:59Z", "1937-01-01T12:00:27.87+00:20", "2262-04-11T23:47:16.854775807Z", "1677-09-21T00:12:43.145224192Z", "2262-04-11T23:47:16.854775808Z", "0000-01-01T00:00:00Z", "9999-12-31T23:59:59.9999999999999Z", "2024-02-29T00:00:00+23:59", "1990-12-31T23:59:60Z", "2006-01-02t15:04:05z", "2006-01-02 15:04:05Z"}
	for range 2000 {
		ns := int64(r.Uint64())
		tm := time.Unix(0, ns).In(time.FixedZone("", (r.IntN(48*60)-24*60)*60))
		seeds = append(seeds, tm.Format(time.RFC3339Nano))
	}
	inputs := append([]string(nil), seeds...)
	for range 8000 {
		inputs = append(inputs, time2Mutate(r, seeds[r.IntN(len(seeds))]))
	}
	for _, release := range time2Builds {
		agree := 0
		for _, s := range inputs {
			got := time2Ask(t, release, "P3 "+time2Hex(s), 1)[0]
			gt, gerr := time.Parse(time.RFC3339Nano, s)
			// Go also takes a fraction with a comma, one-digit hours in
			// offsets and the like; std.time takes RFC 3339 alone, plus
			// its lower-case "t" and "z" (RFC 3339 section 5.6).
			rfc := time2IsRfc3339(s)
			switch {
			case got == "E" && (gerr != nil || !rfc):
			case got != "E" && !rfc:
				t.Errorf("ParseRfc3339(%q) = %s, but it is not RFC 3339", s, got)
			case got != "E" && gerr == nil:
				if want := strconv.FormatInt(gt.UnixNano(), 10); got != want && gt.Year() >= 1678 && gt.Year() <= 2261 {
					t.Errorf("ParseRfc3339(%q) = %s, Go %s", s, got, want)
				}
			case got != "E" && strings.ContainsAny(s, "tz"):
			case got == "E" && gerr == nil && rfc && gt.Year() >= 1678 && gt.Year() <= 2261:
				t.Errorf("ParseRfc3339(%q) failed; Go reads %v", s, gt)
			}
			agree++
		}
		t.Logf("release %v: %d inputs checked", release, agree)
	}
}

// time2IsRfc3339 is a model of RFC 3339 section 5.6's date-time with the
// ranges checked, second 60 excluded.
func time2IsRfc3339(s string) bool {
	d := func(i, n int) (int, bool) {
		if i+n > len(s) {
			return 0, false
		}
		v := 0
		for _, c := range []byte(s[i : i+n]) {
			if c < '0' || c > '9' {
				return 0, false
			}
			v = v*10 + int(c-'0')
		}
		return v, true
	}
	if len(s) < 20 || s[4] != '-' || s[7] != '-' || (s[10] != 'T' && s[10] != 't') || s[13] != ':' || s[16] != ':' {
		return false
	}
	y, ok1 := d(0, 4)
	mo, ok2 := d(5, 2)
	dd, ok3 := d(8, 2)
	h, ok4 := d(11, 2)
	mi, ok5 := d(14, 2)
	se, ok6 := d(17, 2)
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) || mo < 1 || mo > 12 || dd < 1 || dd > time.Date(y, time.Month(mo)+1, 0, 0, 0, 0, 0, time.UTC).Day() || h > 23 || mi > 59 || se > 59 {
		return false
	}
	i := 19
	if i < len(s) && s[i] == '.' {
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i+1 {
			return false
		}
		i = j
	}
	if i < len(s) && (s[i] == 'Z' || s[i] == 'z') {
		return i+1 == len(s)
	}
	if i+6 != len(s) || (s[i] != '+' && s[i] != '-') || s[i+3] != ':' {
		return false
	}
	oh, a := d(i+1, 2)
	om, b := d(i+4, 2)
	return a && b && oh <= 23 && om <= 59
}

func TestStdTime_HttpDateAgainstGo(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 12))
	seeds := []string{"Sun, 06 Nov 1994 08:49:37 GMT", "Sunday, 06-Nov-94 08:49:37 GMT", "Sun Nov  6 08:49:37 1994", "Sun Nov 16 08:49:37 1994", "Thu, 01 Jan 1970 00:00:00 GMT", "Monday, 01-Jan-68 00:00:00 GMT", "Wednesday, 31-Dec-69 23:59:59 GMT", "Fri, 11 Apr 2262 23:47:16 GMT", "Sat, 12 Apr 2262 00:00:00 GMT", "Mon, 29 Feb 2021 00:00:00 GMT", "sun, 06 nov 1994 08:49:37 GMT"}
	for range 1500 {
		tm := time.Unix(r.Int64N(9e9)-2e9, 0).UTC()
		seeds = append(seeds, tm.Format(http.TimeFormat), tm.Format(time.RFC850), tm.Format(time.ANSIC))
	}
	inputs := append([]string(nil), seeds...)
	for range 6000 {
		inputs = append(inputs, time2Mutate(r, seeds[r.IntN(len(seeds))]))
	}
	for _, release := range time2Builds {
		for _, s := range inputs {
			got := time2Ask(t, release, "PH "+time2Hex(s), 1)[0]
			gt, gerr := http.ParseTime(s)
			if got != "E" {
				// Whatever std.time reads, Go reads the same.
				if gerr != nil {
					t.Errorf("ParseHttpDate(%q) = %s; Go refuses: %v", s, got, gerr)
				} else if want := strconv.FormatInt(gt.UnixNano(), 10); got != want {
					t.Errorf("ParseHttpDate(%q) = %s, Go %s", s, got, want)
				}
				continue
			}
			// Go is case-insensitive in names, takes one-digit hours and
			// a zone other than GMT in RFC 850's form; std.time follows RFC
			// 9110 5.6.7, which is case-sensitive. The forms Go writes
			// must read.
			if gerr == nil && (s == gt.UTC().Format(http.TimeFormat) || s == gt.UTC().Format(time.ANSIC)) && gt.Year() >= 1678 && gt.Year() <= 2261 {
				t.Errorf("ParseHttpDate(%q) failed; Go reads %v", s, gt)
			}
		}
	}
}

func TestStdTime_DurationAgainstGo(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 14))
	seeds := []string{"0", "-0", "+0", "", "1ns", "1.5h", "-1.5h", "2h45m", "300ms", "1µs", "1μs", "1us", ".5s", "5.s", ".s", "1", "1d", "9223372036854775807ns", "9223372036854775808ns", "-9223372036854775808ns", "-9223372036854775809ns", "2562047h47m16.854775807s", "2562047h47m16.854775808s", "-2562047h47m16.854775808s", "0.000000000000000000001h", "1.00000000000000000000000000001s", "3.999999999999999999999999h", "1h1h1h1h", "1e3s", "+-1s", "1.1.1s"}
	for range 3000 {
		d := time.Duration(int64(r.Uint64()) >> r.IntN(64))
		seeds = append(seeds, d.String())
	}
	inputs := append([]string(nil), seeds...)
	for range 8000 {
		inputs = append(inputs, time2Mutate(r, seeds[r.IntN(len(seeds))]))
	}
	for _, release := range time2Builds {
		for _, s := range inputs {
			got := time2Ask(t, release, "PD "+time2Hex(s), 1)[0]
			gd, gerr := time.ParseDuration(s)
			want := "E"
			if gerr == nil {
				want = strconv.FormatInt(int64(gd), 10)
			}
			if got != want {
				t.Errorf("ParseDuration(%q): got %s, want %s", s, got, want)
			}
		}
	}
}
