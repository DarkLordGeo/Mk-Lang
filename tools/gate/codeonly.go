// Why: the gate's scan for Mk fuzz targets must tell a call from a mention
// of it in a comment or string, and the gate imports no internal package.
//
// What: mkCodeOnly and mkIdentByte, a copy of internal/driver's codeOnly
// (codeonly.go there). Keep the two equal.
//
// How: one pass over the bytes blanks every line comment, and optionally
// every literal's body, with spaces while keeping newlines, so offsets and
// line numbers stay those of the original text.

package main

import "strings"

// mkCodeOnly is t with every line comment, and with keepStrings false every
// literal's body, replaced by spaces (newlines kept), so a pattern matched
// on it sees only code, at the same offsets: a call mentioned in a
// comment or a string is no call. The literals are Mk's:
// `"..."` and `'...'` with escapes, `r"..."` with none, and `"""`
// multi-line text to the next `"""`. Mk has no block comments.
func mkCodeOnly(t string, keepStrings bool) string {
	b := []byte(t)
	blank := func(i int) {
		if !keepStrings && b[i] != '\n' {
			b[i] = ' '
		}
	}
	for i := 0; i < len(b); i++ {
		switch c := b[i]; {
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case c == '"' && strings.HasPrefix(t[i:], `"""`):
			i += 3
			for ; i < len(b) && !strings.HasPrefix(t[i:], `"""`); i++ {
				if b[i] == '\\' && i+1 < len(b) {
					blank(i)
					i++
				}
				blank(i)
			}
			i += 2 // the closing quotes
		case c == '"' && i > 0 && b[i-1] == 'r' && (i < 2 || !mkIdentByte(b[i-2])):
			for i++; i < len(b) && b[i] != '"' && b[i] != '\n'; i++ {
				blank(i) // a raw string: no escapes
			}
		case c == '"' || c == '\'':
			for i++; i < len(b) && b[i] != c && b[i] != '\n'; i++ {
				if b[i] == '\\' && i+1 < len(b) && b[i+1] != '\n' {
					blank(i)
					i++
				}
				blank(i)
			}
		}
	}
	return string(b)
}

// identByte reports whether c can be part of a name.
func mkIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
