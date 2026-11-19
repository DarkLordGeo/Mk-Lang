// Why: a binding is Mk source, so the names it gives C declarations must
// pass Mk's naming rules: PascalCase items, snake_case fields and
// parameters (E0207), no folded duplicates (E0206), and no keyword,
// universe or primitive name (E0102).
//
// What: splitWords, pascal and snake for recasing, fold for duplicate
// checks, mkWords, and nameScope with claim and fresh.
//
// How: a C function keeps its name, which is its symbol; every other name
// is split into words and recased deterministically, and a clash within a
// scope gets a suffix from fresh.

package cimport

import (
	"strconv"
	"strings"
)

// mkWords are the names a binding never declares as written (keywords,
// contextual words, universe names, primitive types).
var mkWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`import export struct enum error interface impl type const var linear view gen quote
		test extern if else for in match return break continue defer onerr fail try scope go unsafe asm mut fn is requires
		ensures invariant from into ends fuel packed self Self result naked cpu section align
		true false some none ok err fields Assert Panic Cast Trunc SizeOf AlignOf Target Plain Send Share Fields EvalArena
		i8 i16 i32 i64 u8 u16 u32 u64 int uint f32 f64 bool char str void never null nil`) {
		m[w] = true
	}
	return m
}()

// splitWords splits a C name into words at underscores and at a lower
// to upper change ("dwSize" is dw, Size; "HTTPServer" is HTTP, Server).
func splitWords(name string) []string {
	var words []string
	cur := []byte{}
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = []byte{}
		}
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' {
			flush()
			continue
		}
		if isUpperB(c) && len(cur) > 0 {
			prev := cur[len(cur)-1]
			nextLower := i+1 < len(name) && isLowerB(name[i+1])
			if isLowerB(prev) || isUpperB(prev) && nextLower {
				flush()
			}
		}
		cur = append(cur, c)
	}
	flush()
	return words
}

func isUpperB(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLowerB(c byte) bool { return c >= 'a' && c <= 'z' }
func isDigitB(c byte) bool { return c >= '0' && c <= '9' }

// pascal recases a C name for a type or constant: each word capitalized,
// an all-capitals word lowered after its first letter ("SQLITE_OK" is
// SqliteOk, "sqlite3_stmt" Sqlite3Stmt, "FILE" File). A name that would
// start with a digit, or is empty, gets a leading "C".
func pascal(name string) string {
	var b strings.Builder
	for _, w := range splitWords(name) {
		allCaps := true
		for i := 0; i < len(w); i++ {
			if isLowerB(w[i]) {
				allCaps = false
			}
		}
		for i := 0; i < len(w); i++ {
			c := w[i]
			switch {
			case i == 0 && isLowerB(c):
				c -= 'a' - 'A'
			case i > 0 && allCaps && isUpperB(c):
				c += 'a' - 'A'
			}
			b.WriteByte(c)
		}
	}
	s := b.String()
	if s == "" || !isUpperB(s[0]) {
		s = "C" + s
	}
	return s
}

// snake recases a C name for a field or parameter: words lowered and
// joined by "_" ("dwSize" is dw_size, "tm_sec" tm_sec). A name that would
// start with a digit, or is empty, gets a leading "f".
func snake(name string) string {
	var parts []string
	for _, w := range splitWords(name) {
		parts = append(parts, strings.ToLower(w))
	}
	s := strings.Join(parts, "_")
	if s == "" || !isLowerB(s[0]) {
		s = "f" + s
	}
	return s
}

// fold is resolve's folded name: case and underscores ignored.
func fold(name string) string {
	b := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' {
			continue
		}
		if isUpperB(c) {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	return string(b)
}

// nameScope hands out names that fold apart from every name it gave
// before, the rule a module's items and a type's members follow (E0206).
type nameScope struct {
	taken map[string]bool
}

func newScope() *nameScope { return &nameScope{taken: map[string]bool{}} }

// claim takes name as written, or reports that it cannot.
func (s *nameScope) claim(name string) bool {
	f := fold(name)
	if s.taken[f] || mkWords[name] {
		return false
	}
	s.taken[f] = true
	return true
}

// fresh takes base, else base+suffix, else base+suffix+2, 3, ...
func (s *nameScope) fresh(base, suffix string) string {
	if mkWords[base] && suffix == "" {
		suffix = "_"
	}
	if s.claim(base) {
		return base
	}
	if s.claim(base + suffix) {
		return base + suffix
	}
	for i := 2; ; i++ {
		n := base + suffix + strconv.Itoa(i)
		if s.claim(n) {
			return n
		}
	}
}
