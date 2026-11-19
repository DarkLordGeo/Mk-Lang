// Why: std's arena, buffer, list, text and word code must also run in the
// compile-time evaluator, and give the same answers there.
// What: the compile-time tests (arenas, empty data, SpaceAt, word reads), their
// helpers (mkExe, copyStd, mkCheckWith, expectClean, otherLaneGap) and the package's TestMain.
// How: copies std, adds a fixture to std/core and runs `mk check`; a failed check
// is E0901 at a const, and a witness const that must fail shows the bodies ran.
//
// The package imports only the Go standard library: mk is a separate
// process, built once per test binary with the go command.

package std

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"
)

var mkBuild struct {
	once sync.Once
	exe  string
	err  string
}

// mkExe builds cmd/mk from this repository into a temporary directory.
func mkExe(t testing.TB) string {
	t.Helper()
	mkBuild.once.Do(func() {
		// MK_TEST_EXE runs these fixtures with another mk, for instance one
		// built with a pending eval change.
		if exe := os.Getenv("MK_TEST_EXE"); exe != "" {
			mkBuild.exe = exe
			return
		}
		goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
		if runtime.GOOS == "windows" {
			goTool += ".exe"
		}
		if _, err := os.Stat(goTool); err != nil {
			if p, err := exec.LookPath("go"); err == nil {
				goTool = p
			}
		}
		dir, err := packageTempDir("mkstd")
		if err != nil {
			mkBuild.err = err.Error()
			return
		}
		exe := filepath.Join(dir, "mk")
		if runtime.GOOS == "windows" {
			exe += ".exe"
		}
		cmd := exec.Command(goTool, "build", "-o", exe, "./cmd/mk")
		cmd.Dir = ".."
		if out, err := cmd.CombinedOutput(); err != nil {
			mkBuild.err = "go build ./cmd/mk: " + err.Error() + "\n" + string(out)
			return
		}
		mkBuild.exe = exe
	})
	if mkBuild.err != "" {
		t.Fatal(mkBuild.err)
	}
	return mkBuild.exe
}

