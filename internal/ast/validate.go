// Why: every pass relies on the parser's tree being total and its token
// list lossless; a check that states this once catches parser bugs early.
//
// What: Validate (the token and tree checks) and PrintBack.
//
// How: one pass over the tokens and one walk over the tree; the first
// violation is returned. The syntax tests and the corpus in debug builds run
// Validate on every parsed file.

package ast

import (
	"fmt"
	"strings"
)

// Validate checks a parsed file against its normalized text:
//
//   - T1 the tokens cover text exactly: the first starts at 0, each starts where
//     the previous ended, only Term and EOF are empty, and the last is EOF at
//     len(text);
//   - T2 printing every token's text gives text back;
//   - every node has a distinct NodeID in 1..NodeCount, the File included;
//   - every node's span is in f's file and inside the text, and every child's
//     non-empty span lies inside its parent's span, children in source order;
//   - a FromFlow holds exactly one of Sources and Fields non-empty.
//
// It returns the first violation found, naming it; nil when the file is well
// formed. Callers turn an error into an ICE.
func Validate(f *File, text string) error {
	if err := validateTokens(f.Tokens, text); err != nil {
		return err
	}
	file := f.Sp.File
	seen := make([]bool, f.NodeCount+1)
	var err error
	var check func(n, parent Node, prevEnd *uint32)
	check = func(n, parent Node, prevEnd *uint32) {
		if err != nil {
			return
		}
		id := n.ID()
		name, _ := Describe(n, nil)
		if id == NoNode || uint32(id) > f.NodeCount {
			err = fmt.Errorf("%s at %v: NodeID %d outside 1..%d", name, n.Span(), id, f.NodeCount)
			return
		}
		if seen[id] {
			err = fmt.Errorf("%s at %v: NodeID %d used twice", name, n.Span(), id)
			return
		}
		seen[id] = true
		if ff, ok := n.(*FromFlow); ok && (len(ff.Sources) == 0) == (len(ff.Fields) == 0) {
			err = fmt.Errorf("FromFlow at %v: %d sources and %d fields, want exactly one form", n.Span(), len(ff.Sources), len(ff.Fields))
			return
		}
		sp := n.Span()
		if sp.File != file || sp.Start > sp.End || sp.End > uint32(len(text)) {
			err = fmt.Errorf("%s: span %v outside file %d of length %d", name, sp, file, len(text))
			return
		}
		if parent != nil && !sp.IsEmpty() {
			ps := parent.Span()
			if sp.Start < ps.Start || sp.End > ps.End {
				pname, _ := Describe(parent, nil)
				err = fmt.Errorf("%s %v outside its parent %s %v", name, sp, pname, ps)
				return
			}
			if sp.Start < *prevEnd {
				err = fmt.Errorf("%s %v starts before its previous sibling ends at %d", name, sp, *prevEnd)
				return
			}
			*prevEnd = sp.End
		}
		var childEnd uint32
		if !sp.IsEmpty() {
			childEnd = sp.Start
		}
		EachChild(n, func(c Node) { check(c, n, &childEnd) })
	}
	var none uint32
	check(f, nil, &none)
	return err
}

func validateTokens(toks []Token, text string) error {
	if len(toks) == 0 {
		return fmt.Errorf("T1: no tokens (EOF is required)")
	}
	var pos uint32
	var b strings.Builder
	for i, t := range toks {
		if t.Start != pos {
			return fmt.Errorf("T1: token %d (%v) starts at %d, want %d", i, t.Kind, t.Start, pos)
		}
		if t.Kind == TokInvalid || t.Kind >= numTokenKinds {
			return fmt.Errorf("T1: token %d has kind %d", i, t.Kind)
		}
		empty := t.Kind == TokTerm || t.Kind == TokEOF
		if empty != (t.Len == 0) {
			return fmt.Errorf("T1: token %d (%v) has length %d", i, t.Kind, t.Len)
		}
		if t.Kind == TokEOF && i != len(toks)-1 {
			return fmt.Errorf("T1: EOF at token %d of %d", i, len(toks))
		}
		if t.End() > uint32(len(text)) {
			return fmt.Errorf("T1: token %d ends at %d past %d", i, t.End(), len(text))
		}
		if ft := t.Kind.FixedText(); ft != "" && t.Text(text) != ft {
			return fmt.Errorf("T1: token %d (%v) spells %q", i, t.Kind, t.Text(text))
		}
		b.WriteString(t.Text(text))
		pos = t.End()
	}
	if last := toks[len(toks)-1]; last.Kind != TokEOF || last.Start != uint32(len(text)) {
		return fmt.Errorf("T1: last token %v at %d, want EOF at %d", last.Kind, last.Start, len(text))
	}
	if b.String() != text {
		return fmt.Errorf("T2: tokens do not print the text back")
	}
	return nil
}

// PrintBack writes every token's text in order: the file's text, by T1 and T2.
func PrintBack(toks []Token, text string) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.Text(text))
	}
	return b.String()
}
