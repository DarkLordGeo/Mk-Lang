// Why: SHA256SUMS lists every file of a release with its hash, so one
// signature covers them all and users can check with standard tools.
// What: Sums with Format, ParseSums and SumFile.
// How: one line per file, "<hex>  <file>", the format sha256sum and shasum
// print; lower-case hex only.

package release

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Sums maps a release file's name to its SHA-256 in lower-case hex.
type Sums map[string]string

// Format is the SHA256SUMS text: one line "<hex>  <name>\n" per file,
// sorted by name.
func (s Sums) Format() []byte {
	names := make([]string, 0, len(s))
	for n := range s {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(s[n] + "  " + n + "\n")
	}
	return []byte(b.String())
}

// ParseSums reads SHA256SUMS strictly: each line 64 lower-case hex digits,
// two spaces and a plain file name (no '/', '\', or leading '*'), every
// line ending in "\n", no name twice, no blank line.
func ParseSums(data []byte) (Sums, error) {
	text := string(data)
	if text == "" || !strings.HasSuffix(text, "\n") {
		return nil, fmt.Errorf("release: %s is empty or does not end in a newline", SumsFile)
	}
	s := Sums{}
	for i, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		h, name, ok := strings.Cut(line, "  ")
		if !ok || !isHex64(h) || name == "" || strings.ContainsAny(name, "/\\\r\t") || name[0] == '*' || name[0] == ' ' {
			return nil, fmt.Errorf("release: %s line %d is not \"<sha256>  <file>\": %q", SumsFile, i+1, line)
		}
		if _, dup := s[name]; dup {
			return nil, fmt.Errorf("release: %s names %s twice", SumsFile, name)
		}
		s[name] = h
	}
	return s, nil
}

func isHex64(h string) bool {
	if len(h) != 64 {
		return false
	}
	for i := 0; i < len(h); i++ {
		if !(h[i] >= '0' && h[i] <= '9' || h[i] >= 'a' && h[i] <= 'f') {
			return false
		}
	}
	return true
}

// SumFile is the SHA-256 of a file in lower-case hex.
func SumFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
