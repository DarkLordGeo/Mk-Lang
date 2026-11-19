// Why: a fast fixed table hash is only safe to use if its output looks
// random in the bits the Swiss table reads.
// What: TestStdHash_Quality scores the fold hash on integer, short-string
// and structured keys by avalanche and collision counts (H1, H2).
// How: pure Go over the model; SipHash-1-3 (sip13) calibrates, and plain
// foldhash (plainFold) must fail, so the test is known to discriminate.

package std

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

// hashSet is one family of keys, each the byte stream its Hash impl gives.
type hashSet struct {
	name string
	keys [][]byte
}

func u64Stream(xs ...uint64) []byte {
	var b []byte
	for _, x := range xs {
		b = binary.LittleEndian.AppendUint64(b, x)
	}
	return b
}

func strStream(s string) []byte { return append([]byte(s), 0xFF) }

// qualitySets: 65,536 keys each.
func qualitySets() []hashSet {
	const n = 1 << 16
	gen := func(name string, f func(i int) []byte) hashSet {
		s := hashSet{name: name}
		for i := 0; i < n; i++ {
			s.keys = append(s.keys, f(i))
		}
		return s
	}
	return []hashSet{
		// Integer keys (U64 of the value).
		gen("int sequential", func(i int) []byte { return u64Stream(uint64(i)) }),
		gen("int negative", func(i int) []byte { return u64Stream(uint64(-int64(i) - 1)) }),
		gen("int i<<32", func(i int) []byte { return u64Stream(uint64(i) << 32) }),
		gen("int i<<48", func(i int) []byte { return u64Stream(uint64(i) << 48) }),
		gen("int i*4096", func(i int) []byte { return u64Stream(uint64(i) * 4096) }),
		gen("int f64 bits", func(i int) []byte { return u64Stream(math.Float64bits(float64(i))) }),
		// Short strings (the bytes, then 0xFF).
		gen("str key%d", func(i int) []byte { return strStream(fmt.Sprintf("key%d", i)) }),
		gen("str 4 lower", func(i int) []byte {
			return strStream(string([]byte{byte('a' + i%26), byte('a' + i/26%26), byte('a' + i/676%26), byte('a' + i/17576%26)}))
		}),
		gen("str 2 bytes", func(i int) []byte { return strStream(string([]byte{byte(i), byte(i >> 8)})) }),
		gen("str 12 bytes", func(i int) []byte { return strStream(fmt.Sprintf("user-%07d", i)) }),
		// Structured keys (two or three U64 fields).
		gen("pair (i%256, i/256)", func(i int) []byte { return u64Stream(uint64(i%256), uint64(i/256)) }),
		gen("pair high bits", func(i int) []byte { return u64Stream(uint64(i%256)<<40, uint64(i/256)<<40) }),
		gen("triple (i%64, i/64%64, i/4096)", func(i int) []byte { return u64Stream(uint64(i%64), uint64(i/64%64), uint64(i/4096)) }),
		// Long keys: the varying bytes sit in the second pair, which
		// the second lane absorbs, or after a whole round of both lanes.
		gen("quad (0, 0, i, 0) lane 1", func(i int) []byte { return u64Stream(0, 0, uint64(i), 0) }),
		gen("str 32 bytes, i at 24..31", func(i int) []byte { return strStream(fmt.Sprintf("%024d%08x", 0, i)) }),
		gen("str 46-byte path", func(i int) []byte {
			return strStream(fmt.Sprintf("/srv/data/users/%08d/profile/settings.json", i))
		}),
		gen("str 100 bytes, i at 64..71", func(i int) []byte {
			return strStream(fmt.Sprintf("%064d%08d%028d", 7, i, 9))
		}),
	}
}

type quality struct {
	slotColl [2]int     // keys landing on an occupied H1 slot, cap 2^16 and 2^17
	slotWant [2]float64 // a random function's expectation
	full     int        // equal 64-bit hashes
	h2chi    float64    // chi-square of H2 over 128 cells (127 degrees of freedom)
	avWorst  float64    // max |P(output bit flips) - 0.5| over (input bit, output bit)
	avMean   float64
}

