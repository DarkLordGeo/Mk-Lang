// Why: Go's zstd decoder reads on past a zero sequence count in the 2-byte form, where libzstd and Mk stop,
// so the test oracle must document that difference.
// What: the zero-sequence-count case of c2ZstdDiffers, with c2ZLitLen and its helpers.
// How: split from std-compress_zstd_test.go to keep each file under 1,200 lines.

package std

import "encoding/binary"

// c2ZZeroSeq1B rewrites each compressed block of data whose sequences
// section is a zero count in the 2-byte form ending the block (80 00) to
// the 1-byte form (00, the block a byte shorter), walking the frames as far
// as their headers parse; nil when no block has one.
func c2ZZeroSeq1B(data []byte) []byte {
	le := binary.LittleEndian
	var out []byte
	done := 0
	i := 0
frames:
	for i+4 <= len(data) {
		magic := le.Uint32(data[i:])
		if magic&0xFFFFFFF0 == 0x184D2A50 {
			if i+8 > len(data) {
				break
			}
			i += 8 + int(le.Uint32(data[i+4:]))
			continue
		}
		if magic != 0xFD2FB528 || i+5 > len(data) {
			break
		}
		fhd := data[i+4]
		i += 5
		ss := fhd&0x20 != 0
		fcsz := []int{0, 2, 4, 8}[fhd>>6]
		if fhd>>6 == 0 && ss {
			fcsz = 1
		}
		if !ss {
			i++
		}
		i += []int{0, 1, 2, 4}[fhd&3] + fcsz
		for {
			if i+3 > len(data) {
				break frames
			}
			h := int(data[i]) | int(data[i+1])<<8 | int(data[i+2])<<16
			typ, n := (h>>1)&3, h>>3
			if typ == 1 {
				n = 1
			}
			if typ == 3 || i+3+n > len(data) {
				break frames
			}
			body := data[i+3 : i+3+n]
			if typ == 2 && n >= 3 && c2ZLitLen(body) == n-2 && body[n-2] == 0x80 && body[n-1] == 0 {
				nh := (n-1)<<3 | h&7
				out = append(out, data[done:i]...)
				out = append(out, byte(nh), byte(nh>>8), byte(nh>>16))
				out = append(out, body[:n-2]...)
				out = append(out, 0)
				done = i + 3 + n
			}
			i += 3 + n
			if h&1 != 0 {
				if fhd&4 != 0 {
					i += 4
				}
				break
			}
		}
	}
	if out == nil {
		return nil
	}
	return append(out, data[done:]...)
}

// c2ZLitLen is the length of the literals section that starts the
// compressed block body b (RFC 8878 section 3.1.1.3.1.1: its header and
// content), -1 when b is too short for it.
func c2ZLitLen(b []byte) int {
	if len(b) == 0 {
		return -1
	}
	b0 := int(b[0])
	typ, sf := b0&3, (b0>>2)&3
	var hdr, size int
	if typ < 2 {
		// Raw or RLE: the regenerated size in 5, 12 or 20 bits.
		switch sf {
		case 0, 2:
			hdr, size = 1, b0>>3
		case 1:
			if len(b) < 2 {
				return -1
			}
			hdr, size = 2, b0>>4|int(b[1])<<4
		case 3:
			if len(b) < 3 {
				return -1
			}
			hdr, size = 3, b0>>4|int(b[1])<<4|int(b[2])<<12
		}
		if typ == 1 {
			size = 1
		}
	} else {
		// Compressed or treeless: the regenerated then the compressed
		// size, 10, 10, 14 or 18 bits each.
		hdr = []int{3, 3, 4, 5}[sf]
		if len(b) < hdr {
			return -1
		}
		var v uint64
		for k := hdr - 1; k >= 0; k-- {
			v = v<<8 | uint64(b[k])
		}
		bits := []uint{10, 10, 14, 18}[sf]
		size = int(v >> (4 + bits) & (uint64(1)<<bits - 1))
	}
	if hdr+size > len(b) {
		return -1
	}
	return hdr + size
}
