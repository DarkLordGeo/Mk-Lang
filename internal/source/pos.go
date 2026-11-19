// Why: every diagnostic, origin and cache record names a place in a source
// file; positions must be small, comparable and independent of line endings.
//
// What: FileID, Pos, Span (NewSpan, To, Contains), LineCol and Position.
//
// How: a position is a byte offset into one file's normalized text; lines
// and columns (1-based, columns in bytes) are computed from it only for
// printing.

package source

import (
	"fmt"
	"strconv"
)

// FileID names one File in a SourceMap. The zero value NoFile means "no file";
// a valid FileID is only ever handed out by SourceMap.Add. FileID numbers follow
// insertion order and carry no meaning: never order output by them.
type FileID uint32

// NoFile is the zero FileID: no file. Spans and positions in NoFile are "no location".
const NoFile FileID = 0

// IsValid reports whether id names a file (is not NoFile). It does not check
// that any particular SourceMap holds it.
func (id FileID) IsValid() bool { return id != NoFile }

// Pos is one point in one file: a byte offset into the file's normalized text.
// Off may equal the text length (the end-of-file position). The zero Pos (NoFile)
// means "no position".
type Pos struct {
	File FileID
	Off  uint32
}

// IsValid reports whether p is in a file.
func (p Pos) IsValid() bool { return p.File != NoFile }

// Span returns the empty span (an insertion point) at p.
func (p Pos) Span() Span { return Span{File: p.File, Start: p.Off, End: p.Off} }

// Span is a half-open byte range [Start, End) of one file's normalized text.
// Invariant: Start <= End and both lie in the same file; a span never crosses
// files. An empty span (Start == End) is an insertion point. The zero Span (File
// NoFile) means "no location". A Span is never fed to a Hasher.
type Span struct {
	File  FileID
	Start uint32
	End   uint32
}

// NewSpan returns the span [start, end) of file. It raises an ICE when
// start > end, so every span built through it satisfies the invariant.
func NewSpan(file FileID, start, end uint32) Span {
	if start > end {
		ICEf(Span{}, "NewSpan: start %d > end %d in file %d", start, end, file)
	}
	return Span{File: file, Start: start, End: end}
}

// IsValid reports whether s is in a file and satisfies Start <= End.
func (s Span) IsValid() bool { return s.File != NoFile && s.Start <= s.End }

// Len is the span's length in bytes, End - Start.
func (s Span) Len() uint32 { return s.End - s.Start }

// IsEmpty reports whether the span is an insertion point (Start == End).
func (s Span) IsEmpty() bool { return s.Start == s.End }

// StartPos is the position of the span's first byte.
func (s Span) StartPos() Pos { return Pos{File: s.File, Off: s.Start} }

// EndPos is the position just past the span's last byte.
func (s Span) EndPos() Pos { return Pos{File: s.File, Off: s.End} }

// Contains reports whether p lies in s, counting the end position of s, so an
// empty span contains its own insertion point.
func (s Span) Contains(p Pos) bool {
	return s.File != NoFile && s.File == p.File && s.Start <= p.Off && p.Off <= s.End
}

// To returns the smallest span covering both s and o. A zero span on either side
// yields the other unchanged. Two valid spans in different files are an ICE.
func (s Span) To(o Span) Span {
	switch {
	case s.File == NoFile:
		return o
	case o.File == NoFile:
		return s
	case s.File != o.File:
		ICEf(s, "Span.To across files %d and %d", s.File, o.File)
	}
	return Span{File: s.File, Start: min(s.Start, o.Start), End: max(s.End, o.End)}
}

// String renders the span for debugging only, as "file#<id>[start:end]". It
// shows a FileID, so it never appears in diagnostics or any other output; use
// SourceMap.Position for that.
func (s Span) String() string {
	return fmt.Sprintf("file#%d[%d:%d]", s.File, s.Start, s.End)
}

// LineCol is a human position: Line is 1-based, Col is 1-based and counted in
// UTF-8 bytes from the start of the line (identifiers are ASCII, so no width
// table is needed). The zero LineCol means "unknown".
type LineCol struct {
	Line uint32
	Col  uint32
}

// IsValid reports whether lc is a real position (Line and Col at least 1).
func (lc LineCol) IsValid() bool { return lc.Line > 0 && lc.Col > 0 }

// String renders "line:col".
func (lc LineCol) String() string {
	return strconv.FormatUint(uint64(lc.Line), 10) + ":" + strconv.FormatUint(uint64(lc.Col), 10)
}

// Position is a printable location: a file's package-relative path plus a
// LineCol. It is what diagnostics and panic sites print.
type Position struct {
	Path string
	LineCol
}

// IsValid reports whether the position names a file and a real line and column.
func (p Position) IsValid() bool { return p.Path != "" && p.LineCol.IsValid() }

// String renders "path:line:col", or "-" for an invalid position.
func (p Position) String() string {
	if !p.IsValid() {
		return "-"
	}
	return p.Path + ":" + p.LineCol.String()
}
