// Why: str.ToLower and str.ToUpper must apply Unicode's simple case mapping.
// What: tests against Go's strings.ToLower and ToUpper, the same mapping.
// How: at compile time over every CaseTable range's edges; natively over
// every Unicode scalar.

package std

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// caseEdges is every scalar at or next to a CaseRanges boundary, plus
// ASCII, the length-changing mappings and a few unmapped scalars.
func caseEdges() []rune {
	seen := map[rune]bool{}
	var out []rune
	add := func(r rune) {
		if r < 0 || r > unicode.MaxRune || (r >= 0xD800 && r <= 0xDFFF) || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	for r := rune(0); r < 0x80; r++ {
		add(r)
	}
	for _, c := range unicode.CaseRanges {
		for _, r := range []rune{rune(c.Lo) - 1, rune(c.Lo), rune(c.Lo) + 1, rune(c.Hi) - 1, rune(c.Hi), rune(c.Hi) + 1} {
			add(r)
		}
	}
	for _, r := range []rune{0x130, 0x131, 0x17F, 0x1E9E, 0x212A, 0x212B, 0x23A, 0x23E, 0x2C65, 0x10400, 0x1E900, 0x10FFFF, 0xE000, 0xFFFD} {
		add(r)
	}
	return out
}

// TestCaseMapAtCompileTime maps the edge scalars, in strings of up
// to 120 scalars, through an arena over evaluation memory, and compares
// each with Go's strings.ToLower and ToUpper; the empty string maps to "".
func TestCaseMapAtCompileTime(t *testing.T) {
	t.Parallel()
	edges := caseEdges()
	var in, lo, up []string
	for i := 0; i < len(edges); i += 120 {
		s := string(edges[i:min(i+120, len(edges))])
		in = append(in, mkString(s))
		lo = append(lo, mkString(strings.ToLower(s)))
		up = append(up, mkString(strings.ToUpper(s)))
	}
	n := len(in)
	fixture := probeHeap + fmt.Sprintf(`
const CaseIn: [%[1]d]str = [%[2]s]
const CaseLo: [%[1]d]str = [%[3]s]
const CaseUp: [%[1]d]str = [%[4]s]

CaseCheck(from: int, to: int): int {
    var mem: [16384]u8 = [0; 16384]
    unsafe "fixture" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&ProbeHeapTable))
        var a := Arena.New(&heap) else { return 90 }
        var bad := 0
        for k in from..to {
            l := CaseIn[k].ToLower(&a) else {
                a.Free()
                return 91
            }
            if l != CaseLo[k] {
                bad += 1
            }
            u := CaseIn[k].ToUpper(&a) else {
                a.Free()
                return 92
            }
            if u != CaseUp[k] {
                bad += 1
            }
            a.Reset()
        }
        e := "".ToUpper(&a) else {
            a.Free()
            return 93
        }
        if e != "" {
            bad += 1
        }
        a.Free()
        return bad
    }
}

`, n, strings.Join(in, ", "), strings.Join(lo, ", "), strings.Join(up, ", "))
	// One const per two strings: each evaluation has its own fuel.
	var parts []string
	for k := 0; k < n; k += 2 {
		fixture += fmt.Sprintf("const Case%d: int = CaseCheck(%d, %d)\n", k, k, min(k+2, n))
		parts = append(parts, fmt.Sprintf("Case%d", k))
	}
	fixture += "\nconst CaseOk = [0; 1][" + strings.Join(parts, " + ") + "]\n"
	expectClean(t, "lib033", fixture)
}

// TestCaseMapAllScalars maps every Unicode scalar natively at
// release: stdin holds them 256 to a line, the program prints each line's
// ToLower and ToUpper, and both must equal Go's.
func TestCaseMapAllScalars(t *testing.T) {
	t.Parallel()
	p := caseProgram()
	r := runNative(t, buildWithModule(t, p.rel, p.module, p.src, "--release"), p.stdin)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	t.Logf("native run: %v for %d bytes of input", r.took, len(p.stdin))
	compareLines(t, r.stdout, p.want)
}

// caseProgram is TestCaseMapAllScalars's program.
func caseProgram() textProgram {
	var in, want strings.Builder
	var line []rune
	flush := func() {
		s := string(line)
		in.WriteString(s + "\n")
		want.WriteString(strings.ToLower(s) + "\n" + strings.ToUpper(s) + "\n")
		line = line[:0]
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r == '\n' || r == '\r' || (r >= 0xD800 && r <= 0xDFFF) || !utf8.ValidRune(r) {
			continue
		}
		line = append(line, r)
		if len(line) == 256 {
			flush()
		}
	}
	flush()
	src := `export Main(sys: &System): ReadError|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    for {
        l := try inp.Line(sys.arena)
        s := l else {
            break
        }
        lo := try s.ToLower(sys.arena)
        up := try s.ToUpper(sys.arena)
        try Print(mut out, "{lo}\n{up}\n", (lo, up))
    }
    try out.Flush()
}
`
	return textProgram{"case", "", "", src, in.String(), want.String()}
}
