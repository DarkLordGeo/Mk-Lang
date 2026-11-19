// Why: profiles feed codegen and pack keys, so their format and arithmetic
// must be canonical and stable.
// What: tests of the .mkprof format, Merge, Lookup, Summary, Weights, HasKey
// and the LLVM summary.
// How: round trips, pinned bytes, saturation, length and checksum mismatches.

package pgo

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"mk/internal/source"
)

func sum(b byte) source.Hash128 {
	var h source.Hash128
	for i := range h {
		h[i] = b
	}
	return h
}

func sample() *Profile {
	return &Profile{Records: []Record{
		{Key: "mk.app.Main", Checksum: sum(1), Counts: []uint64{1, 0, 1}},
		{Key: "mk.app.Main", Checksum: sum(2), Counts: []uint64{5}},
		{Key: "mk.app.helper", Checksum: sum(1), Counts: []uint64{100, 60, 40, math.MaxUint64}},
		{Key: "mk.std.fmt.Print.0123456789abcdef", Checksum: sum(9), Counts: nil},
		{Key: "mk.ünïcode", Checksum: sum(0), Counts: []uint64{7}},
	}}
}

func mustBytes(t *testing.T, p *Profile) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

func TestPGORoundTrip(t *testing.T) {
	t.Parallel()
	for name, p := range map[string]*Profile{"empty": {}, "sample": sample()} {
		t.Run(name, func(t *testing.T) {
			b := mustBytes(t, p)
			q, err := Read(bytes.NewReader(b))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !q.Equal(p) {
				t.Fatalf("round trip changed the profile:\n%+v\n%+v", p, q)
			}
			if !bytes.Equal(mustBytes(t, q), b) {
				t.Fatal("second Write differs")
			}
		})
	}
}

// TestPGOCanonicalBytes pins the .mkprof layout byte for byte, and checks that
// Write refuses every non-canonical profile and that Hash is the domain hash
// of the canonical bytes.
func TestPGOCanonicalBytes(t *testing.T) {
	t.Parallel()
	p := &Profile{Records: []Record{{Key: "ab", Checksum: sum(0xCC), Counts: []uint64{1, 0x0102030405060708}}}}
	var want []byte
	want = append(want, "MKPROF\x00\x01"...)
	want = append(want, 1, 0, 0, 0, 0, 0, 0, 0)
	want = append(want, 2, 0, 0, 0, 'a', 'b')
	want = append(want, bytes.Repeat([]byte{0xCC}, 16)...)
	want = append(want, 2, 0, 0, 0)
	want = append(want, 1, 0, 0, 0, 0, 0, 0, 0)
	want = append(want, 8, 7, 6, 5, 4, 3, 2, 1)
	got := mustBytes(t, p)
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes\n got %x\nwant %x", got, want)
	}
	if h := p.Hash(); h != source.HashBytes("mk.pgo.v1", want) {
		t.Fatalf("Hash = %s, want the mk.pgo.v1 hash of the canonical bytes", h)
	}
	if h := (&Profile{}).Hash(); h == p.Hash() || h != (*Profile)(nil).Hash() {
		t.Fatal("empty and nil profiles must hash alike and differ from a non-empty one")
	}
	q := sample()
	q.Records[2].Counts = append([]uint64(nil), q.Records[2].Counts...)
	q.Records[2].Counts[0]++
	if q.Hash() == sample().Hash() {
		t.Fatal("a changed counter must change the hash")
	}

	bad := map[string]*Profile{
		"unsorted":          {Records: []Record{{Key: "b"}, {Key: "a"}}},
		"unsorted checksum": {Records: []Record{{Key: "a", Checksum: sum(2)}, {Key: "a", Checksum: sum(1)}}},
		"duplicate":         {Records: []Record{{Key: "a", Checksum: sum(1)}, {Key: "a", Checksum: sum(1)}}},
		"long key":          {Records: []Record{{Key: strings.Repeat("k", MaxKeyLen+1)}}},
		"bad utf-8":         {Records: []Record{{Key: "a\xff"}}},
	}
	for name, p := range bad {
		var buf bytes.Buffer
		if err := p.Write(&buf); err == nil {
			t.Errorf("%s: Write accepted a non-canonical profile", name)
		} else if buf.Len() != 0 {
			t.Errorf("%s: Write wrote %d bytes before failing", name, buf.Len())
		}
	}
	if err := (&Profile{Records: []Record{{Key: strings.Repeat("k", MaxKeyLen)}}}).Check(); err != nil {
		t.Errorf("a key of exactly MaxKeyLen bytes is allowed: %v", err)
	}
}

