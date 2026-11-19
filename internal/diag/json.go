// Why: the JSON report must have its keys in schema order and be the same
// bytes on every run, which a writer fed from Go maps cannot promise, and
// encoding/json escapes characters and rejects NaN in ways the schema does
// not want.
//
// What: JSONWriter, a streaming RFC 8259 writer of one-line documents, with
// BeginObject, Key, String, Int, Uint, Float, Bool, Null and Finish.
//
// How: a stack of frames tracks object or array and first element, so commas
// and misuse are caught as they happen (misuse is an ICE). Strings pass valid
// UTF-8 through and replace invalid bytes with U+FFFD.

package diag

import (
	"math"
	"strconv"
	"unicode/utf8"

	"mk/internal/source"
)

// JSONWriter writes one JSON document on one line, with keys in the order the
// caller writes them. It does not use encoding/json, which escapes <, >, &,
// U+2028 and U+2029 and rejects NaN. Misuse (a value where a key is due, an
// unbalanced End, a key outside an object) is an ICE. The zero JSONWriter is
// ready to use; it is not safe for concurrent use.
type JSONWriter struct {
	b     []byte
	stack []jsonFrame
	// expectValue is true after Key, until the value is written.
	expectValue bool
	// done is true once a top-level value is complete.
	done bool
}

type jsonFrame struct {
	object bool
	first  bool
}

// begin writes the comma or checks the key that precedes a value.
func (w *JSONWriter) begin() {
	if len(w.stack) == 0 {
		if w.done {
			source.ICEf(source.Span{}, "JSONWriter: second top-level value")
		}
		return
	}
	top := &w.stack[len(w.stack)-1]
	if top.object {
		if !w.expectValue {
			source.ICEf(source.Span{}, "JSONWriter: value in an object without a key")
		}
		w.expectValue = false
		return
	}
	if !top.first {
		w.b = append(w.b, ',')
	}
	top.first = false
}

// end marks a value complete at the top level.
func (w *JSONWriter) end() {
	if len(w.stack) == 0 {
		w.done = true
	}
}

// BeginObject opens an object.
func (w *JSONWriter) BeginObject() {
	w.begin()
	w.b = append(w.b, '{')
	w.stack = append(w.stack, jsonFrame{object: true, first: true})
}

// EndObject closes the innermost object.
func (w *JSONWriter) EndObject() {
	if len(w.stack) == 0 || !w.stack[len(w.stack)-1].object || w.expectValue {
		source.ICEf(source.Span{}, "JSONWriter: unbalanced EndObject")
	}
	w.stack = w.stack[:len(w.stack)-1]
	w.b = append(w.b, '}')
	w.end()
}

// BeginArray opens an array.
func (w *JSONWriter) BeginArray() {
	w.begin()
	w.b = append(w.b, '[')
	w.stack = append(w.stack, jsonFrame{first: true})
}

// EndArray closes the innermost array.
func (w *JSONWriter) EndArray() {
	if len(w.stack) == 0 || w.stack[len(w.stack)-1].object {
		source.ICEf(source.Span{}, "JSONWriter: unbalanced EndArray")
	}
	w.stack = w.stack[:len(w.stack)-1]
	w.b = append(w.b, ']')
	w.end()
}

// Key writes an object key; the next call must write its value.
func (w *JSONWriter) Key(k string) {
	if len(w.stack) == 0 || !w.stack[len(w.stack)-1].object || w.expectValue {
		source.ICEf(source.Span{}, "JSONWriter: Key %q outside an object or after a key", k)
	}
	top := &w.stack[len(w.stack)-1]
	if !top.first {
		w.b = append(w.b, ',')
	}
	top.first = false
	w.b = appendJSONString(w.b, k)
	w.b = append(w.b, ':')
	w.expectValue = true
}

// String writes a string value, escaped per RFC 8259.
func (w *JSONWriter) String(s string) {
	w.begin()
	w.b = appendJSONString(w.b, s)
	w.end()
}

// Int writes a signed integer.
func (w *JSONWriter) Int(n int64) {
	w.begin()
	w.b = strconv.AppendInt(w.b, n, 10)
	w.end()
}

// Uint writes an unsigned integer.
func (w *JSONWriter) Uint(n uint64) {
	w.begin()
	w.b = strconv.AppendUint(w.b, n, 10)
	w.end()
}

// Float writes a finite float with the shortest digits that round-trip and no
// exponent (1.0 is 1, -0 keeps its sign); NaN and the infinities are the
// strings "nan", "inf" and "-inf".
func (w *JSONWriter) Float(f float64) {
	switch {
	case math.IsNaN(f):
		w.String("nan")
		return
	case math.IsInf(f, 1):
		w.String("inf")
		return
	case math.IsInf(f, -1):
		w.String("-inf")
		return
	}
	w.begin()
	w.b = strconv.AppendFloat(w.b, f, 'f', -1, 64)
	w.end()
}

// Bool writes true or false.
func (w *JSONWriter) Bool(b bool) {
	w.begin()
	w.b = strconv.AppendBool(w.b, b)
	w.end()
}

// Null writes null.
func (w *JSONWriter) Null() {
	w.begin()
	w.b = append(w.b, "null"...)
	w.end()
}

// Bytes returns what has been written so far. It does not check balance.
func (w *JSONWriter) Bytes() []byte { return w.b }

// Finish returns the document; an unclosed object or array, a dangling key, or
// an empty document is an ICE.
func (w *JSONWriter) Finish() []byte {
	if len(w.stack) != 0 || w.expectValue || !w.done {
		source.ICEf(source.Span{}, "JSONWriter: unbalanced document (%d open)", len(w.stack))
	}
	return w.b
}

const hexDigits = "0123456789abcdef"

// appendJSONString appends s as a JSON string: '"' and '\\' escaped, the five
// short escapes \b \t \n \f \r, every other byte below 0x20 as \u00xx with
// lowercase hex, every byte of an invalid UTF-8 sequence as U+FFFD (so the
// document is always RFC 8259 UTF-8 text, even for a tool's stderr in the
// ANSI code page), and every other byte (including '/', '<', '>', '&',
// U+2028, U+2029 and all valid non-ASCII) copied unchanged.
func appendJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				b = utf8.AppendRune(b, utf8.RuneError)
			} else {
				b = append(b, s[i:i+n]...)
				i += n - 1
			}
			continue
		}
		switch c {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\b':
			b = append(b, '\\', 'b')
		case '\t':
			b = append(b, '\\', 't')
		case '\n':
			b = append(b, '\\', 'n')
		case '\f':
			b = append(b, '\\', 'f')
		case '\r':
			b = append(b, '\\', 'r')
		default:
			if c < 0x20 {
				b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&15])
			} else {
				b = append(b, c)
			}
		}
	}
	return append(b, '"')
}
