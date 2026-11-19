// Why: check (judging a format literal), lower (building the call) and mono
// (judging a forwarded format per instance) must read Print and Arena.Fmt
// format strings the same way; one shared reader keeps them in step.
//
// What: Scan (holes and literal pieces), Spec and parseSpec
// ([[fill]align][+][0][width][.N][type]), Encode (the FmtPiece.spec word),
// Match (holes against the argument's fields) and Args (the argument array
// layout), with Padded and SpecTargets.
//
// How: the package imports nothing of the compiler. A caller describes its
// argument type's fields through Field values and a Sub callback, since each
// of the three substitutes generic field types its own way. Problems are
// reported in a fixed order, so all three callers give the same diagnostic.

package fmtspec

import "strings"

// Class is what a hole's spec needs of a field's type.
type Class uint8

const (
	// Other is any type but an integer or a float.
	Other Class = iota
	// Int is an integer type.
	Int
	// Float is f32 or f64.
	Float
)

// Spec is a hole's spec, decoded: [[fill]align][+][0][width][.N][type].
// The zero Spec with Prec -1 is the default form.
type Spec struct {
	// Type is 0, or the radix letter 'x', 'X', 'b' or 'o'.
	Type byte
	// Prec is N of .N, or -1 for none.
	Prec int
	// Width is 1 to 255, or 0 for none.
	Width int
	// Fill is the pad byte as written, 0 for none (a space).
	Fill byte
	// Align is '<', '>', '^', or 0 for the default (by the field's Class).
	Align byte
	// Sign is +: a sign on a non-negative number too.
	Sign bool
	// Zero is 0: zero digits after the sign up to Width.
	Zero bool
}

// Value reports whether s needs the spec-aware value function
// (FmtSpecValue): a radix, a precision, a sign or zero padding. A spec of
// width, fill and align alone is the runner's.
func (s Spec) Value() bool { return s.Type != 0 || s.Prec >= 0 || s.Sign || s.Zero }

// Hole is one {path} or {path:spec} of a format string: a field name, or a
// field path such as {r.name}.
type Hole struct {
	// Path is the field path, one name per segment.
	Path []string
	Spec Spec
}

// Name is the hole's path as written, its segments joined by ".".
func (h Hole) Name() string { return strings.Join(h.Path, ".") }

// Piece is one literal run and the hole after it: Hole is an index into the
// holes Scan returned, or -1 for a trailing literal with no hole. Lit has
// "{{" and "}}" unescaped.
type Piece struct {
	Lit  []byte
	Hole int
}

// Scan finds the holes left to right and the pieces, or the
// first scan problem in byte order with the holes before it (the pieces are
// then nil). A string with no problem gives one piece per hole, then one
// for a trailing literal when it is not empty.
func Scan(f []byte) (holes []Hole, pieces []Piece, problem string) {
	var lit []byte
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case '{':
			if i+1 < len(f) && f[i+1] == '{' {
				lit = append(lit, '{')
				i++
				continue
			}
			j := i + 1
			for j < len(f) && f[j] != '}' && f[j] != '{' {
				j++
			}
			if j == len(f) || f[j] == '{' {
				return holes, nil, `lone "{"`
			}
			body, spec, hasSpec := strings.Cut(string(f[i+1:j]), ":")
			// A field path, names joined by "."; an expression is no name.
			path := strings.Split(body, ".")
			for _, s := range path {
				if !isIdent(s) {
					return holes, nil, "bad hole name"
				}
			}
			h := Hole{Path: path, Spec: Spec{Prec: -1}}
			if hasSpec {
				s, p := parseSpec(spec)
				if p != "" {
					return holes, nil, p
				}
				h.Spec = s
			}
			pieces = append(pieces, Piece{Lit: lit, Hole: len(holes)})
			holes = append(holes, h)
			lit = nil
			i = j
		case '}':
			if i+1 < len(f) && f[i+1] == '}' {
				lit = append(lit, '}')
				i++
				continue
			}
			return holes, nil, `lone "}"`
		default:
			lit = append(lit, f[i])
		}
	}
	if len(lit) > 0 {
		pieces = append(pieces, Piece{Lit: lit, Hole: -1})
	}
	return holes, pieces, ""
}

