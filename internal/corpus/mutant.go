// Why: a soundness test suite is only trusted if breaking a borrow rule makes
// it fail. Mutants are deliberate breaks of the checker, one rule each, that
// the suite must catch.
//
// What: Mutant and MutantPatch, ReadMutants (the tables
// tests/soundness/mutants.txt and mutants-flows8.txt), ApplyMutant and
// ApplyPatch, and MutantRules, the rules the table must cover.
//
// How: a mutant is a text patch (an anchor that occurs exactly once, and its
// replacement) of one internal/borrow or internal/check file, applied only
// through go test -overlay in a child test binary, never in the tree. A
// field-level row names the witness files under tests/soundness/witness/ it
// must flip, and may patch more files with also triples, to show that two
// rules which each close the same witnesses are needed together.

package corpus

import (
	"bufio"
	"fmt"
	"strings"
)

// Mutant is one row of tests/soundness/mutants.txt.
type Mutant struct {
	ID      string // "M-E0501", "M-R6-last-use"
	Rule    string // "R5", "R6", or several: "R3 R4"
	Class   byte   // 'R' restrictive, 'P' permissive
	File    string // internal/borrow/<file>.go
	Anchor  string // text that occurs exactly once in File
	Replace string // its replacement
	Note    string
	// Pending names a row whose site is not anchored yet: it counts for
	// the coverage of its rule, and TestMutant_TableFresh lists it instead
	// of applying it.
	Pending string
	// Witness names, space-separated, the files of tests/soundness/witness/
	// the mutant must flip (a flows8 row is judged on its witnesses, not on
	// the generator's sample); empty for every other row.
	Witness string
	// Also are further patches applied with the first, each from an
	// `also-file`, `also-anchor`, `also-replace` triple; only a witness
	// row has them.
	Also []MutantPatch
}

// MutantPatch is one text patch of a row: File's Anchor, which occurs
// exactly once, becomes Replace.
type MutantPatch struct {
	File, Anchor, Replace string
}

// Patches are the row's patches, its first one ahead of Also.
func (m Mutant) Patches() []MutantPatch {
	return append([]MutantPatch{{m.File, m.Anchor, m.Replace}}, m.Also...)
}

// Witnesses are the witness files the row names.
func (m Mutant) Witnesses() []string { return strings.Fields(m.Witness) }

// Rules are the rule IDs the row names.
func (m Mutant) Rules() []string { return strings.Fields(strings.ReplaceAll(m.Rule, ",", " ")) }

// ReadMutants parses the table: blocks of `key value` lines from `mutant
// <id>` to `end`, # comments and blank lines between them. anchor and
// replace take \n, \t and \\ escapes.
func ReadMutants(path string, data []byte) ([]Mutant, error) {
	var out []Mutant
	var cur *Mutant
	sc := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(string(data), "\r\n", "\n")))
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	line := 0
	seen := map[string]bool{}
	for sc.Scan() {
		line++
		t := sc.Text()
		if strings.TrimSpace(t) == "" || strings.HasPrefix(strings.TrimSpace(t), "#") {
			continue
		}
		key, val, _ := strings.Cut(t, " ")
		at := fmt.Sprintf("%s:%d", path, line)
		if cur == nil {
			if key != "mutant" || val == "" {
				return nil, fmt.Errorf("%s: want `mutant <id>`, got %q", at, t)
			}
			if seen[val] {
				return nil, fmt.Errorf("%s: mutant %s twice", at, val)
			}
			seen[val] = true
			cur = &Mutant{ID: val}
			continue
		}
		switch key {
		case "rule":
			cur.Rule = val
		case "class":
			if val != "R" && val != "P" {
				return nil, fmt.Errorf("%s: class %q, want R or P", at, val)
			}
			cur.Class = val[0]
		case "file":
			cur.File = val
		case "anchor":
			cur.Anchor = unescapeMutant(val)
		case "replace":
			cur.Replace = unescapeMutant(val)
		case "note":
			cur.Note = val
		case "pending":
			cur.Pending = val
		case "witness":
			cur.Witness = val
		case "also-file":
			cur.Also = append(cur.Also, MutantPatch{File: val})
		case "also-anchor", "also-replace":
			if len(cur.Also) == 0 {
				return nil, fmt.Errorf("%s: mutant %s: %s before also-file", at, cur.ID, key)
			}
			p := &cur.Also[len(cur.Also)-1]
			if key == "also-anchor" {
				p.Anchor = unescapeMutant(val)
			} else {
				p.Replace = unescapeMutant(val)
			}
		case "end":
			if cur.Rule == "" || cur.Class == 0 || cur.File == "" {
				return nil, fmt.Errorf("%s: mutant %s lacks rule, class or file", at, cur.ID)
			}
			if cur.Pending == "" && (cur.Anchor == "" || cur.Anchor == cur.Replace) {
				return nil, fmt.Errorf("%s: mutant %s has no anchor, or a replacement equal to it", at, cur.ID)
			}
			if len(cur.Also) > 0 && (cur.Witness == "" || cur.Pending != "") {
				return nil, fmt.Errorf("%s: mutant %s has also patches but no witnesses, or is pending", at, cur.ID)
			}
			for _, p := range cur.Also {
				if p.File == "" || p.Anchor == "" || p.Anchor == p.Replace {
					return nil, fmt.Errorf("%s: mutant %s: an also patch lacks its file or anchor, or its replacement equals it", at, cur.ID)
				}
			}
			out = append(out, *cur)
			cur = nil
		default:
			return nil, fmt.Errorf("%s: unknown key %q", at, key)
		}
	}
	if cur != nil {
		return nil, fmt.Errorf("%s: mutant %s has no end", path, cur.ID)
	}
	return out, sc.Err()
}

// unescapeMutant reads \n, \t and \\.
func unescapeMutant(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case 't':
				b.WriteByte('\t')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ApplyMutant returns src with m's first patch applied, or an error naming
// the anchor's count when it is not exactly one.
func ApplyMutant(src []byte, m Mutant) ([]byte, error) {
	if m.Pending != "" {
		return nil, fmt.Errorf("mutant %s is pending: %s", m.ID, m.Pending)
	}
	return ApplyPatch(src, m.ID, m.Patches()[0])
}

// ApplyPatch returns src with p, a patch of row id, applied, or an error
// naming the anchor's count when it is not exactly one.
func ApplyPatch(src []byte, id string, p MutantPatch) ([]byte, error) {
	s := strings.ReplaceAll(string(src), "\r\n", "\n")
	if n := strings.Count(s, p.Anchor); n != 1 {
		return nil, fmt.Errorf("mutant %s: its anchor occurs %d times in %s, want exactly once", id, n, p.File)
	}
	return []byte(strings.Replace(s, p.Anchor, p.Replace, 1)), nil
}

// MutantRules are the borrow rules the table must cover
// (TestMutant_Coverage), by the names its Rule column uses: R2 to R18, R20
// and R21. R1's kinds, R9 and R10's
// signature half and R19 are check's (and the driver's).
var MutantRules = []string{"R2", "R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10", "R11", "R12", "R13", "R14", "R15", "R16", "R17", "R18", "R20", "R21"}
