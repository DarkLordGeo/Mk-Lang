// Why: a REPL reads free-form lines, so it must decide when an entry is
// complete, whether it is a declaration or an expression, and how to report
// a diagnostic against what the user typed rather than the scratch file.
//
// What: the entry reader (reader, next, scanState), the classifiers
// (looksLikeDecl, looksLikeFunc, isSignature, constDecl, declName,
// parseImport), and locRewriter with origin, which maps scratch lines back
// to <repl N>:line:col.
//
// How: text-level scanning that tracks brackets, strings and comments; an
// entry continues while a bracket or string is open, a line ends in an
// operator or comma, or a signature continues. No parser runs here: the
// driver's check of the scratch file is the judge.

package repl

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// declKeywords start a top-level item. A function has no keyword, so an entry
// that starts with none of these is tried as an expression first
// (Session.entry).
var declKeywords = map[string]bool{
	"import": true, "export": true, "struct": true, "enum": true, "error": true, "interface": true,
	"impl": true, "type": true, "const": true, "var": true, "linear": true, "view": true,
	"gen": true, "test": true, "extern": true,
}

// firstWord is the first identifier-like word of the entry's first line that
// is not a comment, and whether the entry opens with a /// doc line.
func firstWord(entry string) (word string, doc bool) {
	for line := range strings.SplitSeq(entry, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "///") {
			return "", true
		}
		if strings.HasPrefix(t, "//") {
			continue
		}
		i := 0
		for i < len(t) && (isIdent(t[i])) {
			i++
		}
		return t[:i], false
	}
	return "", false
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// looksLikeDecl reports whether the entry starts with an item keyword or a
// doc comment.
func looksLikeDecl(entry string) bool {
	w, doc := firstWord(entry)
	return doc || declKeywords[w]
}

