// Why: a Mk symbol or string may hold bytes LLVM's lexer does not accept
// bare; every symbol must print through one quoting rule.
//
// What: Quote and the string escapes it and the string constants use.
//
// How: a name of identifier characters prints bare; any other is quoted,
// with '"', a backslash and non-printable bytes escaped as \XX.

package llvm

import "strings"

const hexDigits = "0123456789ABCDEF"

// isBareName reports whether name may be printed without quotes: it matches
// [-a-zA-Z$._][-a-zA-Z$._0-9]*. A leading digit is excluded by the class, so
// an all-digit name, which LLVM would read as a numbered value, is quoted.
func isBareName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-', c == '$', c == '.', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Quote returns the LLVM spelling of a global or local name (without the
// sigil): bare when it matches [-a-zA-Z$._][-a-zA-Z$._0-9]* and is not all
// digits, else "..." with \XX escapes for '"', '\\' and bytes outside
// 0x20..0x7e. Quote("") is `""`, which no builder method accepts as a name.
func Quote(name string) string {
	if isBareName(name) {
		return name
	}
	return `"` + escape(name) + `"`
}

// escape writes s with every byte outside 0x20..0x7e, and '"' and '\\', as
// \XX with two upper-case hex digits; this is the escaping of LLVM string
// literals, c"..." constants, names, module asm and inline asm text.
func escape(s string) string {
	need := false
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			need = true
			break
		}
	}
	if !need {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			sb.WriteByte('\\')
			sb.WriteByte(hexDigits[c>>4])
			sb.WriteByte(hexDigits[c&15])
			continue
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// quoted is s as an LLVM string literal, "..." with \XX escapes.
func quoted(s string) string { return `"` + escape(s) + `"` }
