// Why: std.regex follows Go's regexp syntax and semantics, so Go's regexp
// is the model to test it against.
// What: syntax parity (which patterns compile) and behaviour (IsMatch,
// Find, Group, FindAll, Replace) on generated and hand-picked cases.
// How: generated patterns and inputs, built in dev and release, compared
// line by line with Go's output.

package std

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// rxTmpl is the Replace template of the behaviour test: numbered groups,
// a named one, $$, the whole match, a reference to no group ($2x is the
// name "2x"), a $ that starts no reference, and names that run on into a
// non-ASCII letter or digit ($1\u00e9 is the name "1\u00e9", as Go's
// extract reads it with unicode.IsLetter and IsDigit). rxTmplNoRef names
// no group, so Replace makes no capture pass for it.
const rxTmpl = "[$1|${2}|$3$4|${n}|$$|$0|$2x|$|$1\u00e9|$n\u0661|${1}\u00e9]"
const rxTmplNoRef = "<$$|${|$>"

// rxBehaviour is the Mk side of the behaviour test; Go fills in the
// patterns, their group counts and K inputs per pattern.
const rxBehaviour = `import std.regex

const Pats: []str = %s
const Groups: []int = %s
const Ins: []str = %s
const K: int = %d
const Tmpl: str = %s

Hex(out: mut Writer, s: str): WriteError!void {
    for c in s.AsBytes() {
        if c < 16 {
            try Print(out, "0")
        }
        try Print(out, "{c:x}", (c))
    }
}

Case(out: mut Writer, a: &Arena, re: &regex.Regex, g: int, i: int, j: int, s: str): OutOfMemory|WriteError!void {
    m := re.IsMatch(s)
    try Print(out, "P{i} I{j} {m}", (i, j, m))
    if f := re.Find(s) {
        lo := f.lo
        hi := f.hi
        try Print(out, " {lo},{hi}", (lo, hi))
        for k in 1..g + 1 {
            try Print(out, " g{k}=", (k))
            if t := re.Group(s, f, k) {
                try Print(out, ":")
                try Hex(out, t)
            } else {
                try Print(out, "-")
            }
        }
    } else {
        try Print(out, " -")
    }
    all := try re.FindAll(a, s)
    try Print(out, " all=")
    for x in &all {
        lo := x.lo
        hi := x.hi
        try Print(out, "{lo}-{hi}", (lo, hi))
        // RX-6: Group on every match FindAll gives, not only the first.
        for k in 1..g + 1 {
            if t := re.Group(s, *x, k) {
                try Print(out, "/")
                try Hex(out, t)
            } else {
                try Print(out, "/-")
            }
        }
        try Print(out, ";")
    }
    r := try re.Replace(a, s, Tmpl)
    try Print(out, " r=")
    try Hex(out, r)
    r2 := try re.Replace(a, s, "<$$|${|$>")
    try Print(out, " r2=")
    try Hex(out, r2)
    try Print(out, "\n")
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    for i in 0..Pats.Len() {
        re := regex.Compile(sys.arena, Pats[i]) else {
            try Print(mut out, "P{i} err\n", (i))
            continue
        }
        for j in 0..K {
            try Case(mut out, sys.arena, &re, Groups[i], i, j, Ins[i * K + j])
        }
    }
}
`

// rxGen generates patterns from the syntax std.regex supports.
type rxGen struct {
	r *rand.Rand
}

var (
	rxLits    = []string{"a", "b", "c", "x", "-", " ", `\.`, `\n`, "é", "日", "ab", `\x41`, `\Qa.\E`, "A", "K", "s", "σ", "Σ", "ǅ", "ǈ", "к", "Ω"}
	rxClasses = []string{"[abc]", "[^ab]", "[a-c]", `[^\n]`, "[é日]", "[^é]", "[[:alpha:]]", "[[:^digit:]]", `\d`, `\w`, `\s`, `\W`, `\D`, ".", "[a-]", `[\d-]`, "[]a]", "[^]a]",
		"[[:upper:]]", "[k-s]", "[^K]", `[\x{17F}]`, "[α-ω]", "[^ǆ]", "[à-ÿ]",
		`\pL`, `\PL`, `\p{Lu}`, `\p{^Ll}`, `\P{^Lu}`, `\pN`, `\p{Greek}`, `\p{Han}`, `\p{Latin}`, `\p{Cased_Letter}`, `\p{lowercase letter}`, `[\p{Nd}x]`, `[^\p{Latin}\s]`, `[\PL\d]`, `\p{Any}`, `\p{ASCII}`}
	rxAsserts = []string{"^", "$", `\b`, `\B`, `\A`, `\z`}
	rxOps     = []string{"*", "+", "?", "*?", "+?", "??", "{2}", "{1,3}", "{0,2}", "{2,}", "{0}", "{1,2}?", "{0,}"}
	rxFlags   = []string{"(?i)", "(?m)", "(?s)", "(?U)", "(?im)", "(?ms)", "(?-s)", "(?i-m)"}
)

