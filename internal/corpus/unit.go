// Why: a design block and a conformance unit must be judged by one rule set,
// so both become the same thing, a Unit, before the judge sees them.
//
// What: Unit, UnitFile, UnitMarker and Meta (a conformance unit's header
// lines), BlockFile and PreludeFile, and BlockUnit, which turns a design
// block into a Unit.
//
// How: a design block becomes a one-file unit, block.mk, with the test
// prelude added as prelude.mk unless the verdict is parse; a diagnostic in
// the prelude counts as outside the unit. Markers are sorted by file and
// line.

package corpus

import (
	"slices"
	"strings"

	"mk/internal/diag"
)

// BlockFile is a design block's own file in its unit, and PreludeFile the test
// prelude's: "block.mk" and "prelude.mk".
const (
	BlockFile   = "block.mk"
	PreludeFile = "prelude.mk"
)

// UnitFile is one source file of a unit, LF-normalized. Path is
// unit-relative with '/': "block.mk" for a design block, "E0501.mk" for a
// file unit, "main.mk", "pkg.mk", "a/a.mk" in a directory unit.
type UnitFile struct {
	Path string
	Text string
}

// UnitMarker is one expected diagnostic in a named file of the unit; Line is
// that file's 1-based line.
type UnitMarker struct {
	File string // a UnitFile.Path
	Line int
	Code diag.Code
}

// Meta holds a conformance unit's header lines after the verdict. A design
// block has no header: its Meta has only Prelude
// set (on for ok and reject, off for parse).
type Meta struct {
	Case    string     // "// case: R1.Copies" (model/ only)
	Also    []string   // "// also: H05" (repeatable)
	Rule    []string   // "// rule: R10" (repeatable)
	Needs   diag.Phase // "// needs: types"; PhaseInternal (zero) when absent
	Prelude bool       // the test prelude is added to the unit's module
	Note    string
	// Host is "// host: x86_64-windows": the unit holds when judged for
	// that target only (a target-selected import such as std.linux), so a
	// run judging another target skips it (SKIP, never a pass); "" judges
	// it everywhere.
	Host string
}

// Unit is one judged compilation unit: a design block or a conformance unit.
// Invariants: Codes is non-empty iff Verdict is VerdictReject, sorted, no
// duplicates; Expected is sorted by (File, Line, Code), no duplicates, and
// names only files of Files; Files is sorted by Path, except that a directory
// unit's main.mk comes first. The test prelude is never one of Files: the
// compiler adds it when Meta.Prelude is set, so a diagnostic in it is outside
// the unit (rule 2).
type Unit struct {
	// Key names the unit in reports, the pending allowlist and --only:
	// "doc/reference.md:17" for a block, the repository-relative path
	// for a conformance unit.
	Key string
	// Name is the driver unit name, its virtual root and module path:
	// "doc/reference.md#17" for a block, the
	// repository-relative path for a conformance unit. Distinct per run.
	Name     string
	Verdict  Verdict
	Codes    []diag.Code
	Files    []UnitFile
	Expected []UnitMarker
	Meta     Meta
	// Block is the design block the unit was made from; nil for a
	// conformance unit.
	Block *Block
}

// ParseOnly reports whether the unit is parsed alone, with no std and no
// prelude (verdict parse).
func (u *Unit) ParseOnly() bool { return u.Verdict == VerdictParse }

// Owns reports whether path is one of the unit's own files.
func (u *Unit) Owns(path string) bool {
	return slices.ContainsFunc(u.Files, func(f UnitFile) bool { return f.Path == path })
}

// BlockUnit is the unit of a design block: its source as block.mk, one fresh
// module of its own, with the test prelude unless the verdict is parse.
// Marker lines are block.mk lines.
func BlockUnit(b *Block) *Unit {
	u := &Unit{
		Key:     b.Key(),
		Name:    b.Document + "#" + strings.TrimPrefix(b.Key(), b.Document+":"),
		Verdict: b.Verdict,
		Codes:   slices.Clone(b.Codes),
		Files:   []UnitFile{{Path: BlockFile, Text: b.Source}},
		Meta:    Meta{Prelude: b.Verdict != VerdictParse},
		Block:   b,
	}
	for _, m := range b.Expected {
		u.Expected = append(u.Expected, UnitMarker{File: BlockFile, Line: m.Line, Code: m.Code})
	}
	return u
}

// sortMarkers orders markers by (File, Line, Code) and drops duplicates.
func sortMarkers(ms []UnitMarker) []UnitMarker {
	slices.SortFunc(ms, compareMarkers)
	return slices.Compact(ms)
}

func compareMarkers(a, b UnitMarker) int {
	if c := strings.Compare(a.File, b.File); c != 0 {
		return c
	}
	if a.Line != b.Line {
		return a.Line - b.Line
	}
	return int(a.Code) - int(b.Code)
}
