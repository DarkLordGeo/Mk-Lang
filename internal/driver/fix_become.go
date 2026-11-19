// Why: two changes of release 1.3 break 1.2 code in ways a tool can
// repair: `become` became a keyword, and std.core's StackSize gained a
// variant.
//
// What: the `mk fix` rounds becomeFixes and becomeHoleFixes (with
// becomeStatement, becomeHoles and the hole fact helpers) and
// stackSizeFixes (with namesBothSizes, e0306At and underscoreArm).
//
// How:
//
//   - `become` is a keyword from 1.3: every identifier spelled
//     become in a package file is renamed become_. A become that reads as
//     the statement, at a statement's start and followed by its call's
//     operand, is the keyword and stays.
//   - StackSize gains Bytes in 1.3: a user match over std.core's
//     StackSize that names both 1.2
//     variants and has no `_` arm gains one that panics, so it stays
//     exhaustive. It runs where the match is E0306 because the enum has a
//     member past Default and Large.
//
// Both are rewrite rounds of the fix loop (fix.go): their candidates join
// the proven fixes while the package has diagnostics, and the loop keeps
// them only when its recompile shows progress.

package driver

import (
	"strings"

	"mk/internal/ast"
	checker "mk/internal/check"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// becomeWhy is the AppliedFix reason of the rename.
const becomeWhy = "become is a keyword from 1.3: a name spelled become is become_"

// becomeFixes are the rename's candidates, one per package file that spells
// become as a name.
func (s *Session) becomeFixes(res Result, ov *overlay) []fixCand {
	if res.Sources == nil {
		return nil
	}
	var out []fixCand
	for _, f := range res.Sources.Files() {
		path := s.osPath(res.Sources, f.ID, ov)
		if path == "" || !strings.Contains(f.Text, "become") {
			continue
		}
		toks, _ := syntax.Lex(f.ID, f.Text)
		var edits []diag.Edit
		for i, tk := range toks {
			if tk.Kind == ast.TokBecome && !becomeStatement(toks, i) {
				edits = append(edits, diag.Edit{Span: tk.Span(f.ID), Text: "become_"})
			}
		}
		if len(edits) == 0 {
			continue
		}
		out = append(out, fixCand{path: path, edits: edits, applied: AppliedFix{Code: diag.E0102, At: posText(res, edits[0].Span), Why: becomeWhy}})
	}
	return append(out, s.becomeHoleFixes(res, ov)...)
}

// becomeHoleWhy is the AppliedFix reason of the follow-up on holes.
const becomeHoleWhy = "a hole named become names the field or operand renamed become_"

// becomeHoleFixes follow the rename into text it cannot reach: a format
// string whose hole names a record field `become` (E0706, the record's
// field, or the shorthand's name, now become_), or a path through a
// struct field `become` (`{r.become}`, now become_), and an asm template whose
// hole names an operand passing `become` (E1104 hole, the operand now
// become_). Only the string a diagnostic points at is edited, and in it
// only the hole path segments spelled become (no 1.3 name is, it being a
// keyword); `{{`, escapes and literal text are kept. The fix loop's
// recompile proves each.
func (s *Session) becomeHoleFixes(res Result, ov *overlay) []fixCand {
	if res.Sources == nil {
		return nil
	}
	var out []fixCand
	for _, e := range res.Report.Diags {
		d := e.Diag
		switch {
		case d.Code == diag.E0706 && becomeHoleProblem(d):
		case d.Code == diag.E1104 && holeFactText(d, "reason") == "hole" && holeFactText(d, "operand") == "become":
		default:
			continue
		}
		sf := res.Sources.File(d.At.File)
		if sf == nil || int(d.At.End) > len(sf.Text) || d.At.Start >= d.At.End {
			continue
		}
		path := s.osPath(res.Sources, d.At.File, ov)
		if path == "" {
			continue
		}
		var edits []diag.Edit
		for _, off := range becomeHoles(sf.Text[d.At.Start:d.At.End]) {
			at := d.At.Start + uint32(off)
			edits = append(edits, diag.Edit{Span: source.Span{File: d.At.File, Start: at, End: at + uint32(len("become"))}, Text: "become_"})
		}
		if len(edits) > 0 {
			out = append(out, fixCand{path: path, edits: edits, applied: AppliedFix{Code: d.Code, At: posText(res, d.At), Why: becomeHoleWhy}, d: d})
		}
	}
	return out
}

// becomeHoleProblem reports whether E0706's problem is a hole path with a
// segment spelled become: a later segment (`{r.become}`, a field no 1.3
// struct can have), or the first when the record has the field become_.
func becomeHoleProblem(d *diag.Diagnostic) bool {
	problem := holeFactText(d, "problem")
	i := strings.LastIndex(problem, ": ")
	if i < 0 {
		return false
	}
	for k, seg := range strings.Split(problem[i+2:], ".") {
		if seg == "become" && (k > 0 || holeFactHas(d, "fields", "become_")) {
			return true
		}
	}
	return false
}

// becomeHoles are the offsets in lit, a string literal's source text, of
// the hole path segments spelled become: in `{become}`, `{become:x}` and
// `{r.become}`, not in `{{become}}` nor inside an escape such as `\u{62}`.
func becomeHoles(lit string) []int {
	var out []int
	for i := 0; i < len(lit); i++ {
		switch lit[i] {
		case '\\':
			if i+2 < len(lit) && lit[i+1] == 'u' && lit[i+2] == '{' {
				if j := strings.IndexByte(lit[i:], '}'); j >= 0 {
					i += j
					continue
				}
			}
			i++
		case '{':
			if i+1 < len(lit) && lit[i+1] == '{' {
				i++
				continue
			}
			j := i + 1
			for j < len(lit) && (lit[j] == '_' || lit[j] == '.' || lit[j] >= 'a' && lit[j] <= 'z' || lit[j] >= 'A' && lit[j] <= 'Z' || lit[j] >= '0' && lit[j] <= '9') {
				j++
			}
			if j >= len(lit) || lit[j] != '}' && lit[j] != ':' {
				continue
			}
			at := i + 1
			for _, seg := range strings.Split(lit[i+1:j], ".") {
				if seg == "become" {
					out = append(out, at)
				}
				at += len(seg) + 1
			}
			i = j
		case '}':
			if i+1 < len(lit) && lit[i+1] == '}' {
				i++
			}
		}
	}
	return out
}

// holeFactText is d's text fact key, "" when absent.
func holeFactText(d *diag.Diagnostic, key string) string {
	for _, f := range d.Facts {
		if f.Key == key && f.Value.Kind == diag.FactText {
			return f.Value.Str
		}
	}
	return ""
}

// holeFactHas reports whether d's list fact key holds the text s.
func holeFactHas(d *diag.Diagnostic, key, s string) bool {
	for _, f := range d.Facts {
		if f.Key == key {
			for _, v := range f.Value.List {
				if v.Kind == diag.FactText && v.Str == s {
					return true
				}
			}
		}
	}
	return false
}

// becomeStatement reports whether toks[i], the word become, starts a become
// statement: the first token of a statement (after a statement's end, a
// `{` or a `;`) followed on its line by a name or a parenthesized operand
// set apart by a space. Anything else (a name declared, assigned, read,
// called as `become(x)`, a field or a label) was a 1.2 identifier.
func becomeStatement(toks []ast.Token, i int) bool {
	sig := func(j, step int) int {
		for j += step; j >= 0 && j < len(toks); j += step {
			switch toks[j].Kind {
			case ast.TokWhitespace, ast.TokComment, ast.TokDocComment:
				continue
			}
			return j
		}
		return -1
	}
	p := sig(i, -1)
	if p >= 0 {
		switch toks[p].Kind {
		case ast.TokTerm, ast.TokNewline, ast.TokLBrace, ast.TokSemi:
		default:
			return false
		}
	}
	n := sig(i, 1)
	if n < 0 {
		return false
	}
	switch toks[n].Kind {
	case ast.TokIdent:
		return true
	case ast.TokLParen:
		return toks[n].Start > toks[i].End() // `become (f)(x)`, not 1.2's call `become(x)`
	}
	return false
}

// stackSizeWhy is the AppliedFix reason of the StackSize round.
const stackSizeWhy = "StackSize gains Bytes in 1.3: a match naming Default and Large gains a `_` arm"

// stackSizeFixes are the StackSize round's candidates: one edit per user
// match over std.core.StackSize that is E0306, names Default and Large, and
// has no arm that matches every value.
func (s *Session) stackSizeFixes(res Result, ov *overlay) []fixCand {
	if s == nil || s.w == nil || s.w.in == nil || s.w.tab == nil || s.w.items == nil {
		return nil
	}
	key := types.ItemKey("std.core", "StackSize")
	var out []fixCand
	s.unitBodies(ov, res, func(b *checker.Body, path, text string) {
		ast.Inspect(b.Node, func(n ast.Node) bool {
			if _, lit := n.(*ast.FnLit); lit && n != b.Node {
				return false // a literal is a unit of its own
			}
			m, ok := n.(*ast.MatchStmt)
			if !ok || m.X == nil {
				return true
			}
			t := b.TypeOf(m.X.ID())
			for s.w.tab.Kind(t) == types.KRef || s.w.tab.Kind(t) == types.KMut {
				t = s.w.tab.Get(t).Elem
			}
			ty := s.w.tab.Get(t)
			if ty.Kind != types.KNamed || s.w.items.Get(ty.Item).Key != key || !namesBothSizes(m, s.w.in.Str) {
				return true
			}
			if !e0306At(res, m) {
				return true
			}
			if ed, ok := underscoreArm(text, m); ok {
				ed.Span.File = m.Span().File
				out = append(out, fixCand{path: path, edits: []diag.Edit{ed}, applied: AppliedFix{Code: diag.E0306, At: posText(res, m.Span()), Why: stackSizeWhy}})
			}
			return true
		})
	})
	return out
}

// namesBothSizes reports whether m's arms name StackSize's 1.2 variants,
// Default and Large, and none matches every value.
func namesBothSizes(m *ast.MatchStmt, str func(source.Symbol) string) bool {
	seen := map[string]bool{}
	var walk func(p ast.Pattern) bool
	walk = func(p ast.Pattern) bool {
		switch p := p.(type) {
		case *ast.WildcardPattern, *ast.NamePattern:
			return false
		case *ast.VariantPattern:
			if p.Name != nil {
				seen[str(p.Name.Name)] = true
			}
		case *ast.PathPattern:
			if p.Path != nil && len(p.Path.Names) > 0 {
				seen[str(p.Path.Names[len(p.Path.Names)-1].Name)] = true
			}
		case *ast.AltPattern:
			for _, a := range p.Alts {
				if !walk(a) {
					return false
				}
			}
		}
		return true
	}
	for _, a := range m.Arms {
		if a == nil || a.Pat == nil || !walk(a.Pat) {
			return false
		}
	}
	return seen["Default"] && seen["Large"]
}

// e0306At reports whether the report has E0306 at match m.
func e0306At(res Result, m *ast.MatchStmt) bool {
	sp := m.Span()
	for _, e := range res.Report.Diags {
		if d := e.Diag; d.Code == diag.E0306 && d.At.File == sp.File && d.At.Start >= sp.Start && d.At.Start < sp.End {
			return true
		}
	}
	return false
}

// underscoreArm is the edit adding `_ { Panic(...) }` as m's last arm, on
// lines of its own one level past the indentation of the match's line; a
// closing brace that ends the last arm's line moves to a line of its own,
// as check's futureArm writes the open-set arm.
func underscoreArm(text string, m *ast.MatchStmt) (diag.Edit, bool) {
	sp := m.Span()
	brace := int(sp.End) - 1
	if brace < 0 || brace >= len(text) || text[brace] != '}' {
		return diag.Edit{}, false
	}
	line := strings.LastIndexByte(text[:sp.Start], '\n') + 1
	indent := 0
	for line+indent < len(text) && text[line+indent] == ' ' {
		indent++
	}
	pad := strings.Repeat(" ", indent)
	arm := pad + "    _ {\n" + pad + "        Panic(\"StackSize gained a member this match does not handle\")\n" + pad + "    }\n"
	bl := strings.LastIndexByte(text[:brace], '\n') + 1
	if strings.TrimLeft(text[bl:brace], " \t") == "" {
		return diag.Edit{Span: source.Span{Start: uint32(bl), End: uint32(bl)}, Text: arm}, true // the brace starts its line
	}
	at := brace
	for at > 0 && (text[at-1] == ' ' || text[at-1] == '\t') {
		at--
	}
	return diag.Edit{Span: source.Span{Start: uint32(at), End: uint32(brace)}, Text: "\n" + arm + pad}, true
}
