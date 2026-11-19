// Why: a warm build should not parse a file whose module hits the cache, and
// a cached result should be found without knowing its full key up front.
//
// What: ParseSummary (with ParsedItem, ParsedImport, ParsedUnsafe), its
// Encode/Decode and Bind, and Manifest (Candidate list, Add, MaxCandidates)
// with EncodeManifest and DecodeManifest.
//
// How: the parse summary is the one target-free copy of a file's syntax
// facts the driver needs. A manifest holds the candidates under one lookup
// prefix: each is the read list it was valid under (all must hold against
// the current World) and its payload key.

package iface

import (
	"fmt"
	"slices"

	"mk/internal/source"
	"mk/internal/types"
)

// ParseSummary is the target-free copy of syntax per file: the item keys it
// declares, each item's token hash and anchor offsets, and the file's import
// table hash. Item keys are module-qualified and anchors carry the
// package-relative path, so the summary is keyed by module path, path and
// content (ParseKey), never by content alone; no FileID is stored.
//
// The fields after FileAnchor are what the driver reads of a file before
// it would parse it, so a file whose module hits is never parsed: the
// import lines (the import graph and E0202), whether the file declares
// Main, its exported top-level names (resolve's catalog), and
// its unsafe sites (E1102). Offsets are byte offsets into the file.
type ParseSummary struct {
	ContentHash source.Hash
	ImportsHash source.Hash  // mk.imports over sorted (alias, module path)
	Items       []ParsedItem // in source order
	// FileAnchor is the file's anchor: its non-trivia tokens, TokenHash the
	// driver's file token hash (syntax.FileHash).
	FileAnchor Anchor
	Imports    []ParsedImport // in source order
	Main       bool           // the file declares Main
	Exports    []string       // exported top-level names, in source order
	Unsafe     []ParsedUnsafe // in source order
}

// ParsedImport is one import line: resolve.ImportLine without FileIDs or
// symbols. Node is the import item's node ID in the file's AST.
type ParsedImport struct {
	Binding            string // the alias, else the last path segment
	Path               string // as written, with '.'
	Node               uint32
	Start, End         uint32 // the import item
	PathStart, PathEnd uint32 // the path
}

// ParsedUnsafe is one unsafe block or unsafe impl: the span of its unsafe
// keyword, and which of the two it is.
type ParsedUnsafe struct {
	Start, End uint32
	Impl       bool
}

// ParsedItem is one item of a parse summary.
type ParsedItem struct {
	Name   string
	Key    AnchorKey
	Anchor Anchor
}

func writeAnchor(e *Encoder, a *Anchor) {
	e.String(a.Path)
	e.Hash(a.TokenHash)
	e.Uvarint(uint64(len(a.Starts)))
	prev := uint32(0)
	for i := range a.Starts {
		e.Uvarint(uint64(a.Starts[i] - prev))
		e.Uvarint(uint64(a.Ends[i] - a.Starts[i]))
		prev = a.Starts[i]
	}
}

func readAnchor(d *Decoder) Anchor {
	a := Anchor{Path: d.String(), TokenHash: d.Hash()}
	n := d.Count(2)
	prev := uint64(0)
	for i := 0; i < n && d.Err == nil; i++ {
		s := prev + d.Uvarint()
		l := d.Uvarint()
		if s+l > 0xffffffff || (i > 0 && s == prev) {
			d.Failf("anchor offsets")
			break
		}
		a.Starts = append(a.Starts, uint32(s))
		a.Ends = append(a.Ends, uint32(s+l))
		prev = s
	}
	return a
}

// EncodeParseSummary writes a summary. Starts are delta-encoded and ends
// written as token lengths.
func EncodeParseSummary(p *ParseSummary) []byte {
	var e Encoder
	e.Uvarint(FormatVersion)
	e.Hash(p.ContentHash)
	e.Hash(p.ImportsHash)
	e.Uvarint(uint64(len(p.Items)))
	for i := range p.Items {
		it := &p.Items[i]
		if len(it.Anchor.Starts) != len(it.Anchor.Ends) {
			source.ICEf(source.Span{}, "iface: parse summary anchor %s is ragged", it.Key)
		}
		e.String(it.Name)
		e.String(string(it.Key))
		writeAnchor(&e, &it.Anchor)
	}
	writeAnchor(&e, &p.FileAnchor)
	e.Uvarint(uint64(len(p.Imports)))
	for _, im := range p.Imports {
		e.String(im.Binding)
		e.String(im.Path)
		e.Uvarint(uint64(im.Node))
		writeRange(&e, im.Start, im.End)
		writeRange(&e, im.PathStart, im.PathEnd)
	}
	e.Bool(p.Main)
	e.Uvarint(uint64(len(p.Exports)))
	for _, x := range p.Exports {
		e.String(x)
	}
	e.Uvarint(uint64(len(p.Unsafe)))
	for _, u := range p.Unsafe {
		writeRange(&e, u.Start, u.End)
		e.Bool(u.Impl)
	}
	return e.Out()
}

