// Why: Clang's JSON AST gives each type only as a printed spelling (its
// `qualType` string), and the binding needs the type's structure to map it
// to Mk.
//
// What: ctype, the parsed C type (tkind), lexType and parseType, a C
// abstract declarator parser over spellings such as `int (*)(void *, int)`,
// `const char *`, `struct S[4]` and `union (unnamed union at a.h:2:54)`,
// and ctype.String.
//
// How: follows the C11 grammar's 6.7.7 "Type names": specifiers first
// (builtin words canonicalized, no-op calling-convention attributes
// dropped), then pointers, parenthesised declarators, and array and
// function suffixes, building the type inside out.

package cimport

import (
	"strconv"
	"strings"
)

// tkind is the kind of a parsed C type.
type tkind uint8

const (
	tBad     tkind = iota // a spelling this parser does not read
	tBuiltin              // name is the canonical builtin spelling
	tPointer              // elem
	tArray                // elem, n; n < 0 for an incomplete array
	tFunc                 // ret, params, variadic
	tRecord               // name is the tag ("" for unnamed: anon holds the spelling)
	tEnum                 // name is the tag
	tTypedef              // name
)

// ctype is one parsed C type, qualifiers dropped.
type ctype struct {
	kind     tkind
	name     string // builtin spelling, tag, or typedef name
	union    bool   // tRecord: a union
	anon     string // tRecord or tEnum with no tag: the "(unnamed ... at F:L:C)" text
	elem     *ctype
	n        int64
	ret      *ctype
	params   []*ctype
	variadic bool
}

// builtinWords are the specifier words that make up a builtin type.
var builtinWords = map[string]bool{
	"void": true, "char": true, "short": true, "int": true, "long": true, "float": true, "double": true,
	"signed": true, "unsigned": true, "_Bool": true, "bool": true, "_Complex": true, "__int128": true,
	"_Float16": true, "__fp16": true, "__bf16": true, "__float128": true, "_BitInt": true,
}

// qualWords are dropped wherever they appear.
var qualWords = map[string]bool{
	"const": true, "volatile": true, "restrict": true, "__restrict": true, "_Atomic": true,
	"__unaligned": true, "__ptr32": true, "__ptr64": true, "__sptr": true, "__uptr": true,
	"_Nonnull": true, "_Nullable": true, "_Null_unspecified": true, "__w64": true,
}

// ctok is one token of a type spelling.
type ctok struct {
	s    string
	word bool
}

// lexType splits a spelling into words, numbers and punctuation; a
// parenthesised "(unnamed ...)" or "(anonymous ...)" is one word, and a
// "S::" qualifier before it is kept with it.
func lexType(s string) []ctok {
	var out []ctok
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '(' && (strings.HasPrefix(s[i:], "(unnamed") || strings.HasPrefix(s[i:], "(anonymous")):
			depth, j := 0, i
			for ; j < len(s); j++ {
				if s[j] == '(' {
					depth++
				} else if s[j] == ')' {
					depth--
					if depth == 0 {
						j++
						break
					}
				}
			}
			out = append(out, ctok{s: s[i:j], word: true})
			i = j
		case isIdentByte(c):
			j := i
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			if strings.HasPrefix(s[j:], "::") {
				// "S::(unnamed at ...)" or "S::T": the qualifier names the
				// parent; the part after it is the token.
				i = j + 2
				continue
			}
			out = append(out, ctok{s: s[i:j], word: true})
			i = j
		case c == '.' && strings.HasPrefix(s[i:], "..."):
			out = append(out, ctok{s: "..."})
			i += 3
		default:
			out = append(out, ctok{s: string(c)})
			i++
		}
	}
	return out
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// typeParser is a recursive-descent reader over lexType's tokens.
type typeParser struct {
	toks []ctok
	i    int
	bad  bool
}

// parseType reads one C type name; tBad when it cannot.
func parseType(s string) *ctype {
	p := &typeParser{toks: lexType(dropNoOpAttrs(s))}
	t := p.typeName()
	if p.bad || p.i != len(p.toks) || t == nil {
		return &ctype{kind: tBad, name: s}
	}
	return t
}

// noOpAttrs are the calling-convention attributes Clang prints in a
// function type that x86_64 ignores (one convention per OS there: Clang
// says "calling convention ignored for this target"). vectorcall and the
// ms_abi/sysv_abi pair change the ABI, so they stay and the type is not
// read.
var noOpAttrs = []string{" __attribute__((stdcall))", " __attribute__((cdecl))", " __attribute__((fastcall))",
	" __attribute__((thiscall))", "__attribute__((stdcall)) ", "__attribute__((cdecl)) ", "__attribute__((fastcall)) ",
	"__attribute__((thiscall)) "}