func TestPGOMergeDeterministic(t *testing.T) {
	t.Parallel()
	a := sample()
	b := &Profile{Records: []Record{
		{Key: "mk.app.Main", Checksum: sum(1), Counts: []uint64{2, 1, 1}},
		{Key: "mk.app.new", Checksum: sum(3), Counts: []uint64{4, 4}},
		{Key: "mk.a.first", Checksum: sum(3), Counts: []uint64{1}},
	}}
	// c is b's records out of order, which Merge accepts.
	c := &Profile{Records: []Record{b.Records[2], b.Records[0], b.Records[1]}}
	m1, err := Merge(a, b)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := Merge(c, nil, a)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustBytes(t, m1), mustBytes(t, m2)) {
		t.Fatal("merge order changed the bytes")
	}
	if err := m1.Check(); err != nil {
		t.Fatalf("merge result is not canonical: %v", err)
	}
	got, ok := m1.Lookup("mk.app.Main", sum(1), 3)
	if !ok || got[0] != 3 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("summed Main = %v %v, want [3 1 2]", got, ok)
	}
	if len(m1.Records) != 7 {
		t.Fatalf("merged %d records, want 7", len(m1.Records))
	}
	// The result shares no slice with an input.
	m1.Records[0].Counts[0] = 99
	if b.Records[2].Counts[0] != 1 {
		t.Fatal("Merge aliased an input's counts")
	}
	// Merging one profile with nothing is the identity on canonical input.
	m3, err := Merge(a)
	if err != nil || !m3.Equal(a) {
		t.Fatalf("Merge(a) != a: %v", err)
	}
	if m, err := Merge(); err != nil || len(m.Records) != 0 {
		t.Fatalf("Merge() = %v, %v; want empty", m, err)
	}
	// A hand-built input that Write would refuse is refused by Merge too.
	if m, err := Merge(&Profile{Records: []Record{{Key: "a\xff"}}}); err == nil {
		t.Fatalf("Merge accepted a non-UTF-8 key: %v", m)
	}
}

func TestPGOMergeSaturates(t *testing.T) {
	t.Parallel()
	a := &Profile{Records: []Record{{Key: "f", Counts: []uint64{math.MaxUint64 - 1, 5, math.MaxUint64}}}}
	b := &Profile{Records: []Record{{Key: "f", Counts: []uint64{3, 5, math.MaxUint64}}}}
	m, err := Merge(a, b, b)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{math.MaxUint64, 15, math.MaxUint64}
	got := m.Records[0].Counts
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("counts = %v, want %v", got, want)
		}
	}
	// Duplicates inside one input are summed too.
	d := &Profile{Records: []Record{{Key: "f", Counts: []uint64{1}}, {Key: "f", Counts: []uint64{2}}}}
	if m, err := Merge(d); err != nil || m.Records[0].Counts[0] != 3 || len(m.Records) != 1 {
		t.Fatalf("duplicates within one input: %v %v", m, err)
	}
}

func TestPGOMergeLengthMismatch(t *testing.T) {
	t.Parallel()
	a := &Profile{Records: []Record{{Key: "k", Checksum: sum(7), Counts: []uint64{1, 2}}}}
	b := &Profile{Records: []Record{{Key: "k", Checksum: sum(7), Counts: []uint64{1, 2, 3}}}}
	var msgs []string
	for _, args := range [][]*Profile{{a, b}, {b, a}} {
		m, err := Merge(args...)
		if err == nil || m != nil {
			t.Fatalf("Merge accepted two lengths for one (key, checksum): %v", m)
		}
		msg := err.Error()
		for _, part := range []string{`"k"`, " 2 ", " 3 "} {
			if !strings.Contains(msg, part) {
				t.Errorf("error %q does not name %s", msg, part)
			}
		}
		msgs = append(msgs, msg)
	}
	if msgs[0] != msgs[1] {
		t.Errorf("the error depends on argument order:\n%s\n%s", msgs[0], msgs[1])
	}
	// The first mismatch in key order is reported, whatever the input order.
	x := &Profile{Records: []Record{{Key: "z", Counts: []uint64{1}}, {Key: "a", Counts: []uint64{1}}}}
	y := &Profile{Records: []Record{{Key: "z", Counts: []uint64{1, 1}}, {Key: "a", Counts: nil}}}
	for _, args := range [][]*Profile{{x, y}, {y, x}} {
		_, err := Merge(args...)
		if err == nil || !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), " 0 ") {
			t.Errorf("want the error for \"a\" (lengths 0 and 1), got %v", err)
		}
	}
	// A third length widens the reported range, independent of order.
	c := &Profile{Records: []Record{{Key: "k", Checksum: sum(7), Counts: []uint64{1, 2, 3, 4}}}}
	_, e1 := Merge(b, c, a)
	_, e2 := Merge(a, b, c)
	if e1 == nil || e2 == nil || e1.Error() != e2.Error() || !strings.Contains(e1.Error(), " 2 ") || !strings.Contains(e1.Error(), " 4 ") {
		t.Errorf("three lengths: %v / %v", e1, e2)
	}
}

