// Why: a Text's writer refuses a write that is not whole UTF-8 scalars, so
// Value.Format must never split a scalar across its 512-byte pieces.
// What: Value.Format through Text.Writer and through Print's {v} hole.
// How: inputs put multi-byte scalars, escapes and strings longer than the
// buffer at every offset of the buffer's boundary; both outputs must match.

package std

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// jsonTextDriver reads one JSON text from standard input, formats it into a
// Text through Text.Writer and prints the Text, then prints it a second
// time through Print's {v} hole on stdout, so both paths are compared.
const jsonTextDriver = `import std.json

export Main(sys: &System): json.Error|Utf8Error|ReadError|WriteError!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 24)
    n := try inp.ReadAll(buf)
    text := try str.From(buf[0..n])
    v := try json.Parse(sys.arena, text)
    var t := Text.New(sys.arena)
    var w := t.Writer()
    try v.Format(mut w)
    var out := sys.Stdout()
    try out.Str(t.Str())
    try Print(mut out, "\n{v}\n", (v: &v))
}
`

// jsonTextInput is an array of strings and nested values whose pieces land
// across the 512-byte boundary at every offset.
func jsonTextInput(seed uint64) string {
	r := rand.New(rand.NewPCG(seed, 1))
	pieces := []string{"é", "😀", "€", "a", `\"`, `\\`, `\n`, `\u0001`, `\u00e9`, `\ud83d\ude00`, "/"}
	var sb strings.Builder
	sb.WriteString("[")
	for k := 0; k < 400; k++ {
		if k > 0 {
			sb.WriteString(",")
		}
		switch r.IntN(4) {
		case 0:
			fmt.Fprintf(&sb, `{"k%d":[%d,true,null,"x"]}`, k, r.IntN(1000))
		default:
			sb.WriteString(`"`)
			n := r.IntN(40)
			if r.IntN(20) == 0 {
				n = 300 + r.IntN(700) // longer than the buffer
			}
			for i := 0; i < n; i++ {
				sb.WriteString(pieces[r.IntN(len(pieces))])
			}
			sb.WriteString(`"`)
		}
	}
	sb.WriteString("]")
	return sb.String()
}

func TestSTDJSON_FormatIntoText(t *testing.T) {
	exe := jsonBuild(t, map[string]string{"main.mk": jsonTextDriver}, nil)
	for seed := uint64(1); seed <= 4; seed++ {
		in := jsonTextInput(seed)
		v, err := jmParse([]byte(in))
		if err != nil {
			t.Fatalf("seed %d: model refuses the input: %v near %q", seed, err, in[max(0, err.offset-20):min(len(in), err.offset+20)])
		}
		var sb strings.Builder
		jmFormat(&sb, v)
		want := sb.String()
		r := runNative(t, exe, in)
		wantRun(t, r, 0, want+"\n"+want+"\n")
	}
}
