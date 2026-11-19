// Why: a freestanding build cuts std/core by file prefix, which works only if
// layer-1 files (mem_*, coll_*, text_*) never reach layer 2 (io_*, sync_*).
// What: TestLayerOneTextScan, TestFreestandingCut and their
// planted-bug tests; layers_test.go holds the layer-0 half.
// How: a text scan of every layer-1 call and name, and a check of the cut tree.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// layerTypeDecl matches a top-level type, const or var declaration; group 1
// is the name.
var layerTypeDecl = regexp.MustCompile(`^(?:export )?(?:unsafe )?(?:linear )?(?:view )?(?:packed(?:\(\d+\))? )?(?:struct|enum|error|interface|type|const|var) ([A-Z]\w*)\b`)

// layerString is a string literal (unsafe reasons, messages), dropped before
// the scan.
var layerString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// layerName is a capitalized identifier not reached through a dot (a dot
// precedes a method, field or variant name, which the call scan judges).
var layerName = regexp.MustCompile(`(^|[^.\w])([A-Z]\w*)\b`)

// layerCall is a call: an optional dot (a method), the name, optional type
// arguments, the open parenthesis.
var layerCall = regexp.MustCompile(`(\.?)\b([A-Za-z]\w*)(?:\[[^\]()]*\])?\(`)

// ioOnlyEntries are IoTable's fields (core_iodecl.mk) that no AllocTable,
// WriterTable or ReaderTable has: `.table.X` with X among them is an
// IoTable entry. read and write are left out: Reader's and Writer's tables
// have them too, and a text scan cannot tell the types apart (their
// wrappers IoRead and IoWrite are layer-2 functions, which it does see).
var ioOnlyEntries = []string{"open", "size", "sync", "close", "rename", "remove", "listen", "accept", "dial", "recv", "send", "now", "sleep", "random", "yield", "signal", "wait", "wake", "mkdir", "stat", "opendir", "readdir", "udp", "sendto", "recvfrom", "resolve", "spawn", "procwait", "prockill", "inherited"}

// layerViolation is one finding of the scan.
type layerViolation struct {
	file string // relative to std/
	line int
	name string // the layer-2 name or the IoTable entry
	what string
}

func (v layerViolation) String() string {
	return fmt.Sprintf("std/%s:%d: %s", v.file, v.line, v.what)
}

// scanLayerOne returns every place a layer-1 file of files (std/core
// files, CRLF normalized) reaches layer 2: a call of a free function or a
// method whose name only layer-2 files declare, a use of a type, const or
// free function name only layer-2 files declare (in a signature or a body),
// or a `.table.X` of an IoTable-only entry. Comments and string literals are
// not read.
func scanLayerOne(files []mkFile) []layerViolation {
	freeAt := map[string][]int{}
	methodAt := map[string][]int{}
	nameAt := map[string][]int{}
	for _, f := range files {
		l := coreLayer(f.path)
		if l < 0 {
			continue
		}
		for _, line := range strings.Split(f.text, "\n") {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			if m := layerTypeDecl.FindStringSubmatch(line); m != nil {
				nameAt[m[1]] = append(nameAt[m[1]], l)
				continue
			}
			if m := declLine.FindStringSubmatch(line); m != nil {
				if m[1] == "" {
					freeAt[m[2]] = append(freeAt[m[2]], l)
					nameAt[m[2]] = append(nameAt[m[2]], l)
				} else {
					methodAt[m[2]] = append(methodAt[m[2]], l)
				}
			}
		}
	}
	onlyTwo := func(ls []int) bool { return len(ls) > 0 && !slices.Contains(ls, 0) && !slices.Contains(ls, 1) }
	ioEntry := regexp.MustCompile(`\.table\.(` + strings.Join(ioOnlyEntries, "|") + `)\b`)
	var out []layerViolation
	for _, f := range files {
		if coreLayer(f.path) != 1 {
			continue
		}
		for i, line := range strings.Split(f.text, "\n") {
			code, _, _ := strings.Cut(layerString.ReplaceAllString(line, `""`), "//")
			code = strings.ReplaceAll(code, "raw.", "raw_") // std.raw's names are intrinsics
			seen := map[string]bool{}
			add := func(name, what string) {
				if !seen[name] {
					seen[name] = true
					out = append(out, layerViolation{f.path, i + 1, name, what})
				}
			}
			for _, c := range layerCall.FindAllStringSubmatch(code, -1) {
				if c[1] == "" && onlyTwo(freeAt[c[2]]) {
					add(c[2], "calls "+c[2]+", a function only layer-2 files declare")
				} else if c[1] == "." && onlyTwo(methodAt[c[2]]) {
					add(c[2], "calls ."+c[2]+", a method only layer-2 files declare")
				}
			}
			for _, m := range layerName.FindAllStringSubmatch(code, -1) {
				if onlyTwo(nameAt[m[2]]) {
					add(m[2], "names "+m[2]+", declared only in layer-2 files")
				}
			}
			for _, m := range ioEntry.FindAllStringSubmatch(code, -1) {
				add("table."+m[1], "reaches the IoTable entry "+m[1])
			}
		}
	}
	return out
}

