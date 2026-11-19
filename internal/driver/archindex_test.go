// Why: several tests check which symbols a built static archive exports, and
// each needs the same reader of the archive's symbol table.
// What: archiveIndex, the symbol names of a GNU archive's "/" member.
// How: reads the "/" member's size from its header and splits the big-endian
// count and offset table off the NUL-separated name list.

package driver

import (
	"bytes"
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"testing"
)

// archiveIndex is the symbol names of a GNU archive's "/" member.
func archiveIndex(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("!<arch>\n/ ")) {
		t.Fatalf("%s: no symbol index first", path)
	}
	size, err := strconv.Atoi(string(bytes.TrimSpace(b[8+48 : 8+58])))
	if err != nil {
		t.Fatal(err)
	}
	body := b[68 : 68+size]
	n := int(binary.BigEndian.Uint32(body))
	names := strings.Split(string(body[4+4*n:]), "\x00")
	return names[:n]
}
