// Why: the std.time functions the zone and format oracles do not reach still
// need a check against Go's time package.
// What: Date.Time, Time.Unix, Zone.Fixed, the Duration constructors, Time's
// Eq, Ord and Hash, and Zone.Local's Windows table (built by TziTable).
// How: Date.Time against time.Date, a math/big model of its carries and Time.In;
// the Windows table against the pinned IANA zones and Go's initLocalFromTZI.

package std

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// time2LinesProg answers, a line per request:
//
//	DT y mo d h mi s ns off  Date.Time's nanoseconds
//	UX t                     Time.Unix
//	X off t...               the Date of each instant in Zone.Fixed(off)
//	DM n, DH n, DU n, DN n   Minutes, Hours, Micros, Nanos in nanoseconds
//	C a b                    "eq less has": Eq, Ord's Less, and whether a
//	                         Set holding a has b (Hash with Eq)
//	RT zone t...             Time.In then Date.Time of each instant in a
//	                         tzdata zone
//	W first recs now t...    the offset at each instant of TziTable over
//	                         the per-year records recs from year first,
//	                         with the record now past them (hex)
const time2LinesProg = `import std.time
import std.time.tzdata

Nib(c: u8): int {
    return if c <= 57 { int(c) - 48 } else { int(c) - 87 }
}

Unhex(a: &Arena, s: str): OutOfMemory![]u8 from a {
    b := s.AsBytes()
    out := try a.Slice[u8](b.Len() / 2)
    for i in 0..out.Len() {
        out[i] = Trunc[u8](Nib(b[2 * i]) * 16 + Nib(b[2 * i + 1]))
    }
    return out[..]
}

Num(s: str): i64 {
    return s.ParseInt() else { 0 }
}

Day(out: mut Writer, d: &time.Date): WriteError!void {
    try Print(out, "{y} {m} {d} {h} {mi} {s} {ns} {wd} {yd} {off}\n", (y: d.year, m: d.month, d: d.day, h: d.hour, mi: d.minute, s: d.second, ns: d.nanos, wd: d.weekday, yd: d.year_day, off: d.offset_seconds))
}

Serve(h: &Heap, out: mut Writer, inp: mut Reader): WriteError|ReadError|OutOfMemory!bool {
    var a := try Arena.New(h)
    defer a.Free()
    line := try inp.Line(&a) else {
        return false
    }
    var ws := line.Words()
    op := ws.Next() else {
        return true
    }
    arg := ws.Next() else { "0" }
    if op == "DT" {
        var v: [8]i64 = [0; 8]
        v[0] = Num(arg)
        var n := 1
        for w in ws {
            if n < 8 {
                v[n] = Num(w)
                n += 1
            }
        }
        d := time.Date(year: Trunc[i32](v[0]), month: Cast[int](v[1]), day: Cast[int](v[2]), hour: Cast[int](v[3]), minute: Cast[int](v[4]), second: Cast[int](v[5]), nanos: Cast[int](v[6]), weekday: 0, year_day: 0, offset_seconds: Trunc[i32](v[7]))
        try Print(out, "{t}\n", (t: d.Time().UnixNanos()))
        return true
    }
    if op == "UX" {
        try Print(out, "{s}\n", (s: time.FromUnix(0, Num(arg)).Unix()))
        return true
    }
    if op == "X" {
        z := time.Zone.Fixed(Trunc[i32](Num(arg)))
        for w in ws {
            d := time.FromUnix(0, Num(w)).In(&z)
            try Day(out, &d)
        }
        return true
    }
    if op == "DM" {
        try Print(out, "{n}\n", (n: time.Minutes(Num(arg)).Nanos()))
        return true
    }
    if op == "DH" {
        try Print(out, "{n}\n", (n: time.Hours(Num(arg)).Nanos()))
        return true
    }
    if op == "DU" {
        try Print(out, "{n}\n", (n: time.Micros(Num(arg)).Nanos()))
        return true
    }
    if op == "DN" {
        try Print(out, "{n}\n", (n: time.Nanos(Num(arg)).Nanos()))
        return true
    }
    if op == "C" {
        x := time.FromUnix(0, Num(arg))
        second := ws.Next() else { "0" }
        y := time.FromUnix(0, Num(second))
        var set := Set[time.Time].New(&a)
        _ = try set.Add(x)
        try Print(out, "{e} {l} {h}\n", (e: x.Eq(&y), l: x.Less(&y), h: set.Has(&y)))
        return true
    }
    if op == "RT" {
        db := tzdata.Database
        data := db.find(arg) else {
            try out.Str("E NotFound\n")
            return true
        }
        z := time.Zone.FromTzif(&a, arg, data) else e {
            try Print(out, "E {e}\n", (e))
            return true
        }
        for w in ws {
            try Print(out, "{t}\n", (t: time.FromUnix(0, Num(w)).In(&z).Time().UnixNanos()))
        }
        return true
    }
    if op == "W" {
        recs := try Unhex(&a, ws.Next() else { "" })
        now := try Unhex(&a, ws.Next() else { "" })
        z := try time.TziZoneOf(&a, Num(arg), recs, now)
        for w in ws {
            d := time.FromUnix(0, Num(w)).In(&z)
            try Print(out, "{o}\n", (o: d.offset_seconds))
        }
        return true
    }
    try out.Str("E op\n")
    return true
}

export Main(sys: &System): WriteError|ReadError|OutOfMemory!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    for try Serve(sys.heap, mut out, mut inp) {
        try out.Flush()
    }
}
`