func (g *rxGen) pick(xs []string) string { return xs[g.r.Intn(len(xs))] }

func (g *rxGen) atom(d int) string {
	n := g.r.Intn(10)
	switch {
	case n < 4:
		return g.pick(rxLits)
	case n < 6:
		return g.pick(rxClasses)
	case n < 7:
		return g.pick(rxAsserts)
	}
	if d <= 0 {
		return "a"
	}
	inner := g.alt(d - 1)
	switch g.r.Intn(5) {
	case 0:
		return "(?:" + inner + ")"
	case 1:
		return "(?P<n>" + inner + ")"
	case 2:
		return "(?<m>" + inner + ")"
	}
	return "(" + inner + ")"
}

func (g *rxGen) piece(d int) string {
	a := g.atom(d)
	if g.r.Intn(3) == 0 {
		return a + g.pick(rxOps)
	}
	return a
}

func (g *rxGen) concat(d int) string {
	var b strings.Builder
	for n := 1 + g.r.Intn(3); n > 0; n-- {
		b.WriteString(g.piece(d))
	}
	return b.String()
}

func (g *rxGen) alt(d int) string {
	if g.r.Intn(4) == 0 {
		return g.concat(d) + "|" + g.concat(d)
	}
	return g.concat(d)
}

func (g *rxGen) pattern() string {
	flags := ""
	if g.r.Intn(4) == 0 {
		flags = g.pick(rxFlags)
	}
	body := g.alt(2)
	if g.r.Intn(2) == 0 {
		// A group around a piece of the pattern, so Group and $1 have text.
		body = "(" + g.concat(1) + ")" + body
	}
	return flags + body
}

var rxInputAlphabet = []string{"a", "b", "c", "x", "A", "K", "s", "K", "ſ", "é", "日", " ", "\n", "-", ".", "1", "_", "ab",
	"σ", "Σ", "ς", "ǅ", "Ǆ", "ǆ", "ǉ", "Ǉ", "к", "К", "Ω", "Ω", "ω", "١", "Ⅻ", "É", "ͅ", "ι"}

func rxInput(r *rand.Rand) string {
	var b strings.Builder
	for n := r.Intn(13); n > 0; n-- {
		b.WriteString(rxInputAlphabet[r.Intn(len(rxInputAlphabet))])
	}
	return b.String()
}

// rxCaseLine is the model's line for pattern i on input j, as Case prints.
func rxCaseLine(re *regexp.Regexp, i, j int, s string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "P%d I%d %v", i, j, re.MatchString(s))
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil {
		b.WriteString(" -")
	} else {
		fmt.Fprintf(&b, " %d,%d", loc[0], loc[1])
		for k := 1; k <= re.NumSubexp(); k++ {
			fmt.Fprintf(&b, " g%d=", k)
			if loc[2*k] < 0 {
				b.WriteString("-")
			} else {
				b.WriteString(":" + hex.EncodeToString([]byte(s[loc[2*k]:loc[2*k+1]])))
			}
		}
	}
	b.WriteString(" all=")
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		fmt.Fprintf(&b, "%d-%d", m[0], m[1])
		for k := 1; k <= re.NumSubexp(); k++ {
			if m[2*k] < 0 {
				b.WriteString("/-")
			} else {
				b.WriteString("/" + hex.EncodeToString([]byte(s[m[2*k]:m[2*k+1]])))
			}
		}
		b.WriteString(";")
	}
	b.WriteString(" r=" + hex.EncodeToString([]byte(re.ReplaceAllString(s, rxTmpl))))
	b.WriteString(" r2=" + hex.EncodeToString([]byte(re.ReplaceAllString(s, rxTmplNoRef))))
	return b.String()
}

const rxCases = 8