func score(h func([]byte) uint64, keys [][]byte, avKeys int) quality {
	var q quality
	n := float64(len(keys))
	hs := make([]uint64, len(keys))
	for i, k := range keys {
		hs[i] = h(k)
	}
	for j, bits := range []uint{16, 17} {
		c := 1 << bits
		occ := make([]bool, c)
		for _, x := range hs {
			s := int(x>>7) & (c - 1)
			if occ[s] {
				q.slotColl[j]++
			}
			occ[s] = true
		}
		fc := float64(c)
		q.slotWant[j] = n - fc*(1-math.Pow(1-1/fc, n))
	}
	seen := make(map[uint64]bool, len(hs))
	var cells [128]int
	for _, x := range hs {
		if seen[x] {
			q.full++
		}
		seen[x] = true
		cells[x>>57]++
	}
	e := n / 128
	for _, v := range cells {
		q.h2chi += (float64(v) - e) * (float64(v) - e) / e
	}
	// Avalanche over the first avKeys keys: flip each input bit.
	nb := len(keys[0]) * 8
	flips := make([][64]int, nb)
	for _, k := range keys[:avKeys] {
		base := h(k)
		x := append([]byte{}, k...)
		for i := 0; i < nb; i++ {
			x[i/8] ^= 1 << (i % 8)
			d := base ^ h(x)
			x[i/8] ^= 1 << (i % 8)
			for o := 0; o < 64; o++ {
				flips[i][o] += int(d >> o & 1)
			}
		}
	}
	sum := 0.0
	for i := 0; i < nb; i++ {
		for o := 0; o < 64; o++ {
			d := math.Abs(float64(flips[i][o])/float64(avKeys) - 0.5)
			sum += d
			q.avWorst = max(q.avWorst, d)
		}
	}
	q.avMean = sum / float64(nb*64)
	return q
}

// The bounds, from a random function: the slot collision count within 5
// standard deviations of its expectation (Poisson's sqrt(E) bounds the
// deviation from above); no equal 64-bit hash among 65,536 (probability
// about 2^-33 for a random function); H2's chi-square under 127 + 5 *
// sqrt(2 * 127); avalanche at 4,000 keys, whose binomial standard
// deviation is 0.0079: the worst of the 4,096 to 12,288 cells under 8 of
// them (0.063), the mean under 1.4 times the expected 0.0063 (0.0088).
const (
	avKeys      = 4000
	avWorstMax  = 0.063
	avMeanMax   = 0.0088
	h2ChiMax    = 127 + 5*15.94
	slotSigmaOK = 5
)

func (q quality) failures() []string {
	var f []string
	for j, bits := range []int{16, 17} {
		if math.Abs(float64(q.slotColl[j])-q.slotWant[j]) > slotSigmaOK*math.Sqrt(q.slotWant[j]) {
			f = append(f, fmt.Sprintf("H1 at cap 2^%d: %d collisions, a random function %.0f", bits, q.slotColl[j], q.slotWant[j]))
		}
	}
	if q.full > 0 {
		f = append(f, fmt.Sprintf("%d equal 64-bit hashes", q.full))
	}
	if q.h2chi > h2ChiMax {
		f = append(f, fmt.Sprintf("H2 chi-square %.1f > %.1f", q.h2chi, h2ChiMax))
	}
	if q.avWorst > avWorstMax || q.avMean > avMeanMax {
		f = append(f, fmt.Sprintf("avalanche worst %.4f (max %.4f), mean %.4f (max %.4f)", q.avWorst, avWorstMax, q.avMean, avMeanMax))
	}
	return f
}

// plainFold is foldhash without the quality fold: a candidate measured and
// refused, because it fails avalanche.
func plainFold(b []byte) uint64 {
	n := len(b)
	m := n / 8
	acc := foldF0
	j := 0
	for ; j+2 <= m; j += 2 {
		acc = fold(binary.LittleEndian.Uint64(b[8*j:])^acc, binary.LittleEndian.Uint64(b[8*j+8:])^foldF1)
	}
	var t uint64
	for k := 8 * m; k < n; k++ {
		t |= uint64(b[k]) << (8 * (k - 8*m))
	}
	a, c := t, uint64(0)
	if m%2 == 1 {
		a, c = binary.LittleEndian.Uint64(b[8*(m-1):]), t
	}
	return fold(a^acc, c^foldF2^uint64(n))
}

// TestStdHash_Quality: the fold hash and sip13 meet every bound on
// every set; plain foldhash fails at least one, on integer keys.
func TestStdHash_Quality(t *testing.T) {
	if testing.Short() {
		t.Skip("scores 17 sets of 65,536 keys")
	}
	t.Parallel()
	plainFailed := 0
	for _, s := range qualitySets() {
		for _, c := range []struct {
			name string
			h    func([]byte) uint64
		}{{"fold", foldHash}, {"sip13", sip13}, {"plain foldhash", plainFold}} {
			q := score(c.h, s.keys, avKeys)
			f := q.failures()
			t.Logf("%-32s %-15s H1 %d/%.0f %d/%.0f, H2 chi2 %.1f, avalanche %.4f %.4f, fails %d",
				s.name, c.name, q.slotColl[0], q.slotWant[0], q.slotColl[1], q.slotWant[1], q.h2chi, q.avWorst, q.avMean, len(f))
			switch {
			case c.name == "plain foldhash":
				if len(f) > 0 {
					plainFailed++
				}
			case len(f) > 0:
				t.Errorf("%s on %s: %v", c.name, s.name, f)
			}
		}
	}
	if plainFailed == 0 {
		t.Error("plain foldhash met every bound on every set: the test no longer discriminates")
	}
}