func TestPGOLookupWrongLength(t *testing.T) {
	t.Parallel()
	p := &Profile{Records: []Record{{Key: "k", Checksum: sum(1), Counts: []uint64{10, 20}}}}
	for _, n := range []int{-1, 0, 1, 3, 1 << 30} {
		if c, ok := p.Lookup("k", sum(1), n); ok || c != nil {
			t.Errorf("Lookup(k, s, %d) on a record of length 2 = %v, %v; want stale", n, c, ok)
		}
	}
	c, ok := p.Lookup("k", sum(1), 2)
	if !ok || len(c) != 2 || cap(c) != 2 || c[0] != 10 || c[1] != 20 {
		t.Fatalf("Lookup(k, s, 2) = %v (cap %d), %v", c, cap(c), ok)
	}
	if _, ok := (*Profile)(nil).Lookup("k", sum(1), 2); ok {
		t.Fatal("nil profile has no records")
	}
}

func TestPGOStaleChecksumIgnored(t *testing.T) {
	t.Parallel()
	p := sample()
	if _, ok := p.Lookup("mk.app.helper", sum(2), 4); ok {
		t.Fatal("a record with another checksum must be stale")
	}
	if _, ok := p.Lookup("mk.app.missing", sum(1), 1); ok {
		t.Fatal("a missing key has no data")
	}
	// Two checksums under one key: each matches only itself.
	if c, ok := p.Lookup("mk.app.Main", sum(2), 1); !ok || c[0] != 5 {
		t.Fatalf("Main@2 = %v %v", c, ok)
	}
	if c, ok := p.Lookup("mk.app.Main", sum(1), 3); !ok || c[0] != 1 {
		t.Fatalf("Main@1 = %v %v", c, ok)
	}
	if _, ok := p.Lookup("mk.app.Main", sum(3), 1); ok {
		t.Fatal("Main@3 is not in the profile")
	}
	if c, ok := p.Lookup("mk.std.fmt.Print.0123456789abcdef", sum(9), 0); !ok || len(c) != 0 {
		t.Fatalf("a zero-length record matches n = 0: %v %v", c, ok)
	}
}

func entries(counts ...uint64) *Profile {
	p := &Profile{}
	for i, c := range counts {
		p.Records = append(p.Records, Record{Key: string(rune('a' + i)), Counts: []uint64{c, 1}})
	}
	return p
}

func TestPGOSummaryThresholds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		p         *Profile
		hot, cold []uint64
		notHot    []uint64
	}{
		// Total 100: 50 + 30 = 80 < 90, + 10 = 90 reaches 90%: threshold 10.
		{"exact 90", entries(50, 30, 10, 5, 5, 0), []uint64{50, 30, 10, 11}, []uint64{0}, []uint64{9, 5, 0}},
		// Total 1000: 900 alone reaches 90%.
		{"one dominant", entries(900, 50, 50), []uint64{900, 1000}, nil, []uint64{899, 50, 0}},
		// Ties with the last hot member are hot.
		{"ties", entries(10, 10, 10, 10, 10, 10, 10, 10, 10, 10), []uint64{10}, nil, []uint64{9}},
		// Total 0: nothing is hot or cold.
		{"all zero", entries(0, 0), nil, nil, []uint64{0, 1}},
		{"empty", &Profile{}, nil, nil, []uint64{0, 1}},
		// Records without counters count as entry 0.
		{"no counters", &Profile{Records: []Record{{Key: "a"}, {Key: "b", Counts: []uint64{4}}}}, []uint64{4}, []uint64{0}, []uint64{3}},
		// Sums beyond 64 bits.
		{"huge", entries(math.MaxUint64, math.MaxUint64, 1), []uint64{math.MaxUint64}, nil, []uint64{math.MaxUint64 - 1, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.p.Summarize()
			for _, e := range c.hot {
				if !s.Hot(e) {
					t.Errorf("Hot(%d) = false", e)
				}
				if s.Cold(e) {
					t.Errorf("Cold(%d) = true", e)
				}
			}
			for _, e := range c.notHot {
				if s.Hot(e) {
					t.Errorf("Hot(%d) = true", e)
				}
			}
			for _, e := range c.cold {
				if !s.Cold(e) {
					t.Errorf("Cold(%d) = false", e)
				}
			}
			// Cold(0) holds exactly when the total is not zero.
			if total := c.p.Summarize().Hot(math.MaxUint64); s.Cold(0) != total {
				t.Errorf("Cold(0) = %v with a total that is zero = %v", s.Cold(0), !total)
			}
		})
	}
	// Order of records does not change the thresholds.
	a, b := entries(1, 2, 3, 40, 50).Summarize(), entries(50, 3, 40, 2, 1).Summarize()
	if *a != *b {
		t.Fatal("record order changed the summary")
	}
}