// dropNoOpAttrs removes noOpAttrs from a spelling.
func dropNoOpAttrs(s string) string {
	if !strings.Contains(s, "__attribute__") {
		return s
	}
	for _, a := range noOpAttrs {
		s = strings.ReplaceAll(s, a, "")
	}
	return s
}

func (p *typeParser) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i].s
	}
	return ""
}

func (p *typeParser) next() string {
	s := p.peek()
	p.i++
	return s
}

// typeName is specifiers then an abstract declarator.
func (p *typeParser) typeName() *ctype {
	base := p.specifiers()
	if base == nil {
		p.bad = true
		return nil
	}
	return p.abstract(base)
}

// specifiers reads the declaration specifiers: a builtin word list, a
// struct, union or enum tag, or a typedef name.
func (p *typeParser) specifiers() *ctype {
	var words []string
	var t *ctype
	for p.i < len(p.toks) {
		tk := p.toks[p.i]
		if !tk.word {
			break
		}
		switch {
		case qualWords[tk.s]:
			p.i++
		case builtinWords[tk.s]:
			if t != nil {
				p.bad = true
				return nil
			}
			words = append(words, tk.s)
			p.i++
		case tk.s == "struct" || tk.s == "union" || tk.s == "enum":
			if t != nil || words != nil {
				p.bad = true
				return nil
			}
			p.i++
			tag := p.next()
			t = &ctype{kind: tRecord, union: tk.s == "union"}
			if tk.s == "enum" {
				t.kind = tEnum
			}
			if strings.HasPrefix(tag, "(") || strings.Contains(tag, "::(") {
				t.anon = anonLoc(tag)
			} else if strings.Contains(tag, "::") {
				t.name = tag[strings.LastIndex(tag, "::")+2:]
			} else {
				t.name = tag
			}
		default:
			if t != nil || words != nil {
				return p.finish(words, t)
			}
			if strings.HasPrefix(tk.s, "(") {
				p.bad = true
				return nil
			}
			t = &ctype{kind: tTypedef, name: tk.s}
			p.i++
		}
	}
	return p.finish(words, t)
}

func (p *typeParser) finish(words []string, t *ctype) *ctype {
	if t != nil {
		return t
	}
	if words == nil {
		return nil
	}
	return &ctype{kind: tBuiltin, name: canonBuiltin(words)}
}

// anonLoc is the "F:L:C" of an "(unnamed struct at F:L:C)" spelling, the
// key an unnamed record is found by; "" when it has none.
func anonLoc(s string) string {
	if k := strings.Index(s, " at "); k >= 0 {
		return strings.TrimSuffix(s[k+4:], ")")
	}
	return ""
}

// canonBuiltin orders a builtin's words: "unsigned long long", "long
// double", "signed char", and "int" spelled out where C leaves it implied.
func canonBuiltin(words []string) string {
	count := map[string]int{}
	for _, w := range words {
		count[w]++
	}
	switch {
	case count["void"] > 0:
		return "void"
	case count["_Bool"] > 0 || count["bool"] > 0:
		return "_Bool"
	case count["_Complex"] > 0:
		return "_Complex"
	case count["__int128"] > 0:
		if count["unsigned"] > 0 {
			return "unsigned __int128"
		}
		return "__int128"
	case count["float"] > 0:
		return "float"
	case count["double"] > 0:
		if count["long"] > 0 {
			return "long double"
		}
		return "double"
	case count["_Float16"] > 0 || count["__fp16"] > 0 || count["__bf16"] > 0 || count["__float128"] > 0 || count["_BitInt"] > 0:
		return "unsupported"
	case count["char"] > 0:
		if count["unsigned"] > 0 {
			return "unsigned char"
		}
		if count["signed"] > 0 {
			return "signed char"
		}
		return "char"
	}
	sign := ""
	if count["unsigned"] > 0 {
		sign = "unsigned "
	}
	switch {
	case count["short"] > 0:
		return sign + "short"
	case count["long"] >= 2:
		return sign + "long long"
	case count["long"] == 1:
		return sign + "long"
	}
	return sign + "int"
}