// time2TziExport exports TziTable, which Zone.Local builds on Windows from
// the OS's per-year records, for time2LinesProg alone.
const time2TziExport = `/// Test-only: the zone Zone.Local builds on Windows from per-year records.
export TziZoneOf(a: &Arena, first: i64, recs: []u8, now: []u8): OutOfMemory!Zone from a {
    return try TziTable(a, first, recs, TziRule(now, 0))
}
`

func init() {
	time2Progs["lines"] = struct {
		src      string
		fixtures map[string]string
	}{time2LinesProg, map[string]string{"time/tzi_export.mk": time2TziExport}}
}

// time2Lines asks time2LinesProg.
func time2Lines(t testing.TB, release bool, line string, n int) []string {
	t.Helper()
	return time2AskProg(t, "lines", release, line, n)
}

// time2DaysBig is Hinnant's days_from_civil over big integers, any year.
func time2DaysBig(y *big.Int, m int64) *big.Int {
	yy := new(big.Int).Set(y)
	if m <= 2 {
		yy.Sub(yy, big.NewInt(1))
	}
	era := new(big.Int).Div(yy, big.NewInt(400)) // Euclidean: floor for 400 > 0
	yoe := new(big.Int).Sub(yy, new(big.Int).Mul(era, big.NewInt(400))).Int64()
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + 1 - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	out := new(big.Int).Mul(era, big.NewInt(146097))
	return out.Add(out, big.NewInt(doe-719468))
}

// time2DateBig is Date.Time's exact value, clamped to i64: the fields
// carried as Go's time.Date carries them, without overflow.
func time2DateBig(f [8]int64) int64 {
	mo := big.NewInt(f[1] - 1)
	if f[1] == math.MinInt64 {
		mo = new(big.Int).Sub(big.NewInt(f[1]), big.NewInt(1))
	}
	q := new(big.Int).Div(mo, big.NewInt(12))
	m := new(big.Int).Sub(mo, new(big.Int).Mul(q, big.NewInt(12))).Int64() + 1
	y := new(big.Int).Add(big.NewInt(f[0]), q)
	secs := new(big.Int).Mul(time2DaysBig(y, m), big.NewInt(86400))
	day := new(big.Int).Sub(big.NewInt(f[2]), big.NewInt(1))
	secs.Add(secs, day.Mul(day, big.NewInt(86400)))
	secs.Add(secs, new(big.Int).Mul(big.NewInt(f[3]), big.NewInt(3600)))
	secs.Add(secs, new(big.Int).Mul(big.NewInt(f[4]), big.NewInt(60)))
	secs.Add(secs, big.NewInt(f[5]))
	secs.Sub(secs, big.NewInt(f[7]))
	ns := secs.Mul(secs, big.NewInt(1e9))
	return time2Sat(ns.Add(ns, big.NewInt(f[6])))
}

