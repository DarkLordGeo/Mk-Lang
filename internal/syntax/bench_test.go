// Why: the lexer and parser sit on every build's critical path, so their
// speed per line is tracked.
// What: BenchmarkLex, BenchmarkParse and BenchmarkParseGenerated.
// How: lexes and parses the concatenated corpus and a generated file of plain
// code. Command: go test -run=^$ -bench=. -benchmem ./internal/syntax

package syntax

import (
	"strings"
	"testing"

	"mk/internal/source"
)

// corpusText is every design block and program, concatenated.
func corpusText(b *testing.B) (string, int) {
	var sb strings.Builder
	for _, blk := range append(docBlocks(b), programFiles(b)...) {
		sb.WriteString(blk.text)
		sb.WriteString("\n")
	}
	s := sb.String()
	return s, strings.Count(s, "\n")
}

// generated is a deterministic file of about n lines of plain code.
func generated(n int) string {
	var sb strings.Builder
	for i := 0; sb.Len() < n*24; i++ {
		id := itoa(i)
		sb.WriteString("struct P" + id + " {\n    x: int\n    y: int\n}\n\n")
		sb.WriteString("F" + id + "(xs: []int, p: &P" + id + "): int {\n")
		sb.WriteString("    var total := 0\n")
		sb.WriteString("    for i, x in xs {\n        if x > p.x && i < p.y {\n            total += x * 2\n        }\n    }\n")
		sb.WriteString("    match Classify(total) {\n        .Small { total += 1 }\n        .Big(n: n) { total -= n }\n    }\n")
		sb.WriteString("    return G(total, p.x) else { return 0 }\n}\n\n")
	}
	return sb.String()
}

func benchLex(b *testing.B, text string, lines int) {
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		Lex(1, text)
	}
	b.ReportMetric(float64(lines)*float64(b.N)/b.Elapsed().Seconds(), "lines/s")
}

func benchParse(b *testing.B, text string, lines int) {
	sm := source.NewSourceMap()
	src := sm.Add("bench.mk", []byte(text))
	in := source.NewInterner()
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		ParseFile(src, in, Options{AllowBodiless: true})
	}
	b.ReportMetric(float64(lines)*float64(b.N)/b.Elapsed().Seconds(), "lines/s")
}

func BenchmarkLex(b *testing.B) {
	text, lines := corpusText(b)
	benchLex(b, text, lines)
}

func BenchmarkParse(b *testing.B) {
	text, lines := corpusText(b)
	benchParse(b, text, lines)
}

func BenchmarkParseGenerated(b *testing.B) {
	text := generated(100_000)
	benchParse(b, text, strings.Count(text, "\n"))
}
