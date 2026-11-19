// Why: the `.Or(x)` default is removed from the language, so no `.Or(` may
// creep back into std.
// What: TestRwData_OrOnlyInSimdIntrinsics over json, regex, text, compress,
// hash, crypto, simd and math.
// How: allows only std.simd's private `|` intrinsics on Vec and Mask, named by
// check's operator table (internal/check/simd.go).

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRwData_OrOnlyInSimdIntrinsics(t *testing.T) {
	allowed := map[string]bool{
		"simd/vec.mk: Vec[T, N].Or(self, b: Vec[T, N]): Vec[T, N]":    true,
		"simd/vec.mk: Mask[T, N].Or(self, b: Mask[T, N]): Mask[T, N]": true,
	}
	seen := 0
	for _, m := range []string{"json", "regex", "text", "compress", "hash", "crypto", "simd", "math"} {
		err := filepath.WalkDir(m, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if !strings.Contains(line, ".Or(") {
					continue
				}
				key := filepath.ToSlash(p) + ": " + strings.TrimSpace(line)
				ok := false
				for a := range allowed {
					if strings.HasPrefix(key, a) {
						ok = true
						seen++
					}
				}
				if !ok {
					t.Errorf("%s:%d: a .Or( outside std.simd's operator intrinsics; write `e else { x }`: %s", p, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen != len(allowed) {
		t.Errorf("found %d of the %d std.simd intrinsic declarations named .Or; if check renamed them, drop them from this test", seen, len(allowed))
	}
}
