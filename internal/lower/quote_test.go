// Why: a quote lowers to template bytes that gen decodes, so both sides
// must agree on the bytes.
// What: TestQuoteLowering, TestQuoteTemplateGolden, decodeTemplate.
// How: checks the CallBuiltin BuiltinQuote shape, and pins the template
// bytes by testdata/quote_template.golden (the bytes in hex), which gen's
// TestQuoteTemplateGolden reads too.

package lower

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/syntax"
	"mk/internal/types"
)

const quoteSrc = `import std.core

Derive(g: mut core.Gen, t: core.Type) {
	n := t.Name()
	g.Emit(quote {
		impl Eq for $n {
			Eq(&self, other: &$(t.Name())): bool {
				return x == 1_0 && "s" != r"r"
			}
		}
	})
}
`

// quoteEdited is quoteSrc with whitespace and comment edits inside the
// quote only.
const quoteEdited = `import std.core

Derive(g: mut core.Gen, t: core.Type) {
	n := t.Name()
	g.Emit(quote {
		impl   Eq for $n {      // a comment
			Eq(&self,   other: &$( t.Name() )): bool {
				return x ==  1_0 && "s" != r"r" /// doc
			}
		}
	})
}
`

// decodeTemplate reads a template back as one line per token.
func decodeTemplate(t *testing.T, b []byte) []string {
	t.Helper()
	if !bytes.HasPrefix(b, []byte(quoteMagic)) {
		t.Fatalf("no magic: %q", b)
	}
	b = b[len(quoteMagic):]
	next := func() uint64 {
		v, n := binary.Uvarint(b)
		if n <= 0 {
			t.Fatalf("bad uvarint")
		}
		b = b[n:]
		return v
	}
	count := next()
	var out []string
	for i := uint64(0); i < count; i++ {
		k := ast.TokenKind(next())
		switch {
		case k == ast.TokDollar:
			out = append(out, fmt.Sprintf("hole %d", next()))
		case quoteText(k):
			n := next()
			out = append(out, fmt.Sprintf("%v %q", k, b[:n]))
			b = b[n:]
		default:
			out = append(out, k.String())
		}
	}
	if len(b) != 0 {
		t.Fatalf("%d bytes after the last token", len(b))
	}
	return out
}

// firstQuote parses src and returns its file and first quote.
func firstQuote(t *testing.T, w *world, src string) (*ast.File, string, *ast.QuoteExpr) {
	t.Helper()
	sf := w.sm.Add(fmt.Sprintf("q%d.mk", w.sm.Len()), []byte(src))
	f, ds := syntax.ParseFile(sf, w.in, syntax.Options{})
	if len(ds) > 0 {
		t.Fatalf("parse: %v", ds[0].Message)
	}
	var q *ast.QuoteExpr
	ast.Inspect(f, func(n ast.Node) bool {
		if x, ok := n.(*ast.QuoteExpr); ok && q == nil {
			q = x
		}
		return q == nil
	})
	if q == nil {
		t.Fatalf("no quote")
	}
	return f, sf.Text, q
}

func TestQuoteTemplateGolden(t *testing.T) {
	w := newWorld(t, false)
	f, text, q := firstQuote(t, w, quoteSrc)
	got := quoteTemplate(f, text, q)
	// The golden is the bytes in hex on one line, so no checkout can turn
	// a 0x0a byte (TokInt's kind) into a line ending.
	path := filepath.Join("testdata", "quote_template.golden")
	if *update {
		if err := os.WriteFile(path, []byte(hex.EncodeToString(got)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	want, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("template differs from %s:\ngot  %s\nwant %s", path, strings.Join(decodeTemplate(t, got), " | "),
			strings.Join(decodeTemplate(t, want), " | "))
	}
	toks := decodeTemplate(t, got)
	joined := strings.Join(toks, " | ")
	for _, frag := range []string{`"impl" | Ident "Eq" | "for" | hole 0 | "{"`, `"&" | hole 1 | ")" | ":" | Ident "bool"`,
		`Int "1_0"`, `Str "\"s\""`, `RawStr "r\"r\""`, "\n"} {
		if frag == "\n" {
			if !strings.Contains(joined, "Newline") && !strings.Contains(joined, `"\n"`) {
				t.Fatalf("no newline token kept: %s", joined)
			}
			continue
		}
		if !strings.Contains(joined, frag) {
			t.Fatalf("missing %q in %s", frag, joined)
		}
	}
	for _, tok := range toks {
		if strings.Contains(tok, "Whitespace") || strings.Contains(tok, "Comment") || tok == ast.TokTerm.String() {
			t.Fatalf("trivia or a terminator in the template: %s", joined)
		}
	}
	// Whitespace and comment edits inside a quote leave
	// the template unchanged.
	f2, text2, q2 := firstQuote(t, w, quoteEdited)
	if got2 := quoteTemplate(f2, text2, q2); !bytes.Equal(got, got2) {
		t.Fatalf("an edit of whitespace and comments changed the template:\n%s\n%s", joined,
			strings.Join(decodeTemplate(t, got2), " | "))
	}
}

func TestQuoteLowering(t *testing.T) {
	l := lowerSrc(t, quoteSrc)
	b := l.body("Derive")
	f := l.print("Derive")
	t.Log(f)
	var calls []mir.Terminator
	for _, bk := range b.Blocks {
		if bk.Term.Kind == mir.TermCall && bk.Term.Callee.Kind == mir.CallBuiltin && bk.Term.Callee.Builtin == mir.BuiltinQuote {
			calls = append(calls, bk.Term)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("want one BuiltinQuote call, got %d:\n%s", len(calls), f)
	}
	c := calls[0]
	args := b.Args[c.Args.Start:c.Args.End()]
	if len(args) != 3 {
		t.Fatalf("want the template and 2 splices, got %d args", len(args))
	}
	tmpl := args[0].Operand
	cv := l.w.consts.Get(tmpl.Const)
	if tmpl.Kind != mir.OpConst || cv.Type != l.w.tab.Slice(types.U8) || len(cv.Relocs) != 1 {
		t.Fatalf("template is not a []u8 const with its data: %+v", tmpl)
	}
	data := l.w.consts.Get(cv.Relocs[0].Data).Bytes
	if !bytes.HasPrefix(data, []byte(quoteMagic)) {
		t.Fatalf("template data does not start with %q", quoteMagic)
	}
	if got := l.w.tab.TypeString(b.PlaceType(c.Dest), nil); !strings.Contains(got, "Code") {
		t.Fatalf("dest is %s, want Code", got)
	}
	// The splices in source order: n, then t.Name()'s result.
	if args[1].Operand.Place.Local == args[2].Operand.Place.Local {
		t.Fatalf("both splices are one local")
	}
	wantIn(t, f, "call std.core.Type:Name(", "call std.core.Type:Name(", "call builtin Quote(const []u8", "Emit(")
}