// layerOneAllowed are the layer-1 places known to reach layer 2, each a
// file and a name; anything else fails. When an allowance no longer occurs
// the test logs it, so a fix needs no change here, and the entry is removed
// afterwards.
//
// Empty now: mem_internal.mk's MutexStateAt and MutexValueAt, which took
// *Mutex[T], moved into sync_mutex.mk, their only user, and mem_chan.mk no
// longer uses sync_chan.mk's ChanState, ChanWaiter, ChanMeta and WaitQ. So
// every layer-1 file is held to the rule.
var layerOneAllowed = map[string][]string{}

func layerAllowed(v layerViolation) bool {
	for _, n := range layerOneAllowed[v.file] {
		if n == "*" || n == v.name {
			return true
		}
	}
	return false
}

// TestLayerOneTextScan: no layer-1 file of std/core reaches layer 2
// beyond layerOneAllowed. The live tree's violations are
// logged, the allowed ones included, so the debt stays visible.
func TestLayerOneTextScan(t *testing.T) {
	t.Parallel()
	vs := scanLayerOne(mkFiles(t, "core"))
	used := map[string]bool{}
	for _, v := range vs {
		if layerAllowed(v) {
			used[v.file] = true
			t.Logf("allowed (listed exception): %s", v)
			continue
		}
		t.Errorf("%s (4.1 rule 5: a layer-1 body reaches no IoTable entry and no layer-2 item)", v)
	}
	var files []string
	for f := range layerOneAllowed {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if !used[f] {
			t.Logf("allowance for std/%s no longer occurs: remove it from layerOneAllowed", f)
		}
	}
}

// TestLayerOneTextScanSeesPlants: the scan fires on each kind of
// reach, planted into a copy of mem_alloc.mk's text in memory: an IoNow
// call (a layer-2 function), an IoTable-only entry through .table, a
// Mutex[int] parameter (a layer-2 type, in a signature, as the old
// mem_internal.mk helpers had), a .Lock call (a method only sync_mutex.mk
// declares) and MutexValueAt (a function sync_mutex.mk declares); and it
// does not fire on a string or a comment that
// names them, on AllocTable's entries, or on layer-0 names (Io, SysErr).
func TestLayerOneTextScanSeesPlants(t *testing.T) {
	t.Parallel()
	files := mkFiles(t, "core")
	i := slices.IndexFunc(files, func(f mkFile) bool { return f.path == "core/mem_alloc.mk" })
	if i < 0 {
		t.Fatal("core/mem_alloc.mk not found")
	}
	files[i].text += `
PlantedReach(io: Io, m: &Mutex[int]): int {
    // IoNow(io) and Mutex in a comment are not read
    raw.Fail(8, "IoNow(io) in a string is not read; MutexSpins")
    _ = IoNow(io)
    r := io.table.sleep(io, 0)
    g := m.Lock(io)
    v := MutexValueAt(raw.Of(m))
    if r.err == SysErr.Canceled {
        return MutexSpins
    }
    f := a.table.alloc
    return 0
}
`
	got := map[string]int{}
	for _, v := range scanLayerOne(files) {
		if v.file == "core/mem_alloc.mk" {
			got[v.name]++
		}
	}
	for _, name := range []string{"IoNow", "table.sleep", "Mutex", "Lock", "MutexSpins", "MutexValueAt"} {
		if got[name] != 1 {
			t.Errorf("planted %s: %d findings, want 1 (all: %v)", name, got[name], got)
		}
	}
	for name := range got {
		if !slices.Contains([]string{"IoNow", "table.sleep", "Mutex", "Lock", "MutexSpins", "MutexValueAt"}, name) {
			t.Errorf("finding %q in the plant, which reaches no layer-2 item", name)
		}
	}
}