// writeRange writes a byte range as its start and length.
func writeRange(e *Encoder, start, end uint32) {
	if end < start {
		source.ICEf(source.Span{}, "iface: parse summary range %d..%d", start, end)
	}
	e.Uvarint(uint64(start))
	e.Uvarint(uint64(end - start))
}

func readRange(d *Decoder) (uint32, uint32) {
	s, l := d.Uvarint(), d.Uvarint()
	if s+l > 0xffffffff {
		d.Failf("parse summary range")
		return 0, 0
	}
	return uint32(s), uint32(s + l)
}

// DecodeParseSummary reads a summary. Its anchors have no File: Bind sets it.
func DecodeParseSummary(b []byte) (*ParseSummary, error) {
	d := NewDecoder(b)
	if v := d.Uvarint(); d.Err == nil && v != FormatVersion {
		return nil, fmt.Errorf("%w: format version %d", ErrCorrupt, v)
	}
	p := &ParseSummary{ContentHash: d.Hash(), ImportsHash: d.Hash()}
	n := d.Count(36)
	for i := 0; i < n && d.Err == nil; i++ {
		p.Items = append(p.Items, ParsedItem{Name: d.String(), Key: AnchorKey(d.String()), Anchor: readAnchor(d)})
	}
	p.FileAnchor = readAnchor(d)
	n = d.Count(4)
	for i := 0; i < n && d.Err == nil; i++ {
		im := ParsedImport{Binding: d.String(), Path: d.String()}
		node := d.Uvarint()
		if node > 0xffffffff {
			d.Failf("parse summary node")
			break
		}
		im.Node = uint32(node)
		im.Start, im.End = readRange(d)
		im.PathStart, im.PathEnd = readRange(d)
		p.Imports = append(p.Imports, im)
	}
	p.Main = d.Bool()
	n = d.Count(1)
	for i := 0; i < n && d.Err == nil; i++ {
		p.Exports = append(p.Exports, d.String())
	}
	n = d.Count(3)
	for i := 0; i < n && d.Err == nil; i++ {
		var u ParsedUnsafe
		u.Start, u.End = readRange(d)
		u.Impl = d.Bool()
		p.Unsafe = append(p.Unsafe, u)
	}
	if err := d.Done(); err != nil {
		return nil, err
	}
	return p, nil
}

// Bind sets the File of every anchor of p from sm by its path; false when
// the path is not loaded.
func (p *ParseSummary) Bind(sm *source.SourceMap) bool {
	f, ok := sm.ByPath(p.FileAnchor.Path)
	if !ok {
		return false
	}
	p.FileAnchor.File = f.ID
	for i := range p.Items {
		g, ok := sm.ByPath(p.Items[i].Anchor.Path)
		if !ok {
			return false
		}
		p.Items[i].Anchor.File = g.ID
	}
	return true
}

// MaxCandidates bounds a manifest; the oldest candidate is dropped first
// (a chosen bound, not measured).
const MaxCandidates = 8

// Manifest is the candidate list under one lookup prefix.
type Manifest struct {
	Candidates []Candidate
}

// Candidate is one earlier result: the reads it was valid under (all must
// hold against the current World) and the key of its payload entry.
type Candidate struct {
	Reads []types.Read
	Entry source.Hash
}

// sameReads compares two read lists as sets.
func sameReads(a, b []types.Read) bool {
	var sa, sb types.ReadSet
	for _, r := range a {
		sa.Add(r)
	}
	for _, r := range b {
		sb.Add(r)
	}
	return slices.Equal(sa.Sorted(), sb.Sorted())
}

// Add inserts c at the front, dropping a candidate with equal reads and then
// the oldest beyond MaxCandidates. Deterministic for a given sequence of Adds.
func (m *Manifest) Add(c Candidate) {
	out := []Candidate{c}
	for _, o := range m.Candidates {
		if !sameReads(o.Reads, c.Reads) {
			out = append(out, o)
		}
	}
	if len(out) > MaxCandidates {
		out = out[:MaxCandidates]
	}
	m.Candidates = out
}

// EncodeManifest writes the candidates in order, each read list sorted.
func EncodeManifest(m *Manifest) []byte {
	var e Encoder
	e.Uvarint(FormatVersion)
	e.Uvarint(uint64(len(m.Candidates)))
	for _, c := range m.Candidates {
		writeReads(&e, c.Reads)
		e.Hash(c.Entry)
	}
	return e.Out()
}

// DecodeManifest reads EncodeManifest's output.
func DecodeManifest(b []byte) (*Manifest, error) {
	d := NewDecoder(b)
	if v := d.Uvarint(); d.Err == nil && v != FormatVersion {
		return nil, fmt.Errorf("%w: format version %d", ErrCorrupt, v)
	}
	m := &Manifest{}
	n := d.Count(33)
	if n > MaxCandidates {
		d.Failf("%d candidates", n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		m.Candidates = append(m.Candidates, Candidate{Reads: readReads(d), Entry: d.Hash()})
	}
	if err := d.Done(); err != nil {
		return nil, err
	}
	return m, nil
}