// rxBehaviourCorpus is the generated corpus: patterns, group counts,
// inputs (rxCases per pattern) and the model's output.
func rxBehaviourCorpus(seed int64, n int) (pats []string, groups []int, ins []string, want string) {
	r := rand.New(rand.NewSource(seed))
	g := &rxGen{r: r}
	var lines []string
	for len(pats) < n {
		p := g.pattern()
		re, err := regexp.Compile(p)
		if err != nil {
			continue
		}
		i := len(pats)
		pats = append(pats, p)
		groups = append(groups, re.NumSubexp())
		// The empty input, then inputs the model matches (up to 5 of 600
		// random tries), then the rest at random.
		cases := []string{""}
		for try := 0; try < 600 && len(cases) < 6; try++ {
			if s := rxInput(r); re.MatchString(s) {
				cases = append(cases, s)
			}
		}
		for len(cases) < rxCases {
			cases = append(cases, rxInput(r))
		}
		for j, s := range cases {
			ins = append(ins, s)
			lines = append(lines, rxCaseLine(re, i, j, s))
		}
	}
	return pats, groups, ins, strings.Join(lines, "\n") + "\n"
}

func rxBehaviourRun(t *testing.T, seed int64, n int, flags ...string) {
	pats, groups, ins, want := rxBehaviourCorpus(seed, n)
	src := fmt.Sprintf(rxBehaviour, rxStrs(pats), rxInts(groups), rxStrs(ins), rxCases, rxQuote(rxTmpl))
	exe := regexBuild(t, src, flags...)
	got := regexExec(t, exe)
	matched, grouped := strings.Count(want, " true "), strings.Count(want, "=:")
	t.Logf("%d patterns, %d cases, %d matching, %d group texts", len(pats), len(ins), matched, grouped)
	if matched < len(ins)/4 || grouped < len(ins)/8 {
		t.Errorf("the corpus is too weak: %d matching cases, %d group texts", matched, grouped)
	}
	regexCompare(t, got, want, func(line string) string {
		var i, j int
		if _, err := fmt.Sscanf(line, "P%d I%d", &i, &j); err != nil || i >= len(pats) {
			return "?"
		}
		return fmt.Sprintf("pattern %q, input %q", pats[i], ins[i*rxCases+j])
	})
}

// TestSTDREGEX_DifferentialDev: 400 generated patterns, 8 inputs each,
// every API call against Go's regexp, in a dev build.
func TestSTDREGEX_DifferentialDev(t *testing.T) {
	rxBehaviourRun(t, 1, 400)
}

// TestSTDREGEX_DifferentialRelease is the same corpus, another seed, in a
// release build.
func TestSTDREGEX_DifferentialRelease(t *testing.T) {
	rxBehaviourRun(t, 2, 400, "-release")
}

// rxSyntax is the Mk side of the syntax parity test.
const rxSyntax = `import std.regex

const Pats: []str = %s

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for i in 0..Pats.Len() {
        r := regex.Compile(sys.arena, Pats[i]) else e {
            match e {
                regex.Syntax(offset) {
                    try Print(mut out, "{i} syntax {offset}\n", (i, offset))
                }
                regex.TooLarge {
                    try Print(mut out, "{i} large\n", (i))
                }
                OutOfMemory {
                    try Print(mut out, "{i} oom\n", (i))
                }
                _ {
                    Panic("regex.Error gained a member this match does not handle")
                }
            }
            continue
        }
        _ = r
        try Print(mut out, "{i} ok\n", (i))
    }
}
`

