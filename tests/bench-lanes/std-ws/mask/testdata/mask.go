// ws-mask, Go, scalar (the SIMD table's Go cell, labelled as such): RFC
// 6455 5.3's masking as Go's WebSocket libraries do it (gorilla/websocket's
// maskBytes: 8 bytes a step as one uint64 XOR, SWAR), Go having no vector
// operations without assembly and no WebSocket in its standard library: a
// scalar head to phase 0, the uint64 loop, a scalar tail. n bytes of
// splitmix64 output (seed 1) in a buffer that starts on a 64-byte boundary
// (BS-4), rounds times masked in
// place with round r's key (the four low bytes of (r + 1) *
// 0x9E3779B97F4A7C15) from phase r % 4; the checksum adds, per round, the
// byte at (r * 7919) % n and the returned phase.
// Usage: ws-mask <n> <rounds>.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"reflect"
	"strconv"
)

func mask(b []byte, key [4]byte, phase int) int {
	p := phase & 3
	i := 0
	for i < len(b) && p != 0 {
		b[i] ^= key[p]
		p = (p + 1) & 3
		i++
	}
	if len(b)-i >= 8 {
		k := uint64(binary.LittleEndian.Uint32(key[:]))
		k |= k << 32
		for ; len(b)-i >= 8; i += 8 {
			v := binary.LittleEndian.Uint64(b[i:]) ^ k
			binary.LittleEndian.PutUint64(b[i:], v)
		}
	}
	for ; i < len(b); i++ {
		b[i] ^= key[p]
		p = (p + 1) & 3
	}
	return p
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: ws-mask <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	// BS-4: the buffer starts on a 64-byte boundary in every column.
	raw := make([]byte, n+64)
	off := int(-reflect.ValueOf(raw).Pointer() & 63)
	data := raw[off : off+n]
	if reflect.ValueOf(data).Pointer()%64 != 0 {
		fmt.Fprintln(os.Stderr, "ws-mask: buffer not 64-byte aligned")
		os.Exit(3)
	}
	st := uint64(1)
	for i := 0; i < n; {
		st += 0x9E3779B97F4A7C15
		z := st
		z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
		z = (z ^ z>>27) * 0x94D049BB133111EB
		z ^= z >> 31
		for k := 0; k < 8 && i < n; k++ {
			data[i] = byte(z >> (8 * k))
			i++
		}
	}
	var total uint64
	for r := 0; r < rounds; r++ {
		kw := (uint64(r) + 1) * 0x9E3779B97F4A7C15
		key := [4]byte{byte(kw), byte(kw >> 8), byte(kw >> 16), byte(kw >> 24)}
		p := mask(data, key, r%4)
		total += uint64(data[(r*7919)%n]) + uint64(p)
	}
	fmt.Println(total)
}
