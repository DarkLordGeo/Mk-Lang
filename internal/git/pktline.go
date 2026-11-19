// Why: every message of git's wire protocol is framed as pkt-lines.
//
// What: PktReader (Next, NextText) and PktWriter (Line, Data, Flush, Delim,
// ResponseEnd), and PktKind.
//
// How: git's gitprotocol-common pkt-line format: four hex digits giving the length
// including themselves, then the payload; 0000 is a flush, 0001 a
// delimiter, 0002 a response end. Any other length under 4, or one over
// 65,520, is an error.

package git

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// PktKind is what a pkt-line is.
type PktKind uint8

const (
	PktData        PktKind = iota // a payload
	PktFlush                      // 0000
	PktDelim                      // 0001
	PktResponseEnd                // 0002
)

// MaxPktLen is the largest pkt-line, header included (git's
// LARGE_PACKET_MAX).
const MaxPktLen = 65520

// PktReader reads pkt-lines from a stream.
type PktReader struct {
	r   io.Reader
	hdr [4]byte
	buf []byte
}

// NewPktReader reads pkt-lines from r.
func NewPktReader(r io.Reader) *PktReader { return &PktReader{r: r} }

// Next reads one pkt-line. The payload of a PktData is valid until the
// next call. At the end of the stream before a header it returns io.EOF;
// a stream cut inside a line is ReasonCorrupt.
func (p *PktReader) Next() (PktKind, []byte, error) {
	n, err := io.ReadFull(p.r, p.hdr[:])
	if err == io.EOF {
		return 0, nil, io.EOF
	}
	if err != nil {
		if n > 0 && (err == io.ErrUnexpectedEOF) {
			return 0, nil, corrupt("a pkt-line header is cut short")
		}
		return 0, nil, errorf(ReasonNetwork, "reading the reply: %v", err)
	}
	size, ok := parseHex4(p.hdr)
	if !ok {
		return 0, nil, corrupt("a pkt-line header %q is not four hex digits", p.hdr[:])
	}
	switch {
	case size == 0:
		return PktFlush, nil, nil
	case size == 1:
		return PktDelim, nil, nil
	case size == 2:
		return PktResponseEnd, nil, nil
	case size < 4:
		return 0, nil, corrupt("a pkt-line has length %d", size)
	case size > MaxPktLen:
		return 0, nil, corrupt("a pkt-line has length %d, over %d", size, MaxPktLen)
	}
	if cap(p.buf) < size-4 {
		p.buf = make([]byte, size-4)
	}
	p.buf = p.buf[:size-4]
	if _, err := io.ReadFull(p.r, p.buf); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return 0, nil, corrupt("a pkt-line of length %d is cut short", size)
		}
		return 0, nil, errorf(ReasonNetwork, "reading the reply: %v", err)
	}
	// An ERR line may come in any section of any reply; it is the
	// server's refusal, with its text (gitprotocol-v2 "error-line").
	if msg, ok := bytes.CutPrefix(p.buf, []byte("ERR ")); ok {
		return 0, nil, errorf(ReasonNetwork, "the server answered ERR %s", clean(strings.TrimSpace(string(msg))))
	}
	return PktData, p.buf, nil
}

// NextText reads one pkt-line and, for a data line, drops one trailing
// "\n" (text lines end in one).
func (p *PktReader) NextText() (PktKind, string, error) {
	k, b, err := p.Next()
	if err != nil || k != PktData {
		return k, "", err
	}
	return k, string(bytes.TrimSuffix(b, []byte("\n"))), nil
}

func parseHex4(h [4]byte) (int, bool) {
	n := 0
	for _, c := range h {
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, false
		}
		n = n<<4 | int(d)
	}
	return n, true
}

// PktWriter builds a pkt-line stream in memory (requests are small).
type PktWriter struct {
	bytes.Buffer
}

// Line appends a data line holding text and "\n".
func (w *PktWriter) Line(text string) {
	w.Data([]byte(text + "\n"))
}

// Data appends a data line holding b. A payload over MaxPktLen-4 bytes is
// a programming error of the caller and panics: requests and the test
// helpers split their data.
func (w *PktWriter) Data(b []byte) {
	if len(b)+4 > MaxPktLen {
		panic(fmt.Sprintf("git: pkt-line payload of %d bytes", len(b)))
	}
	fmt.Fprintf(&w.Buffer, "%04x", len(b)+4)
	w.Write(b)
}

// Flush appends 0000.
func (w *PktWriter) Flush() { w.WriteString("0000") }

// Delim appends 0001.
func (w *PktWriter) Delim() { w.WriteString("0001") }

// ResponseEnd appends 0002.
func (w *PktWriter) ResponseEnd() { w.WriteString("0002") }
