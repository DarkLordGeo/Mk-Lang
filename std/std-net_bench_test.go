// Why: the std-net3 bench rows must treat every language's cell alike, or the
// comparison is unfair.
// What: TestStdNet_ConnRowsPickTheTransportAtRunTime and
// TestStdNet_InmemConnCopiesWithCopyFrom.
// How: reads the row sources: each cell picks its transport at run time with
// no opacity device, and the Mk inmem-conn cell copies with CopyFrom.

package std

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// net3BenchDir holds the std-net3 bench rows, from std/.
const net3BenchDir = "../tests/bench-lanes/std-net3"

// net3Code is src without its comments (// to the end of the line and
// /* */ blocks), so a header that names a banned device is not read as
// using it.
func net3Code(src string) string {
	src = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(src, "")
	return regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(src, "")
}

// net3RowSources is each source file of a row: its cells by extension and
// the Mk cell's .mk files.
func net3RowSources(t *testing.T, row string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(net3BenchDir, row), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch filepath.Ext(p) {
		case ".c", ".rs", ".zig", ".go":
			out = append(out, p)
		case ".mk":
			if filepath.Base(p) != "pkg.mk" {
				out = append(out, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStdNet_ConnRowsPickTheTransportAtRunTime(t *testing.T) {
	devices := regexp.MustCompile(`std::hint::|black_box|\bvolatile\b|doNotOptimizeAway|go:noinline`)
	for _, row := range []string{"inmem-conn", "line-echo", "tcp-pingpong"} {
		srcs := net3RowSources(t, row)
		if len(srcs) < 5 {
			t.Fatalf("%s: %d sources, want the five cells", row, len(srcs))
		}
		for _, p := range srcs {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			code := net3Code(string(b))
			if m := devices.FindString(code); m != "" {
				t.Errorf("%s uses %q: an opacity device, banned by tools/bench (Rust) and unequal across cells", p, m)
			}
			// The in-memory rows pick the transport from the mode: every
			// cell names the null mode in code.
			if row != "tcp-pingpong" && !strings.Contains(code, `"null"`) {
				t.Errorf("%s does not pick its transport from the mode (no \"null\" in its code)", p)
			}
		}
	}
}

func TestStdNet_InmemConnCopiesWithCopyFrom(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(net3BenchDir, "inmem-conn", "mk", "inmemconn.mk"))
	if err != nil {
		t.Fatal(err)
	}
	code := net3Code(string(b))
	if n := strings.Count(code, ".CopyFrom("); n != 2 {
		t.Errorf("inmemconn.mk has %d CopyFrom calls, want 2 (Read and WriteAll)", n)
	}
	if regexp.MustCompile(`for i in 0\.\.n \{\s*(buf|s\.data)\[i\]`).MatchString(code) {
		t.Errorf("inmemconn.mk still copies the message with a byte loop")
	}
}