// parseSpec reads the text after a hole's ":" as
// [[fill]align][+][0][width][.N][type], positionally, the whole text:
// fill is one printable ASCII byte other than "{" and "}" (Scan ends a hole
// at the first brace) written only before an align, width 1 to 255, N 0 to
// 99, type one of x X b o. An align or a 0 needs a width, a 0 takes no align
// (it pads after the sign), and + takes no radix (a radix prints the bits,
// which have no sign).
func parseSpec(s string) (Spec, string) {
	sp := Spec{Prec: -1}
	isAlign := func(c byte) bool { return c == '<' || c == '>' || c == '^' }
	i := 0
	switch {
	case len(s) >= 2 && isAlign(s[1]) && s[0] >= 0x20 && s[0] <= 0x7E && s[0] != '{' && s[0] != '}':
		// A brace is never a fill; Scan ends the hole at it first.
		sp.Fill, sp.Align, i = s[0], s[1], 2
	case len(s) >= 1 && isAlign(s[0]):
		sp.Align, i = s[0], 1
	}
	if i < len(s) && s[i] == '+' {
		sp.Sign = true
		i++
	}
	if i < len(s) && s[i] == '0' {
		sp.Zero = true
		i++
	}
	hasWidth := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		hasWidth = true
		if sp.Width <= 255 {
			sp.Width = sp.Width*10 + int(s[i]-'0')
		}
		i++
	}
	if i < len(s) && s[i] == '.' {
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i+1 || j > i+3 {
			return sp, "unknown spec"
		}
		sp.Prec = 0
		for _, d := range []byte(s[i+1 : j]) {
			sp.Prec = sp.Prec*10 + int(d-'0')
		}
		i = j
	}
	if i < len(s) && (s[i] == 'x' || s[i] == 'X' || s[i] == 'b' || s[i] == 'o') {
		sp.Type = s[i]
		i++
	}
	switch {
	case i != len(s):
		return sp, "unknown spec"
	case hasWidth && (sp.Width < 1 || sp.Width > 255):
		return sp, "width is 1 to 255"
	case sp.Align != 0 && !hasWidth:
		return sp, "align needs a width"
	case sp.Zero && !hasWidth:
		return sp, "0 needs a width"
	case sp.Zero && sp.Align != 0:
		return sp, "0 takes no align"
	case sp.Sign && sp.Type != 0:
		return sp, "+ takes no radix"
	}
	return sp, ""
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// The FmtPiece.spec word (std/core/core_fmt.mk): bits 0-3 the
// type, 4 sign, 5 zero, 6-7 align, 8-15 N (type 2 only), 16-23 width,
// 24-31 fill.
const (
	TypeDefault  = 0 // shortest digits, or the value's Format
	TypeHex      = 1 // x
	TypePrec     = 2 // .N
	TypeHexUpper = 3 // X
	TypeBin      = 4 // b
	TypeOct      = 5 // o

	FlagSign = 1 << 4
	FlagZero = 1 << 5

	AlignLeft   = 0 // bits 6-7
	AlignRight  = 1
	AlignCenter = 2
)

// Encode is the spec's FmtPiece.spec word for a field of class c. With no
// width the word has no align and no fill, so a plain hole is 0, {x:x} 1
// and {x:.N} 2 | N<<8; with a width the default align is resolved here
// (right for numbers, left for the rest), so the runner never needs the
// type, and the fill defaults to a space.
func Encode(s Spec, c Class) uint32 {
	var w uint32
	switch {
	case s.Type == 'x':
		w = TypeHex
	case s.Type == 'X':
		w = TypeHexUpper
	case s.Type == 'b':
		w = TypeBin
	case s.Type == 'o':
		w = TypeOct
	case s.Prec >= 0:
		w = TypePrec | uint32(s.Prec)<<8
	}
	if s.Sign {
		w |= FlagSign
	}
	if s.Zero {
		w |= FlagZero
	}
	if s.Width > 0 {
		a := uint32(AlignLeft)
		switch {
		case s.Align == '>', s.Align == 0 && c != Other:
			a = AlignRight
		case s.Align == '^':
			a = AlignCenter
		}
		fill := uint32(' ')
		if s.Fill != 0 {
			fill = uint32(s.Fill)
		}
		w |= a<<6 | uint32(s.Width)<<16 | fill<<24
	}
	return w
}

// Padded reports whether some hole has a width: the call then runs
// through the padding runner (FmtPadRun, FmtArenaPad); an unpadded Print
// is lowered straight-line and an unpadded Arena.Fmt runs through
// FmtArena (internal/lower/format.go), so a program that never pads links
// no padding code.
func Padded(holes []Hole) bool {
	for _, h := range holes {
		if h.Spec.Width > 0 {
			return true
		}
	}
	return false
}

// SpecTargets reports, per argument-array entry (Args's targets), whether
// it takes the spec-aware value function: some hole that reads it has a
// Spec.Value, as {n} and {n:x} share one entry.
func SpecTargets(holes []Hole, argOf []int, ntargets int) []bool {
	out := make([]bool, ntargets)
	for i, h := range holes {
		if h.Spec.Value() {
			out[argOf[i]] = true
		}
	}
	return out
}

// Field is one field a format argument type exposes, in field order: a
// record's fields in canonical order, a struct's exported fields in
// declaration order. T is the caller's type handle.
type Field[T any] struct {
	Name  string
	Index uint32 // the field's index in its type
	Type  T
	Class Class
	// Deref is set by Match on a path step whose parent value is a shared
	// reference: the step reads the field of the referent.
	Deref bool
}

