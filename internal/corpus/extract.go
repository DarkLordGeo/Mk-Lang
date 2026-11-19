// Why: every fenced mk block in the documentation is a test, so the corpus
// needs an exact, unforgiving reader of those blocks: a malformed block must
// stop the run, not pass unchecked.
//
// What: Verdict, Marker, Block and ExtractError, and Extract, which returns
// every block of one document with its verdict line and expected markers.
//
// How: a block opens and closes on exact fence lines at column 0; its first
// line is "// verdict: ok", "parse" or "reject" with codes. A "// <- E...."
// marker names the codes expected on its line; marker codes must be in the
// verdict and every verdict code needs a marker. Text outside blocks is prose.

package corpus

import (
	"fmt"
	"slices"
	"strings"

	"mk/internal/diag"
)

// fence is three backticks (U+0060), spelled so this file stays readable.
const fence = "\x60\x60\x60"

// openFence and closeFence are the exact fence lines after trailing spaces
// and tabs are trimmed. They must start at column 0.
const (
	openFence  = fence + "mk"
	closeFence = fence
)

// verdictPrefix opens every verdict line.
const verdictPrefix = "// verdict: "

// markerPrefix introduces a marker comment; its last occurrence on a line
// counts.
const markerPrefix = "// <- "

// Verdict is what a block or unit claims about itself (see doc/reference.md).
type Verdict uint8

const (
	VerdictNone   Verdict = iota // not a verdict; never on an extracted block
	VerdictOK                    // compiles with the test prelude
	VerdictParse                 // parses; names may be undefined
	VerdictReject                // fails with exactly the listed codes on the marked lines
)

var verdictNames = [...]string{"none", "ok", "parse", "reject"}

// String is the verdict's word as the design writes it: ok, parse, reject.
func (v Verdict) String() string {
	if int(v) < len(verdictNames) {
		return verdictNames[v]
	}
	return "verdict?"
}

// Marker is one expected diagnostic of a block: Line is 1-based within
// Block.Source (the first line after the verdict is line 1), so document line
// Block.Line+Line. A marker line naming several codes gives one Marker per
// code.
type Marker struct {
	Line int
	Code diag.Code
}

// Block is one fenced mk block of a design document. Invariants:
// Codes is non-empty iff Verdict is VerdictReject, and is sorted with no
// duplicates; every marker code is in Codes and every code of Codes has a
// marker; ok and parse blocks have no markers; Expected is sorted by (Line,
// Code) with no duplicates. Source is the body, each line LF-terminated,
// without the verdict line; marker comments stay in it.
type Block struct {
	Document string // repository-relative, "doc/reference.md"
	Line     int    // 1-based document line of the verdict line
	Verdict  Verdict
	Codes    []diag.Code
	Source   string
	Expected []Marker
}

// Key is the block's name in reports, the pending allowlist and --only:
// "doc/reference.md:17".
func (b *Block) Key() string { return fmt.Sprintf("%s:%d", b.Document, b.Line) }

// ExtractError is a malformed block. Any one stops the whole run.
type ExtractError struct {
	Document string
	Line     int // 1-based document line
	Msg      string
}

func (e *ExtractError) Error() string { return fmt.Sprintf("%s:%d: %s", e.Document, e.Line, e.Msg) }

// splitLines splits text on LF, strips one trailing CR from each line, and
// drops the empty piece after a final LF, as Rust's str::lines does.
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// Extract reads every mk block of one document. document is its
// repository-relative path, used in blocks and errors. A block opens on a
// line that is exactly three backticks and "mk" from column 0, trailing
// spaces and tabs allowed; an indented fence, a tilde fence or a fence with
// more text is prose. The next line is the verdict. The body runs to the next
// line that is exactly the closing fence from column 0. Text outside blocks,
// marker-like comments included, is prose.
func Extract(document, text string) ([]Block, error) {
	lines := splitLines(text)
	var out []Block
	for i := 0; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") != openFence {
			continue
		}
		b, next, err := extractBlock(document, lines, i)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
		i = next
	}
	return out, nil
}

