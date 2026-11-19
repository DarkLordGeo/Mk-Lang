//go:build ignore

// tamper writes a broken or unusual copy of an app/archive archive, for the
// refusal check of ../README.md ("Every column refuses a broken archive").
// It is no column's program and tools/bench never builds it as one; the
// test TestAppArchiveRefusals (tools/bench) builds it, and by hand it runs
// as `go run tamper.go <in.mka> <out.mka> <mode> [<n>]`. It grew from the
// scratch tools used to check the row's refusals.
//
// A copy that is "re-signed" has its manifest signed again with the fixed
// seed; every copy but seal's is sealed again under the fixed key, so only
// the step a mode names fails.
//
// Modes that every column refuses:
//   - seal:   one ciphertext byte flipped (the tag fails);
//   - sig:    one manifest byte changed, resealed (the signature fails);
//   - data:   one payload byte changed, recompressed (a digest fails);
//   - long:   one byte added to the payload, recompressed;
//   - short:  a body of 20 bytes, too short for a length and a signature;
//   - mtop:   the manifest length's top bit set;
//   - neg:    the first two sizes written -1 and s1+s2+1, so the total is
//     unchanged, re-signed;
//   - wrap:   the first two sizes written s1+2^63 and s2+2^63, whose u64
//     sum wraps to s1+s2, re-signed;
//   - i64sum: the first two sizes 2^63-1001 and s2+2000, each an i64 and
//     their sum not, re-signed;
//   - huge:   the first size 2^62, re-signed;
//   - total:  the last size raised so the sizes add up to <n> (2^62 is the
//     bound Mk's MaxTotal lets through), the payload unchanged, re-signed.
//
// Modes on which the columns differ (README, the paragraph after open's
// steps), each re-signed: trail (four bytes after the final DEFLATE
// block), plus (the first size written "+N"), under (the first size of 10
// or more written with "_" after its first digit), upper (the first digest
// with a-f in uppercase hex) and nonl (no final line feed). And empty: a
// valid archive of no files, which every column accepts.
package main

import (
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strconv"
	"strings"
)

// fixed is the row's fixed test material (README.md, Format).
func fixed(first byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = first + byte(i)
	}
	return b
}

// body is the archive body of manifest, signed with the fixed seed, and the
// DEFLATE payload packed.
func body(manifest, packed []byte) []byte {
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(fixed(64, 32)), manifest)
	b := binary.LittleEndian.AppendUint64(nil, uint64(len(manifest)))
	b = append(b, manifest...)
	b = append(b, sig...)
	return append(b, packed...)
}

func deflate(raw []byte) []byte {
	var z bytes.Buffer
	w, _ := flate.NewWriter(&z, 6)
	w.Write(raw)
	w.Close()
	return z.Bytes()
}

