// Why: a vector step that silently does nothing still passes when the scalar path does its work.
// What: each base64 and hex vector step, called alone through the test fixture, for lengths 0 to 200.
// How: each step must take every whole block it may, write what Go's encoding/base64 and encoding/hex
// write, and stop at the block holding the first bad byte, placed at every position.

package std

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"testing"
)

// vecdWant is Base64DecodeVec's answer for b with an exact dst: blocks of
// 16 characters while 16 remain, 16 bytes of dst remain, and the block is
// all in the alphabet.
func vecdWant(url bool, b []byte) string {
	chars := b64Std
	if url {
		chars = b64Url
	}
	dst := len(b) / 4 * 3
	i, o := 0, 0
	for len(b)-i >= 16 && dst-o >= 16 {
		ok := true
		for _, c := range b[i : i+16] {
			if bytesIndex(chars, c) < 0 {
				ok = false
			}
		}
		if !ok {
			break
		}
		i += 16
		o += 12
	}
	out := b64Values(chars, b[:i])
	return fmt.Sprintf("ok %d %d %s", i, o, encHex(out))
}

func bytesIndex(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func TestStdEncoding_VectorSteps(t *testing.T) {
	r := &encRng{s: 29}
	var cases []encCase
	for n := 0; n <= 200; n++ {
		data := r.bytes(n)
		for _, url := range []bool{false, true} {
			name, enc := "std", base64.RawStdEncoding
			if url {
				name, enc = "url", base64.RawURLEncoding
			}
			// Encode: 12 bytes a block while 16 bytes of input remain.
			read := 0
			for n-read >= 16 {
				read += 12
			}
			want := fmt.Sprintf("ok %d %d %s", read, read/3*4, encHex([]byte(enc.EncodeToString(data[:read]))))
			cases = append(cases, encCase{in: "vece " + name + " " + encHex(data), want: want})
			text := []byte(enc.EncodeToString(data))
			text = text[:len(text)/4*4]
			cases = append(cases, encCase{in: "vecd " + name + " " + encHex(text), want: vecdWant(url, text)})
			if n%7 == 0 {
				for i := range text {
					c := append([]byte(nil), text...)
					c[i] = "=*-_+/ "[r.intn(7)]
					cases = append(cases, encCase{in: "vecd " + name + " " + encHex(c), want: vecdWant(url, c)})
				}
			}
		}
		// Hex: 16 bytes a block; decode 32 characters a block while valid.
		blocks := n / 16 * 16
		cases = append(cases, encCase{in: "hvece " + encHex(data), want: fmt.Sprintf("ok %d %s", blocks, encHex([]byte(hex.EncodeToString(data[:blocks]))))})
		text := []byte(hex.EncodeToString(data))
		if n%2 == 0 {
			text = []byte(fmt.Sprintf("%X", data))
			if n == 0 {
				text = nil
			}
		}
		cases = append(cases, encCase{in: "hvecd " + encHex(text), want: fmt.Sprintf("ok %d %s", blocks, hex.EncodeToString(data[:blocks]))})
		if n%5 == 0 && n > 0 {
			for i := range text {
				c := append([]byte(nil), text...)
				c[i] = "gG/:@`\x00 "[r.intn(8)]
				good := min(i/32*16, blocks)
				cases = append(cases, encCase{in: "hvecd " + encHex(c), want: fmt.Sprintf("ok %d %s", good, hex.EncodeToString(data[:good]))})
			}
		}
	}
	// Every byte value at every lane of one block: in or out of each alphabet.
	for b := 0; b < 256; b++ {
		for _, lane := range []int{0, 5, 15} {
			c := []byte("QUJDREVGR0hJSktMTU5PUFFSU1RVVldY")
			c[lane] = byte(b)
			if b >= 0x80 {
				continue
			}
			cases = append(cases, encCase{in: "vecd std " + encHex(c), want: vecdWant(false, c)})
			cases = append(cases, encCase{in: "vecd url " + encHex(c), want: vecdWant(true, c)})
			h := []byte("00112233445566778899aabbccddeeff")
			h[lane] = byte(b)
			good := 16
			if !isHexDigit(byte(b)) {
				good = 0
			}
			hd, _ := hex.DecodeString(string(h[:2*good]))
			cases = append(cases, encCase{in: "hvecd " + encHex(h), want: fmt.Sprintf("ok %d %s", good, encHex(hd))})
		}
	}
	encRunAll(t, cases)
}
