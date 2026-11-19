// Why: the pinned database holds only version 2 TZif with an empty version 1
// block, so the 32-bit reader, version '4' and leap-second records go untested.
// What: TestStdTime_TzifVersionsAgainstGo over the TZif forms of RFC 8536 versions 1 to 4.
// How: real zones rewritten as version 1 and as version '4' with leap records, and
// random version 1 zones, read by FromTzif and by Go's LoadLocationFromTZData.
//
// Go refuses the version byte '4', so it reads that file with the byte set to
// '3', which is the only difference RFC 8536 section 3.1 states between the
// two (a version 4 leap table may start with a correction other than one and
// may end with an expiry; both readers skip leap records).

package std

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"
)

// time2Tz is a zone's TZif data, the fields of RFC 8536 section 3.2.
type time2Tz struct {
	trans  []int64
	idx    []byte
	types  [][6]byte // utoff (big-endian i32), isdst, desigidx
	chars  []byte
	leaps  [][2]int64 // occurrence, correction
	isstd  []byte
	isut   []byte
	footer string
}

// time2ParseTz reads the version 2+ part of a TZif file.
func time2ParseTz(t testing.TB, data []byte) time2Tz {
	t.Helper()
	cnt := func(at int) [6]int {
		var c [6]int
		for k := range c {
			c[k] = int(binary.BigEndian.Uint32(data[at+20+4*k:]))
		}
		return c
	}
	c1 := cnt(0)
	at := 44 + c1[3]*5 + c1[4]*6 + c1[5] + c1[2]*8 + c1[1] + c1[0]
	c := cnt(at)
	isut, isstd, leap, timecnt, typecnt, charcnt := c[0], c[1], c[2], c[3], c[4], c[5]
	p := at + 44
	var z time2Tz
	for i := range timecnt {
		z.trans = append(z.trans, int64(binary.BigEndian.Uint64(data[p+8*i:])))
	}
	p += 8 * timecnt
	z.idx = append(z.idx, data[p:p+timecnt]...)
	p += timecnt
	for i := range typecnt {
		var ty [6]byte
		copy(ty[:], data[p+6*i:])
		z.types = append(z.types, ty)
	}
	p += 6 * typecnt
	z.chars = append(z.chars, data[p:p+charcnt]...)
	p += charcnt
	for i := range leap {
		z.leaps = append(z.leaps, [2]int64{int64(binary.BigEndian.Uint64(data[p+12*i:])), int64(int32(binary.BigEndian.Uint32(data[p+12*i+8:])))})
	}
	p += 12 * leap
	z.isstd = append(z.isstd, data[p:p+isstd]...)
	p += isstd
	z.isut = append(z.isut, data[p:p+isut]...)
	p += isut
	z.footer = strings.TrimSuffix(strings.TrimPrefix(string(data[p:]), "\n"), "\n")
	return z
}

// fits32 is z with only its transitions and leap records that fit 32 bits.
func (z time2Tz) fits32() time2Tz {
	out := z
	out.trans, out.idx, out.leaps = nil, nil, nil
	for i, tr := range z.trans {
		if tr >= math.MinInt32 && tr <= math.MaxInt32 {
			out.trans = append(out.trans, tr)
			out.idx = append(out.idx, z.idx[i])
		}
	}
	for _, l := range z.leaps {
		if l[0] >= math.MinInt32 && l[0] <= math.MaxInt32 {
			out.leaps = append(out.leaps, l)
		}
	}
	return out
}

// header is a TZif header for z's counts.
func (z time2Tz) header(b *bytes.Buffer, version byte) {
	b.WriteString("TZif")
	b.WriteByte(version)
	b.Write(make([]byte, 15))
	for _, n := range []int{len(z.isut), len(z.isstd), len(z.leaps), len(z.trans), len(z.types), len(z.chars)} {
		b.Write(binary.BigEndian.AppendUint32(nil, uint32(n)))
	}
}

// block is z's data block with times of tb bytes.
func (z time2Tz) block(b *bytes.Buffer, tb int) {
	put := func(v int64) {
		if tb == 4 {
			b.Write(binary.BigEndian.AppendUint32(nil, uint32(int32(v))))
		} else {
			b.Write(binary.BigEndian.AppendUint64(nil, uint64(v)))
		}
	}
	for _, tr := range z.trans {
		put(tr)
	}
	b.Write(z.idx)
	for _, ty := range z.types {
		b.Write(ty[:])
	}
	b.Write(z.chars)
	for _, l := range z.leaps {
		put(l[0])
		b.Write(binary.BigEndian.AppendUint32(nil, uint32(int32(l[1]))))
	}
	b.Write(z.isstd)
	b.Write(z.isut)
}

// encode is z as TZif of the version given: 0 is version 1 alone (32-bit
// times, no footer); '2' to '4' have a version 1 block of z's 32-bit part
// first, as zic's "fat" output does.
func (z time2Tz) encode(version byte) []byte {
	var b bytes.Buffer
	v1 := z.fits32()
	v1.header(&b, version)
	v1.block(&b, 4)
	if version == 0 {
		return b.Bytes()
	}
	z.header(&b, version)
	z.block(&b, 8)
	b.WriteString("\n" + z.footer + "\n")
	return b.Bytes()
}