func TestStdTime_DateTimeAgainstGo(t *testing.T) {
	r := rand.New(rand.NewPCG(31, 32))
	ask := func(release bool, f [8]int64) string {
		return time2Lines(t, release, "DT "+time2Ints(f[:]), 1)[0]
	}
	edge := []int64{0, 1, -1, 11, 12, 13, 59, 60, 61, 23, 24, 1e9 - 1, 1e9, -1e9, 1 << 40, -(1 << 40), 1 << 62, -(1 << 62), math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, math.MinInt64 + 1}
	pick := func(scale int64) int64 {
		switch r.IntN(4) {
		case 0:
			return edge[r.IntN(len(edge))]
		case 1:
			return int64(r.Uint64()) >> r.IntN(64)
		default:
			return r.Int64N(2*scale+1) - scale
		}
	}
	n, exact := 0, 0
	for _, release := range time2Builds {
		// Within Go's range: time.Date with a FixedZone, which carries
		// every field the same way.
		for range 3000 {
			f := [8]int64{int64(1600 + r.IntN(800)), r.Int64N(201) - 100, r.Int64N(2001) - 1000, r.Int64N(2e5) - 1e5, r.Int64N(2e6) - 1e6, r.Int64N(2e8) - 1e8, r.Int64N(2e12) - 1e12, int64(r.IntN(2*93600) - 93600)}
			want := time.Date(int(f[0]), time.Month(f[1]), int(f[2]), int(f[3]), int(f[4]), int(f[5]), int(f[6]), time.FixedZone("", int(f[7])))
			got := ask(release, f)
			if y := want.Year(); y >= 1678 && y <= 2261 {
				if got != strconv.FormatInt(want.UnixNano(), 10) {
					t.Errorf("Date%v.Time() = %s, Go %d", f, got, want.UnixNano())
				}
				n++
			}
			if b := strconv.FormatInt(time2DateBig(f), 10); got != b {
				t.Errorf("Date%v.Time() = %s, model %s", f, got, b)
			}
		}
		// Any fields: the math/big model wherever no field passes 2^62,
		// and an answer (no panic) everywhere.
		for range 4000 {
			f := [8]int64{int64(int32(r.Uint32())), pick(1000), pick(100000), pick(1e6), pick(1e7), pick(1e9), pick(1e15), int64(int32(r.Uint32()))}
			if r.IntN(3) == 0 {
				f[0] = int64(1970 + r.IntN(100))
			}
			got := ask(release, f)
			if _, err := strconv.ParseInt(got, 10, 64); err != nil {
				t.Fatalf("Date%v.Time() answered %q", f, got)
			}
			small := true
			for _, v := range f {
				if v > 1<<62 || v < -(1<<62) {
					small = false
				}
			}
			if small {
				exact++
				if want := strconv.FormatInt(time2DateBig(f), 10); got != want {
					t.Errorf("Date%v.Time() = %s, model %s", f, got, want)
				}
			}
		}
	}
	t.Logf("%d against time.Date, %d against the model with extreme fields, dev and release", n, exact)
}

func TestStdTime_RoundTripAgainstGo(t *testing.T) {
	zones, _, _ := time2Zones(t)
	r := rand.New(rand.NewPCG(33, 34))
	n := 0
	for zi, z := range zones {
		if zi%5 != 0 {
			continue
		}
		loc, err := time.LoadLocationFromTZData(z.name, z.data)
		if err != nil {
			t.Fatal(err)
		}
		inst := time2Instants(loc, r)
		for _, release := range time2Builds {
			for i := 0; i < len(inst); i += 2000 {
				batch := inst[i:min(i+2000, len(inst))]
				got := time2Lines(t, release, "RT "+z.name+" "+time2Ints(batch), len(batch))
				for k, ns := range batch {
					if got[k] != strconv.FormatInt(ns, 10) {
						t.Errorf("%s: In(%d).Time() = %s", z.name, ns, got[k])
					}
					n++
				}
			}
		}
	}
	t.Logf("%d instants: Date.Time inverts Time.In, dev and release", n)
}

