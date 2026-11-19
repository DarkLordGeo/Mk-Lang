// Why: a runtime atomic whose ordering nobody justified is a latent race; the
// rule that each one names its edge must hold mechanically.
// What: TestRtSched_AtomicsNameTheirEdge over std/rt and std/task, and
// TestRtSched_AtomicsScanSeesMissing, the scan's check on a planted file.
// How: a lexical scan for atomic calls without an "edge" comment nearby.
//
// raw.Atomic's plain methods are sequentially consistent, so the comment says
// which happens-before edge, or which total-order argument, the operation is
// there for; "Edge: none" with the reason is allowed for counters. Its ordered
// forms (LoadOrd, StoreOrd, ...) are the runtime's few exceptions, and their
// comment names the weaker order and why it suffices.
//
// The check is lexical: it finds every name declared with an atomic type
// (a field, parameter, local or global of type raw.Atomic[...], &raw.Atomic
// or *raw.Atomic, and locals bound to a function that returns
// *raw.Atomic[...]), then every call .Load(, .Store(, .Add(, .Cas( or
// .Swap(, or its Ord form (.LoadOrd( and so on), on such a name, and
// requires a comment containing "edge" (any case) on that line or within
// the 8 lines above it. Test files are exempt.

package std

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	rtsAtomicDecl  = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*:\s*[&*]?(?:\[\w*\])?raw\.Atomic\[`)
	rtsAtomicNew   = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*:=\s*raw\.Atomic\[\w+\]\.New`)
	rtsAtomicFunc  = regexp.MustCompile(`^(?:export\s+)?([A-Z]\w*)\(.*\)\s*:\s*\*raw\.Atomic\[`)
	rtsAtomicBind  = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*:=\s*([A-Z]\w*)\(`)
	rtsAtomicCall  = regexp.MustCompile(`\.(Load|Store|Add|Cas|Swap)(?:Ord)?\(`)
	rtsAtomicEdge  = regexp.MustCompile(`(?i)//.*\bedges?\b`)
	rtsAtomicWindo = 8
)

// rtsAtomicFiles is every non-test .mk file of std/rt and std/task.
func rtsAtomicFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, dir := range []string{"rt", "task"} {
		m, err := filepath.Glob(filepath.Join(dir, "*.mk"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range m {
			if !strings.HasPrefix(filepath.Base(f), "tests_") {
				files = append(files, f)
			}
		}
	}
	sort.Strings(files)
	return files
}

func rtsReadLines(t *testing.T, f string) []string {
	t.Helper()
	fh, err := os.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	var lines []string
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// rtsReceiver is the name an operation at code[:end] is called on: the
// trailing identifier once balanced [index] groups and closing parentheses
// are stripped, so (*c).ring[i & 255] is ring and (*word) is word.
func rtsReceiver(code string) string {
	s := strings.TrimRight(code, " ")
	for {
		switch {
		case strings.HasSuffix(s, "]"):
			depth := 0
			i := len(s) - 1
			for ; i >= 0; i-- {
				if s[i] == ']' {
					depth++
				} else if s[i] == '[' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if i < 0 {
				return ""
			}
			s = s[:i]
			continue
		case strings.HasSuffix(s, ")"):
			s = s[:len(s)-1]
			continue
		}
		break
	}
	j := len(s)
	for j > 0 {
		c := s[j-1]
		if c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			j--
			continue
		}
		break
	}
	return s[j:]
}

// rtsCode is the part of a line before its comment.
func rtsCode(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

// rtsAtomicScan returns the number of atomic sites of the files (in the
// order given, text holding each file's lines) and one line per site with
// no edge comment within rtsAtomicWindo lines above it.
func rtsAtomicScan(files []string, text map[string][]string) (int, []string) {
	names := map[string]bool{}
	funcs := map[string]bool{}
	for _, f := range files {
		for _, l := range text[f] {
			c := rtsCode(l)
			for _, m := range rtsAtomicDecl.FindAllStringSubmatch(c, -1) {
				names[m[1]] = true
			}
			for _, m := range rtsAtomicNew.FindAllStringSubmatch(c, -1) {
				names[m[1]] = true
			}
			if m := rtsAtomicFunc.FindStringSubmatch(c); m != nil {
				funcs[m[1]] = true
			}
		}
	}
	for _, f := range files {
		for _, l := range text[f] {
			for _, m := range rtsAtomicBind.FindAllStringSubmatch(rtsCode(l), -1) {
				if funcs[m[2]] {
					names[m[1]] = true
				}
			}
		}
	}
	sites := 0
	var missing []string
	for _, f := range files {
		lines := text[f]
		for i, l := range lines {
			c := rtsCode(l)
			found := false
			for _, m := range rtsAtomicCall.FindAllStringIndex(c, -1) {
				if names[rtsReceiver(c[:m[0]])] {
					found = true
				}
			}
			if !found {
				continue
			}
			sites++
			ok := rtsAtomicEdge.MatchString(l)
			for k := i - 1; k >= 0 && k >= i-rtsAtomicWindo && !ok; k-- {
				ok = rtsAtomicEdge.MatchString(lines[k])
			}
			if !ok {
				missing = append(missing, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(f), i+1, strings.TrimSpace(l)))
			}
		}
	}
	return sites, missing
}

// TestRtSched_AtomicsNameTheirEdge: every atomic site of std/rt and std/task
// has an edge comment.
func TestRtSched_AtomicsNameTheirEdge(t *testing.T) {
	files := rtsAtomicFiles(t)
	if len(files) == 0 {
		t.Fatal("no runtime files found")
	}
	text := map[string][]string{}
	for _, f := range files {
		text[f] = rtsReadLines(t, f)
	}
	sites, missing := rtsAtomicScan(files, text)
	for _, m := range missing {
		t.Errorf("atomic operation without an edge comment within %d lines: %s", rtsAtomicWindo, m)
	}
	if sites < 50 {
		t.Fatalf("found only %d atomic sites: the scan is broken", sites)
	}
	t.Logf("%d atomic sites, %d without an edge comment", sites, len(missing))
}

// TestRtSched_AtomicsScanSeesMissing: the scan's own evidence. A planted
// file with an atomic field, a parameter, an array of atomics and a local
// bound to a function returning *raw.Atomic flags each unannotated site,
// an ordered form (LoadOrd) among them,
// and an "Edge:" comment within the window clears it.
func TestRtSched_AtomicsScanSeesMissing(t *testing.T) {
	plant := []string{
		"struct S {",
		"    a: raw.Atomic[u32]",
		"    r: [4]raw.Atomic[u64]",
		"}",
		"W(): *raw.Atomic[u32] {",
		"}",
		"F(s: &S, l: &raw.Atomic[u32]) {",
		"    s.a.Store(1)",
		"    _ = (*s).r[Trunc[int](x) & 3].Load()",
		"    w := W()",
		"    (*w).Add(1)",
		"    l.Cas(0, 1)",
		"    l.LoadOrd(0)",
		"    s.notatomic.Load()",
		"    // Edge: covered.",
		"    s.a.Swap(2)",
		"    s.a.StoreOrd(3, 2)",
		"}",
	}
	sites, missing := rtsAtomicScan([]string{"plant.mk"}, map[string][]string{"plant.mk": plant})
	if sites != 7 || len(missing) != 5 {
		t.Fatalf("sites %d, missing %d (%v); want 7 and 5", sites, len(missing), missing)
	}
}
