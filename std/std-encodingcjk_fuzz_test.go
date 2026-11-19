// Why: std.encoding.cjk's long-running tests live apart from std-encodingcjk_test.go to keep files small.
// What: its random, adversarial and link tests and its fuzz target.
// How: they use the driver of std-encodingcjk_test.go and the model of std-encodingcjk_model_test.go.

package std

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// cjkFuzzBytes is n bytes biased to the CJK encodings' lead and trail
// ranges, escape sequences and ASCII.
func cjkFuzzBytes(r *encRng, n int) []byte {
	var out []byte
	pieces := [][]byte{{0x1B, 0x28, 0x42}, {0x1B, 0x28, 0x4A}, {0x1B, 0x28, 0x49}, {0x1B, 0x24, 0x40}, {0x1B, 0x24, 0x42}, {0x1B}, {0x1B, 0x24}, {0x1B, 0x28}, {0x8E}, {0x8F}, {0x80}, {0xFF}, {0x0E}, {0x0F}, {0x5C}, {0x7E}}
	for len(out) < n {
		switch r.intn(6) {
		case 0:
			out = append(out, pieces[r.intn(len(pieces))]...)
		case 1:
			out = append(out, byte(0x21+r.intn(0x5E)))
		case 2:
			out = append(out, byte(0x81+r.intn(0x7E)), byte(0x30+r.intn(10)), byte(0x81+r.intn(0x7E)), byte(0x30+r.intn(10)))
		case 3:
			out = append(out, byte(0x81+r.intn(0x7F)), byte(0x40+r.intn(0xC0)))
		case 4:
			out = append(out, byte(r.next()))
		default:
			out = append(out, byte(0xA1+r.intn(0x5E)), byte(0xA1+r.intn(0x5E)))
		}
	}
	return out[:n]
}

// cjkCuts is up to four ascending offsets in [0, n].
func cjkCuts(r *encRng, n int) []int {
	var cuts []int
	at := 0
	for k := r.intn(5); k > 0 && at <= n; k-- {
		at += r.intn(n - at + 1)
		cuts = append(cuts, at)
	}
	return cuts
}

func TestStdEncodingCjk_Adversarial(t *testing.T) {
	m := cjkModelGet(t)
	r := &encRng{s: 744}
	var cases []encCase
	for k := 0; k < 3000; k++ {
		name := cjkNames[k%len(cjkNames)]
		b := cjkFuzzBytes(r, r.intn(48))
		cases = append(cases, cjkDecCase(m, name, b), cjkDecsCase(m, name, b), cjkPushCase(m, name, b, cjkCuts(r, len(b))))
	}
	// A Decoder cut at every offset of streams that change mode, end
	// inside a unit and repeat escapes.
	for _, h := range []string{
		"1b2442242224241b2842", "1b284a5c7e1b28491b2440", "1b24423021", "1b2842", "1b28", "1b",
		"8130813082358f33", "81308130", "a1a1a2", "8fa2af8ea1", "82a0e040", "88628864", "1b2442",
		"1b24421b24421b2842", "1b28421b2842", "1b28491b2849",
	} {
		b, _ := hexDecodeString(h)
		for _, name := range cjkNames {
			for cut := 0; cut <= len(b); cut++ {
				cases = append(cases, cjkPushCase(m, name, b, []int{cut}))
				for cut2 := cut; cut2 <= len(b); cut2 += 2 {
					cases = append(cases, cjkPushCase(m, name, b, []int{cut, cut2}))
				}
			}
		}
	}
	// Text that changes ISO-2022-JP's encoder state, and text in each
	// encoding's repertoire, encode as the model says and decode back.
	texts := []string{"", "a", "¥", "‾", "¥a", "a¥b‾c", "あいう", "aあb", "ｱｲｳ", "−", "\u000e", "x\u001by", "¥\u000e", "あ\u000f", "😀", "€", "", "", "Ê̄", "Ё", "한국어", "中文", "￿", "\U0010ffff"}
	for k := 0; k < 400; k++ {
		texts = append(texts, cjkRandomText(m, r, cjkNames[k%len(cjkNames)], 1+r.intn(12)))
	}
	for _, s := range texts {
		for _, name := range cjkNames {
			cases = append(cases, cjkEncCase(m, name, s))
			if out, bad := m.encode(name, s); bad < 0 {
				back, _ := m.decode(name, out)
				if back != s && !cjkLossy(name, s) {
					t.Errorf("model: %s: %q encodes to %x, which decodes to %q", name, s, out, back)
				}
				cases = append(cases, cjkDecsCase(m, name, out))
			}
		}
	}
	cjkRunAll(t, cases)
}

// cjkLossy is whether s holds a code point that name's encoder maps to
// bytes that decode to another, the mappings the standard keeps one way:
// U+2212 (as U+FF0D), U+00A5 and U+203E in EUC-JP and Shift_JIS (as 0x5C
// and 0x7E), halfwidth katakana in ISO-2022-JP (as fullwidth) and
// gb18030's side table of PUA code points.
func cjkLossy(name, s string) bool {
	for _, r := range s {
		switch {
		case r == 0x2212:
			return true
		case (r == 0xA5 || r == 0x203E) && (name == "EUC-JP" || name == "Shift_JIS"):
			return true
		case r >= 0xFF61 && r <= 0xFF9F && name == "ISO-2022-JP":
			return true
		case (name == "gb18030" || name == "GBK") && r >= 0xE78D && r <= 0xE864:
			return true
		}
	}
	return false
}

