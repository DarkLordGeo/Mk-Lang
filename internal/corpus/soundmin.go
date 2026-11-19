// Why: a violating or miscompiled program is found at full size, often
// hundreds of lines; a record is only useful once it is cut to the smallest
// program that still shows the fault.
//
// What: MinBudget, MinStats, Minimise, MinimiseStats, MinimiseBatch (a keep
// that judges many candidates at once), and the candidate passes.
//
// How: the program's statement tree is rebuilt from its text by brace
// depth (the generator writes one statement per line, so the tree is
// exact). Passes drop whole top-level items, ddmin chunks and single
// statements, replace a block by one of its segments, drop names, and edit
// expressions, repeated until a round removes nothing
// or the budget ends. A round's candidates are checked in parallel and the
// first success in candidate order wins, so the result depends only on the
// candidate bound.

package corpus

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"mk/internal/source"
)

// MinBudget bounds a minimisation.
type MinBudget struct {
	Candidates int           // default 2,000
	Time       time.Duration // default 120 s
	Workers    int
}

// MinStats says what a minimisation did, for the record's minimised line.
type MinStats struct {
	FromLines, ToLines int
	Candidates         int
	Rounds             int
}

// Minimise returns the smallest text it finds for which keep holds, by the
// passes below, within budget. keep(text) must hold on entry; Minimise
// calls it from Workers goroutines at once.
func Minimise(text string, keep func(string) bool, b MinBudget) string {
	out, _ := MinimiseStats(text, keep, b)
	return out
}

// MinimiseStats is Minimise with what it did.
func MinimiseStats(text string, keep func(string) bool, b MinBudget) (string, MinStats) {
	if b.Workers <= 0 {
		b.Workers = soundWorkers()
	}
	batch := func(cands []string) []bool {
		ok := make([]bool, len(cands))
		var wg sync.WaitGroup
		for i := range cands {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// A keep that crashes keeps nothing.
				source.Guard("minimise candidate", source.Span{}, func() { ok[i] = keep(cands[i]) })
			}()
		}
		wg.Wait()
		return ok
	}
	return MinimiseBatch(text, batch, b.Workers, b)
}

// MinimiseBatch is Minimise with a keep that judges chunk candidates at
// once (the oracle builds one batch package per chunk).
func MinimiseBatch(text string, keep func([]string) []bool, chunk int, b MinBudget) (string, MinStats) {
	if b.Candidates <= 0 {
		b.Candidates = 2000
	}
	if b.Time <= 0 {
		b.Time = 120 * time.Second
	}
	st := MinStats{FromLines: lineCount(text)}
	deadline := time.Now().Add(b.Time)
	cur := text
	for st.Candidates < b.Candidates && time.Now().Before(deadline) {
		st.Rounds++
		cands := minCandidates(cur)
		found := ""
		for lo := 0; lo < len(cands) && found == ""; lo += chunk {
			if st.Candidates >= b.Candidates || !time.Now().Before(deadline) {
				break
			}
			hi := min(lo+chunk, len(cands), lo+b.Candidates-st.Candidates)
			ok := keep(cands[lo:hi])
			st.Candidates += hi - lo
			for i, y := range ok {
				if y {
					found = cands[lo+i]
					break
				}
			}
		}
		if found == "" {
			break
		}
		cur = found
	}
	st.ToLines = lineCount(cur)
	return cur, st
}

func lineCount(s string) int { return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1 }

// mnode is one statement or item of the tree: a leaf line, or a block
// whose head line ends in "{", with one segment of children per branch
// (an if with an else has two, joined by "} else {").
type mnode struct {
	head string
	segs [][]*mnode
	mids []string // the lines between segments
	tail string   // the closing line; "" for a leaf
}

func (n *mnode) block() bool { return n.tail != "" }

// parseTree reads text into its top-level nodes; a blank line separates
// top-level items and is kept as a leaf of its own.
func parseTree(text string) []*mnode {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	i := 0
	return parseNodes(lines, &i, false)
}

func parseNodes(lines []string, i *int, inBlock bool) []*mnode {
	var out []*mnode
	for *i < len(lines) {
		l := lines[*i]
		t := strings.TrimSpace(l)
		if inBlock && strings.HasPrefix(t, "}") {
			return out
		}
		*i++
		if !strings.HasSuffix(t, "{") || strings.HasPrefix(t, "//") {
			out = append(out, &mnode{head: l})
			continue
		}
		n := &mnode{head: l}
		for {
			n.segs = append(n.segs, parseNodes(lines, i, true))
			if *i >= len(lines) {
				n.tail = "}" // unbalanced: never from the generator
				break
			}
			end := lines[*i]
			*i++
			if strings.HasSuffix(strings.TrimSpace(end), "{") {
				n.mids = append(n.mids, end)
				continue
			}
			n.tail = end
			break
		}
		out = append(out, n)
	}
	return out
}

func renderTree(ns []*mnode) string {
	var b strings.Builder
	renderNodes(&b, ns)
	return b.String()
}

func renderNodes(b *strings.Builder, ns []*mnode) {
	for _, n := range ns {
		b.WriteString(n.head)
		b.WriteByte('\n')
		for j, seg := range n.segs {
			renderNodes(b, seg)
			if j < len(n.mids) {
				b.WriteString(n.mids[j])
				b.WriteByte('\n')
			}
		}
		if n.block() {
			b.WriteString(n.tail)
			b.WriteByte('\n')
		}
	}
}

