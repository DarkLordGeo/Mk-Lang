// Why: std's API docs must state cost, allocation and errors, and must not
// contradict the code they describe.
// What: doc checks over std/fs, net, proc, log, cli, http and sim.
// How: reads every exported function's /// block for a cost, an allocation
// and its errors; checks two once-wrong comments and the replay's one-task limit.

package std

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rwIoDirs are the module directories these checks cover.
var rwIoDirs = []string{"fs", "net", "proc", "log", "cli", "http", "sim"}

var (
	rwIoExport = regexp.MustCompile(`^export [A-Z][A-Za-z0-9_]*(\.[A-Z][A-Za-z0-9_]*)?(\[[^\]]*\])?\(`)
	rwIoFails  = regexp.MustCompile(`\)\s*:\s*[^{]*!`)
	rwIoErrs   = regexp.MustCompile(`[Ff]ail|[Ee]rror|Canceled|OutOfMemory|NotFound|TooLarge|Closed|Refused|Reset|AddrInUse|InvalidAddress|Unreachable|NoSpace|Exists|BadRequest|Denied|Os\b`)
	rwIoSpace  = regexp.MustCompile(`\s+`)
)

// TestRwIo_ExportDocsGiveCostAllocationAndErrors: every exported
// function of the seven modules states its cost (O(...), or a count of
// runtime calls), what it allocates (or "nothing"), and, when its result
// can fail, what its errors mean.
func TestRwIo_ExportDocsGiveCostAllocationAndErrors(t *testing.T) {
	t.Parallel()
	n := 0
	for _, dir := range rwIoDirs {
		files, err := filepath.Glob(dir + "/*.mk")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasPrefix(filepath.Base(f), "tests_") {
				continue
			}
			lines := strings.Split(strings.ReplaceAll(readText(t, filepath.ToSlash(f)), "\r\n", "\n"), "\n")
			for i, l := range lines {
				if !rwIoExport.MatchString(l) {
					continue
				}
				n++
				doc := rwIoSpace.ReplaceAllString(docAbove(lines, i), " ")
				var miss []string
				if !strings.Contains(doc, "O(") && !strings.Contains(doc, "runtime call") && !strings.Contains(doc, "IoTable call") {
					miss = append(miss, "its worst-case cost")
				}
				if !strings.Contains(doc, "alloc") && !strings.Contains(doc, "nothing") {
					miss = append(miss, "what it allocates")
				}
				if rwIoFails.MatchString(l) && !rwIoErrs.MatchString(doc) {
					miss = append(miss, "what its errors mean")
				}
				if len(miss) > 0 {
					t.Errorf("%s:%d: %s: the /// block does not state %s (J3)", filepath.ToSlash(f), i+1, strings.SplitN(l, "(", 2)[0], strings.Join(miss, ", "))
				}
			}
		}
	}
	if n < 70 {
		t.Fatalf("found %d exported functions in %v; the pattern no longer matches the files", n, rwIoDirs)
	}
}

// TestRwIo_CommentsMatchTheCode checks two comments that once said the
// opposite of the code: serve.mk's header said the LF scan was not built
// on std.simd while HeadScan called simd.IndexByte, and ReadRequest's ///
// said the head was read one byte per Reader.Read while ReadHead reads one
// line per Reader.Until.
func TestRwIo_CommentsMatchTheCode(t *testing.T) {
	t.Parallel()
	serve := readText(t, "http/serve.mk")
	if strings.Contains(serve, "simd.IndexByte(") && regexp.MustCompile(`(?s)Not built:[^\n]*(\n//[^\n]*)*std\.simd`).MatchString(serve) {
		t.Errorf("http/serve.mk: the header says the std.simd scan for LF is not built, but the file calls simd.IndexByte")
	}
	parse := strings.Split(strings.ReplaceAll(readText(t, "http/parse.mk"), "\r\n", "\n"), "\n")
	for i, l := range parse {
		if strings.HasPrefix(l, "export ReadRequest(") {
			doc := rwIoSpace.ReplaceAllString(docAbove(parse, i), " ")
			if strings.Contains(doc, "one byte per Reader.Read") || !strings.Contains(doc, "Reader.Until") {
				t.Errorf("http/parse.mk:%d: ReadRequest's /// must say the head is read one line per Reader.Until, as ReadHead does: %q", i+1, doc)
			}
		}
	}
}

// TestRwIo_ReplayStatesTheOneTaskLimit: the docs must say that a replay is
// exact for one task's calls, since the log names no task; Recorder.New and
// Replayer.New say so.
func TestRwIo_ReplayStatesTheOneTaskLimit(t *testing.T) {
	t.Parallel()
	lines := strings.Split(strings.ReplaceAll(readText(t, "sim/record.mk"), "\r\n", "\n"), "\n")
	seen := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "export Recorder.New(") || strings.HasPrefix(l, "export Replayer.New(") {
			seen++
			doc := rwIoSpace.ReplaceAllString(docAbove(lines, i), " ")
			if !strings.Contains(doc, "one task") || !strings.Contains(doc, "no task") {
				t.Errorf("sim/record.mk:%d: the /// block must state the one-task limit of a replay (the log names no task): %q", i+1, doc)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("found %d of Recorder.New and Replayer.New in sim/record.mk", seen)
	}
}