// mkCheckWith copies std (its .mk files and pkg.mk), adds fixtures (paths
// relative to std/), and returns the output and exit status of mk check.
func mkCheckWith(t *testing.T, fixtures map[string]string) (string, int) {
	t.Helper()
	exe := mkExe(t)
	root := t.TempDir()
	stdDir := copyStd(t, root, fixtures)
	prog := filepath.Join(root, "main.mk")
	if err := os.WriteFile(prog, []byte("export Main(sys: &System) {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "check", prog)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MK_STD="+stdDir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(out.String(), "\r\n", "\n"), status
}

// copyStd copies every .mk and .def file (the Windows import-library
// definitions a native link needs) of std/ to root/std, then writes each
// fixture (a path relative to std/ and its text) over or beside them, and
// returns root/std, the directory MK_STD names.
func copyStd(t testing.TB, root string, fixtures map[string]string) string {
	t.Helper()
	stdDir := filepath.Join(root, "std")
	err := fs.WalkDir(os.DirFS("."), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".mk") || strings.HasSuffix(p, ".def")) {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(stdDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for p, text := range fixtures {
		dst := filepath.Join(stdDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return stdDir
}

// ptrCastICE is eval's message for the one std intrinsic it has no key for
// yet.
const ptrCastICE = "intrinsic std.raw.PtrCast has no evaluator implementation"

// expectClean runs fixture, whose one check const is `const X = [0; 1][e]`
// with e the number of failed checks, after adding the witness const
// `const Ran = [0; 1][e + 1]`, and wants exactly the witness's E0901. The
// witness fails only if e was really evaluated: an evaluation that stops
// without a value (today, any call of a generic function with a body,
// which needs mono's instances) reports nothing, so a check const alone
// could pass without running. When the output holds one of blockers (a known
// gap elsewhere in the compiler), the test skips naming it; once the gap is closed
// the same test must pass.
func expectClean(t *testing.T, name, fixture string, blockers ...string) {
	t.Helper()
	m := checkConst.FindStringSubmatch(fixture)
	if m == nil {
		t.Fatalf("fixture %s has no check const `const X = [0; 1][e]`", name)
	}
	fixture += "\nconst Ran = [0; 1][" + m[1] + " + 1]\n"
	if d := os.Getenv("MK_TEST_DUMP"); d != "" {
		_ = os.WriteFile(filepath.Join(d, "zz_"+name+".mk"), []byte(fixture), 0o644)
	}
	out, status := mkCheckWith(t, map[string]string{"core/zz_" + name + ".mk": fixture})
	for _, b := range blockers {
		if strings.Contains(out, b) {
			t.Skipf("blocked: %s", b)
		}
	}
	witness := "std/core/zz_" + name + ".mk:" + strconv.Itoa(strings.Count(fixture, "\n")) + ":"
	seen := 0
	lines := strings.Split(out, "\n")
	var stray, gaps []string
	for i, line := range lines {
		if !diagLine.MatchString(line) {
			continue
		}
		if strings.HasPrefix(line, witness) && strings.Contains(line, " E0901 ") {
			seen++
			continue
		}
		src := ""
		if i+1 < len(lines) {
			src = lines[i+1]
		}
		if g := otherLaneGap(line, src); g != "" {
			gaps = append(gaps, g+": "+line)
			continue
		}
		stray = append(stray, line)
	}
	if len(gaps) > 0 && len(stray) == 0 && seen == 0 {
		// Every diagnostic is a named compiler gap, which stops the
		// check before evaluation; the fixture itself is clean.
		t.Skipf("blocked by std gaps:\n%s", strings.Join(gaps, "\n"))
	}
	for _, line := range stray {
		t.Errorf("unexpected diagnostic: %s", line)
	}
	for _, g := range gaps {
		t.Errorf("unexpected diagnostic (a named gap, but not alone): %s", g)
	}
	if seen != 1 || status != 1 {
		t.Errorf("want only the witness's E0901 (the check ran) and exit 1; status %d, output:\n%s", status, out)
	}
}

// destructureSrc is the source line of a struct destructuring statement,
// `Name(...) := place`, as a diagnostic quotes it.
var destructureSrc = regexp.MustCompile(`^\s*\|\s+[A-Z]\w*\(.*\) := \w+\s*$`)

// otherLaneGap names the compiler pass and the defect when diag (a
// diagnostic's first line, with src its quoted source line) is a known
// defect of that pass in a correct std body, else "". Each entry is narrow:
// one code, one shape.
//   - borrow: `Name(...) := v` of a linear v is E0402 "this temporary is not
//     consumed", but a destructure consumes its value.
//   - check: in a `Slice[T]` body, self is typed `Slice[T]`, not `[]T`,
//     so `self.Items()` holds H(self) and Get and GetMut are E0602.
func otherLaneGap(diag, src string) string {
	switch {
	case strings.Contains(diag, " E0402 not-consumed: this temporary is not consumed") && destructureSrc.MatchString(src):
		return "borrow: a destructure of a linear value does not consume it"
	case strings.HasPrefix(diag, "std/core/core_slice.mk:") && strings.Contains(diag, " E0602 "):
		return "check: Slice[T] body sees self as Slice[T], not []T"
	}
	return ""
}

// checkConst is a fixture's check const; group 1 is its failure count.
var checkConst = regexp.MustCompile(`(?m)^const \w+ = \[0; 1\]\[(.+)\]$`)

// diagLine is the first line of a terminal diagnostic: path:line:col: Ennnn.
var diagLine = regexp.MustCompile(`^\S+:\d+:\d+: E\d{4} `)

// arenaInFrame is a fixture helper: an Arena whose one chunk is a byte array
// of the caller's frame and whose chunk source, the heap's Alloc, is never
// asked (Bump's fast path).
const arenaInFrame = `import std.raw

FrameHeader(heap: &Heap, chunk: mut []u8): ArenaHdr {
    unsafe "fixture" {
        return ArenaHdr(src: raw.Of(heap.Alloc()), own: Alloc(ctx: raw.Null[u8](), table: &ArenaTable),
            base: raw.DataMut(chunk), used: 0, size: chunk.Len(), chunks: raw.Null[Chunk](), next: 4096)
    }
}

EndFrameArena(a: Arena) {
    Arena(hdr: p) := a
    unsafe "fixture" {
        raw.Forget(p)
    }
}
`

// TestArenaBumpAtCompileTime: Arena.Dup over an arena whose chunk is
// evaluation memory runs at compile time. Bump once computed with
// raw.Addr and raw.FromAddr, which eval refuses (pointer-bytes).
func TestArenaBumpAtCompileTime(t *testing.T) {
	t.Parallel()
	expectClean(t, "r1a", arenaInFrame+`
DupInFrame(): int {
    var chunk: [64]u8 = [0; 64]
    heap := MakeHeap(raw.Null[u8](), raw.Of(&ArenaTable))
    var hdr := FrameHeader(&heap, mut chunk[..])
    unsafe "fixture" {
        a := Arena(hdr: raw.OfMut(mut hdr))
        var bad := 0
        s := a.Dup("hello") else {
            EndFrameArena(a)
            return 90
        }
        e := a.Dup("") else {
            EndFrameArena(a)
            return 91
        }
        if s != "hello" || e != "" || e.Len() != 0 {
            bad += 1
        }
        if raw.IsNull(raw.StrData(e)) {
            bad += 1
        }
        EndFrameArena(a)
        if hdr.used != 5 {
            bad += 1
        }
        return bad
    }
}

const R1a = [0; 1][DupInFrame()]
`)
}

// probeHeap is a heap over a byte array of the caller's frame: its state is
// the count of 16-byte units in use, kept in the array's first two bytes, so
// no table entry casts ctx. Possible only because ctx is a pointer.
const probeHeap = `import std.raw

const ProbeHeapBytes = 16384
const ProbeHeapTable = AllocTable(alloc: ProbeHeapAlloc, resize: ProbeHeapResize, free: ProbeHeapFree)

ProbeHeapAlloc(ctx: *u8, size: int, align: int): *u8 {
    unsafe "ctx is the fixture's byte array" {
        used := int(raw.Read(ctx)) + int(raw.Read(raw.Offset(ctx, 1))) * 256
        units := (size + 15) / 16
        if 16 + (used + units) * 16 > ProbeHeapBytes {
            return raw.Null[u8]()
        }
        n := used + units
        raw.Write(ctx, Trunc[u8](n))
        raw.Write(raw.Offset(ctx, 1), Trunc[u8](n / 256))
        return raw.Offset(ctx, 16 + used * 16)
    }
}

ProbeHeapResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    return false
}

ProbeHeapFree(ctx: *u8, p: *u8, size: int, align: int) {
}
`

// TestArenaLifecycleAtCompileTime: Arena.New, Dup, a chunk of its own
// for a large request, Alloc()'s table (alloc, resize in place, free), Sub,
// Reset and Free all run at compile time over a heap of evaluation memory
// Today eval has no key for raw.PtrCast, which every typed allocation
// needs, so the test skips naming that gap.
func TestArenaLifecycleAtCompileTime(t *testing.T) {
	t.Parallel()
	expectClean(t, "r1b", probeHeap+`
ArenaLife(): int {
    var mem: [16384]u8 = [0; 16384]
    unsafe "fixture" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&ProbeHeapTable))
        var a := Arena.New(&heap) else { return 90 }
        var bad := 0
        s := a.Dup("x") else {
            a.Free()
            return 91
        }
        if s != "x" {
            bad += 1
        }
        if raw.IsNull(Bump(a.hdr, 3000, 1)) {
            bad += 1
        }
        p := AllocBytes(a.Alloc(), 10, 8)
        if raw.IsNull(p) || !ResizeBytes(a.Alloc(), p, 10, 20, 8) {
            bad += 1
        }
        FreeBytes(a.Alloc(), p, 20, 8)
        var b := a.Sub() else {
            a.Free()
            return 92
        }
        t := b.Dup("yz") else {
            b.Free()
            a.Free()
            return 93
        }
        if t != "yz" {
            bad += 1
        }
        b.Free()
        a.Reset()
        u := a.Dup("w") else {
            a.Free()
            return 94
        }
        if u != "w" {
            bad += 1
        }
        a.Free()
        return bad
    }
}

const R1b = [0; 1][ArenaLife()]
`, ptrCastICE)
}

// TestBufStateLenField: a std-internal module builds BufState with
// its declared field names, len included, and reads them.
func TestBufStateLenField(t *testing.T) {
	t.Parallel()
	expectClean(t, "r2", `import std.raw

BufLen(): int {
    b := BufState(lock: raw.Atomic[u32].New(0), h: 0, buf: raw.Null[u8](), len: 3, cap: 8192, start: 1)
    return b.len + b.start - 4 + (b.cap - 8192)
}

const R2 = [0; 1][BufLen()]
`)
}

// TestEmptyListDataIsNonnull: a new List's Items() and Done() hold a
// nonnull data pointer (codegen marks it nonnull). List is
// generic, so this skips naming the driver's message until mono gives
// instance bodies to the evaluator.
func TestEmptyListDataIsNonnull(t *testing.T) {
	t.Parallel()
	expectClean(t, "r4a", arenaInFrame+`
EmptyList(): int {
    var chunk: [16]u8 = [0; 16]
    heap := MakeHeap(raw.Null[u8](), raw.Of(&ArenaTable))
    var hdr := FrameHeader(&heap, mut chunk[..])
    unsafe "fixture" {
        a := Arena(hdr: raw.OfMut(mut hdr))
        var bad := 0
        l := List[int].New(&a)
        if l.Len() != 0 || l.Items().Len() != 0 || raw.IsNull(raw.Data(l.Items())) {
            bad += 1
        }
        d := l.Done()
        if d.Len() != 0 || raw.IsNull(raw.DataMut(d)) {
            bad += 1
        }
        if raw.IsNull(Dangling[u64]()) {
            bad += 1
        }
        EndFrameArena(a)
        return bad
    }
}

const R4a = [0; 1][EmptyList()]
`, genericICE)
}

// genericICE is the driver's message for an evaluation that reaches a
// generic function with a body: it needs mono's instance body.
const genericICE = "compile-time evaluation needs a generic instance body"

// TestEmptyTextDataIsNonnull: the same for Text, and DanglingBytes is
// not null; neither needs a generic instance, so this runs today.
func TestEmptyTextDataIsNonnull(t *testing.T) {
	t.Parallel()
	expectClean(t, "r4b", arenaInFrame+`
EmptyText(): int {
    var chunk: [16]u8 = [0; 16]
    heap := MakeHeap(raw.Null[u8](), raw.Of(&ArenaTable))
    var hdr := FrameHeader(&heap, mut chunk[..])
    unsafe "fixture" {
        a := Arena(hdr: raw.OfMut(mut hdr))
        var bad := 0
        x := Text.New(&a)
        if raw.IsNull(x.ptr) || raw.IsNull(DanglingBytes()) {
            bad += 1
        }
        EndFrameArena(a)
        return bad
    }
}

const R4b = [0; 1][EmptyText()]
`)
}

// TestNoNullDataInEmptyConstructors: no std constructor stores a null
// data pointer, since an empty slice or str made from it would break
// codegen's nonnull attribute; a source scan, because the generic
// constructors cannot run at compile time without mono's instances.
func TestNoNullDataInEmptyConstructors(t *testing.T) {
	null := regexp.MustCompile(`\bptr: raw\.Null\[`)
	err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
			return err
		}
		b, err := FS.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if null.MatchString(line) {
				t.Errorf("std/%s:%d: a data pointer starts null: %s", p, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for file, ctor := range map[string]string{
		"core/coll_list.mk": "List[T](ptr: Dangling[T]()", "core/coll_heap.mk": "HeapList[T](ptr: Dangling[T]()",
		"core/text_text.mk": "Text(ptr: DanglingBytes()",
	} {
		if !strings.Contains(readText(t, file), ctor) {
			t.Errorf("std/%s: the empty constructor is not %s...)", file, ctor)
		}
	}
	if !strings.Contains(readText(t, "core/coll_heap.mk"), "HeapText(ptr: DanglingBytes()") {
		t.Error("std/core/coll_heap.mk: HeapText.New does not start at DanglingBytes()")
	}
}

// mkString spells s as an Mk string literal: printable ASCII as itself,
// everything else as \u{X}.
func mkString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r >= 0x20 && r < 0x7F && r != '"' && r != '\\' && r != '{' && r != '}' {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, `\u{%X}`, r)
	}
	b.WriteByte('"')
	return b.String()
}

// TestSpaceAtMatchesUnicode runs SpaceAt at compile time on every
// White_Space scalar, the scalars on either side of each, all of Latin-1 but
// NUL, and former or look-alike spaces (U+180E, U+200B, U+2060, U+FEFF) and
// 4-byte scalars, and compares each with Go's unicode.White_Space.
func TestSpaceAtMatchesUnicode(t *testing.T) {
	t.Parallel()
	set := map[rune]bool{}
	for r := rune(1); r < 0x100; r++ {
		set[r] = true
	}
	for _, r := range whiteSpaceRanges() {
		for c := r.lo - 1; c <= r.hi+1; c++ {
			set[c] = true
		}
	}
	for _, r := range []rune{0x7FF, 0x800, 0x180E, 0x200B, 0x2060, 0xFEFF, 0xFFFD, 0xFFFF, 0x10000, 0x1F600, 0x10FFFF} {
		set[r] = true
	}
	var runes []rune
	for r := range set {
		if r > 0 && utf8.ValidRune(r) {
			runes = append(runes, r)
		}
	}
	slices.Sort(runes)
	var text strings.Builder
	want := make([]string, len(runes))
	for i, r := range runes {
		text.WriteRune(r)
		want[i] = "0"
		if unicode.Is(unicode.White_Space, r) {
			want[i] = strconv.Itoa(utf8.RuneLen(r))
		}
	}
	expectClean(t, "s_g175", fmt.Sprintf(`
const SpaceText = %s
const SpaceWant: [%d]int = [%s]

SpaceCheck(): int {
    s := SpaceText
    var i := 0
    var j := 0
    var bad := 0
    for i < s.Len() {
        if j >= %d || SpaceAt(s, i) != SpaceWant[j] {
            bad += 1
        }
        i += ScalarLen(s.Byte(i))
        j += 1
    }
    if j != %d {
        bad += 1
    }
    return bad
}

const SG175 = [0; 1][SpaceCheck()]
`, mkString(text.String()), len(runes), strings.Join(want, ", "), len(runes), len(runes)))
}

// TestWordBytes8 runs Words.Next's fast-path test at compile time on
// words with each byte value at positions 0, 3 and 7 of a run of 'a': it
// holds exactly when every byte is in 0x21..0x7F.
func TestWordBytes8(t *testing.T) {
	t.Parallel()
	var words, want []string
	for _, pos := range []int{0, 3, 7} {
		for v := 0; v < 256; v++ {
			w := uint64(0x6161616161616161)
			w &^= 0xFF << (8 * pos)
			w |= uint64(v) << (8 * pos)
			words = append(words, fmt.Sprintf("0x%X", w))
			want = append(want, strconv.FormatBool(v >= 0x21 && v <= 0x7F))
		}
	}
	expectClean(t, "std15a", fmt.Sprintf(`
const Words8: [%d]u64 = [%s]
const Want8: [%d]bool = [%s]

Words8Check(): int {
    var bad := 0
    for i in 0..%d {
        if WordBytes8(Words8[i]) != Want8[i] {
            bad += 1
        }
    }
    return bad
}

const STD15a = [0; 1][Words8Check()]
`, len(words), strings.Join(words, ", "), len(want), strings.Join(want, ", "), len(words)))
}

// TestWordsAtEveryOffset: Words over the same ASCII and non-ASCII
// text started at offsets 0 to 7 gives Go's fields of White_Space at
// compile time, through the 8-byte fast path and its fallbacks, from every
// alignment. The fast path's load casts *u8 to *u64 (raw.PtrCast), so this
// skips naming the eval gap until eval has the key.
func TestWordsAtEveryOffset(t *testing.T) {
	t.Parallel()
	// Words of 7, 8, 9, 16 and 17 bytes end at every position of an 8-byte
	// window; one holds a 2-byte scalar after 8 ASCII bytes.
	base := "1234567 alpha  beta\u3000abcdefgh ijklmnopq\u2009rstuvwxyzABCDEFG HIJKLMNOPQRSTUVW\tXYZabcdefgh\u00e9ij\u0085" +
		"0123456789abcdefg gammadeltaepsilon-zeta\neta thetaiotakappalambdamu\u3000nu"
	isSpace := func(r rune) bool { return unicode.Is(unicode.White_Space, r) }
	var fixture strings.Builder
	fixture.WriteString("\nconst WordsBase = " + mkString(base) + "\n\nWordsCheck(): int {\n    var bad := 0\n")
	for k := 0; k < 8; k++ {
		fields := strings.FieldsFunc(base[k:], isSpace)
		var lits []string
		for _, f := range fields {
			lits = append(lits, mkString(f))
		}
		fmt.Fprintf(&fixture, "    bad += WordsFrom(WordsBase[%d..], [%s])\n", k, strings.Join(lits, ", "))
	}
	fixture.WriteString("    return bad\n}\n")
	// One WordsFrom per field count, since an array argument has a fixed length.
	counts := map[int]bool{}
	for k := 0; k < 8; k++ {
		counts[len(strings.FieldsFunc(base[k:], isSpace))] = true
	}
	if len(counts) != 2 {
		t.Fatalf("fixture text gives %d field counts, want 2 (offset 7 drops the first word)", len(counts))
	}
	src := fixture.String()
	src = strings.Replace(src, "WordsFrom(WordsBase[7..]", "WordsFromShort(WordsBase[7..]", 1)
	n := len(strings.FieldsFunc(base, isSpace))
	for _, c := range []struct {
		name string
		n    int
	}{{"WordsFrom", n}, {"WordsFromShort", n - 1}} {
		src += fmt.Sprintf(`
%s(s: str, want: [%d]str): int {
    var bad := 0
    var j := 0
    var it := s.Words()
    for w := it.Next() {
        if j >= %d || w != want[j] {
            bad += 1
        }
        j += 1
    }
    if j != %d {
        bad += 1
    }
    return bad
}
`, c.name, c.n, c.n, c.n)
	}
	src += "\nconst STD15b = [0; 1][WordsCheck()]\n"
	expectClean(t, "std15b", src, ptrCastICE)
}

// TestMain removes, after every test has run, the directories this package's
// tests share (packageTempDir): the mk built here, and builds one test makes
// and later ones reuse, so no single test may remove them.
func TestMain(m *testing.M) {
	code := m.Run()
	packageTemps.removeAll() // every packageTempDir, the mk built here among them
	os.Exit(code)
}