func tamper(in, out, mode string, n int64) error {
	b, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	if len(b) < 8 {
		return errors.New("not an archive")
	}
	magic := bytes.Clone(b[:8])
	blk, _ := aes.NewCipher(fixed(0, 32))
	g, _ := cipher.NewGCM(blk)
	if mode == "seal" {
		c := bytes.Clone(b)
		c[len(c)/2] ^= 1
		return os.WriteFile(out, c, 0o644)
	}
	plain, err := g.Open(nil, fixed(160, 12), b[8:], magic)
	if err != nil {
		return errors.New("the seal does not open")
	}
	m := binary.LittleEndian.Uint64(plain)
	if len(plain) < 8+64 || m > uint64(len(plain)-8-64) {
		return errors.New("a cut archive")
	}
	manifest := bytes.Clone(plain[8 : 8+m])
	packed := bytes.Clone(plain[8+m+64:])
	lines := strings.SplitAfter(string(manifest), "\n")
	lines = lines[:len(lines)-1] // the empty string after the last "\n"
	if len(lines) < 2 {
		return errors.New("the archive needs two files or more")
	}
	size := func(l string) (int64, string, string) {
		f := strings.SplitN(l, " ", 3)
		n, _ := strconv.ParseInt(f[1], 10, 64)
		return n, f[0], f[2]
	}
	resign := func() {
		manifest = []byte(strings.Join(lines, ""))
		plain = body(manifest, packed)
	}
	switch mode {
	case "short":
		plain = plain[:20]
	case "sig":
		plain[8+3] ^= 1
	case "data", "long":
		raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(packed)))
		if err != nil {
			return err
		}
		if mode == "data" {
			raw[len(raw)/2] ^= 1
		} else {
			raw = append(raw, 'x')
		}
		plain = append(plain[:8+m+64:8+m+64], deflate(raw)...)
	case "mtop":
		plain[7] |= 0x80
	case "neg":
		n1, h1, r1 := size(lines[0])
		n2, h2, r2 := size(lines[1])
		lines[0] = fmt.Sprintf("%s %d %s", h1, -1, r1)
		lines[1] = fmt.Sprintf("%s %d %s", h2, n1+n2+1, r2)
		resign()
	case "wrap":
		n1, h1, r1 := size(lines[0])
		n2, h2, r2 := size(lines[1])
		half := new(big.Int).Lsh(big.NewInt(1), 63)
		lines[0] = fmt.Sprintf("%s %s %s", h1, new(big.Int).Add(big.NewInt(n1), half), r1)
		lines[1] = fmt.Sprintf("%s %s %s", h2, new(big.Int).Add(big.NewInt(n2), half), r2)
		resign()
	case "i64sum":
		_, h1, r1 := size(lines[0])
		n2, h2, r2 := size(lines[1])
		lines[0] = fmt.Sprintf("%s %d %s", h1, int64(1<<63-1-1000), r1)
		lines[1] = fmt.Sprintf("%s %d %s", h2, n2+2000, r2)
		resign()
	case "huge":
		_, h1, r1 := size(lines[0])
		lines[0] = fmt.Sprintf("%s %d %s", h1, int64(1<<62), r1)
		resign()
	case "total":
		var sum int64
		for _, l := range lines[:len(lines)-1] {
			s, _, _ := size(l)
			sum += s
		}
		last, h, r := size(lines[len(lines)-1])
		if n < sum+last {
			return fmt.Errorf("total %d is below the archive's own %d", n, sum+last)
		}
		lines[len(lines)-1] = fmt.Sprintf("%s %d %s", h, n-sum, r)
		resign()
	case "trail":
		plain = append(plain, "JUNK"...)
	case "plus":
		s, h, r := size(lines[0])
		lines[0] = fmt.Sprintf("%s +%d %s", h, s, r)
		resign()
	case "under":
		for i, l := range lines {
			if s, h, r := size(l); s >= 10 {
				t := strconv.FormatInt(s, 10)
				lines[i] = fmt.Sprintf("%s %s_%s %s", h, t[:1], t[1:], r)
				break
			}
		}
		resign()
	case "upper":
		for i, l := range lines {
			if strings.ContainsAny(l[:64], "abcdef") {
				lines[i] = strings.ToUpper(l[:64]) + l[64:]
				break
			}
		}
		resign()
	case "nonl":
		resign()
		manifest = manifest[:len(manifest)-1]
		plain = body(manifest, packed)
	case "empty":
		packed = deflate(nil)
		lines = nil
		resign()
	default:
		return fmt.Errorf("no mode %q", mode)
	}
	return os.WriteFile(out, g.Seal(bytes.Clone(magic), fixed(160, 12), plain, magic), 0o644)
}

func main() {
	var n int64
	var err error
	switch {
	case len(os.Args) == 5 && os.Args[3] == "total":
		n, err = strconv.ParseInt(os.Args[4], 10, 64)
	case len(os.Args) != 4 || os.Args[3] == "total":
		err = errors.New("usage: tamper <in.mka> <out.mka> <mode> | tamper <in.mka> <out.mka> total <n>")
	}
	if err == nil {
		err = tamper(os.Args[1], os.Args[2], os.Args[3], n)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tamper:", err)
		os.Exit(1)
	}
}
