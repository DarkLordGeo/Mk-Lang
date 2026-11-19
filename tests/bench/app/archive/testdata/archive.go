// archive, Go, std: the task of ../README.md. `create` walks the tree with
// filepath.WalkDir, keeps the regular files, sorts them by their relative
// path with `/` in byte order, reads each whole with os.ReadFile, hashes it
// with crypto/sha256 and appends it to one payload slice; the payload is
// compressed by compress/flate at flate.DefaultCompression (level 6), the
// manifest is signed by crypto/ed25519 (NewKeyFromSeed on the fixed seed),
// and the archive body is sealed by crypto/aes with cipher.NewGCM under
// the fixed key and nonce. `open` reverses it: it opens the seal, verifies
// the signature with ed25519.Verify, inflates the payload with
// flate.NewReader through io.ReadAll, limited to one byte past the
// manifest's total and never preallocated, checks every file's SHA-256 against the manifest, and
// prints the manifest and a summary line through a bufio.Writer over
// stdout.
// Usage: archive create <dir> <out> [<files>] | archive open <archive>.
package main

import (
	"bufio"
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

var magic = []byte("MKARCH01")

// fixed is the row's fixed test material (README.md, Format): the AES-256
// key is the bytes 0 to 31, the nonce 160 to 171 and the Ed25519 seed 64
// to 95.
func fixed(first byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = first + byte(i)
	}
	return b
}

func sealer() (cipher.AEAD, error) {
	block, err := aes.NewCipher(fixed(0, 32))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

type file struct {
	rel, path string
}

func create(dir, out string, limit int) error {
	var files []file
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, file{filepath.ToSlash(rel), p})
		return nil
	})
	if err != nil {
		return err
	}
	slices.SortFunc(files, func(a, b file) int { return strings.Compare(a.rel, b.rel) })
	if limit >= 0 && limit < len(files) {
		files = files[:limit]
	}
	var manifest []byte
	var payload []byte
	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		manifest = hex.AppendEncode(manifest, sum[:])
		manifest = fmt.Appendf(manifest, " %d %s\n", len(data), f.rel)
		payload = append(payload, data...)
	}
	var packed bytes.Buffer
	zw, err := flate.NewWriter(&packed, flate.DefaultCompression)
	if err != nil {
		return err
	}
	if _, err := zw.Write(payload); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(fixed(64, 32)), manifest)
	body := make([]byte, 0, 8+len(manifest)+64+packed.Len())
	body = binary.LittleEndian.AppendUint64(body, uint64(len(manifest)))
	body = append(body, manifest...)
	body = append(body, sig...)
	body = append(body, packed.Bytes()...)
	gcm, err := sealer()
	if err != nil {
		return err
	}
	sealed := gcm.Seal(slices.Clip(magic), fixed(160, 12), body, magic)
	w, err := os.Create(out)
	if err != nil {
		return err
	}
	if _, err := w.Write(sealed); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	fmt.Printf("archive: %d files, %d bytes\n", len(files), len(payload))
	return nil
}

type entry struct {
	digest []byte
	size   int
}

func open(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) < 8+16 || !bytes.Equal(b[:8], magic) {
		return errors.New("not an archive")
	}
	gcm, err := sealer()
	if err != nil {
		return err
	}
	plain, err := gcm.Open(b[8:8], fixed(160, 12), b[8:], magic)
	if err != nil {
		return errors.New("the seal does not open")
	}
	if len(plain) < 8+64 {
		return errors.New("a cut archive")
	}
	mlen := binary.LittleEndian.Uint64(plain)
	if mlen > uint64(len(plain)-8-64) {
		return errors.New("a cut archive")
	}
	manifest := plain[8 : 8+mlen]
	sig := plain[8+mlen : 8+mlen+64]
	packed := plain[8+mlen+64:]
	public := ed25519.NewKeyFromSeed(fixed(64, 32)).Public().(ed25519.PublicKey)
	if !ed25519.Verify(public, manifest, sig) {
		return errors.New("a bad signature")
	}
	var entries []entry
	total := 0
	for rest := manifest; len(rest) > 0; {
		line, tail, ok := bytes.Cut(rest, []byte{'\n'})
		if !ok || len(line) < 67 || line[64] != ' ' {
			return errors.New("a bad manifest line")
		}
		rest = tail
		digest, err := hex.DecodeString(string(line[:64]))
		if err != nil {
			return err
		}
		size, _, ok := bytes.Cut(line[65:], []byte{' '})
		if !ok {
			return errors.New("a bad manifest line")
		}
		n, err := strconv.Atoi(string(size))
		if err != nil {
			return err
		}
		if n < 0 || n > math.MaxInt-total {
			return errors.New("a bad manifest line")
		}
		entries = append(entries, entry{digest, n})
		total += n
	}
	// The payload is read without preallocating the manifest's total, as
	// Mk's compress.Inflate does with the total as its limit: a signed
	// total far past the archive's own size commits nothing. One byte past
	// the total is read, so a longer payload shows.
	limit := int64(total)
	if limit < math.MaxInt64 {
		limit++
	}
	data, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(packed)), limit))
	if err != nil {
		return fmt.Errorf("the payload's DEFLATE stream is broken: %w", err)
	}
	if len(data) < total {
		return errors.New("the payload is shorter than the manifest's size")
	}
	if len(data) > total {
		return errors.New("the payload is longer than the manifest's size")
	}
	at := 0
	for _, e := range entries {
		sum := sha256.Sum256(data[at : at+e.size])
		if !bytes.Equal(sum[:], e.digest) {
			return errors.New("a file's digest differs from the manifest")
		}
		at += e.size
	}
	w := bufio.NewWriterSize(os.Stdout, 64<<10)
	w.Write(manifest)
	fmt.Fprintf(w, "ok: %d files, %d bytes\n", len(entries), total)
	return w.Flush()
}

func main() {
	var err error
	switch {
	case len(os.Args) >= 4 && len(os.Args) <= 5 && os.Args[1] == "create":
		limit := -1
		if len(os.Args) == 5 {
			limit, err = strconv.Atoi(os.Args[4])
		}
		if err == nil {
			err = create(os.Args[2], os.Args[3], limit)
		}
	case len(os.Args) == 3 && os.Args[1] == "open":
		err = open(os.Args[2])
	default:
		err = errors.New("usage: archive create <dir> <out> [<files>] | archive open <archive>")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "archive:", err)
		os.Exit(1)
	}
}