// minCandidates lists the candidates of one round in order: whole
// top-level items (helpers, header items, consts) first, then ddmin chunks
// and single statements of every block outermost first, then a block
// replaced by one of its segments, then expression edits.
func minCandidates(text string) []string {
	root := parseTree(text)
	var out []string
	seen := map[string]bool{text: true}
	add := func() {
		s := renderTree(root)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	// The sibling lists, outermost first.
	var lists []*[]*mnode
	var walk func(p *[]*mnode)
	walk = func(p *[]*mnode) {
		lists = append(lists, p)
		for _, n := range *p {
			for j := range n.segs {
				walk(&n.segs[j])
			}
		}
	}
	walk(&root)
	// Deletions: chunks of n/2, n/4, ... 1 of each list.
	for _, p := range lists {
		orig := *p
		for size := len(orig) / 2; size >= 1; size /= 2 {
			for lo := 0; lo < len(orig); lo += size {
				hi := min(lo+size, len(orig))
				*p = append(append([]*mnode{}, orig[:lo]...), orig[hi:]...)
				add()
			}
			if size == 1 {
				break
			}
		}
		if len(orig) == 1 {
			*p = nil
			add()
		}
		*p = orig
	}
	// A name dropped with every statement that mentions it (the pass that
	// folds lines of names no longer declared, taken whole: a declaration
	// and its uses go together, or neither can).
	// A dropped statement that declares a name drops that name too, so a
	// chain of references (v, r := &v, q := r) goes in one candidate.
	for _, name := range localNames(text) {
		names := map[string]bool{name: true}
		mentions := func(l string) bool {
			for _, m := range reLocal.FindAllString(l, -1) {
				if names[m] {
					return true
				}
			}
			return false
		}
		var drop func(ns []*mnode) ([]*mnode, bool)
		drop = func(ns []*mnode) ([]*mnode, bool) {
			var out []*mnode
			grew := false
			for _, n := range ns {
				if mentions(n.head) {
					for _, d := range declared(n.head) {
						if !names[d] {
							names[d], grew = true, true
						}
					}
					continue
				}
				c := *n
				c.segs = make([][]*mnode, len(n.segs))
				for j, seg := range n.segs {
					var g bool
					c.segs[j], g = drop(seg)
					grew = grew || g
				}
				out = append(out, &c)
			}
			return out, grew
		}
		var kept []*mnode
		for grew := true; grew; {
			kept, grew = drop(root)
		}
		s := renderTree(kept)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	// A block replaced by one of its segments (an if by its then-part, a
	// loop by one iteration's statements).
	for _, p := range lists {
		orig := *p
		for k, n := range orig {
			if !n.block() {
				continue
			}
			for _, seg := range n.segs {
				*p = append(append(append([]*mnode{}, orig[:k]...), seg...), orig[k+1:]...)
				add()
			}
		}
		*p = orig
	}
	// Expression edits on single lines.
	for _, p := range lists {
		for _, n := range *p {
			orig := n.head
			for _, e := range lineEdits(orig) {
				n.head = e
				add()
			}
			n.head = orig
		}
	}
	return out
}

// localNames are the generator's local names in text (letters then
// digits: v12, xs3, dp4), in order of first appearance.
func localNames(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reLocal.FindAllString(text, -1) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// declared are the local names line l declares: `var x :=`, `x :=`,
// `if x :=`, `for x in`, `for i, x in`, `scope s :=`.
func declared(l string) []string {
	m := reDecl.FindStringSubmatch(l)
	if m == nil {
		return nil
	}
	var out []string
	for _, s := range m[1:] {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

var (
	reDecl  = regexp.MustCompile(`^\s*(?:var\s+|if\s+|for\s+|scope\s+)?([a-z]{1,2}[0-9]+)(?:\s*,\s*([a-z]{1,2}[0-9]+))?\s*(?::=|:\s|\s+in\s)`)
	reLocal = regexp.MustCompile(`\b[a-z]{1,2}[0-9]+\b`)
	reLit   = regexp.MustCompile(`\b[1-9][0-9]?\b`)
	reId    = regexp.MustCompile(`Id\(([a-z][a-z0-9]*)\)`)
	rePick  = regexp.MustCompile(`Pick\(k \^ [0-9]+, ([a-z][a-z0-9]*), (&?[a-z][a-z0-9]*)\)`)
)

// lineEdits are the expression edits of one line: a call of Id or Pick
// by an argument, a small literal by 0.
func lineEdits(l string) []string {
	var out []string
	if m := reId.FindStringSubmatchIndex(l); m != nil {
		out = append(out, l[:m[0]]+l[m[2]:m[3]]+l[m[1]:])
	}
	if m := rePick.FindStringSubmatchIndex(l); m != nil {
		out = append(out, l[:m[0]]+l[m[2]:m[3]]+l[m[1]:], l[:m[0]]+l[m[4]:m[5]]+l[m[1]:])
	}
	t := strings.TrimSpace(l)
	if strings.HasPrefix(t, "var ") || strings.HasPrefix(t, "*") || strings.Contains(t, " = ") {
		if !strings.Contains(t, "6364136223846793005") && !strings.HasPrefix(t, "//") {
			if m := reLit.FindStringIndex(l); m != nil {
				out = append(out, l[:m[0]]+"0"+l[m[1]:])
			}
		}
	}
	return out
}
