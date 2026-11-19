// Why: formatting a selection must change only the selection, even when the
// formatter would also change text outside it.
//
// What: rangeFormatting and lineHunks.
//
// How: format.Source formats the whole buffer, a Myers line diff splits the
// change into hunks, and only the hunks inside the range become edits.

package lsp

import (
	"errors"
	"strings"

	"mk/internal/format"
)

type rangeFormattingParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

func (s *Server) rangeFormatting(p rangeFormattingParams) (any, *rpcError) {
	path, ok := uriPath(p.TextDocument.URI)
	if !ok {
		return nil, invalid(errors.New("not a file URI: " + p.TextDocument.URI))
	}
	raw, err := s.bufferText(path)
	if err != nil {
		return nil, invalid(err)
	}
	out, err := format.Source(path, raw)
	if err != nil || string(out) == string(raw) {
		return []textEdit{}, nil
	}
	// Lines of the client's text (CRLF kept in the line) and of the
	// formatted text; the formatter writes LF.
	old := strings.SplitAfter(string(raw), "\n")
	nu := strings.SplitAfter(string(out), "\n")
	from, to := p.Range.Start.Line, p.Range.End.Line
	if p.Range.End.Character == 0 && to > from {
		to-- // a range ending at a line's start does not take that line
	}
	eol := "\n"
	if strings.Contains(string(raw), "\r\n") {
		eol = "\r\n"
	}
	withEOL := func(ls []string) string {
		var b strings.Builder
		for _, l := range ls {
			b.WriteString(strings.TrimRight(l, "\r\n") + eol)
		}
		return b.String()
	}
	edits := []textEdit{}
	lineEdit := func(a, b int, text string) {
		edits = append(edits, textEdit{Range: Range{Start: Position{Line: a}, End: Position{Line: b}}, NewText: text})
	}
	for _, h := range lineHunks(old, nu) {
		oa, ob, na, nb := h[0], h[1], h[2], h[3]
		switch {
		case ob-oa == nb-na:
			// Line for line: edit each changed line inside the range.
			for k := 0; k < ob-oa; k++ {
				if l := oa + k; l >= from && l <= to && !sameLine(old[l], nu[na+k]) {
					lineEdit(l, l+1, withEOL(nu[na+k:na+k+1]))
				}
			}
		case oa >= from && ob <= to+1 && (ob > oa || oa <= to):
			// A hunk that adds or drops lines, wholly inside the range.
			lineEdit(oa, ob, withEOL(nu[na:nb]))
		}
	}
	return edits, nil
}

// sameLine compares two lines without their line ends.
func sameLine(a, b string) bool { return strings.TrimRight(a, "\r\n") == strings.TrimRight(b, "\r\n") }

// maxDiffEdits bounds the line diff's edit distance; past it the changed
// middle is one hunk (formatting a range then edits it only when the whole
// change lies inside).
const maxDiffEdits = 2000

// lineHunks are the changed regions between the lines old and nu, in
// order, each as [oa, ob, na, nb): old[oa:ob] became nu[na:nb]. Lines
// compare without their line ends. Myers' O(ND) diff over the lines left
// after the common prefix and suffix.
func lineHunks(old, nu []string) [][4]int {
	pre := 0
	for pre < len(old) && pre < len(nu) && sameLine(old[pre], nu[pre]) {
		pre++
	}
	suf := 0
	for suf < len(old)-pre && suf < len(nu)-pre && sameLine(old[len(old)-1-suf], nu[len(nu)-1-suf]) {
		suf++
	}
	a, b := old[pre:len(old)-suf], nu[pre:len(nu)-suf]
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	matches, ok := myers(a, b)
	if !ok {
		return [][4]int{{pre, pre + len(a), pre, pre + len(b)}}
	}
	var out [][4]int
	x, y := 0, 0
	for _, m := range append(matches, [2]int{len(a), len(b)}) {
		if m[0] > x || m[1] > y {
			out = append(out, [4]int{pre + x, pre + m[0], pre + y, pre + m[1]})
		}
		x, y = m[0]+1, m[1]+1
	}
	return out
}

// myers is the matched line pairs (i of a, j of b) of a shortest edit
// script from a to b, ascending; false past maxDiffEdits.
func myers(a, b []string) ([][2]int, bool) {
	n, m := len(a), len(b)
	lim := min(n+m, maxDiffEdits)
	off := lim + 1
	v := make([]int, 2*lim+3)
	var trace [][]int
	end := -1
	for d := 0; d <= lim && end < 0; d++ {
		// v on the diagonals -d-1 .. d+1, which the walk back reads.
		trace = append(trace, append([]int(nil), v[off-d-1:off+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && sameLine(a[x], b[y]) {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				end = d
				break
			}
		}
	}
	if end < 0 {
		return nil, false
	}
	var rev [][2]int
	x, y := n, m
	for d := end; d > 0; d-- {
		pv := trace[d]
		at := func(k int) int { return pv[k+d+1] }
		k := x - y
		pk := k - 1
		if k == -d || k != d && at(k-1) < at(k+1) {
			pk = k + 1
		}
		px := at(pk)
		py := px - pk
		// The snake after the step from (px, py).
		sx, sy := px, py
		if pk == k+1 {
			sy++ // a line of b inserted
		} else {
			sx++ // a line of a deleted
		}
		for x > sx && y > sy {
			x, y = x-1, y-1
			rev = append(rev, [2]int{x, y})
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		rev = append(rev, [2]int{x, y})
	}
	out := make([][2]int, len(rev))
	for i, p := range rev {
		out[len(rev)-1-i] = p
	}
	return out, true
}
