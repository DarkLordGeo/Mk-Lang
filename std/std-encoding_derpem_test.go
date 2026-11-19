// Why: std.encoding's DER reader and writer and its PEM reader and writer parse untrusted input,
// so every offset, bound and error must match a trusted model.
// What: a Go model of the DER reader held to encoding/asn1, a Go model of RFC 7468's lax PEM
// reader held to encoding/pem, and the Mk driver checked against both; FormatPem against pem.EncodeToMemory.
// How: valid trees, every single-byte corruption, adversarial lengths and tags, nesting past the
// depth bound of 32, and PEM blocks both readers accept.

package std

import (
	"bytes"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// derNode is one element as the model reads it.
type derNode struct {
	tag     uint32
	class   byte
	cons    bool
	content []byte
	depth   int
}

// derRead is the model's ReadTlv: the element at the front of data, the
// rest, or the error as the driver prints it.
func derRead(data []byte, depth int) (derNode, []byte, string) {
	n := len(data)
	if n == 0 {
		return derNode{}, nil, b64Bad(0)
	}
	id := data[0]
	tag := uint32(id & 0x1F)
	i := 1
	if tag == 0x1F {
		tag = 0
		for {
			if i >= n {
				return derNode{}, nil, b64Bad(n)
			}
			g := data[i]
			if i == 1 && g == 0x80 || tag > 0x1FFFFFFF>>7 {
				return derNode{}, nil, b64Bad(i)
			}
			tag = tag<<7 | uint32(g&0x7F)
			i++
			if g&0x80 == 0 {
				break
			}
		}
		if tag < 31 {
			return derNode{}, nil, b64Bad(1)
		}
	}
	if i >= n {
		return derNode{}, nil, b64Bad(n)
	}
	at := i
	l0 := data[i]
	i++
	length := int(l0)
	if l0 >= 0x80 {
		k := int(l0 & 0x7F)
		if k == 0 || k > 8 {
			return derNode{}, nil, b64Bad(at)
		}
		if n-i < k {
			return derNode{}, nil, b64Bad(n)
		}
		if data[i] == 0 {
			return derNode{}, nil, b64Bad(at)
		}
		var v uint64
		for j := 0; j < k; j++ {
			v = v<<8 | uint64(data[i+j])
		}
		i += k
		if v < 128 {
			return derNode{}, nil, b64Bad(at)
		}
		if v > uint64(n-i) {
			return derNode{}, nil, b64Bad(n)
		}
		length = int(v)
	} else if length > n-i {
		return derNode{}, nil, b64Bad(n)
	}
	return derNode{tag: tag, class: id >> 6, cons: id&0x20 != 0, content: data[i : i+length], depth: depth}, data[i+length:], ""
}

// derDump is the driver's Dump of v, or the first error of the walk.
func derDump(v derNode, out *strings.Builder) string {
	fmt.Fprintf(out, "%d.%d", v.tag, v.class)
	if v.cons {
		out.WriteString(".c{")
		rest := v.content
		first := true
		for len(rest) > 0 {
			if v.depth+1 > 32 {
				return "TooLarge"
			}
			child, r, err := derRead(rest, v.depth+1)
			if err != "" {
				if strings.HasPrefix(err, "BadInput") {
					var off int
					fmt.Sscanf(err, "BadInput(offset: %d)", &off)
					return b64Bad(len(v.content) - len(rest) + off)
				}
				return err
			}
			rest = r
			if !first {
				out.WriteString(",")
			}
			first = false
			if err := derDump(child, out); err != "" {
				return err
			}
		}
		out.WriteString("}")
		return ""
	}
	out.WriteString(".p:" + encHex(v.content))
	c := v.content
	if v.class != 0 {
		return ""
	}
	switch v.tag {
	case 2:
		if len(c) == 0 || len(c) >= 2 && (c[0] == 0 && c[1] < 0x80 || c[0] == 0xFF && c[1] >= 0x80) {
			out.WriteString("[int " + b64Bad(0) + "]")
		} else {
			out.WriteString("[int " + encHex(c) + "]")
		}
	case 1:
		if len(c) != 1 || c[0] != 0 && c[0] != 0xFF {
			out.WriteString("[bool " + b64Bad(0) + "]")
		} else {
			fmt.Fprintf(out, "[bool %v]", c[0] == 0xFF)
		}
	case 3:
		if len(c) == 0 || c[0] > 7 || len(c) == 1 && c[0] != 0 || c[0] != 0 && c[len(c)-1]&(1<<c[0]-1) != 0 {
			out.WriteString("[bits " + b64Bad(0) + "]")
		} else {
			out.WriteString("[bits " + encHex(c[1:]) + "]")
		}
	}
	return ""
}

// derWant is the driver's answer to "der data".
func derWant(data []byte) string {
	v, rest, err := derRead(data, 0)
	if err != "" {
		return "err " + err
	}
	var b strings.Builder
	if err := derDump(v, &b); err != "" {
		return "err " + err
	}
	return fmt.Sprintf("ok %s rest %d rewrite true", b.String(), len(rest))
}

func derCase(data []byte) encCase { return encCase{in: "der " + encHex(data), want: derWant(data)} }

// derTLV encodes one element (class, constructed, tag number, content)
// with minimal identifier and length octets.
func derTLV(class byte, cons bool, tag uint32, content []byte) []byte {
	id := class << 6
	if cons {
		id |= 0x20
	}
	var out []byte
	if tag < 31 {
		out = append(out, id|byte(tag))
	} else {
		out = append(out, id|0x1F)
		var groups []byte
		for t := tag; ; t >>= 7 {
			groups = append([]byte{byte(t & 0x7F)}, groups...)
			if t < 0x80 {
				break
			}
		}
		for i := range groups[:len(groups)-1] {
			groups[i] |= 0x80
		}
		out = append(out, groups...)
	}
	n := len(content)
	if n < 128 {
		out = append(out, byte(n))
	} else {
		var lb []byte
		for v := n; v > 0; v >>= 8 {
			lb = append([]byte{byte(v)}, lb...)
		}
		out = append(out, 0x80|byte(len(lb)))
		out = append(out, lb...)
	}
	return append(out, content...)
}

// derRandom is a random valid DER tree of at most depth levels.
func derRandom(r *encRng, depth int) []byte {
	if depth > 0 && r.intn(3) != 0 {
		var content []byte
		for k := r.intn(4); k > 0; k-- {
			content = append(content, derRandom(r, depth-1)...)
		}
		class := byte(r.intn(4))
		tag := uint32(16)
		if class != 0 || r.intn(3) == 0 {
			tag = uint32(r.intn(40))
		}
		return derTLV(class, true, tag, content)
	}
	switch r.intn(6) {
	case 0:
		b := new(big.Int).SetBytes(r.bytes(r.intn(20)))
		if r.intn(2) == 0 {
			b.Neg(b)
		}
		enc, _ := asn1.Marshal(b)
		return enc
	case 1:
		enc, _ := asn1.Marshal(r.intn(2) == 0)
		return enc
	case 2:
		bits := r.intn(40)
		bs := asn1.BitString{Bytes: make([]byte, (bits+7)/8), BitLength: bits}
		for i := 0; i < bits; i++ {
			if r.intn(2) == 0 {
				bs.Bytes[i/8] |= 0x80 >> (i % 8)
			}
		}
		enc, _ := asn1.Marshal(bs)
		return enc
	case 3:
		return derTLV(0, false, 4, r.bytes(r.intn(300)))
	case 4:
		return derTLV(byte(r.intn(4)), false, uint32(r.intn(1<<r.intn(22))), r.bytes(r.intn(8)))
	}
	return derTLV(0, false, 5, nil)
}

// TestStdEncoding_DerModelAgreesWithGo holds the model's element reader
// and typed readers to encoding/asn1 on valid trees and their corruptions,
// with tag numbers below 2^28 (asn1's limit).
func TestStdEncoding_DerModelAgreesWithGo(t *testing.T) {
	r := &encRng{s: 13}
	for k := 0; k < 20000; k++ {
		data := derRandom(r, 3)
		if r.intn(2) == 0 {
			data[r.intn(len(data))] = byte(r.next())
		}
		v, rest, err := derRead(data, 0)
		var rv asn1.RawValue
		grest, gerr := asn1.Unmarshal(data, &rv)
		if (err == "") != (gerr == nil) {
			t.Fatalf("%x: model %q, asn1 %v", data, err, gerr)
		}
		if err != "" {
			continue
		}
		if rv.Class != int(v.class) || rv.Tag != int(v.tag) || rv.IsCompound != v.cons || !bytes.Equal(rv.Bytes, v.content) || len(grest) != len(rest) {
			t.Fatalf("%x: model %+v, asn1 %+v", data, v, rv)
		}
		if v.class != 0 || v.cons {
			continue
		}
		var b strings.Builder
		derDump(v, &b)
		modelBad := strings.Contains(b.String(), "BadInput")
		switch v.tag {
		case 2:
			var x *big.Int
			_, gerr = asn1.Unmarshal(data, &x)
		case 1:
			var x bool
			_, gerr = asn1.Unmarshal(data, &x)
		case 3:
			var x asn1.BitString
			_, gerr = asn1.Unmarshal(data, &x)
		default:
			continue
		}
		if modelBad != (gerr != nil) {
			t.Fatalf("%x: model %s, asn1 %v", data, b.String(), gerr)
		}
	}
}

func TestStdEncoding_Der(t *testing.T) {
	r := &encRng{s: 17}
	var cases []encCase
	for k := 0; k < 400; k++ {
		data := derRandom(r, 4)
		cases = append(cases, derCase(data), derCase(append(append([]byte(nil), data...), 0x05, 0x00)))
		for j := 0; j < 6 && len(data) > 0; j++ {
			c := append([]byte(nil), data...)
			c[r.intn(len(c))] = byte(r.next())
			cases = append(cases, derCase(c))
		}
		cases = append(cases, derCase(data[:r.intn(len(data))]))
	}
	// Identifier and length edges.
	for _, h := range []string{"", "30", "3080", "300100", "3081", "308100", "30817f", "3081800000", "3082007f", "30820080",
		"3089010000000000000000", "3088ffffffffffffffff", "1f00", "1f1e00", "1f1f00", "1f8001", "1f807f00", "1fff7f00",
		"1f8fffffff7f00", "1f81ffffff7f00", "9f8fffffff7f00", "0500", "0400", "0201ff", "020100", "02020080", "0202ff7f",
		"02020000", "0202ff80", "0200", "010100", "0101ff", "010101", "0100", "01020000", "030100", "030101", "030200ff",
		"030207c0", "030207c1", "030208ff", "0300", "2100", "2300", "a000", "e000", "3003020101", "3103020100"} {
		cases = append(cases, derCase(cryptoUnhex(t, h)))
	}
	// The indefinite length byte 0x80 with 128 bytes after it, at the top
	// and as a child: refused at the 0x80, never read as a one-byte length
	// of 128 (ReadTlv's short form takes only bytes below 0x80).
	body := make([]byte, 128)
	for i := range body {
		body[i] = byte(i)
	}
	for _, h := range []string{"0480", "3080", "3081820480"} {
		cases = append(cases, derCase(append(cryptoUnhex(t, h), body...)))
	}
	// Nesting: 32 levels inside the top one read, 33 do not.
	for _, depth := range []int{31, 32, 33, 40} {
		data := derTLV(0, false, 5, nil)
		for d := 0; d < depth; d++ {
			data = derTLV(0, true, 16, data)
		}
		cases = append(cases, derCase(data))
	}
	// The deepest tree the reader takes, 33 constructed levels (depths 0 to
	// 32, the innermost empty), and one level more; the writer nests as
	// deep as the reader, so the first is written back whole.
	for _, levels := range []int{33, 34} {
		data := derTLV(0, true, 16, nil)
		for d := 1; d < levels; d++ {
			data = derTLV(0, true, 16, data)
		}
		cases = append(cases, derCase(data))
	}
	encRunAll(t, cases)
}

func TestStdEncoding_DerWriterInteger(t *testing.T) {
	r := &encRng{s: 19}
	var cases []encCase
	for _, h := range []string{"", "00", "0000", "01", "7f", "80", "ff", "0080", "000080", "0100"} {
		m := cryptoUnhex(t, h)
		enc, _ := asn1.Marshal(new(big.Int).SetBytes(m))
		cases = append(cases, encCase{in: "deri " + h, want: "ok " + encHex(enc)})
	}
	for k := 0; k < 300; k++ {
		m := r.bytes(r.intn(140))
		enc, _ := asn1.Marshal(new(big.Int).SetBytes(m))
		cases = append(cases, encCase{in: "deri " + encHex(m), want: "ok " + encHex(enc)})
	}
	encRunAll(t, cases)
}

// TestStdEncoding_DerResultsOutliveReader: DER results borrow the input, not the reader. The driver's
// FirstInteger returns a parsed INTEGER from the function that read it,
// and WriteSeq a writer's Bytes from the function that made the writer;
// both compile only because those results name the input and the writer's arena. The
// encoding is asn1.Marshal's of a SEQUENCE holding the integer.
func TestStdEncoding_DerResultsOutliveReader(t *testing.T) {
	r := &encRng{s: 29}
	var cases []encCase
	for k := 0; k < 100; k++ {
		m := r.bytes(r.intn(100))
		seq, err := asn1.Marshal(struct{ N *big.Int }{new(big.Int).SetBytes(m)})
		if err != nil {
			t.Fatal(err)
		}
		// The INTEGER is the sequence's only child, in short-form lengths
		// (at most 101 content bytes): its content starts four bytes in.
		cases = append(cases, encCase{in: "derseq " + encHex(m), want: "ok " + encHex(seq) + " " + encHex(seq[4:])})
	}
	encRunAll(t, cases)
}

// pemModel is ParsePem's answer: "ok N label:der ..." or the error.
func pemModel(text []byte) string {
	lineAt := func(i int) (int, int) {
		j := i
		for j < len(text) && text[j] != '\n' && text[j] != '\r' {
			j++
		}
		k := j
		if k < len(text) && text[k] == '\r' {
			k++
		}
		if k < len(text) && text[k] == '\n' && (k == j || text[j] == '\r') {
			k++
		}
		return j, k
	}
	trim := func(b []byte) []byte { return bytes.TrimRight(b, " \t") }
	boundary := func(line []byte, head string) (string, bool) {
		if !bytes.HasPrefix(line, []byte(head)) || !bytes.HasSuffix(line, []byte("-----")) || len(line) < len(head)+5 {
			return "", false
		}
		return string(line[len(head) : len(line)-5]), true
	}
	labelOk := func(l string) bool {
		for i := 0; i < len(l); i++ {
			c := l[i]
			if c == '-' || c == ' ' {
				if i == 0 || i == len(l)-1 || l[i-1] == '-' || l[i-1] == ' ' {
					return false
				}
			} else if c < 0x21 || c > 0x7E {
				return false
			}
		}
		return true
	}
	var parts []string
	i := 0
	for i < len(text) {
		end, next := lineAt(i)
		label, ok := boundary(trim(text[i:end]), "-----BEGIN ")
		if !ok {
			i = next
			continue
		}
		begin := i
		if !labelOk(label) {
			return "err " + b64Bad(begin)
		}
		j, close, after := next, -1, -1
		for j < len(text) {
			e, nx := lineAt(j)
			line := trim(text[j:e])
			if bytes.HasPrefix(line, []byte("-----END ")) {
				named, ok := boundary(line, "-----END ")
				if !ok || named != label {
					return "err " + b64Bad(j)
				}
				close, after = j, nx
				break
			}
			if bytes.IndexByte(text[j:e], ':') >= 0 {
				return "err " + b64Bad(j)
			}
			j = nx
		}
		if close < 0 {
			return "err " + b64Bad(begin)
		}
		var chars []byte
		for _, c := range text[next:close] {
			// RFC 7468 section 3's W: WHATWG's whitespace and VT.
			if !b64IsSpace(c) && c != 0x0B {
				chars = append(chars, c)
			}
		}
		der, bad := b64Model("std", chars)
		if bad != "" {
			return "err " + b64Bad(begin)
		}
		parts = append(parts, encHex([]byte(label))+":"+encHex(der))
		i = after
	}
	out := fmt.Sprintf("ok %d", len(parts))
	for _, p := range parts {
		out += " " + p
	}
	return out
}

func pemCase(text []byte) encCase { return encCase{in: "pem " + encHex(text), want: pemModel(text)} }

func TestStdEncoding_Pem(t *testing.T) {
	r := &encRng{s: 23}
	var cases []encCase
	for k := 0; k < 300; k++ {
		label := []string{"CERTIFICATE", "PRIVATE KEY", "EC PRIVATE KEY", "X", "A-B C"}[r.intn(5)]
		der := r.bytes(r.intn(200))
		block := pem.EncodeToMemory(&pem.Block{Type: label, Bytes: der})
		// encoding/pem agrees on what it writes.
		got, rest := pem.Decode(block)
		if got == nil || got.Type != label || !bytes.Equal(got.Bytes, der) || len(rest) != 0 {
			t.Fatal("encoding/pem does not read its own block")
		}
		want := fmt.Sprintf("ok 1 %s:%s", encHex([]byte(label)), encHex(der))
		if pemModel(block) != want {
			t.Fatalf("model: %q gives %s", block, pemModel(block))
		}
		cases = append(cases, encCase{in: "pemf " + encHex([]byte(label)) + " " + encHex(der), want: "ok " + encHex(block)})
		cases = append(cases, pemCase(block))
		text := append([]byte("explanatory text\r\n"), block...)
		text = append(text, bytes.ReplaceAll(block, []byte("\n"), []byte("\r\n"))...)
		text = append(text, "trailer"...)
		cases = append(cases, pemCase(text))
		for j := 0; j < 4; j++ {
			c := append([]byte(nil), block...)
			c[r.intn(len(c))] = "-: =A\n\r\t"[r.intn(8)]
			cases = append(cases, pemCase(c))
		}
	}
	for _, s := range []string{"", "no blocks here\n", "-----BEGIN X-----\n-----END X-----\n", "-----BEGIN X-----\nQQ==\n-----END Y-----\n",
		"-----BEGIN X-----\nQQ==\n", "-----BEGIN X-----\nProc-Type: 4,ENCRYPTED\n\nQQ==\n-----END X-----\n",
		"-----BEGIN  X-----\nQQ==\n-----END  X-----\n", "-----BEGIN X -----\nQQ==\n-----END X -----\n",
		"-----BEGIN X-----\nQR==\n-----END X-----\n", "-----BEGIN X-----\nQQ\n-----END X-----\n", "-----BEGIN X-----  \n Q Q = = \n-----END X-----\t\n",
		"-----BEGIN X-----\rQQ==\r-----END X-----", "  -----BEGIN X-----\nQQ==\n-----END X-----\n", "-----BEGIN X-----"} {
		cases = append(cases, pemCase([]byte(s)))
	}
	encRunAll(t, cases)
}