func TestPGOWeightsScale(t *testing.T) {
	t.Parallel()
	eq := func(a, b []uint32) bool {
		if len(a) != len(b) || (a == nil) != (b == nil) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	const m = math.MaxUint32
	cases := []struct {
		in   []uint64
		want []uint32
	}{
		{nil, nil},
		{[]uint64{}, nil},
		{[]uint64{0, 0, 0}, nil},
		{[]uint64{3, 0, 1}, []uint32{3, 0, 1}},
		{[]uint64{m, 1}, []uint32{m, 1}},
		{[]uint64{m + 1, 0}, []uint32{m, 0}},
		{[]uint64{2 * (m + 1), m + 1, 0}, []uint32{m, m/2 + 1, 0}},
		{[]uint64{math.MaxUint64, math.MaxUint64 / 2, 1, 0}, []uint32{m, m / 2, 1, 0}},
		{[]uint64{math.MaxUint64, math.MaxUint64}, []uint32{m, m}},
	}
	for _, c := range cases {
		if got := Weights(c.in); !eq(got, c.want) {
			t.Errorf("Weights(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	// Ratios are kept to within rounding.
	in := []uint64{1 << 40, 3 << 38, 1 << 38, 12345}
	w := Weights(in)
	for i := range in {
		exact := float64(in[i]) * m / float64(in[0])
		if d := float64(w[i]) - exact; d > 1 || d < -1 {
			t.Errorf("weight %d = %d, exact %.1f", i, w[i], exact)
		}
	}
}

// le encodes v little-endian in n bytes.
func le(n int, v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b[:n]
}

func rec(key string, s byte, counts ...uint64) []byte {
	var b []byte
	b = append(b, le(4, uint64(len(key)))...)
	b = append(b, key...)
	b = append(b, bytes.Repeat([]byte{s}, 16)...)
	b = append(b, le(4, uint64(len(counts)))...)
	for _, c := range counts {
		b = append(b, le(8, c)...)
	}
	return b
}

func file(count uint64, recs ...[]byte) []byte {
	b := append([]byte(Magic), le(8, count)...)
	for _, r := range recs {
		b = append(b, r...)
	}
	return b
}

func TestPGOReadRejects(t *testing.T) {
	t.Parallel()
	good := file(2, rec("a", 1, 5, 6), rec("b", 0, 7))
	if _, err := Read(bytes.NewReader(good)); err != nil {
		t.Fatalf("good file rejected: %v", err)
	}
	cases := map[string][]byte{
		"empty input":       nil,
		"short magic":       []byte("MKPR"),
		"bad magic":         append([]byte("MKPROF\x00\x02"), le(8, 0)...),
		"wrong case magic":  append([]byte("mkprof\x00\x01"), le(8, 0)...),
		"no count":          []byte(Magic),
		"short count":       append([]byte(Magic), 0, 0, 0),
		"count over 2^24":   file(MaxCount + 1),
		"huge count":        file(math.MaxUint64),
		"missing record":    file(3, rec("a", 1, 5, 6), rec("b", 0, 7)),
		"key over 64 KiB":   file(1, rec(strings.Repeat("k", MaxKeyLen+1), 0, 1)),
		"key not UTF-8":     file(1, rec("a\xffb", 0, 1)),
		"n over 2^24":       file(1, append(append(append(le(4, 1), 'a'), bytes.Repeat([]byte{0}, 16)...), le(4, MaxCount+1)...)),
		"huge n, no data":   file(1, append(append(append(le(4, 1), 'a'), bytes.Repeat([]byte{0}, 16)...), le(4, MaxCount)...)),
		"unsorted keys":     file(2, rec("b", 0, 1), rec("a", 0, 1)),
		"unsorted sums":     file(2, rec("a", 2, 1), rec("a", 1, 1)),
		"duplicate":         file(2, rec("a", 1, 1), rec("a", 1, 2)),
		"trailing byte":     append(good, 0),
		"trailing record":   append(good, rec("c", 0, 1)...),
		"key len past data": file(1, le(4, 1000)),
	}
	// Every truncation of a good file is rejected.
	for i := 0; i < len(good); i++ {
		cases["truncated at "+string(rune('0'+i/10))+string(rune('0'+i%10))] = good[:i]
	}
	for name, b := range cases {
		p, err := Read(bytes.NewReader(b))
		if err == nil {
			t.Errorf("%s: Read accepted malformed input: %+v", name, p)
		} else if !strings.HasPrefix(err.Error(), "pgo: ") {
			t.Errorf("%s: error %q lacks the package prefix", name, err)
		}
	}
	// Limits are inclusive: a key of exactly 64 KiB is accepted.
	if _, err := Read(bytes.NewReader(file(1, rec(strings.Repeat("k", MaxKeyLen), 0, 1)))); err != nil {
		t.Errorf("a 64 KiB key is allowed: %v", err)
	}
}

// TestPGOHasKey: HasKey sees a key under any checksum and nothing else, so
// codegen lists a function as stale only when the profile knew it.
func TestPGOHasKey(t *testing.T) {
	t.Parallel()
	p := &Profile{Records: []Record{{Key: "a", Checksum: sum(1)}, {Key: "k", Checksum: sum(1)}, {Key: "k", Checksum: sum(2)}, {Key: "z", Checksum: sum(3)}}}
	for key, want := range map[string]bool{"a": true, "k": true, "z": true, "": false, "b": false, "kk": false, "zz": false} {
		if got := p.HasKey(key); got != want {
			t.Errorf("HasKey(%q) = %v, want %v", key, got, want)
		}
	}
	if (*Profile)(nil).HasKey("a") {
		t.Error("nil profile has no keys")
	}
}

// TestPGOLLVMSummary: LLVM's ProfileSummary numbers over the whole profile,
// worked out by hand for counts {10, 2, 0} and {5} (total 17).
func TestPGOLLVMSummary(t *testing.T) {
	t.Parallel()
	p := &Profile{Records: []Record{{Key: "a", Checksum: sum(1), Counts: []uint64{10, 2, 0}}, {Key: "b", Checksum: sum(1), Counts: []uint64{5}}}}
	s := p.LLVM()
	if s.TotalCount != 17 || s.MaxCount != 10 || s.MaxInternalCount != 2 || s.MaxFunctionCount != 10 || s.NumCounts != 4 || s.NumFunctions != 2 {
		t.Fatalf("summary %+v", s)
	}
	want := map[uint32][2]uint64{10000: {0, 0}, 100000: {10, 1}, 600000: {10, 1}, 700000: {5, 2}, 900000: {5, 2},
		950000: {2, 3}, 990000: {2, 3}, 999999: {2, 3}}
	if len(s.Detailed) != len(Cutoffs) {
		t.Fatalf("%d cutoffs", len(s.Detailed))
	}
	for _, e := range s.Detailed {
		if w, ok := want[e.Cutoff]; ok && (e.MinCount != w[0] || e.NumCount != w[1]) {
			t.Errorf("cutoff %d: {%d, %d}, want %v", e.Cutoff, e.MinCount, e.NumCount, w)
		}
	}
	if z := (*Profile)(nil).LLVM(); z.TotalCount != 0 || len(z.Detailed) != len(Cutoffs) {
		t.Errorf("empty profile: %+v", z)
	}
	huge := &Profile{Records: []Record{{Key: "h", Checksum: sum(1), Counts: []uint64{^uint64(0), ^uint64(0)}}}}
	if s := huge.LLVM(); s.TotalCount != ^uint64(0) {
		t.Errorf("TotalCount does not saturate: %d", s.TotalCount)
	}
}
