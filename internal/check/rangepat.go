// Why: range patterns (`0..=9`, `'a'..='z'`) need typed ends and an
// interval form that exhaustiveness can count over.
//
// What: rangePat (types a range pattern, E0315 for an empty range),
// rangeKeys, litKey, ivDomain, ivSigned, ivText and rangeEndText.
//
// How: each end is typed as a literal of the scrutinee's integer or char
// type and mapped to a uint64 key; exhaust.go counts intervals of keys over
// the domain: integers by width and sign, char over the Unicode scalar
// values without the surrogates.

package check

import (
	"strconv"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// A key orders the values of an interval type as unsigned 64-bit numbers:
// an unsigned integer is itself, a signed one has its sign bit flipped, a
// char is its scalar value. Intervals are inclusive [lo, hi] in keys, so
// the largest value of a 64-bit type needs no 65th bit.

// ivDomain is the interval type of t: its pieces of keys, in order (one for
// an integer type, two for char, around the surrogates); ok is false for
// any other type.
func (u *unit) ivDomain(t types.TypeID) (pieces [][2]uint64, ok bool) {
	ty := u.tab.Get(t)
	switch {
	case ty.Kind == types.KChar:
		return [][2]uint64{{0, 0xD7FF}, {0xE000, 0x10FFFF}}, true
	case ty.IsInteger():
		bits := uint(ty.Bits)
		if ty.Kind == types.KIntPtr || bits == 0 || bits > 64 {
			bits = 64 // int and uint are 64-bit on every supported target
		}
		if ty.Signed {
			half := uint64(1) << (bits - 1)
			return [][2]uint64{{1<<63 - half, 1<<63 + half - 1}}, true
		}
		max := ^uint64(0)
		if bits < 64 {
			max = 1<<bits - 1
		}
		return [][2]uint64{{0, max}}, true
	}
	return nil, false
}

// ivSigned reports whether t's keys flip the sign bit.
func (u *unit) ivSigned(t types.TypeID) bool {
	ty := u.tab.Get(t)
	return ty.IsInteger() && ty.Signed
}

// litKey is the key of a literal pattern's value in t's domain; ok is false
// when the literal has no value of t (an error check reports elsewhere).
func (u *unit) litKey(l *ast.LiteralPattern, t types.TypeID) (uint64, bool) {
	if l == nil || l.Lit == nil {
		return 0, false
	}
	switch l.Lit.Kind {
	case ast.TokInt:
		if !u.isInt(t) {
			return 0, false
		}
		v, ok := parseIntLit(l.Lit.Raw)
		if !ok {
			return 0, false
		}
		if l.Neg {
			v = -v
		}
		if u.ivSigned(t) {
			v ^= 1 << 63
		}
		return v, true
	case ast.TokChar:
		if u.tab.Kind(t) != types.KChar {
			return 0, false
		}
		r, ok := litChar(l.Lit.Raw)
		return uint64(r), ok
	case ast.TokByte:
		if t != types.U8 {
			return 0, false
		}
		r, ok := litChar(l.Lit.Raw)
		return uint64(r), ok
	}
	return 0, false
}

// ivText spells key k of t as a literal.
func (u *unit) ivText(k uint64, t types.TypeID) string {
	switch {
	case u.tab.Kind(t) == types.KChar:
		r := rune(k)
		if r > ' ' && r < 0x7F && r != '\\' && r != '\'' {
			return "'" + string(r) + "'"
		}
		return `'\u{` + strconv.FormatUint(k, 16) + `}'`
	case u.ivSigned(t):
		return strconv.FormatInt(int64(k^1<<63), 10)
	}
	return strconv.FormatUint(k, 10)
}

// rangeKeys is the inclusive key interval a range pattern matches in t; ok
// is false when an end has no value of t or the range is empty (both
// reported by rangePat).
func (u *unit) rangeKeys(p *ast.RangePattern, t types.TypeID) (lo, hi uint64, ok bool) {
	pieces, ok := u.ivDomain(t)
	if !ok {
		return 0, 0, false
	}
	lo, hi = pieces[0][0], pieces[len(pieces)-1][1]
	if p.Lo != nil {
		if lo, ok = u.litKey(p.Lo, t); !ok {
			return 0, 0, false
		}
	}
	if p.Hi != nil {
		h, ok := u.litKey(p.Hi, t)
		if !ok || h <= lo {
			return 0, 0, false
		}
		hi = h - 1
	}
	return lo, hi, lo <= hi
}

// rangePat types a range pattern against t: t is an integer type
// or char, each end a literal of it (an Int, '-' allowed, for an integer;
// a Char for char), and lo below hi, else E0315 empty-range. An open end
// reaches t's limit.
func (u *unit) rangePat(p *ast.RangePattern, t types.TypeID) {
	if t == types.Poison {
		return
	}
	if _, ok := u.ivDomain(t); !ok {
		u.patMismatch(p, "an integer or `char`", t)
		return
	}
	isChar := u.tab.Kind(t) == types.KChar
	good := true
	for _, end := range []*ast.LiteralPattern{p.Lo, p.Hi} {
		if end == nil || end.Lit == nil {
			continue
		}
		u.setType(end, t)
		if (end.Lit.Kind == ast.TokChar) != isChar {
			kind := "an integer"
			if isChar {
				kind = "a `char`"
			}
			u.err(diag.E0301, end.Span(), "a range end of `"+u.str(t)+"` is "+kind+" literal").Text("expected", u.str(t)).Text("found", "literal")
			good = false
			continue
		}
		before := len(u.b.Diags)
		lt := u.literal(end.Lit, end.Neg, t)
		u.setType(end.Lit, lt)
		if c := u.b.Consts; c != nil && end.Neg {
			u.b.setConst(end.ID(), c[u.b.slot(end.Lit.ID())])
		}
		if len(u.b.Diags) > before || lt != t {
			good = false
		}
	}
	if !good {
		return
	}
	if _, _, ok := u.rangeKeys(p, t); !ok {
		lo, hi := "the type's least value", "the type's limit"
		if p.Lo != nil {
			lo = rangeEndText(p.Lo)
		}
		if p.Hi != nil {
			hi = rangeEndText(p.Hi)
		}
		u.err(diag.E0315, p.Span(), "the range is empty: "+lo+" is not below "+hi).
			Text("lo", lo).Text("hi", hi).
			Alt("ranges are half-open, `lo <= x < hi`: write a `hi` above `lo`")
	}
}

// rangeEndText is a range end as written.
func rangeEndText(l *ast.LiteralPattern) string {
	if l.Neg {
		return "-" + l.Lit.Raw
	}
	return l.Lit.Raw
}