// freestandingCut copies std with fixtures, deletes core/io_*.mk and
// core/sync_*.mk (layer 2), runs mk check --max-diagnostics 0 on an empty
// Main, and returns each diagnostic in a core_*, mem_*, coll_* or text_*
// file as (file relative to std/, "Exxxx message").
func freestandingCut(t *testing.T, fixtures map[string]string) [][2]string {
	t.Helper()
	root := t.TempDir()
	stdDir := copyStd(t, root, fixtures)
	for _, pat := range []string{"io_*.mk", "sync_*.mk"} {
		ms, err := filepath.Glob(filepath.Join(stdDir, "core", pat))
		if err != nil || len(ms) == 0 {
			t.Fatalf("no core/%s to cut (%v)", pat, err)
		}
		for _, m := range ms {
			if err := os.Remove(m); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte("export Main() {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, status := mkCmd(t, root, stdDir, "check", "--max-diagnostics", "0", "main.mk")
	if status == 3 || strings.Contains(out, " E0001 ") {
		t.Fatalf("mk check on the cut crashed (exit %d):\n%s", status, out)
	}
	lower := regexp.MustCompile(`^std/(core/(?:core|mem|coll|text)_\w+\.mk):\d+:\d+: (E\d{4} .*)$`)
	var got [][2]string
	for _, line := range strings.Split(out, "\n") {
		if m := lower.FindStringSubmatch(line); m != nil {
			got = append(got, [2]string{m[1], m[2]})
		}
	}
	return got
}

// TestFreestandingCut is the layering rule's purpose checked by the
// resolver: a copy of std without core/io_*.mk and core/sync_*.mk (layer 2)
// draws no diagnostic in a core_*, mem_*, coll_* or text_* file, beyond
// layerOneAllowed (a diagnostic in an allowed file must name an allowed
// name, or any name for "*"). The cut once reported mem_internal.mk's
// `Mutex` helpers and 40 uses of sync_chan.mk's types in mem_chan.mk; now
// it reports none.
func TestFreestandingCut(t *testing.T) {
	t.Parallel()
	got := freestandingCut(t, nil)
	for _, d := range got {
		ok := false
		for _, name := range layerOneAllowed[d[0]] {
			if name == "*" || strings.Contains(d[1], "`"+name+"`") {
				ok = true
			}
		}
		if ok {
			t.Logf("allowed (listed exception): std/%s: %s", d[0], d[1])
			continue
		}
		t.Errorf("without layer 2: std/%s: %s", d[0], d[1])
	}
	t.Logf("%d diagnostics in layer-0 and layer-1 files of the cut, every one allowed", len(got))
}

// TestFreestandingCutSeesPlant: the cut fails on the old helpers' shape,
// a layer-1 helper whose signature names Mutex[T], planted at the end of
// mem_alloc.mk: exactly one diagnostic, in mem_alloc.mk, naming `Mutex`.
func TestFreestandingCutSeesPlant(t *testing.T) {
	t.Parallel()
	src := readText(t, "core/mem_alloc.mk")
	planted := src + "\n// The plant of TestFreestandingCutSeesPlant.\nPlantedMutexAt[T](m: *Mutex[T]): *T {\n    return raw.Null[T]()\n}\n"
	got := freestandingCut(t, map[string]string{"core/mem_alloc.mk": planted})
	if len(got) != 1 || got[0][0] != "core/mem_alloc.mk" || !strings.Contains(got[0][1], "`Mutex`") {
		t.Errorf("want one diagnostic in core/mem_alloc.mk naming `Mutex`, got %q", got)
	}
}