// extractBlock reads the block whose opening fence is lines[open] (0-based)
// and returns it with the 0-based index of its closing fence.
func extractBlock(document string, lines []string, open int) (Block, int, error) {
	errAt := func(line int, format string, args ...any) error {
		return &ExtractError{Document: document, Line: line, Msg: fmt.Sprintf(format, args...)}
	}
	if open+1 >= len(lines) {
		return Block{}, 0, errAt(open+1, "empty block at end of file")
	}
	b := Block{Document: document, Line: open + 2}
	v, codes, err := parseVerdict(lines[open+1])
	if err != nil {
		return Block{}, 0, errAt(b.Line, "%v", err)
	}
	b.Verdict, b.Codes = v, codes
	var src strings.Builder
	end := -1
	for j := open + 2; j < len(lines); j++ {
		line := lines[j]
		if strings.TrimRight(line, " \t") == closeFence {
			end = j
			break
		}
		src.WriteString(line)
		src.WriteByte('\n')
		k := j - open - 1 // 1-based line within Source
		ms, err := parseMarker(line)
		if err != nil {
			return Block{}, 0, errAt(j+1, "%v", err)
		}
		for _, c := range ms {
			if b.Verdict != VerdictReject {
				return Block{}, 0, errAt(j+1, "marker %s in a block whose verdict is %s", c, b.Verdict)
			}
			if !slices.Contains(b.Codes, c) {
				return Block{}, 0, errAt(j+1, "marker %s is not in the verdict's codes", c)
			}
			b.Expected = append(b.Expected, Marker{Line: k, Code: c})
		}
	}
	if end < 0 {
		// A missing closing fence could consume the rest of the file; the
		// corpus refuses it instead, so a typo cannot swallow later blocks.
		return Block{}, 0, errAt(open+1, "block has no closing fence")
	}
	b.Source = src.String()
	slices.SortFunc(b.Expected, func(x, y Marker) int {
		if x.Line != y.Line {
			return x.Line - y.Line
		}
		return int(x.Code) - int(y.Code)
	})
	b.Expected = slices.Compact(b.Expected)
	for _, c := range b.Codes {
		if !slices.ContainsFunc(b.Expected, func(m Marker) bool { return m.Code == c }) {
			return Block{}, 0, errAt(b.Line, "verdict code %s has no marker in the block", c)
		}
	}
	return b, end, nil
}

// parseVerdict reads a verdict line: exactly "// verdict: " then
// "ok", "parse", or "reject" and one or more space-separated catalogue codes.
// The codes come back sorted; a duplicate or unknown code is an error. A
// reject verdict may name codes of different groups.
func parseVerdict(line string) (Verdict, []diag.Code, error) {
	rest, ok := strings.CutPrefix(line, verdictPrefix)
	if !ok {
		return VerdictNone, nil, fmt.Errorf("bad verdict line: %s", line)
	}
	rest = strings.TrimRight(rest, " \t")
	switch rest {
	case "ok":
		return VerdictOK, nil, nil
	case "parse":
		return VerdictParse, nil, nil
	}
	list, ok := strings.CutPrefix(rest, "reject ")
	words := strings.Fields(list)
	if !ok || len(words) == 0 {
		return VerdictNone, nil, fmt.Errorf("bad verdict line: %s", line)
	}
	var codes []diag.Code
	for _, w := range words {
		c, ok := diag.ParseCode(w)
		if !ok || c == diag.E0000 {
			return VerdictNone, nil, fmt.Errorf("unknown code %q in verdict", w)
		}
		if slices.Contains(codes, c) {
			return VerdictNone, nil, fmt.Errorf("code %s twice in verdict", c)
		}
		codes = append(codes, c)
	}
	slices.Sort(codes)
	return VerdictReject, codes, nil
}

// parseMarker returns the codes of the marker comment on a body line, or nil
// when the line has none. The last "// <- " counts; what follows it is one or
// more space-separated catalogue codes; anything else is an error.
func parseMarker(line string) ([]diag.Code, error) {
	i := strings.LastIndex(line, markerPrefix)
	if i < 0 {
		return nil, nil
	}
	words := strings.Fields(line[i+len(markerPrefix):])
	if len(words) == 0 {
		return nil, fmt.Errorf("marker with no code")
	}
	var out []diag.Code
	for _, w := range words {
		c, ok := diag.ParseCode(w)
		if !ok || c == diag.E0000 {
			return nil, fmt.Errorf("unknown code %q in marker", w)
		}
		out = append(out, c)
	}
	return out, nil
}
