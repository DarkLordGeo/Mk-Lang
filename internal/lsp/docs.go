// Why: the editor's text and positions differ from the compiler's: open buffers,
// UTF-16 columns, CRLF.
//
// What: document and store (the open buffers), the URI and path mapping, and
// lines, which converts between byte offsets and LSP positions.
//
// How: texts are kept as the client sent them and normalised as
// source.Normalize does, so a span of a check maps line for line. Positions
// are UTF-8 when the client offers it, else UTF-16.

package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

	"mk/internal/source"
)

// document is one open buffer.
type document struct {
	uri     string
	path    string // absolute OS path
	version int
	raw     string // as the client sent it
	text    lines  // normalized
	toks    []token
	decls   []decl
	scanned bool
}

// scanned returns the buffer's tokens and declarations, scanning once per
// version.
func (d *document) scan() ([]token, []decl) {
	if !d.scanned {
		d.toks = scan(d.text.s)
		d.decls = decls(d.text.s, d.toks)
		d.scanned = true
	}
	return d.toks, d.decls
}

// store holds the open documents by path key.
type store struct {
	mu   sync.Mutex
	docs map[string]*document
	// good is, per open document, the last text whose check reached the
	// types phase: queries answer from it while the buffer does not parse
	// (as it does not right after typing `p.`).
	good map[string]string
}

func newStore() *store { return &store{docs: map[string]*document{}, good: map[string]string{}} }

func (s *store) setGood(path, raw string) {
	s.mu.Lock()
	if _, open := s.docs[pathKey(path)]; open {
		s.good[pathKey(path)] = raw
	}
	s.mu.Unlock()
}

func (s *store) goodText(path string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.good[pathKey(path)]
	return g, ok
}

// pathKey is a path's identity: cleaned, and case-folded where the file
// system folds case.
func pathKey(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func (s *store) open(uri, path string, version int, text string) *document {
	d := &document{uri: uri, path: path, version: version, raw: text, text: newLines(source.Normalize([]byte(text)))}
	s.mu.Lock()
	s.docs[pathKey(path)] = d
	s.mu.Unlock()
	return d
}

func (s *store) get(path string) *document {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[pathKey(path)]
}

func (s *store) close(path string) {
	s.mu.Lock()
	delete(s.docs, pathKey(path))
	delete(s.good, pathKey(path))
	s.mu.Unlock()
}

// all returns the open documents, sorted by path key, so the order of
// what is published or checked for them never comes from a map.
func (s *store) all() []*document {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*document, 0, len(s.docs))
	for _, d := range s.docs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return pathKey(out[i].path) < pathKey(out[j].path) })
	return out
}

// uriPath turns a file URI into an absolute OS path.
func uriPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		// file:///C:/x is /C:/x; file://server/share/x is a UNC path.
		if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		} else if u.Host != "" {
			p = "//" + u.Host + p
		}
	}
	if p == "" {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(p)), true
}

// pathURI turns an absolute OS path into a file URI.
func pathURI(p string) string {
	s := filepath.ToSlash(p)
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return (&url.URL{Scheme: "file", Path: s}).String()
}

// lines is a normalized text with its line starts.
type lines struct {
	s      string
	starts []int
}

func newLines(s string) lines {
	starts := make([]int, 1, 1+strings.Count(s, "\n"))
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return lines{s: s, starts: starts}
}

// line is the text of zero-based line n without its newline.
func (l lines) line(n int) string {
	if n < 0 || n >= len(l.starts) {
		return ""
	}
	end := len(l.s)
	if n+1 < len(l.starts) {
		end = l.starts[n+1] - 1
	}
	return l.s[l.starts[n]:end]
}

// pos converts a byte offset to a Position in the encoding.
func (l lines) pos(off int, utf8Enc bool) Position {
	off = min(max(off, 0), len(l.s))
	lo, hi := 0, len(l.starts)
	for lo < hi {
		m := (lo + hi) / 2
		if l.starts[m] <= off {
			lo = m + 1
		} else {
			hi = m
		}
	}
	n := lo - 1
	col := off - l.starts[n]
	if utf8Enc {
		return Position{Line: n, Character: col}
	}
	return Position{Line: n, Character: utf16Len(l.line(n)[:min(col, len(l.line(n)))])}
}

// off converts a Position to a byte offset, clamped to the line.
func (l lines) off(p Position, utf8Enc bool) int {
	if p.Line < 0 {
		return 0
	}
	if p.Line >= len(l.starts) {
		return len(l.s)
	}
	text := l.line(p.Line)
	base := l.starts[p.Line]
	if utf8Enc {
		return base + min(max(p.Character, 0), len(text))
	}
	units := 0
	for i, r := range text {
		if units >= p.Character {
			return base + i
		}
		units += max(1, utf16.RuneLen(r))
	}
	return base + len(text)
}

func (l lines) span(start, end int, utf8Enc bool) Range {
	return Range{Start: l.pos(start, utf8Enc), End: l.pos(end, utf8Enc)}
}

// endPos is the position just past the last character.
func (l lines) endPos(utf8Enc bool) Position { return l.pos(len(l.s), utf8Enc) }

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += max(1, utf16.RuneLen(r))
	}
	return n
}

// sameURI reports whether two file URIs name one file.
func sameURI(a, b string) bool {
	pa, oka := uriPath(a)
	pb, okb := uriPath(b)
	return oka && okb && pathKey(pa) == pathKey(pb)
}