// cjkRandomText is n scalars that name's decoder gives for random
// pointers, so mostly text the encoding holds.
func cjkRandomText(m *cjkModel, r *encRng, name string, n int) string {
	var idx []int32
	switch name {
	case "GBK", "gb18030":
		idx = m.x.gb18030
	case "Big5":
		idx = m.x.big5
	case "EUC-KR":
		idx = m.x.euckr
	default:
		idx = m.x.jis0208
	}
	var sb strings.Builder
	for sb.Len() < 4*n && n > 0 {
		if r.intn(4) == 0 {
			sb.WriteByte(byte(0x20 + r.intn(0x5F)))
		} else if cp := idx[r.intn(len(idx))]; cp > 0 {
			sb.WriteRune(rune(cp))
		}
		n--
	}
	return sb.String()
}

// TestStdEncodingCjk_LinksOnlyNamed builds release programs and looks for
// 64 bytes of two index tables in them: encoding.Lookup links neither,
// cjk.ShiftJis links jis0208 and not gb18030, and cjk.Lookup both.
func TestStdEncodingCjk_LinksOnlyNamed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds three release programs")
	}
	x, err := cjkLoad(cjkDir(t))
	if err != nil {
		t.Fatal(err)
	}
	jis := cjkDec(x.jis0208, 2)[4000:4064]
	gb := cjkDec(x.gb18030, 2)[4000:4064]
	root := t.TempDir()
	stdDir := encStdDir(t, root)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	for _, c := range []struct {
		name, imports, enc string
		wantJis, wantGb    bool
	}{
		{"lookup", "import std.encoding", "encoding.Lookup(sys.args[0]) else { encoding.Utf8 }", false, false},
		{"sjis", "import std.encoding.cjk", "cjk.ShiftJis", true, false},
		{"all", "import std.encoding\nimport std.encoding.cjk", "cjk.Lookup(sys.args[0]) else { encoding.Utf8 }", true, true},
	} {
		dir := filepath.Join(root, c.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		src := strings.ReplaceAll(c.imports+`

export Main(sys: &System): OutOfMemory|WriteError!void {
    e := ENC
    n := (try e.Decode(sys.arena, sys.args[0].AsBytes())).Len()
    var w := sys.Stdout()
    try Print(mut w, "{n} {s}\n", (n, s: e.Name()))
}
`, "ENC", c.enc)
		if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(dir, "main")
		if runtime.GOOS == "windows" {
			exe += ".exe"
		}
		out, status := mkCmd(t, dir, stdDir, "build", "--release", "-o", exe, "main.mk")
		if status != 0 {
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			t.Fatalf("%s: mk build exit %d\n%s", c.name, status, out)
		}
		bin, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if got := bytes.Contains(bin, jis); got != c.wantJis {
			t.Errorf("%s (%d bytes): index jis0208 present %v, want %v", c.name, len(bin), got, c.wantJis)
		}
		if got := bytes.Contains(bin, gb); got != c.wantGb {
			t.Errorf("%s (%d bytes): index gb18030 present %v, want %v", c.name, len(bin), got, c.wantGb)
		}
		t.Logf("%s: %d bytes", c.name, len(bin))
	}
}

// hexDecodeString is encoding/hex's DecodeString.
func hexDecodeString(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := range out {
		v, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(v)
	}
	return out, nil
}

// FuzzStdEncodingCjk_RoundTrip decodes any bytes in one of the encodings
// (Decode, DecodeStrict and a split Decoder, against the model), then
// encodes what decoded with the U+FFFDs taken out (against the model),
// and decodes that again: what Encode accepts must decode to the text it
// was given, except for the mappings the standard keeps one way.
func FuzzStdEncodingCjk_RoundTrip(f *testing.F) {
	r := &encRng{s: 7440}
	for k := 0; k < 40; k++ {
		f.Add(cjkFuzzBytes(r, r.intn(40)), uint8(k), uint8(r.next()))
	}
	for _, h := range []string{"1b24422422", "8130813082358f33", "88628864", "8fa2af", "82a0", "b0a1", "a4a4"} {
		b, _ := hexDecodeString(h)
		for k := 0; k < len(cjkNames); k++ {
			f.Add(b, uint8(k), uint8(3))
		}
	}
	f.Fuzz(func(t *testing.T, b []byte, which, cut uint8) {
		if len(b) > 2048 {
			return
		}
		m := cjkModelGet(t)
		name := cjkNames[int(which)%len(cjkNames)]
		text, _ := m.decode(name, b)
		c := int(cut) % (len(b) + 1)
		clean := strings.ReplaceAll(text, "�", "")
		cases := []encCase{cjkDecCase(m, name, b), cjkDecsCase(m, name, b), cjkPushCase(m, name, b, []int{c}), cjkEncCase(m, name, clean)}
		if out, bad := m.encode(name, clean); bad < 0 {
			back, _ := m.decode(name, out)
			if back != clean && !cjkLossy(name, clean) {
				t.Fatalf("model: %s: %q encodes to %x, which decodes to %q", name, clean, out, back)
			}
			cases = append(cases, cjkDecCase(m, name, out))
		}
		exe := cjkExe(t, "release")
		var in strings.Builder
		for _, cs := range cases {
			in.WriteString(cs.in + "\n")
		}
		res := runNative(t, exe, in.String())
		got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(res.stdout, "\r\n", "\n"), "\n"), "\n")
		if res.code != 0 || len(got) != len(cases) {
			t.Fatalf("driver exit %d, %d answers for %d\nstderr %s", res.code, len(got), len(cases), res.stderr)
		}
		for i, cs := range cases {
			if got[i] != cs.want {
				t.Fatalf("%s\n got  %s\n want %s", encShort(cs.in), encShort(got[i]), encShort(cs.want))
			}
		}
	})
}