func TestStdTime_UnixAndFixedAgainstGo(t *testing.T) {
	r := rand.New(rand.NewPCG(35, 36))
	ts := []int64{0, 1, -1, 999_999_999, -999_999_999, 1e9, -1e9, -1e9 - 1, math.MaxInt64, math.MinInt64}
	for range 2000 {
		ts = append(ts, int64(r.Uint64())>>r.IntN(64), -int64(r.Uint64()>>1)>>r.IntN(63))
	}
	offs := []int64{0, 1, -1, 3600, -3600, 19800, 93599, -89999, 86400 * 3, math.MaxInt32, math.MinInt32}
	for range 200 {
		offs = append(offs, int64(int32(r.Uint32())), int64(r.IntN(2*93600)-93600))
	}
	for _, release := range time2Builds {
		for _, ns := range ts {
			if got, want := time2Lines(t, release, fmt.Sprintf("UX %d", ns), 1)[0], strconv.FormatInt(time.Unix(0, ns).Unix(), 10); got != want {
				t.Errorf("FromUnix(0, %d).Unix() = %s, Go %s", ns, got, want)
			}
		}
		for i, off := range offs {
			batch := ts[(i*37)%len(ts):]
			batch = batch[:min(len(batch), 200)]
			got := time2Lines(t, release, fmt.Sprintf("X %d %s", off, time2Ints(batch)), len(batch))
			loc := time.FixedZone("", int(off))
			for k, ns := range batch {
				if want := time2Fields(time.Unix(0, ns).In(loc)); got[k] != want {
					t.Errorf("FromUnix(0, %d).In(Fixed(%d)) = %s, Go %s", ns, off, got[k], want)
				}
			}
		}
	}
}

func TestStdTime_DurationsAgainstGo(t *testing.T) {
	r := rand.New(rand.NewPCG(37, 38))
	kinds := []struct {
		op   string
		unit time.Duration
	}{{"DM", time.Minute}, {"DH", time.Hour}, {"DU", time.Microsecond}, {"DN", time.Nanosecond}}
	for _, release := range time2Builds {
		for _, k := range kinds {
			lim := int64(math.MaxInt64 / k.unit)
			ns := []int64{0, 1, -1, lim, -lim, lim - 1}
			for range 500 {
				if k.unit == time.Nanosecond {
					ns = append(ns, int64(r.Uint64()))
				} else {
					ns = append(ns, r.Int64N(2*lim)-lim+1)
				}
			}
			for _, n := range ns {
				got := time2Lines(t, release, fmt.Sprintf("%s %d", k.op, n), 1)[0]
				if want := strconv.FormatInt(int64(time.Duration(n)*k.unit), 10); got != want {
					t.Errorf("%s(%d) = %s, Go %s", k.op, n, got, want)
				}
			}
		}
	}
	// Past i64, Minutes, Hours and Micros panic (code 1), as Seconds does:
	// a fresh process for each, which must exit without an answer.
	exe := time2StartProg(t, "lines", false).exe
	for _, c := range []string{"DM 153722868", "DH -2562048", "DU 9223372036854776"} {
		cmd := exec.Command(exe)
		cmd.Stdin = strings.NewReader(c + "\n")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "panic") {
			t.Errorf("%s: want a panic; exit %v, output %q", c, err, out)
		}
	}
}

func TestStdTime_EqOrdHashAgainstGo(t *testing.T) {
	r := rand.New(rand.NewPCG(39, 40))
	vals := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 1e9}
	for range 1000 {
		vals = append(vals, int64(r.Uint64()))
	}
	for _, release := range time2Builds {
		seen := map[int64]bool{}
		for i := range 3000 {
			a := vals[r.IntN(len(vals))]
			b := a
			switch i % 3 {
			case 1:
				b = vals[r.IntN(len(vals))]
			case 2:
				b = a + int64(r.IntN(3)) - 1
			}
			ta, tb := time.Unix(0, a), time.Unix(0, b)
			set := map[int64]bool{a: true}
			want := fmt.Sprintf("%v %v %v", ta.Equal(tb), ta.Before(tb), set[b])
			if got := time2Lines(t, release, fmt.Sprintf("C %d %d", a, b), 1)[0]; got != want {
				t.Errorf("Time(%d) against Time(%d): eq less has = %s, Go %s", a, b, got, want)
			}
			seen[a] = true
		}
	}
}

