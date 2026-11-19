// Why: pkg.sum records one hash per dependency that must be the same whether
// the dependency came as an archive or as a git tag, so the hash is over the
// unpacked files, not the bytes fetched.
//
// What: TreeHash with String and ParseTreeHash, Tree.Hash, Tree.Verify and
// Tree.WriteVerified, and ReasonSumMismatch.
//
// How: SHA-256 over one line per regular file, "<SHA-256 of its bytes, 64
// hex>  <slash path>\n", in ascending byte order of the path; nothing else
// enters (no modes, no directories). An archive's lone top directory is
// stripped first, and the hash is checked in memory before anything reaches
// the disk.

package fetch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// TreeHash is a tree's tree-sha256.
type TreeHash [32]byte

// treeHashPrefix is the hash's spelling in pkg.sum and the seed.
const treeHashPrefix = "tree-sha256:"

// ReasonSumMismatch: a tree's hash is not the one recorded (E0202
// sum-mismatch).
const ReasonSumMismatch = "sum-mismatch"

// String is "tree-sha256:" and 64 lowercase hex digits.
func (h TreeHash) String() string { return treeHashPrefix + hex.EncodeToString(h[:]) }

// ParseTreeHash reads "tree-sha256:" and 64 hex digits of either case.
func ParseTreeHash(s string) (TreeHash, bool) {
	var h TreeHash
	hexPart, ok := strings.CutPrefix(s, treeHashPrefix)
	if !ok || len(hexPart) != 64 {
		return h, false
	}
	if _, err := hex.Decode(h[:], []byte(hexPart)); err != nil {
		return h, false
	}
	return h, true
}

// Hash is the tree hash of t's files. It does not depend on the order of
// t.Files.
func (t *Tree) Hash() TreeHash {
	files := slices.Clone(t.Files)
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	outer := sha256.New()
	var line bytes.Buffer
	for _, f := range files {
		sum := sha256.Sum256(f.Data)
		line.Reset()
		line.WriteString(hex.EncodeToString(sum[:]))
		line.WriteString("  ")
		line.WriteString(f.Path)
		line.WriteByte('\n')
		outer.Write(line.Bytes())
	}
	var h TreeHash
	outer.Sum(h[:0])
	return h
}

// Verify checks t's hash against want; a mismatch is ReasonSumMismatch,
// naming both hashes.
func (t *Tree) Verify(want TreeHash) error {
	if got := t.Hash(); got != want {
		return errorf(ReasonSumMismatch, "the tree has hash %s, but %s was recorded", got, want)
	}
	return nil
}

// WriteVerified checks t's hash against want in memory and only then
// writes the tree under dir (Tree.Write): a mismatch writes nothing.
func (t *Tree) WriteVerified(dir string, want TreeHash) error {
	if err := t.Verify(want); err != nil {
		return err
	}
	return t.Write(dir)
}
