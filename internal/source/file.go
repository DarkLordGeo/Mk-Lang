// Why: every span, line number and content hash must mean the same thing on
// every system, whatever line endings or byte-order mark the file was saved
// with.
//
// What: Normalize, File (lines, offsets, slices, positions) and SourceMap,
// the session's set of files (Add, File, ByPath, Files, Text, Position).
//
// How: Normalize drops a UTF-8 byte-order mark and turns every CRLF into LF,
// once, before anything is hashed; a lone CR is kept for the lexer to
// report. Source is UTF-8. Files lists files in byte order of their paths.

package source

import (
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
)

// MaxFileSize is the largest normalized text a File may hold, in bytes, so every
// offset and span end fits in a uint32. The driver checks a file against it and
// reports a diagnostic before calling SourceMap.Add, which raises an ICE past it.
const MaxFileSize = math.MaxUint32 - 1

// utf8BOM is the byte-order mark Normalize strips from byte 0.
const utf8BOM = "\xEF\xBB\xBF"

// fileHashDomain is the Hasher domain of File.Hash.
const fileHashDomain = "mk.file.v1"

// Normalize turns a file's raw bytes into the text every span, line table,
// print-back and hash is computed over: a UTF-8 byte-order mark at byte 0 is
// dropped and every "\r\n" becomes "\n". A lone "\r" is kept so the lexer can
// report it (E0101); after Normalize every remaining '\r' was a lone CR in the
// raw input. Normalize is not idempotent ("\r\r\n" becomes "\r\n", then "\n"),
// so it is applied exactly once, to raw bytes, by SourceMap.Add. UTF-8
// validity is not checked here; the lexer checks it.
func Normalize(raw []byte) string {
	s := string(raw)
	s = strings.TrimPrefix(s, utf8BOM)
	if !strings.Contains(s, "\r\n") {
		return s
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// File is one source file, immutable once SourceMap.Add returns it, and safe to
// read from any goroutine.
type File struct {
	// ID is the file's identity in its SourceMap; never NoFile for an added file.
	ID FileID
	// Path is package-relative (or std-relative) with '/' separators, nonempty
	// and unique within the SourceMap. Diagnostics print it and sort by it.
	Path string
	// Text is the normalized text (see Normalize). Every offset of this file is
	// a byte offset into Text, and len(Text) <= MaxFileSize.
	Text string
	// Hash is the stable hash of Text under the domain "mk.file.v1": the file's
	// content identity for the cache. It does not depend on Path or ID.
	Hash Hash
	// lineStarts[0] == 0 and lineStarts[i] is the offset just after the i-th
	// '\n'. A file ending in '\n' therefore has an empty last line.
	lineStarts []uint32
}

// newFile builds the line table and hash of already normalized text.
func newFile(path, text string) *File {
	ls := make([]uint32, 1, 1+strings.Count(text, "\n"))
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			ls = append(ls, uint32(i+1))
		}
	}
	h := NewHasher(fileHashDomain)
	h.String(text)
	return &File{Path: path, Text: text, Hash: h.Sum(), lineStarts: ls}
}

// Len is the length of the normalized text in bytes; Len() is itself a valid
// offset (end of file, where the end-of-file token sits).
func (f *File) Len() uint32 { return uint32(len(f.Text)) }

// LineCount is the number of lines, at least 1. A trailing '\n' starts one more,
// empty, line.
func (f *File) LineCount() int { return len(f.lineStarts) }

// LineStart returns the offset of the first byte of 1-based line n. It raises an
// ICE when n is out of range.
func (f *File) LineStart(n int) uint32 {
	if n < 1 || n > len(f.lineStarts) {
		ICEf(Span{File: f.ID}, "%s: line %d out of range 1..%d", f.Path, n, len(f.lineStarts))
	}
	return f.lineStarts[n-1]
}

// Line returns the text of 1-based line n without its '\n'. It raises an ICE
// when n is out of range.
func (f *File) Line(n int) string {
	start := f.LineStart(n)
	end := uint32(len(f.Text))
	if n < len(f.lineStarts) {
		end = f.lineStarts[n] - 1
	}
	return f.Text[start:end]
}

// LineCol converts an offset to a 1-based line and 1-based byte column in
// O(log lines). off may equal Len(). An offset past the end is an ICE.
func (f *File) LineCol(off uint32) LineCol {
	if off > uint32(len(f.Text)) {
		ICEf(Span{File: f.ID}, "%s: offset %d past end %d", f.Path, off, len(f.Text))
	}
	i := sort.Search(len(f.lineStarts), func(i int) bool { return f.lineStarts[i] > off }) - 1
	return LineCol{Line: uint32(i + 1), Col: off - f.lineStarts[i] + 1}
}