// time2Systime is a SYSTEMTIME: year (0 for a recurring date), month,
// weekday, week (or day of the month with a year), hour, minute, second.
func time2Systime(b []byte, year, month, wd, day, h, m, s int) {
	for i, v := range []int{year, month, wd, day, h, m, s, 0} {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
}

// time2Tzi is a TIME_ZONE_INFORMATION record.
type time2Tzi struct {
	bias, stdBias, dstBias int32
	std, dst               [7]int // SYSTEMTIME fields, month 0 for none
}

func (z time2Tzi) bytes() []byte {
	b := make([]byte, 172)
	binary.LittleEndian.PutUint32(b[0:], uint32(z.bias))
	time2Systime(b[68:], z.std[0], z.std[1], z.std[2], z.std[3], z.std[4], z.std[5], z.std[6])
	binary.LittleEndian.PutUint32(b[84:], uint32(z.stdBias))
	time2Systime(b[152:], z.dst[0], z.dst[1], z.dst[2], z.dst[3], z.dst[4], z.dst[5], z.dst[6])
	binary.LittleEndian.PutUint32(b[168:], uint32(z.dstBias))
	return b
}

// time2Table asks for TziTable's offsets at the instants.
func time2TziAsk(t *testing.T, release bool, first int, recs []time2Tzi, now time2Tzi, inst []int64) []string {
	var rb []byte
	for _, z := range recs {
		rb = append(rb, z.bytes()...)
	}
	var out []string
	for i := 0; i < len(inst); i += 1000 {
		batch := inst[i:min(i+1000, len(inst))]
		out = append(out, time2Lines(t, release, fmt.Sprintf("W %d %s %s %s", first, hex.EncodeToString(rb), hex.EncodeToString(now.bytes()), time2Ints(batch)), len(batch))...)
	}
	return out
}

// time2TransBetween is every transition of loc from a to b, the second
// either side, and one instant a week.
func time2TransBetween(loc *time.Location, a, b time.Time) []int64 {
	var out []int64
	for u := a; u.Before(b); u = u.Add(7 * 24 * time.Hour) {
		out = append(out, u.UnixNano())
	}
	t := a.In(loc)
	for t.Before(b) {
		_, end := t.ZoneBounds()
		if end.IsZero() || !end.After(t) || !end.Before(b) {
			break
		}
		e := end.UnixNano()
		out = append(out, e-1e9, e-1, e, e+1e9)
		t = end
	}
	return out
}

// time2Recurring is the same record for each year from 1970 to 2026.
func time2Recurring(z time2Tzi) []time2Tzi {
	out := make([]time2Tzi, 2026-1970+1)
	for i := range out {
		out[i] = z
	}
	return out
}

func TestStdTime_WindowsTableAgainstIana(t *testing.T) {
	zones, _, _ := time2Zones(t)
	paris := time2Tzi{bias: -60, dstBias: -60, std: [7]int{0, 10, 0, 5, 3, 0, 0}, dst: [7]int{0, 3, 0, 5, 2, 0, 0}}
	sydney := time2Tzi{bias: -600, dstBias: -60, std: [7]int{0, 4, 0, 1, 3, 0, 0}, dst: [7]int{0, 10, 0, 1, 2, 0, 0}}
	nyRecs := time2NyRecords()
	cases := []struct {
		zone string
		from int
		recs []time2Tzi
		now  time2Tzi
	}{
		{"Europe/Paris", 1996, time2Recurring(paris), paris},
		{"Australia/Sydney", 2009, time2Recurring(sydney), sydney},
		{"America/New_York", 1970, nyRecs, nyRecs[len(nyRecs)-1]},
	}
	for _, release := range time2Builds {
		for _, c := range cases {
			loc := mustLoc(t, zones, c.zone)
			inst := time2TransBetween(loc, time.Date(c.from, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
			got := time2TziAsk(t, release, 1970, c.recs, c.now, inst)
			bad := 0
			for k, ns := range inst {
				_, off := time.Unix(0, ns).In(loc).Zone()
				if got[k] != strconv.Itoa(off) {
					if bad++; bad <= 10 {
						t.Errorf("%s at %s (release %v): offset %s, IANA %d", c.zone, time.Unix(0, ns).UTC().Format(time.RFC3339), release, got[k], off)
					}
				}
			}
			t.Logf("%s from %d: %d instants", c.zone, c.from, len(inst))
		}
	}
}

// time2GoTzi is Go's time.Local on Windows for a record (initLocalFromTZI
// in time/zoneinfo_windows.go, built for the years around center): the
// offset at each instant.
func time2GoTzi(z time2Tzi, center int, inst []int64) []int {
	if z.std[1] == 0 {
		out := make([]int, len(inst))
		for i := range out {
			out[i] = -int(z.bias) * 60
		}
		return out
	}
	offs := []int{-int(z.bias+z.stdBias) * 60, -int(z.bias+z.dstBias) * 60}
	d0, d1, i0, i1 := z.std, z.dst, 0, 1
	if d0[1] > d1[1] {
		d0, d1, i0, i1 = d1, d0, i1, i0
	}
	pseudo := func(year int, d [7]int) int64 {
		day := 1
		t := time.Date(year, time.Month(d[1]), day, d[4], d[5], d[6], 0, time.UTC)
		i := d[2] - int(t.Weekday())
		if i < 0 {
			i += 7
		}
		day += i
		if week := d[3] - 1; week < 4 {
			day += week * 7
		} else {
			day += 4 * 7
			if day > time.Date(year, time.Month(d[1])+1, 0, 0, 0, 0, 0, time.UTC).Day() {
				day -= 7
			}
		}
		return t.Unix() + int64(day-1)*86400
	}
	type tx struct {
		when int64
		idx  int
	}
	var txs []tx
	for y := center - 100; y < center+100; y++ {
		txs = append(txs, tx{pseudo(y, d0) - int64(offs[i1]), i0}, tx{pseudo(y, d1) - int64(offs[i0]), i1})
	}
	out := make([]int, len(inst))
	for k, ns := range inst {
		s := time.Unix(0, ns).Unix()
		j := 0
		for j+1 < len(txs) && txs[j+1].when <= s {
			j++
		}
		out[k] = offs[txs[j].idx]
	}
	return out
}

func TestStdTime_WindowsTableAgainstGoTzi(t *testing.T) {
	r := rand.New(rand.NewPCG(41, 42))
	var inst []int64
	for y := 1971; y < 2120; y++ {
		for range 40 {
			inst = append(inst, time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC).Unix()*1e9+r.Int64N(365*86400)*1e9)
		}
	}
	records := []time2Tzi{
		// No daylight time: StandardBias is ignored.
		{bias: -240, stdBias: -60},
		{bias: 300},
	}
	for range 60 {
		sm := 1 + r.IntN(12)
		dm := 1 + r.IntN(12)
		for dm == sm {
			dm = 1 + r.IntN(12)
		}
		records = append(records, time2Tzi{
			bias:    int32(r.IntN(4*26)-4*12) * 15,
			stdBias: []int32{0, 0, 30}[r.IntN(3)],
			dstBias: []int32{-60, -30, -120, 60}[r.IntN(4)],
			std:     [7]int{0, sm, r.IntN(7), 1 + r.IntN(5), r.IntN(24), []int{0, 30, 59}[r.IntN(3)], 0},
			dst:     [7]int{0, dm, r.IntN(7), 1 + r.IntN(5), r.IntN(24), []int{0, 30, 59}[r.IntN(3)], 0},
		})
	}
	for _, release := range time2Builds {
		for _, z := range records {
			want := time2GoTzi(z, 2026, inst)
			got := time2TziAsk(t, release, 1970, time2Recurring(z), z, inst)
			bad := 0
			for k := range inst {
				if got[k] != strconv.Itoa(want[k]) {
					if bad++; bad <= 5 {
						t.Errorf("record %+v at %s (release %v): offset %s, Go %d", z, time.Unix(0, inst[k]).UTC().Format(time.RFC3339), release, got[k], want[k])
					}
				}
			}
		}
	}
}