var rxSyntaxTokens = []string{"a", "b", "1", "0", "9", ",", "-", ":", "<", ">", "^", "$", ".", "*", "+", "?", "|", "(", ")", "[", "]", "{", "}", `\`, "P", "i", "m", "s", "U", "x", "_", "=", "!", "(?", "(?P<", "(?<", "[:", ":]", `\d`, `\x`, `\Q`, `\E`, "{2}", "{1,3}", "{,2}", "é", `\b`, `\8`, `\0`, "[^", "{1001}", "]", "(?i)", "alpha", "{0", "K",
	`\p`, `\P`, "L", "N", "{L}", "{Lu}", "{^Greek}", "{Greek", "Han}", "{lowercase_letter}", "{ Any }", "{Bogus}", "{Latn}", "σ", "ǅ"}

// TestSTDREGEX_SyntaxParity: Compile accepts exactly what Go's
// regexp.Compile accepts, on 4,000 random token strings, \p{..} and (?i)
// beside non-ASCII scalars included.
func TestSTDREGEX_SyntaxParity(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	var pats []string
	seen := map[string]bool{}
	for len(pats) < 4000 {
		var b strings.Builder
		for n := 1 + r.Intn(7); n > 0; n-- {
			b.WriteString(rxSyntaxTokens[r.Intn(len(rxSyntaxTokens))])
		}
		p := b.String()
		if !seen[p] {
			seen[p] = true
			pats = append(pats, p)
		}
	}
	exe := regexBuild(t, fmt.Sprintf(rxSyntax, rxStrs(pats)))
	got := strings.Split(strings.TrimSuffix(regexExec(t, exe), "\n"), "\n")
	if len(got) != len(pats) {
		t.Fatalf("%d lines for %d patterns", len(got), len(pats))
	}
	accepted, bad := 0, 0
	for i, p := range pats {
		_, err := regexp.Compile(p)
		f := strings.Fields(got[i])
		mk := f[1]
		switch {
		case err == nil && mk == "ok":
			accepted++
		case err != nil && mk != "ok":
		default:
			bad++
			if bad <= 20 {
				t.Errorf("%q: Go %v, Mk %s", p, err, got[i])
			}
		}
	}
	t.Logf("%d patterns: %d accepted by both, %d refused by both, %d differ", len(pats), accepted, len(pats)-accepted-bad, bad)
	if accepted < 300 {
		t.Errorf("only %d patterns accepted by both: the generator is too weak", accepted)
	}
}

// rxEdge are hand cases: the pattern, the input, and the line Case prints
// (computed by the model).
var rxEdgePats = []string{
	``, `a*`, `(a*)*`, `(a|ab)(c|bcd)(d*)`, `(?m)^b$`, `(?m)$`, `\b`, `\B`, `x*`, `(x*)`, `é*`, `(?s).`, `.`,
	`a*?`, `(a+?)(a*)`, `(?U)(a+)(a*)`, `(?i)k`, `(?i)s+`, `(?i)[k]`, `(?i)\W`, `[[:word:]]+`, `\Q.*\E+`,
	`(?:(a)|b)+`, `(a)|(b)`, `^$`, `\A\z`, `a{2,3}?`, `(?:a{0,2}){2}`, `(|a)*`, `(|a)+`, `()`, `(?P<n>.)(?P<n>.)`,
	`\x{65e5}+`, `[^\x00-\x{10FFFF}]`, `[\x{D7FF}-\x{E000}]`, `.{3}`, `\d+\.\d+`, `(\w+)\s(\w+)`,
	// Over the DFA's budget (Pike only), and assertions the DFA decides
	// from its context flags, at later starts of FindAll and Replace.
	`(a|b)*a(a|b){12}`, `(a|b){12}a(a|b)*`, `[a-q][^u-z]{13}x`, `\bab?`, `b\b`, `(?m)^[a-c]|\B\d`, `(?m)a$|^$`, `(?s).\z`, `\B..\b`,
	`(?m)$\n?`, `(?i)k\b|\bS`,
	// The prefilter: a prefix whose tail fails, so the scan returns to a
	// start state and skips again; overlapping occurrences of the prefix.
	`ab(c|d)e`, `aab`, `(ab)+c`, `éé?`,
	// Groups of FindAll's later matches, whose assertions look at the
	// bytes before the match; the backtracker's hand-offs to the Pike VM.
	`\b(a)`, `(?m)^(b)`, `(\B)(a)`, `(?m)(a)$`, `(a)(?:bc)?`, `(a)*`,
}

var rxEdgeIns = []string{"", "a", "aaa", "abcd", "a\nb\nc", "b\n", "日本", "ab ab", "kKK", "sſS", "1.5 x", "aab", ".*.*", "퟿", "abxabxabce", "aaaab", "abababc", "éaéé",
	"xa a", "a\nb", "aa\nba", "abd abc a"}

// TestSTDREGEX_EdgeCases: every hand pattern on every hand input, against
// the model.
func TestSTDREGEX_EdgeCases(t *testing.T) {
	var pats, ins []string
	var groups []int
	var lines []string
	for _, p := range rxEdgePats {
		re := regexp.MustCompile(p)
		i := len(pats)
		pats = append(pats, p)
		groups = append(groups, re.NumSubexp())
		for j, s := range rxEdgeIns {
			ins = append(ins, s)
			lines = append(lines, rxCaseLine(re, i, j, s))
		}
	}
	src := fmt.Sprintf(rxBehaviour, rxStrs(pats), rxInts(groups), rxStrs(ins), len(rxEdgeIns), rxQuote(rxTmpl))
	exe := regexBuild(t, src)
	got := regexExec(t, exe)
	regexCompare(t, got, strings.Join(lines, "\n")+"\n", func(line string) string {
		var i, j int
		if _, err := fmt.Sscanf(line, "P%d I%d", &i, &j); err != nil || i >= len(pats) {
			return "?"
		}
		return fmt.Sprintf("pattern %q, input %q", pats[i], ins[i*len(rxEdgeIns)+j])
	})
}
