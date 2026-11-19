// Why: parameter, return and function attributes carry the facts LLVM
// optimizes on (noalias, nonnull, range); a wrong or unparsable one breaks
// the build or the program.
//
// What: Attrs and its builders (Range among them) and checkAttrs, which
// rejects spellings codegen must not emit.
//
// How: attributes print inline, never as #N groups; no capture attributes,
// and no syntax newer than LLVM 21, the oldest LLVM mk supports.

package llvm

import "strconv"

type attrKind uint8

const (
	attrFlag attrKind = iota
	attrInt
	attrTyped
	attrStr
	attrRange
)

type attr struct {
	kind  attrKind
	name  string
	n     uint64
	t     Type
	value string
	// lo, hi are a range attribute's bounds (attrRange).
	lo, hi int64
}

// Attrs is an ordered attribute list, printed inline in call order (no #N
// groups). It is a value: each method returns a new list and never changes
// the receiver's, so one Attrs may be the base of several.
type Attrs struct{ items []attr }

func (a Attrs) with(x attr) Attrs {
	items := make([]attr, len(a.items), len(a.items)+1)
	copy(items, a.items)
	return Attrs{append(items, x)}
}

// Flag adds a keyword attribute: nonnull, noalias, noundef, readonly,
// zeroext, signext, immarg, nounwind, noreturn, cold, hot, inlinehint,
// uwtable, noinline, naked.
func (a Attrs) Flag(name string) Attrs { return a.with(attr{kind: attrFlag, name: name}) }

// Int adds an integer attribute: align is printed "align n", every other
// name "name(n)" (dereferenceable(16), alignstack(16)).
func (a Attrs) Int(name string, n uint64) Attrs {
	return a.with(attr{kind: attrInt, name: name, n: n})
}

// Typed adds a type attribute, printed "name(T)": sret(T), byval(T).
func (a Attrs) Typed(name string, t Type) Attrs {
	return a.with(attr{kind: attrTyped, name: name, t: t})
}

// Range adds range(T lo, hi): the value is in [lo, hi) of T's bit
// patterns, wrapping when hi <= lo as !range does (lo != hi); outside it the
// value is poison. LLVM 19 and later, so within the LLVM 21 floor.
func (a Attrs) Range(t Type, lo, hi int64) Attrs {
	return a.with(attr{kind: attrRange, name: "range", t: t, lo: lo, hi: hi})
}

// Str adds a string attribute, printed "key"="value", or "key" alone when
// value is empty ("no-builtins").
func (a Attrs) Str(key, value string) Attrs {
	return a.with(attr{kind: attrStr, name: key, value: value})
}

// Len is the number of attributes in the list.
func (a Attrs) Len() int { return len(a.items) }

// Has reports whether the list holds an attribute of that name.
func (a Attrs) Has(name string) bool {
	for _, x := range a.items {
		if x.name == name {
			return true
		}
	}
	return false
}

// Without is the list minus every attribute with one of the names, in the
// same order (codegen drops a definition's profile attributes, hot, cold and
// inlinehint, from the call sites that repeat its attributes).
func (a Attrs) Without(names ...string) Attrs {
	var items []attr
	for _, x := range a.items {
		drop := false
		for _, n := range names {
			drop = drop || x.name == n
		}
		if !drop {
			items = append(items, x)
		}
	}
	return Attrs{items}
}

// String is the attributes in order, separated by single spaces.
func (a Attrs) String() string {
	b := appendSpAttrs(nil, a)
	if len(b) == 0 {
		return ""
	}
	return string(b[1:])
}

// appendSpAttrs appends " attr" for each attribute in order: the inline
// form at a call site or in a signature, with no allocation.
func appendSpAttrs(buf []byte, a Attrs) []byte {
	for _, x := range a.items {
		buf = append(buf, ' ')
		switch x.kind {
		case attrFlag:
			buf = append(buf, x.name...)
		case attrInt:
			buf = append(buf, x.name...)
			if x.name == "align" {
				buf = append(buf, ' ')
				buf = strconv.AppendUint(buf, x.n, 10)
			} else {
				buf = append(buf, '(')
				buf = strconv.AppendUint(buf, x.n, 10)
				buf = append(buf, ')')
			}
		case attrTyped:
			buf = append(buf, x.name...)
			buf = append(buf, '(')
			buf = append(buf, x.t.String()...)
			buf = append(buf, ')')
		case attrRange:
			buf = append(buf, "range("...)
			buf = append(buf, x.t.String()...)
			buf = append(buf, ' ')
			buf = strconv.AppendInt(buf, x.lo, 10)
			buf = append(buf, ", "...)
			buf = strconv.AppendInt(buf, x.hi, 10)
			buf = append(buf, ')')
		case attrStr:
			buf = appendQuoted(buf, x.name)
			if x.value != "" {
				buf = append(buf, '=')
				buf = appendQuoted(buf, x.value)
			}
		}
	}
	return buf
}

// checkAttrs rejects the attribute spellings codegen must not emit: capture
// attributes (no signature fact proves them yet) and spellings LLVM 21 does
// not parse or has renamed; and types from another module.
func (m *Module) checkAttrs(a Attrs, what string) {
	for _, x := range a.items {
		switch x.name {
		case "nocapture", "captures", "initializes", "nneg", "dead_on_return":
			panic("llvm: " + what + ": attribute " + x.name + " is not emitted")
		}
		if x.kind == attrRange && x.lo == x.hi {
			panic("llvm: " + what + ": an empty or full range attribute")
		}
		if x.kind == attrTyped || x.kind == attrRange {
			m.checkType(x.t, what)
		}
	}
}

// sp prefixes s with a space when it is not empty.
func sp(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}