// Offset is the inverse of LineCol: the offset of lc, and false when lc is not a
// position in the file. A column may point at the line's '\n' or, on the last
// line, at the end of the file.
func (f *File) Offset(lc LineCol) (uint32, bool) {
	if lc.Line < 1 || int(lc.Line) > len(f.lineStarts) || lc.Col < 1 {
		return 0, false
	}
	start := f.lineStarts[lc.Line-1]
	end := uint32(len(f.Text))
	if int(lc.Line) < len(f.lineStarts) {
		end = f.lineStarts[lc.Line] - 1
	}
	off := uint64(start) + uint64(lc.Col) - 1
	if off > uint64(end) {
		return 0, false
	}
	return uint32(off), true
}

// Slice returns the text a span of this file covers. A span of another file,
// or one outside the text, is an ICE.
func (f *File) Slice(s Span) string {
	if s.File != f.ID || s.Start > s.End || s.End > uint32(len(f.Text)) {
		ICEf(s, "%s: span %v not in file #%d of length %d", f.Path, s, f.ID, len(f.Text))
	}
	return f.Text[s.Start:s.End]
}

// Position renders an offset of this file as a printable Position.
func (f *File) Position(off uint32) Position {
	return Position{Path: f.Path, LineCol: f.LineCol(off)}
}

// SourceMap owns every File of a session. Add may be called from many
// goroutines at once; File and every read of an added file take no lock.
// FileIDs follow the order of Add calls, which the driver makes in byte order of
// path where it can, but which concurrent module loading may perturb: output
// never depends on FileID order (use Files, sorted by path). Slot 0 is reserved
// for NoFile. The zero SourceMap is not usable; call NewSourceMap.
type SourceMap struct {
	mu     sync.Mutex
	files  AppendList[*File]
	byPath map[string]*File
}

// NewSourceMap returns an empty map with slot 0 reserved for NoFile.
func NewSourceMap() *SourceMap {
	m := &SourceMap{byPath: make(map[string]*File)}
	m.files.Append(nil)
	return m
}

// Add normalizes raw (see Normalize) and records it as a new File at path. raw
// is the file's bytes exactly as read: never pass text that was normalized
// already. path must be nonempty, use '/' separators and not be in the map yet;
// a violation, or normalized text longer than MaxFileSize, is an ICE. Hashing
// and the line table are computed outside the lock.
func (m *SourceMap) Add(path string, raw []byte) *File {
	if path == "" || strings.ContainsRune(path, '\\') {
		ICEf(Span{}, "SourceMap.Add: bad path %q (need nonempty, '/' separators)", path)
	}
	text := Normalize(raw)
	if uint64(len(text)) > MaxFileSize {
		ICEf(Span{}, "SourceMap.Add: %s is %d bytes, over MaxFileSize", path, len(text))
	}
	f := newFile(path, text)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.byPath[path]; dup {
		ICEf(Span{}, "SourceMap.Add: duplicate path %q", path)
	}
	f.ID = FileID(m.files.Append(f))
	m.byPath[path] = f
	return f
}

// File returns the file with the given ID, or nil for NoFile. An ID this map
// never handed out is an ICE.
func (m *SourceMap) File(id FileID) *File {
	if id == NoFile {
		return nil
	}
	f, ok := m.files.Get(uint32(id))
	if !ok {
		ICEf(Span{}, "SourceMap.File: unknown file #%d", id)
	}
	return f
}

// ByPath returns the file added at path, if any.
func (m *SourceMap) ByPath(path string) (*File, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.byPath[path]
	return f, ok
}

// Len is the number of files added (slot 0 excluded).
func (m *SourceMap) Len() int { return int(m.files.Len()) - 1 }

// Files returns a new slice of every added file sorted by Path, bytewise: the
// one order in which files reach output.
func (m *SourceMap) Files() []*File {
	n := m.files.Len()
	out := make([]*File, 0, n)
	for i := uint32(1); i < n; i++ {
		f, _ := m.files.Get(i)
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b *File) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// Text returns the source text a span covers; the zero span gives "". A span
// outside its file is an ICE.
func (m *SourceMap) Text(s Span) string {
	if s.File == NoFile {
		return ""
	}
	return m.File(s.File).Slice(s)
}

// LineCol converts a position to its line and column; the zero Pos gives the
// zero LineCol.
func (m *SourceMap) LineCol(p Pos) LineCol {
	if p.File == NoFile {
		return LineCol{}
	}
	return m.File(p.File).LineCol(p.Off)
}

// Position renders a position as path, line and column; the zero Pos gives the
// invalid Position, which prints as "-".
func (m *SourceMap) Position(p Pos) Position {
	if p.File == NoFile {
		return Position{}
	}
	return m.File(p.File).Position(p.Off)
}

// SpanPositions returns the printable start and end of a span.
func (m *SourceMap) SpanPositions(s Span) (start, end Position) {
	return m.Position(s.StartPos()), m.Position(s.EndPos())
}
