// Why: where std.regex's tables (UCD 18.0.0) and Go's unicode package
// (17.0.0) disagree, Go's regexp cannot judge std.regex.
// What: TestSTDREGEX_Ucd18Cases, hand cases on those scalars and names.
// How: each expectation is read off the UCD files, as
// TestSTDREGEX_Ucd18Cases says.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// rxUcdCases: pattern, input, and the expected IsMatch and Find.
var rxUcdCases = []struct {
	pat, in string
	want    string
}{
	{`(?i)ß`, "\U0001DF95", "true 0,4"},
	{`(?i)ß`, "ẞ", "true 0,3"},
	{`(?i)ẞ`, "a\U0001DF95", "true 1,5"},
	{`(?i)\x{1DF95}`, "xß", "true 1,3"},
	{`\x{1DF95}`, "ß", "false -"},
	{`(?i)\x{277}`, "\uA7DD", "true 0,3"},
	{`\p{Lu}`, "a\uA7DD", "true 1,4"},
	{`\p{Seal}+`, "a\U0003D000\U0003FC3F", "true 1,9"},
	{`\p{Jurchen}`, "\U000191D3", "false -"},
	{`\p{Jurchen}`, "\U00018E00", "true 0,4"},
	{`\p{Proto_Cuneiform}`, "\U00012550\U000125A8", "true 4,8"},
	{`\p{Cuneiform}+`, "\U00012550\U000125A8", "true 0,4"},
	{`\p{Assigned}`, "\U0003D000", "true 0,4"},
	{`\p{Cn}`, "\U0003D000", "false -"},
	{`\pC`, "\U0003FC40", "true 0,4"},
}

const rxUcdProgram = `import std.regex

const Pats: []str = %s
const Ins: []str = %s

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    for i in 0..Pats.Len() {
        re := regex.Compile(sys.arena, Pats[i]) else {
            try Print(mut out, "err\n")
            continue
        }
        s := Ins[i]
        m := re.IsMatch(s)
        try Print(mut out, "{m}", (m))
        if f := re.Find(s) {
            lo := f.lo
            hi := f.hi
            try Print(mut out, " {lo},{hi}\n", (lo, hi))
        } else {
            try Print(mut out, " -\n")
        }
    }
}
`

// TestSTDREGEX_Ucd18Cases runs the hand cases, in a release build. Each
// expectation is read off the UCD files: U+1DF95 folds to U+00DF
// (CaseFolding.txt, status S), U+0277 and U+A7DD are a case pair, and Seal,
// Jurchen and Proto_Cuneiform are scripts new in 18.0.0 (Proto_Cuneiform
// took U+125A8..U+1264B from Cuneiform).
func TestSTDREGEX_Ucd18Cases(t *testing.T) {
	var pats, ins, want []string
	for _, c := range rxUcdCases {
		pats = append(pats, c.pat)
		ins = append(ins, c.in)
		want = append(want, c.want)
	}
	exe := regexBuild(t, fmt.Sprintf(rxUcdProgram, rxStrs(pats), rxStrs(ins)), "-release")
	got := strings.Split(strings.TrimSuffix(regexExec(t, exe), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("%d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%q on %q: got %q, want %q", pats[i], ins[i], got[i], want[i])
		}
	}
}