// abstract reads an abstract declarator around base (C11 6.7.7):
// pointers, then a direct declarator of parenthesised declarators, array
// and function suffixes. It builds the type inside out.
func (p *typeParser) abstract(base *ctype) *ctype {
	ptrs := 0
	for p.peek() == "*" || qualWords[p.peek()] {
		if p.next() == "*" {
			ptrs++
		}
	}
	t := base
	for ; ptrs > 0; ptrs-- {
		t = &ctype{kind: tPointer, elem: t}
	}
	// A parenthesised inner declarator binds tighter than the suffixes
	// after it: "int (*)(void)" is a pointer to a function.
	var inner []ctok
	if p.peek() == "(" && p.isInnerDeclarator() {
		p.i++
		start := p.i
		depth := 1
		for p.i < len(p.toks) && depth > 0 {
			switch p.toks[p.i].s {
			case "(":
				depth++
			case ")":
				depth--
			}
			p.i++
		}
		if depth != 0 {
			p.bad = true
			return nil
		}
		inner = p.toks[start : p.i-1]
	}
	t = p.suffixes(t)
	if p.bad {
		return nil
	}
	if inner != nil {
		q := &typeParser{toks: inner}
		t = q.abstract(t)
		if q.bad || q.i != len(q.toks) {
			p.bad = true
			return nil
		}
	}
	return t
}

// isInnerDeclarator reports whether the "(" at p.i opens a nested
// declarator ("(*)", "(*[4])") rather than a parameter list.
func (p *typeParser) isInnerDeclarator() bool {
	if p.i+1 >= len(p.toks) {
		return false
	}
	nx := p.toks[p.i+1].s
	return nx == "*" || nx == "(" || nx == "["
}

// suffixes reads array and function suffixes, left to right: "T [2][3]" is
// an array of 2 arrays of 3, so the suffixes apply right to left.
func (p *typeParser) suffixes(base *ctype) *ctype {
	type suffix struct {
		array    bool
		n        int64
		params   []*ctype
		variadic bool
	}
	var sfx []suffix
	for {
		switch p.peek() {
		case "[":
			p.i++
			n := int64(-1)
			if p.peek() != "]" {
				v, err := strconv.ParseInt(p.next(), 10, 64)
				if err != nil {
					p.bad = true
					return nil
				}
				n = v
			}
			if p.next() != "]" {
				p.bad = true
				return nil
			}
			sfx = append(sfx, suffix{array: true, n: n})
		case "(":
			p.i++
			var s suffix
			if p.peek() == ")" {
				p.i++
				sfx = append(sfx, s)
				continue
			}
			for {
				if p.peek() == "..." {
					p.i++
					s.variadic = true
				} else {
					pt := p.typeName()
					if p.bad {
						return nil
					}
					s.params = append(s.params, pt)
				}
				if p.peek() == "," {
					p.i++
					continue
				}
				break
			}
			if p.next() != ")" {
				p.bad = true
				return nil
			}
			// "(void)" is no parameters.
			if len(s.params) == 1 && s.params[0].kind == tBuiltin && s.params[0].name == "void" {
				s.params = nil
			}
			sfx = append(sfx, s)
		default:
			t := base
			for k := len(sfx) - 1; k >= 0; k-- {
				s := sfx[k]
				if s.array {
					t = &ctype{kind: tArray, elem: t, n: s.n}
				} else {
					t = &ctype{kind: tFunc, ret: t, params: s.params, variadic: s.variadic}
				}
			}
			return t
		}
	}
}

// String spells t back in C, for comments and tests.
func (t *ctype) String() string {
	if t == nil {
		return "<nil>"
	}
	switch t.kind {
	case tBuiltin, tTypedef:
		return t.name
	case tRecord:
		kw := "struct "
		if t.union {
			kw = "union "
		}
		if t.name == "" {
			return kw + "(unnamed at " + t.anon + ")"
		}
		return kw + t.name
	case tEnum:
		if t.name == "" {
			return "enum (unnamed at " + t.anon + ")"
		}
		return "enum " + t.name
	case tPointer:
		return t.elem.String() + " *"
	case tArray:
		if t.n < 0 {
			return t.elem.String() + "[]"
		}
		return t.elem.String() + "[" + strconv.FormatInt(t.n, 10) + "]"
	case tFunc:
		var ps []string
		for _, q := range t.params {
			ps = append(ps, q.String())
		}
		if t.variadic {
			ps = append(ps, "...")
		}
		return t.ret.String() + " (" + strings.Join(ps, ", ") + ")"
	}
	return "<bad " + t.name + ">"
}
