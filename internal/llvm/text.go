// Why: printing IR is on the build's hot path; fmt and a string per
// instruction would allocate for every line.
//
// What: the append helpers (appendHex16 among them) and the chunk buffer
// WriteTo streams the module through.
//
// How: text is appended to reused []byte buffers with strconv.Append*, so
// printing allocates nothing once the chunk exists.

package llvm

import (
	"io"
	"strconv"
)

// appendRef appends v's reference without its type.
func appendRef(buf []byte, v Value) []byte {
	switch v.kind {
	case vkLocal:
		buf = append(buf, "%v"...)
		return strconv.AppendInt(buf, v.n, 10)
	case vkParam:
		buf = append(buf, "%p"...)
		return strconv.AppendInt(buf, v.n, 10)
	case vkInt:
		return strconv.AppendInt(buf, v.n, 10)
	case vkFP:
		return appendHex16(buf, uint64(v.n))
	}
	return append(buf, v.ref...)
}

// appendTyped appends the typed operand "T ref".
func appendTyped(buf []byte, v Value) []byte {
	buf = append(buf, v.Ty.String()...)
	buf = append(buf, ' ')
	return appendRef(buf, v)
}

// appendHex16 appends 0x and the 16 upper-case hex digits of x.
func appendHex16(buf []byte, x uint64) []byte {
	buf = append(buf, '0', 'x')
	for sh := 60; sh >= 0; sh -= 4 {
		buf = append(buf, hexDigits[(x>>uint(sh))&15])
	}
	return buf
}

// appendAlign appends ", align n", or nothing for 0.
func appendAlign(buf []byte, align uint32) []byte {
	if align == 0 {
		return buf
	}
	buf = append(buf, ", align "...)
	return strconv.AppendUint(buf, uint64(align), 10)
}

// appendMDRef appends !n, or null for the zero MD.
func appendMDRef(buf []byte, md MD) []byte {
	if md.id == 0 {
		return append(buf, "null"...)
	}
	buf = append(buf, '!')
	return strconv.AppendUint(buf, uint64(md.id-1), 10)
}

// appendEscaped appends s with LLVM's \XX escapes (see escape).
func appendEscaped(buf []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			buf = append(buf, '\\', hexDigits[c>>4], hexDigits[c&15])
			continue
		}
		buf = append(buf, c)
	}
	return buf
}

// appendQuoted appends s as an LLVM string literal "...".
func appendQuoted(buf []byte, s string) []byte {
	buf = append(buf, '"')
	buf = appendEscaped(buf, s)
	return append(buf, '"')
}

// printChunk is the size of the reused print buffer. Text is flushed to
// the writer when the buffer is half full; a block's text of a quarter
// chunk or more is written to the writer directly, without a copy.
const printChunk = 64 << 10

// printer streams a module to w through buf.
type printer struct {
	w   io.Writer
	buf []byte
	n   int64
	err error
}

func (p *printer) write(b []byte) {
	if p.err != nil || len(b) == 0 {
		return
	}
	n, err := p.w.Write(b)
	p.n += int64(n)
	p.err = err
}

func (p *printer) flush() {
	p.write(p.buf)
	p.buf = p.buf[:0]
}

// line ends a unit of output: it flushes the buffer when half full.
func (p *printer) line() {
	if len(p.buf) >= printChunk/2 {
		p.flush()
	}
}

// bytes appends b, writing it directly when it is large.
func (p *printer) bytes(b []byte) {
	if len(b) >= printChunk/4 {
		p.flush()
		p.write(b)
		return
	}
	p.buf = append(p.buf, b...)
	p.line()
}

// str appends s, writing it directly when it is large.
func (p *printer) str(s string) {
	if len(s) >= printChunk/4 {
		p.flush()
		if p.err == nil {
			n, err := io.WriteString(p.w, s)
			p.n += int64(n)
			p.err = err
		}
		return
	}
	p.buf = append(p.buf, s...)
}