// time2Leaps are leap records as a "right/" zone holds them: each
// occurrence counted in the seconds before it, the correction one more
// each time. first is the first record's correction (one, or more for a
// version 4 table that starts late).
func time2Leaps(first int64, n int) [][2]int64 {
	var out [][2]int64
	at := int64(78796800) // 1972-06-30T23:59:60Z
	for i := range n {
		out = append(out, [2]int64{at + int64(i), first + int64(i)})
		at += 15_768_000 * int64(1+i%2)
	}
	return out
}

// time2TzifBoth reads data with std.time and goData with Go, requires both
// to accept, and compares the offsets at the instants past the first
// transition; it returns how many it compared.
func time2TzifBoth(t *testing.T, what string, data, goData []byte, extra []int64) int {
	t.Helper()
	loc, err := time.LoadLocationFromTZData(what, goData)
	if err != nil {
		t.Fatalf("%s: Go refuses it: %v", what, err)
	}
	r := rand.New(rand.NewPCG(uint64(len(data)), 43))
	probes := append([]int64(nil), extra...)
	tm := time.Date(1901, 1, 1, 0, 0, 0, 0, time.UTC).In(loc)
	stop := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	for tm.Before(stop) {
		_, end := tm.ZoneBounds()
		if end.IsZero() || !end.After(tm) {
			break
		}
		e := end.UnixNano()
		probes = append(probes, e-1e9, e-1, e, e+1, e+1e9)
		tm = end
	}
	for range 50 {
		probes = append(probes, int64(r.Uint64()))
	}
	compared := 0
	for i := 0; i < len(probes); i += 1000 {
		batch := probes[i:min(i+1000, len(probes))]
		ans := time2Ask(t, false, "Z "+hex.EncodeToString(data)+" "+time2Ints(batch), 1+len(batch))
		if ans[0] != "ok" {
			t.Fatalf("%s: FromTzif: %s", what, ans[0])
		}
		for k, ns := range batch {
			at := time.Unix(0, ns).In(loc)
			// Before the first transition Go applies its lookupFirstZone
			// heuristic and std.time RFC 8536's type 0, so they may differ.
			if start, _ := at.ZoneBounds(); start.IsZero() {
				continue
			}
			_, off := at.Zone()
			if ans[1+k] != strconv.Itoa(off) {
				t.Errorf("%s at %d: offset %s, Go %d", what, ns, ans[1+k], off)
			}
			compared++
		}
	}
	return compared
}

func TestStdTime_TzifVersionsAgainstGo(t *testing.T) {
	zones, _, _ := time2Zones(t)
	n, files := 0, 0
	for zi, z := range zones {
		if zi%12 != 0 {
			continue
		}
		tz := time2ParseTz(t, z.data)
		// Version 1: the 32-bit reader, no footer.
		v1 := tz.fits32().encode(0)
		n += time2TzifBoth(t, z.name+" v1", v1, v1, nil)
		// Version 1 with leap records and indicators.
		withLeaps := tz
		withLeaps.leaps = time2Leaps(1, 27)
		withLeaps.isstd = make([]byte, len(tz.types))
		withLeaps.isut = make([]byte, len(tz.types))
		for k := range withLeaps.isstd {
			withLeaps.isstd[k] = byte(k % 2)
			withLeaps.isut[k] = withLeaps.isstd[k] & byte(k/2%2)
		}
		v1l := withLeaps.fits32().encode(0)
		n += time2TzifBoth(t, z.name+" v1 leaps", v1l, v1l, nil)
		// Versions 2, 3 and 4 with leap records; a version 4 table that
		// starts at a correction of 10.
		for _, v := range []byte{'2', '3', '4'} {
			lz := withLeaps
			if v == '4' {
				lz.leaps = time2Leaps(10, 18)
			}
			data := lz.encode(v)
			goData := data
			if v == '4' {
				goData = bytes.ReplaceAll(data, []byte("TZif4"), []byte("TZif3"))
			}
			n += time2TzifBoth(t, z.name+" v"+string(v), data, goData, nil)
		}
		files += 5
	}
	// Random version 1 zones: up to 8 types, up to 200 transitions over
	// the whole 32-bit range.
	r := rand.New(rand.NewPCG(44, 45))
	for range 150 {
		var z time2Tz
		nt := 1 + r.IntN(8)
		for k := range nt {
			off := int32(r.IntN(93600+89999+1) - 89999)
			var ty [6]byte
			binary.BigEndian.PutUint32(ty[:], uint32(off))
			ty[4] = byte(r.IntN(2))
			ty[5] = byte(k % 4)
			z.types = append(z.types, ty)
		}
		z.chars = []byte("A\x00B\x00")
		seen := map[int64]bool{}
		for range r.IntN(200) {
			tr := int64(int32(r.Uint32()))
			if !seen[tr] {
				seen[tr] = true
				z.trans = append(z.trans, tr)
			}
		}
		for i := 1; i < len(z.trans); i++ {
			for j := i; j > 0 && z.trans[j] < z.trans[j-1]; j-- {
				z.trans[j], z.trans[j-1] = z.trans[j-1], z.trans[j]
			}
		}
		for range z.trans {
			z.idx = append(z.idx, byte(r.IntN(nt)))
		}
		if r.IntN(2) == 0 {
			z.leaps = time2Leaps(1, r.IntN(5))
		}
		data := z.encode(0)
		n += time2TzifBoth(t, "random v1", data, data, []int64{math.MinInt32 * 1e9, math.MaxInt32 * 1e9, math.MaxInt64})
		files++
	}
	if n == 0 {
		t.Fatal("no instant compared")
	}
	t.Logf("%d TZif files of versions 1 to 4 (leap records included): %d instants agree with Go", files, n)
}