// Sub lists the fields a hole's path may name inside a value of type t (a
// record's fields, a struct's exported ones; through one shared reference,
// with deref true). ok is false for a type with no fields to name.
type Sub[T any] func(t T) (fs []Field[T], deref bool, ok bool)

// Resolved is one hole matched to its field: Top is the index of the
// top-level field among the argument type's fields, Path the fields from
// it down (Path[0] the top-level one), and Field the last of them.
type Resolved[T any] struct {
	Top   int
	Path  []Field[T]
	Field Field[T]
}

// Match matches holes to fields and reports the first problem in this order: a
// hole naming no field (or a path step that names none), then a spec on the
// wrong type or a path that ends at a field without Format, then a top-level
// field no hole names (a field used only through a path is used). It returns
// each hole's field, or the problem. sub is read only for paths of two
// segments or more. isFormat is read for every hole's value: the call's
// Fields[Format] bound is judged here, by the text, so a top-level field used
// only through paths need not implement Format. With a nil isFormat a
// one-segment hole is not judged (a caller after check, which judged it) and a
// path is refused.
func Match[T any](holes []Hole, fields []Field[T], sub Sub[T], isFormat func(T) bool) ([]Resolved[T], string) {
	idx := map[string]int{}
	for i, f := range fields {
		idx[f.Name] = i
	}
	rs := make([]Resolved[T], len(holes))
	for i, h := range holes {
		k, ok := idx[h.Path[0]]
		if !ok {
			return nil, "hole names no field: " + h.Name()
		}
		r := Resolved[T]{Top: k, Path: []Field[T]{fields[k]}, Field: fields[k]}
		for s, name := range h.Path[1:] {
			if sub == nil {
				return nil, "path through a type with no fields: " + strings.Join(h.Path[:s+1], ".")
			}
			fs, deref, ok := sub(r.Field.Type)
			if !ok {
				return nil, "path through a type with no fields: " + strings.Join(h.Path[:s+1], ".")
			}
			found := false
			for _, f := range fs {
				if f.Name == name {
					f.Deref = deref
					r.Path = append(r.Path, f)
					r.Field = f
					found = true
					break
				}
			}
			if !found {
				return nil, "path names no exported field: " + strings.Join(h.Path[:s+2], ".")
			}
		}
		rs[i] = r
	}
	for i, h := range holes {
		c := rs[i].Field.Class
		switch {
		case h.Spec.Type != 0 && c != Int:
			return nil, string(h.Spec.Type) + " needs an integer field: " + h.Name()
		case h.Spec.Prec >= 0 && c != Float:
			return nil, ".N needs a float field: " + h.Name()
		case h.Spec.Sign && c == Other:
			return nil, "+ needs a number field: " + h.Name()
		case h.Spec.Zero && c == Other:
			return nil, "0 needs a number field: " + h.Name()
		case len(h.Path) > 1 && (isFormat == nil || !isFormat(rs[i].Field.Type)):
			return nil, "path ends at a field without Format: " + h.Name()
		case len(h.Path) == 1 && isFormat != nil && !isFormat(rs[i].Field.Type):
			return nil, "field without Format: " + h.Name()
		}
	}
	used := make([]bool, len(fields))
	for _, r := range rs {
		used[r.Top] = true
	}
	for i, f := range fields {
		if !used[i] {
			return nil, "field used by no hole: " + f.Name
		}
	}
	return rs, ""
}

// Args lays out a call's argument array (FmtArg per target): first
// each top-level field some one-segment hole names, in field order, then
// each longer path in the order of its first hole. argOf[i] is hole i's
// index in it. With no path the array is one entry per field, in order.
func Args[T any](holes []Hole, rs []Resolved[T], nfields int) (targets []Resolved[T], argOf []int) {
	argOf = make([]int, len(holes))
	direct := make([]int, nfields)
	for i := range direct {
		direct[i] = -1
	}
	for i, h := range holes {
		if len(h.Path) == 1 {
			direct[rs[i].Top] = 0
		}
	}
	for k := range direct {
		if direct[k] == 0 {
			direct[k] = len(targets)
			for i, h := range holes {
				if len(h.Path) == 1 && rs[i].Top == k {
					targets = append(targets, rs[i])
					break
				}
			}
		}
	}
	paths := map[string]int{}
	for i, h := range holes {
		if len(h.Path) == 1 {
			argOf[i] = direct[rs[i].Top]
			continue
		}
		n := h.Name()
		a, ok := paths[n]
		if !ok {
			a = len(targets)
			paths[n] = a
			targets = append(targets, rs[i])
		}
		argOf[i] = a
	}
	return targets, argOf
}
