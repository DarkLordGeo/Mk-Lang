// ws-frames, Go: std.websocket's receive path written once per language
// (README.md): decode a buffer of masked client frames into messages,
// fragments joined, each payload unmasked a byte at a time into one
// message buffer, each text message checked as UTF-8 by RFC 3629's table
// (a branch per lead byte), rounds passes. The buffer is batches copies of
// one batch of seven messages (binary 0, 125, 65,535 and 70,000 bytes; text
// 126 bytes, 30,000 bytes in three fragments split inside a character, and
// 1,000 bytes ending in a byte that is not UTF-8), each frame masked with
// the four low bytes of (f + 1) * 0x9E3779B97F4A7C15, f the frame's index
// in the buffer; binary payloads are splitmix64 bytes (seed 1), text the
// 10-byte unit "a", U+00E9, U+20AC, U+1F600 repeated. Per message the
// checksum adds its length, 7 when it is binary or valid text, and its
// byte at (r * 7919 + m) % length, m its index in the pass. Unsigned
// arithmetic wraps. Usage: ws-frames <batches> <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
)

// message is one message of the batch: its opcode, length and fragments.
type message struct {
	op    byte
	n     int
	frags int
}

var batch = []message{{2, 0, 1}, {2, 125, 1}, {1, 126, 1}, {2, 65535, 1}, {2, 70000, 1}, {1, 30000, 3}, {1, 1000, 1}}

var unit = []byte{0x61, 0xC3, 0xA9, 0xE2, 0x82, 0xAC, 0xF0, 0x9F, 0x98, 0x80}

// maxMsg is the longest message, the message buffer's size.
const maxMsg = 70000

// build is the frames of batches copies of the batch.
func build(batches int) []byte {
	pool := make([]byte, maxMsg)
	st := uint64(1)
	for i := 0; i < maxMsg; {
		st += 0x9E3779B97F4A7C15
		z := st
		z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
		z = (z ^ z>>27) * 0x94D049BB133111EB
		z ^= z >> 31
		for k := 0; k < 8 && i < maxMsg; k++ {
			pool[i] = byte(z >> (8 * k))
			i++
		}
	}
	text := make([]byte, maxMsg)
	for i := range text {
		text[i] = unit[i%10]
	}
	var out []byte
	f := uint64(0)
	for b := 0; b < batches; b++ {
		for _, m := range batch {
			src := pool
			if m.op == 1 {
				src = text
			}
			payload := make([]byte, m.n)
			copy(payload, src[:m.n])
			if m.n == 1000 {
				payload[999] = 0xFF
			}
			// Fragments: the first n/3 - 1 bytes, the next n/3 + 1, the rest.
			at := 0
			for j := 0; j < m.frags; j++ {
				l := m.n - at
				if m.frags == 3 && j == 0 {
					l = m.n/3 - 1
				} else if m.frags == 3 && j == 1 {
					l = m.n/3 + 1
				}
				var b0 byte
				if j == 0 {
					b0 = m.op
				}
				if j == m.frags-1 {
					b0 |= 0x80
				}
				out = append(out, b0)
				switch {
				case l <= 125:
					out = append(out, 0x80|byte(l))
				case l <= 65535:
					out = append(out, 0x80|126, byte(l>>8), byte(l))
				default:
					out = append(out, 0x80|127, 0, 0, 0, 0, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
				}
				kw := (f + 1) * 0x9E3779B97F4A7C15
				key := [4]byte{byte(kw), byte(kw >> 8), byte(kw >> 16), byte(kw >> 24)}
				out = append(out, key[:]...)
				for i := 0; i < l; i++ {
					out = append(out, payload[at+i]^key[i&3])
				}
				at += l
				f++
			}
		}
	}
	return out
}

// validUtf8 reports whether s is UTF-8 by RFC 3629's table of well-formed
// sequences: a branch per lead byte, the second byte's range by the lead,
// the rest continuation bytes.
func validUtf8(s []byte) bool {
	n := len(s)
	i := 0
	for i < n {
		c := s[i]
		if c < 0x80 {
			i++
			continue
		}
		need := 0
		lo, hi := byte(0x80), byte(0xBF)
		switch {
		case c < 0xC2:
			return false
		case c < 0xE0:
			need = 1
		case c < 0xF0:
			need = 2
			if c == 0xE0 {
				lo = 0xA0
			} else if c == 0xED {
				hi = 0x9F
			}
		case c < 0xF5:
			need = 3
			if c == 0xF0 {
				lo = 0x90
			} else if c == 0xF4 {
				hi = 0x8F
			}
		default:
			return false
		}
		if i+need >= n {
			return false
		}
		b := s[i+1]
		if b < lo || b > hi {
			return false
		}
		for k := 2; k <= need; k++ {
			if s[i+k]&0xC0 != 0x80 {
				return false
			}
		}
		i += need + 1
	}
	return true
}

// decode is one pass over buf: the checksum it adds for round r.
func decode(buf, msg []byte, r uint64) uint64 {
	var sum uint64
	pos := 0
	mlen := 0
	var mop byte
	var count uint64
	for pos+2 <= len(buf) {
		b0 := buf[pos]
		b1 := buf[pos+1]
		l := int(b1 & 0x7F)
		hl := 2
		if l == 126 {
			if pos+4 > len(buf) {
				return sum + 1000003
			}
			l = int(buf[pos+2])<<8 | int(buf[pos+3])
			hl = 4
		} else if l == 127 {
			if pos+10 > len(buf) {
				return sum + 1000003
			}
			var x uint64
			for k := 2; k < 10; k++ {
				x = x<<8 | uint64(buf[pos+k])
			}
			if x > maxMsg {
				return sum + 1000003
			}
			l = int(x)
			hl = 10
		}
		op := b0 & 0x0F
		if b0&0x70 != 0 || b1&0x80 == 0 || l > len(buf)-pos-hl-4 {
			return sum + 1000003
		}
		if op != 0 {
			mop = op
			mlen = 0
		}
		if l > maxMsg-mlen {
			return sum + 1000003
		}
		k := pos + hl
		key := [4]byte{buf[k], buf[k+1], buf[k+2], buf[k+3]}
		pos = k + 4
		for i := 0; i < l; i++ {
			msg[mlen+i] = buf[pos+i] ^ key[i&3]
		}
		mlen += l
		pos += l
		if b0&0x80 != 0 {
			sum += uint64(mlen)
			if mop != 1 || validUtf8(msg[:mlen]) {
				sum += 7
			}
			if mlen > 0 {
				sum += uint64(msg[(r*7919+count)%uint64(mlen)])
			}
			count++
		}
	}
	return sum
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: ws-frames <batches> <rounds>")
		os.Exit(2)
	}
	batches, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	buf := build(batches)
	msg := make([]byte, maxMsg)
	var total uint64
	for r := 0; r < rounds; r++ {
		total += decode(buf, msg, uint64(r))
	}
	fmt.Println(total)
}