var (
	reTypeDecl = regexp.MustCompile(`^(?:export\s+)?(?:extern\s+)?(?:(?:linear|view)\s+)*(?:struct|enum|error|interface|type|const|var)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	// reFuncDecl reads the grammar's FuncName: Name [TypeArgs] "." Name, or
	// Name, then the function's TypeParams or Params. The receiver's type
	// arguments (Holder[T].Get) are not part of the name.
	reFuncDecl = regexp.MustCompile(`^(?:export\s+)?(?:extern\s+)?(?:naked\s+)?([A-Za-z_][A-Za-z0-9_]*)(?:\[[^\]]*\])?(?:\.([A-Za-z_][A-Za-z0-9_]*))?\s*[\[(]`)
	reImport   = regexp.MustCompile(`^import\s+(?:([A-Za-z_][A-Za-z0-9_]*)\s*=\s*)?([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*$`)
	reConst    = regexp.MustCompile(`^(export\s+)?const\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
)

// declName is the name of the item a declaration entry declares ("T.M" for
// a method, T without its type arguments), or "" when it declares none a
// later entry could replace (impl, test, gen).
func declName(entry string) string {
	for line := range strings.SplitSeq(entry, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		if m := reTypeDecl.FindStringSubmatch(t); m != nil {
			return m[1]
		}
		if m := reFuncDecl.FindStringSubmatch(t); m != nil && !declKeywords[m[1]] {
			if m[2] != "" {
				return m[1] + "." + m[2]
			}
			return m[1]
		}
		return ""
	}
	return ""
}

// constDecl reports whether the entry is one const item, and its name and
// whether it is exported.
func constDecl(entry string) (name string, export, ok bool) {
	t := strings.TrimSpace(stripLeadingComments(entry))
	m := reConst.FindStringSubmatch(t)
	if m == nil {
		return "", false, false
	}
	return m[2], m[1] != "", true
}

// stripLeadingComments drops the blank and // comment lines (/// included)
// an entry starts with.
func stripLeadingComments(entry string) string {
	rest := entry
	for rest != "" {
		line, after, _ := strings.Cut(rest, "\n")
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "//") {
			return rest
		}
		rest = after
	}
	return ""
}

// onlyComments reports whether an entry holds nothing but blank lines and
// comments.
func onlyComments(entry string) bool { return strings.TrimSpace(stripLeadingComments(entry)) == "" }

// looksLikeFunc reports whether the entry's first line reads as a function
// head with a body: a name (or T.M), parameters, then a { on that line; or
// the entry is a signature (isSignature) with a body.
func looksLikeFunc(entry string) bool {
	if isSignature(entry, false) && strings.Contains(entry, "{") {
		return true
	}
	for line := range strings.SplitSeq(entry, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		return reFuncDecl.MatchString(t) && strings.Contains(t, ")") && strings.HasSuffix(t, "{")
	}
	return false
}

// funcHead reads the entry (its leading comments skipped) as FuncName
// [TypeParams] "(" params ")" rest. ok is false when it does not start that
// way or the parameters do not close.
func funcHead(entry string) (params, rest string, ok bool) {
	t := strings.TrimLeft(stripLeadingComments(entry), " \t")
	m := reFuncDecl.FindStringSubmatchIndex(t)
	if m == nil || declKeywords[t[m[2]:m[3]]] {
		return "", "", false
	}
	i := m[1] - 1 // the '[' of TypeParams or the '(' of Params
	if t[i] == '[' {
		j := closing(t, i)
		if j < 0 {
			return "", "", false
		}
		i = j + 1
		for i < len(t) && (t[i] == ' ' || t[i] == '\t') {
			i++
		}
		if i >= len(t) || t[i] != '(' {
			return "", "", false
		}
	}
	j := closing(t, i)
	if j < 0 {
		return "", "", false
	}
	return t[i+1 : j], t[j+1:], true
}

// closing is the index of the bracket that closes the one at t[i], outside
// string and char literals and comments, or -1.
func closing(t string, i int) int {
	depth := 0
	for ; i < len(t); i++ {
		c := t[i]
		switch {
		case strings.HasPrefix(t[i:], "//"):
			for i < len(t) && t[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'':
			raw := i > 0 && t[i-1] == 'r'
			for i++; i < len(t) && t[i] != c && t[i] != '\n'; i++ {
				if t[i] == '\\' && !raw {
					i++
				}
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// clauseWords begin a signature's continuation line, and
// may follow its parameters on the same line.
var clauseWords = map[string]bool{"from": true, "ends": true, "requires": true, "ensures": true}

// primitiveTypes are the type names no expression can pass as a value, so
// `x: int` inside parentheses is a parameter, not a named argument.
var primitiveTypes = map[string]bool{
	"int": true, "uint": true, "i8": true, "i16": true, "i32": true, "i64": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "f32": true, "f64": true,
	"bool": true, "char": true, "str": true, "void": true,
}

var reSelf = regexp.MustCompile(`\bself\b`)

var reParamPrim = regexp.MustCompile(`^\s*(?:mut\s+)?[a-z_][A-Za-z0-9_]*\s*:\s*([a-z][a-z0-9]*)\s*$`)

// isSignature reports whether the entry reads as a function signature and
// not as a call: after its parameters comes a result type (":"), a flow or
// a contract. With loose set, a parameter list naming self or a parameter
// of a primitive type (`x: int`) also counts, which tells a function with
// no result type from a call.
func isSignature(entry string, loose bool) bool {
	params, rest, ok := funcHead(entry)
	if !ok {
		return false
	}
	r := strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(r, ":") && !strings.HasPrefix(r, ":=") {
		return true
	}
	if w := leadingWord(r); clauseWords[w] || (w == "cpu" || w == "section") && strings.HasPrefix(strings.TrimLeft(r[len(w):], " \t"), `"`) {
		return true
	}
	if !loose {
		return false
	}
	if reSelf.MatchString(params) {
		return true
	}
	for p := range strings.SplitSeq(params, ",") {
		if m := reParamPrim.FindStringSubmatch(p); m != nil && primitiveTypes[m[1]] {
			return true
		}
	}
	return false
}

// leadingWord is the identifier t starts with.
func leadingWord(t string) string {
	i := 0
	for i < len(t) && isIdent(t[i]) {
		i++
	}
	return t[:i]
}

// openSignature reports whether the entry so far is a signature whose body
// has not started, so a following line may continue it.
func openSignature(entry string) bool {
	if !isSignature(entry, true) {
		return false
	}
	_, rest, _ := funcHead(entry)
	return !strings.Contains(rest, "{")
}

// continuesSignature reports whether a line continues a signature: it
// begins with from, ends, *, requires or ensures, or with cpu or section and
// a string.
func continuesSignature(line string) bool {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "*") {
		return true
	}
	w := leadingWord(t)
	if clauseWords[w] {
		return true
	}
	return (w == "cpu" || w == "section") && strings.HasPrefix(strings.TrimLeft(t[len(w):], " \t"), `"`)
}

// parseImport reads an import entry: the name its module is used by in code
// (the alias, else the path's last segment) and the line as it is written
// into the scratch file. ok is false when the entry is not one import line.
func parseImport(entry string) (name, line string, ok bool) {
	t := strings.TrimSpace(entry)
	m := reImport.FindStringSubmatch(t)
	if m == nil {
		return "", "", false
	}
	name = m[1]
	if name == "" {
		name = m[2][strings.LastIndexByte(m[2], '.')+1:]
	}
	return name, t, true
}

// uses reports whether text names the module bound to name: name followed by
// a '.', not preceded by an identifier character or a '.'.
func uses(text, name string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], name+".")
		if j < 0 {
			return false
		}
		j += i
		if j == 0 || !isIdent(text[j-1]) && text[j-1] != '.' {
			return true
		}
		i = j + 1
	}
}

// reader reads entries: one line, or several while brackets are open, a
// string is open, the line ends in a binary operator or a comma, the entry
// so far is only comments (they belong to what follows), or a signature
// continues on the next line (a line that begins with from,
// ends, *, requires, ensures, or cpu or section and a string; after such a
// line the body's { may start a line of its own).
type reader struct {
	sc      *bufio.Scanner
	pending *string // a line read ahead that the next entry starts with
}

func newReader(sc *bufio.Scanner) *reader {
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return &reader{sc: sc}
}

// line reads one physical line, prompting first unless it was read ahead.
func (r *reader) line(prompt func(cont bool), cont bool) (string, bool) {
	if r.pending != nil {
		l := *r.pending
		r.pending = nil
		return l, true
	}
	prompt(cont)
	if !r.sc.Scan() {
		return "", false
	}
	return strings.TrimRight(r.sc.Text(), "\r"), true
}

// next returns the next entry, without its final newline; prompt is called
// before each physical line with true for a continuation line. ok is false
// at the end of input with nothing read.
func (r *reader) next(prompt func(cont bool)) (entry string, ok bool) {
	var b strings.Builder
	var st scanState
	cont := false
	clause := false // a line continued the signature, so { may start a line
	add := func(line string) {
		if cont {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		st.scan(line)
		cont = true
	}
	for {
		line, more := r.line(prompt, cont)
		if !more {
			return b.String(), b.Len() > 0
		}
		if !cont && strings.TrimSpace(line) == "" {
			continue
		}
		add(line)
		if st.open() || trailingOperator(line) || onlyComments(b.String()) {
			continue
		}
		if !openSignature(b.String()) {
			return b.String(), true
		}
		// A signature with no body yet: look at the next line. A blank line
		// or the end of input ends the entry; any other line that does not
		// continue the signature starts the next entry. clause survives a
		// continuation line that spans lines (an open bracket).
		for {
			next, more := r.line(prompt, true)
			if !more || strings.TrimSpace(next) == "" {
				return b.String(), true
			}
			if continuesSignature(next) || clause && strings.HasPrefix(strings.TrimSpace(next), "{") {
				clause = true
				add(next)
				if st.open() {
					break // the body: read on until it closes
				}
				continue
			}
			r.pending = &next
			return b.String(), true
		}
	}
}

// scanState follows brackets and multi-line strings across lines, outside
// string and char literals and comments.
type scanState struct {
	depth  int
	triple bool // inside a """ string
}

func (s *scanState) open() bool { return s.depth > 0 || s.triple }

func (s *scanState) scan(line string) {
	for i := 0; i < len(line); i++ {
		if s.triple {
			if strings.HasPrefix(line[i:], `"""`) {
				s.triple = false
				i += 2
			}
			continue
		}
		c := line[i]
		switch {
		case strings.HasPrefix(line[i:], "//"):
			return
		case strings.HasPrefix(line[i:], `"""`):
			s.triple = true
			i += 2
		case c == '"' || c == '\'':
			raw := i > 0 && line[i-1] == 'r'
			for i++; i < len(line) && line[i] != c; i++ {
				if line[i] == '\\' && !raw {
					i++
				}
			}
		case c == '(' || c == '[' || c == '{':
			s.depth++
		case c == ')' || c == ']' || c == '}':
			if s.depth > 0 {
				s.depth--
			}
		}
	}
}

// commentStart is the byte offset of the line's // comment outside string
// and char literals, or -1.
func commentStart(line string) int {
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case strings.HasPrefix(line[i:], "//"):
			return i
		case strings.HasPrefix(line[i:], `"""`):
			return -1 // a multi-line string opens: no comment on this line
		case c == '"' || c == '\'':
			raw := i > 0 && line[i-1] == 'r'
			for i++; i < len(line) && line[i] != c; i++ {
				if line[i] == '\\' && !raw {
					i++
				}
			}
		}
	}
	return -1
}

// withClause appends clause to a line, before its // comment if it has one,
// so the comment does not swallow the clause.
func withClause(line, clause string) string {
	if i := commentStart(line); i >= 0 {
		return strings.TrimRight(line[:i], " \t") + " " + clause + " " + line[i:]
	}
	return line + " " + clause
}

// trailingOperator reports whether a line ends in a binary operator or a
// comma, so the expression continues on the next line (operators end a
// line, never start one).
func trailingOperator(line string) bool {
	t := line
	if i := commentStart(t); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSpace(t)
	if t == "" {
		return false
	}
	return strings.ContainsRune("+-*/%&|^=<>,", rune(t[len(t)-1]))
}

// origin is where a scratch-file line came from: entry n's line l (1-based);
// n is 0 for the lines the REPL writes itself.
type origin struct{ n, l int }

// locRewriter rewrites the scratch file's locations (path:line:col and
// path:line) in diagnostic and query text to <repl N>:line:col. A location
// is the file's name at a word boundary (not after an identifier character,
// '.' or '-'), with or without a directory, outside a "string" on its line,
// so a value or a quoted source line that spells the name is left alone.
type locRewriter struct {
	re   *regexp.Regexp
	orig []origin // index: scratch line - 1
}

func newLocRewriter(base string, orig []origin) *locRewriter {
	re := regexp.MustCompile(`(?:[^\s"'\[\](){},]*/)?` + regexp.QuoteMeta(base) + `:(\d+)(?::(\d+))?`)
	return &locRewriter{re: re, orig: orig}
}

func (lr *locRewriter) rewrite(text string) string {
	var b strings.Builder
	for line := range strings.SplitAfterSeq(text, "\n") {
		quoted := quotedSpans(line)
		last := 0
		for _, m := range lr.re.FindAllStringSubmatchIndex(line, -1) {
			if m[0] > 0 && (isIdent(line[m[0]-1]) || line[m[0]-1] == '.' || line[m[0]-1] == '-') || inSpans(quoted, m[0]) {
				continue
			}
			b.WriteString(line[last:m[0]])
			col := ""
			if m[4] >= 0 {
				col = line[m[4]:m[5]]
			}
			b.WriteString(lr.label(line[m[2]:m[3]], col))
			last = m[1]
		}
		b.WriteString(line[last:])
	}
	return b.String()
}

// label is the <repl N> location of scratch line ln (decimal) and column
// col ("" none).
func (lr *locRewriter) label(lnText, col string) string {
	ln, _ := strconv.Atoi(lnText)
	label := "<repl>"
	l := ln
	if ln >= 1 && ln <= len(lr.orig) {
		o := lr.orig[ln-1]
		if o.n > 0 {
			label, l = "<repl "+strconv.Itoa(o.n)+">", o.l
		}
		if o.l == 0 {
			// A line the REPL wrote for the entry (its const head): the
			// entry, with no line of its own.
			return label
		}
	}
	out := label + ":" + strconv.Itoa(l)
	if col != "" {
		out += ":" + col
	}
	return out
}

// quotedSpans are the byte ranges [start, end) of the "..." strings on one
// line (a string left open runs to the line's end).
func quotedSpans(line string) [][2]int {
	var spans [][2]int
	for i := 0; i < len(line); i++ {
		if line[i] != '"' {
			continue
		}
		raw := i > 0 && line[i-1] == 'r'
		j := i + 1
		for ; j < len(line) && line[j] != '"'; j++ {
			if line[j] == '\\' && !raw {
				j++
			}
		}
		spans = append(spans, [2]int{i, min(j+1, len(line))})
		i = j
	}
	return spans
}

func inSpans(spans [][2]int, at int) bool {
	for _, s := range spans {
		if at >= s[0] && at < s[1] {
			return true
		}
	}
	return false
}
