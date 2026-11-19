// Why: DER's ReadTlv takes a short path for a one-byte identifier and a one-byte length that
// fits; that path must agree byte for byte with the general one.
// What: TestStdEncoding_DerFastPathPinned (std/x509/testdata), TestStdEncoding_DerFastPathLimbo
// (the x509-limbo suite pinned in Mk-evidence) and the seeds of FuzzStdEncoding_Der.
// How: the driver's "der" record walks every element against derRead, the Go model of the
// general path; certificates are read whole and from every offset, with windows around the length bound.

package std

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// derPinned is the DER of every CERTIFICATE block under std/x509/testdata,
// with its file name.
func derPinned(t testing.TB) (names []string, ders [][]byte) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(x509Dir, "*.pem"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no pinned certificates under %s (%v)", x509Dir, err)
	}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for i, d := range x509Der(string(b)) {
			names = append(names, fmt.Sprintf("%s#%d", filepath.Base(p), i))
			ders = append(ders, d)
		}
	}
	return names, ders
}

// derShortAt is whether ReadTlv takes its short form on data: a low tag
// number, and one length byte below 128 whose content fits.
func derShortAt(data []byte) bool {
	return len(data) >= 2 && data[0]&0x1F != 0x1F && data[1] < 0x80 && int(data[1]) <= len(data)-2
}

// derCount is how many elements the driver's walk of the element at the
// front of top reads, and how many of them ReadTlv takes by the short
// form.
func derCount(top []byte) (all, short int) {
	type item struct {
		data  []byte
		depth int
	}
	all = 1
	if derShortAt(top) {
		short++
	}
	v, _, err := derRead(top, 0)
	if err != "" {
		return all, short
	}
	var stack []item
	if v.cons {
		stack = append(stack, item{v.content, 1})
	}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for rest := it.data; len(rest) > 0; {
			all++
			if derShortAt(rest) {
				short++
			}
			c, r, err := derRead(rest, it.depth)
			if err != "" {
				break
			}
			if c.cons {
				stack = append(stack, item{c.content, it.depth + 1})
			}
			rest = r
		}
	}
	return all, short
}

// derWindows is the cases for der read from every offset.
func derWindows(der []byte) []encCase {
	n := len(der)
	var out []encCase
	seen := map[[2]int]bool{}
	add := func(p, e int) {
		if e < p || e > n || seen[[2]int{p, e}] {
			return
		}
		seen[[2]int{p, e}] = true
		out = append(out, derCase(der[p:e]))
	}
	for p := 0; p < n; p++ {
		add(p, min(n, p+1))
		add(p, min(n, p+12))
		if n-p >= 2 {
			end := p + 2 + int(der[p+1])
			add(p, min(n, end-1))
			add(p, min(n, end))
			add(p, min(n, end+1))
		}
	}
	return out
}

func TestStdEncoding_DerFastPathPinned(t *testing.T) {
	names, ders := derPinned(t)
	var cases []encCase
	allSum, shortSum := 0, 0
	for i, d := range ders {
		cases = append(cases, derCase(d))
		cases = append(cases, derWindows(d)...)
		all, short := derCount(d)
		if short == 0 {
			t.Fatalf("%s: %d elements, %d by the short form: the fast path is not exercised", names[i], all, short)
		}
		allSum += all
		shortSum += short
		t.Logf("%s: %d bytes, %d elements, %d by the short form", names[i], len(d), all, short)
	}
	t.Logf("%d pinned certificates: %d elements, %d by the short form; %d cases", len(ders), allSum, shortSum, len(cases))
	encRunAll(t, cases)
}

func TestStdEncoding_DerFastPathLimbo(t *testing.T) {
	path := filepath.Join(x509Evidence(), "x509", "limbo.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("the limbo suite is not in Mk-evidence/x509 (set MK_EVIDENCE): %v", err)
	}
	var suite struct {
		Testcases []x509Limbo `json:"testcases"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var cases []encCase
	long := 0
	for _, c := range suite.Testcases {
		texts := append(append([]string{c.PeerCertificate}, c.Intermediates...), c.TrustedCerts...)
		for _, s := range texts {
			for _, d := range x509Der(s) {
				if seen[string(d)] {
					continue
				}
				seen[string(d)] = true
				if derRecordTooLong(d) {
					long++
					continue
				}
				cases = append(cases, derCase(d))
			}
		}
	}
	if len(cases) == 0 {
		t.Fatal("no certificates in the limbo suite")
	}
	t.Logf("%d distinct limbo certificates read whole (%d left out: their record is longer than the driver's stdin reads)", len(cases), long)
	encRunAll(t, cases)
}

// derRecordTooLong is whether the driver's "der" record of d, with its
// newline, is longer than the line its stdin returns: std.core's
// Reader.Line on stdin stops at LineMax, 65536 bytes (core/text_text.mk),
// and the driver ends with ReadError.
func derRecordTooLong(d []byte) bool { return len("der ")+2*len(d)+1 > 65536 }

// derPinnedSeeds adds the pinned certificates and their halves to f.
func derPinnedSeeds(f *testing.F) {
	_, ders := derPinned(f)
	for _, d := range ders {
		f.Add(d)
		f.Add(d[:len(d)/2])
	}
}
