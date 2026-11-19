// Why: Mk names are found exactly first and then by a folded form that
// ignores case and underscores, and the case rule's fixes need to recase
// names; check uses the same helpers for members.
//
// What: Fold, IsPascal, IsSnake, ToSnake, ToPascal, Match and Lookup.
//
// How: names are ASCII, so folding and recasing work bytewise. Lookup takes
// the first exact candidate, else lists every folded match in candidate
// order.

package resolve

import "strings"

// Fold is the case-and-underscore folded form of a name: ASCII lower case
// with every '_' removed. Names are ASCII, so bytewise.
func Fold(name string) string {
	need := false
	for i := 0; i < len(name); i++ {
		if c := name[i]; c == '_' || (c >= 'A' && c <= 'Z') {
			need = true
			break
		}
	}
	if !need {
		return name
	}
	b := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_':
		case c >= 'A' && c <= 'Z':
			b = append(b, c+('a'-'A'))
		default:
			b = append(b, c)
		}
	}
	return string(b)
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLower(c byte) bool { return c >= 'a' && c <= 'z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// IsPascal reports whether name matches [A-Z][A-Za-z0-9]*.
func IsPascal(name string) bool {
	if name == "" || !isUpper(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if c := name[i]; !isUpper(c) && !isLower(c) && !isDigit(c) {
			return false
		}
	}
	return true
}

// IsSnake reports whether name matches [a-z][a-z0-9_]*.
func IsSnake(name string) bool {
	if name == "" || !isLower(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if c := name[i]; !isLower(c) && !isDigit(c) && c != '_' {
			return false
		}
	}
	return true
}

// ToSnake recases a name to snake_case for the E0207 fix: "MaxUsers" is
// "max_users", "HTTPServer" is "http_server", "maxUsers" is "max_users". A
// word boundary is an upper-case letter after a lower-case letter or digit,
// or the last upper-case letter of a run that a lower-case letter follows.
// Existing underscores stay; a leading or doubled one is dropped.
func ToSnake(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
			continue
		}
		if isUpper(c) {
			if i > 0 && b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				prev := name[i-1]
				nextLower := i+1 < len(name) && isLower(name[i+1])
				if isLower(prev) || isDigit(prev) || (isUpper(prev) && nextLower) {
					b.WriteByte('_')
				}
			}
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return strings.TrimSuffix(b.String(), "_")
}

// ToPascal recases a name to PascalCase for the E0207 fix: "max_users" is
// "MaxUsers", "maxUsers" is "MaxUsers". Each underscore-separated word gets an
// upper-case first letter; the rest of each word is kept.
func ToPascal(name string) string {
	var b strings.Builder
	up := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' {
			up = true
			continue
		}
		if up && isLower(c) {
			c -= 'a' - 'A'
		}
		up = false
		b.WriteByte(c)
	}
	return b.String()
}

// Match is the outcome of the folded fallback over a candidate set.
type Match struct {
	Exact  int   // index of the exact match, -1 if none
	Folded []int // indices whose Fold equals Fold(name), when Exact is -1
}

// Lookup applies the name rule to one candidate list: exact first,
// then folded. check uses it for members (E0203, E0207). The first exact
// candidate wins; Folded lists every folded match in candidate order.
func Lookup(name string, candidates []string) Match {
	for i, c := range candidates {
		if c == name {
			return Match{Exact: i}
		}
	}
	m := Match{Exact: -1}
	f := Fold(name)
	for i, c := range candidates {
		if Fold(c) == f {
			m.Folded = append(m.Folded, i)
		}
	}
	return m
}
