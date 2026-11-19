// Why: names and byte strings in IR must be escaped exactly as LLVM reads them.
// What: TestLLVMQuote, FuzzLLVMQuote and TestLLVMBytesEscape.
// How: table cases; the fuzz target checks that Quote leaves a bare name as
// it is and quotes every other one.

package llvm

import (
	"regexp"
	"strconv"
	"testing"
)

var bareRE = regexp.MustCompile(`^[-a-zA-Z$._][-a-zA-Z$._0-9]*$`)

// unquote reverses Quote's escaping of a quoted name or c"" body.
func unquote(t *testing.T, s string) string {
	t.Helper()
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			if s[i] < 0x20 || s[i] > 0x7e || s[i] == '"' {
				t.Fatalf("unescaped byte %#x in %q", s[i], s)
			}
			out = append(out, s[i])
			continue
		}
		if i+3 > len(s) {
			t.Fatalf("short escape in %q", s)
		}
		n, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil || s[i+1:i+3] != string([]byte{hexDigits[n>>4], hexDigits[n&15]}) {
			t.Fatalf("bad escape %q in %q", s[i:i+3], s)
		}
		out = append(out, byte(n))
		i += 2
	}
	return string(out)
}

func TestLLVMQuote(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"memcpy", "memcpy"},
		{"mk.std.fs.File.Close", "mk.std.fs.File.Close"},
		{"_start", "_start"},
		{"$x-1.y_2", "$x-1.y_2"},
		{"mk.std.coll.List.Push.3f9a0c1d2e4b5a69", "mk.std.coll.List.Push.3f9a0c1d2e4b5a69"},
		{"mk.main.Sort#fn2", `"mk.main.Sort#fn2"`},
		{`mk.main#test("adds")`, `"mk.main#test(\22adds\22)"`},
		{`a\b`, `"a\5Cb"`},
		{"0", `"0"`},
		{"123", `"123"`},
		{"1abc", `"1abc"`},
		{"a b", `"a b"`},
		{"tab\there", `"tab\09here"`},
		{"caf\xc3\xa9", `"caf\C3\A9"`},
		{"\x7f", `"\7F"`},
		{"", `""`},
		{"~", `"~"`},
	}
	for _, c := range cases {
		if got := Quote(c.in); got != c.want {
			t.Errorf("Quote(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func FuzzLLVMQuote(f *testing.F) {
	for _, s := range []string{"", "a", "0", "mk.main#test(\"x\")", "\x00\xff", `\"`, "a.b-c$d_e"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q := Quote(s)
		if bareRE.MatchString(q) {
			if q != s {
				t.Fatalf("bare Quote(%q) = %q changed the name", s, q)
			}
			return
		}
		if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
			t.Fatalf("Quote(%q) = %q is neither bare nor quoted", s, q)
		}
		if bareRE.MatchString(s) {
			t.Fatalf("Quote(%q) quoted a bare name", s)
		}
		if got := unquote(t, q[1:len(q)-1]); got != s {
			t.Fatalf("unquote(Quote(%q)) = %q", s, got)
		}
	})
}

func TestLLVMBytesEscape(t *testing.T) {
	t.Parallel()
	m := NewModule("t", "", "")
	cases := []struct {
		in   []byte
		want string
	}{
		{nil, `[0 x i8] c""`},
		{[]byte("hello"), `[5 x i8] c"hello"`},
		{[]byte("a\"b\\c"), `[5 x i8] c"a\22b\5Cc"`},
		{[]byte("line\n\x00"), `[6 x i8] c"line\0A\00"`},
		{[]byte{0x1f, 0x20, 0x7e, 0x7f, 0x80, 0xff}, `[6 x i8] c"\1F ~\7F\80\FF"`},
		{[]byte("100%"), `[4 x i8] c"100%"`},
	}
	for _, c := range cases {
		v := m.Bytes(c.in)
		if got := v.String(); got != c.want {
			t.Errorf("Bytes(%q) = %s, want %s", c.in, got, c.want)
		}
		body := v.Ref()[2 : len(v.Ref())-1]
		if got := unquote(t, body); got != string(c.in) {
			t.Errorf("Bytes(%q) does not round-trip: %q", c.in, got)
		}
	}
}
