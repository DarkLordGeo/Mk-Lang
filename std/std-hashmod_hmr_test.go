// Why: three performance defects in std.hash (a long dependency chain in
// CRC-32, a one-shot BLAKE3 that built a full state, missing benchmark rows)
// must not come back.
// What: TestSTDHASHMOD_CrcCarriedTermsLast, HMR2_Blake3OneShotBuildsNoState,
// HMR2_Blake3TreeShapes and HMR3_ShortAndStreamRows.
// How: reads the Mk source's term order, builds probes, and checks the rows'
// files under tests/bench-lanes/std-hashmod.

package std

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestSTDHASHMOD_CrcCarriedTermsLast: in both multi-word steps of
// CrcUpdate, only the four table terms of a (c ^ the first word) depend on
// the register c, so they must be the last four xors of the statement.
// Written first, all sixteen xors of the 16-byte step were on the
// loop-carried chain (as the release disassembly shows); written last,
// four are.
func TestSTDHASHMOD_CrcCarriedTermsLast(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("hash", "crc32.mk"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	start := strings.Index(text, "\nCrcUpdate(")
	if start < 0 {
		t.Fatal("no CrcUpdate in hash/crc32.mk")
	}
	body := text[start:]
	// A step's statement: "c = t[" up to the first line not ending in "^".
	stmt := regexp.MustCompile(`(?m)^\s*c = t\[(?:[^\n]*\^\n)*[^\n]*\n`)
	term := regexp.MustCompile(`t\[(?:(\d+) \+ )?int\(\(?([a-z])`)
	var steps []string
	for _, s := range stmt.FindAllString(body, -1) {
		if strings.Count(s, "t[") >= 8 { // not the byte tail's one term
			steps = append(steps, s)
		}
	}
	if len(steps) != 2 {
		t.Fatalf("found %d multi-term steps in CrcUpdate, want 2 (16-byte and 8-byte)", len(steps))
	}
	wantA := [][]string{{"3840", "3584", "3328", "3072"}, {"1792", "1536", "1280", "1024"}}
	for k, s := range steps {
		var vars strings.Builder
		var aBase []string
		for _, m := range term.FindAllStringSubmatch(s, -1) {
			vars.WriteString(m[2])
			if m[2] == "a" {
				aBase = append(aBase, m[1])
			}
		}
		seq := vars.String()
		if want := 16 >> k; len(seq) != want {
			t.Errorf("step %d has %d table terms, want %d: %q", k, len(seq), want, seq)
			continue
		}
		if !regexp.MustCompile(`^[bde]+aaaa$`).MatchString(seq) {
			t.Errorf("step %d: the four terms of a (on the chain through c) must come last, got the order %q", k, seq)
		}
		if strings.Join(aBase, " ") != strings.Join(wantA[k], " ") {
			t.Errorf("step %d: the terms of a use tables %v, want %v", k, aBase, wantA[k])
		}
	}
}

// hashModObjdump is llvm-objdump, found as internal/codegen's tests find
// clang (MK_LLVM, C:\Program Files\LLVM\bin, PATH), or "" when absent.
func hashModObjdump() string {
	name := "llvm-objdump"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var dirs []string
	if d := os.Getenv("MK_LLVM"); d != "" {
		dirs = append(dirs, d)
	}
	if runtime.GOOS == "windows" {
		dirs = append(dirs, `C:\Program Files\LLVM\bin`)
	} else {
		for v := 21; v >= 18; v-- {
			dirs = append(dirs, fmt.Sprintf("/usr/lib/llvm-%d/bin", v))
		}
	}
	for _, d := range dirs {
		if p := filepath.Join(d, name); hashModFileExists(p) {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func hashModFileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// hashModB3Short hashes every 32-byte piece of a buffer with the one-shot
// (oneShot) or through a Blake3State.
func hashModB3Short(oneShot bool) string {
	call := "hash.Blake3(buf[j..j + 32])"
	if !oneShot {
		call = "St(buf[j..j + 32])"
	}
	return `import std.hash

St(b: []u8): [32]u8 {
    var s := hash.Blake3State.New()
    s.Bytes(b)
    return s.Sum()
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    n := 4096
    buf := try sys.arena.Slice[u8](n)
    var x: u64 = 1
    for i in 0..n {
        x = x *% 6364136223846793005 +% 1442695040888963407
        buf[i] = Trunc[u8](x >> 56)
    }
    var sum: u64 = 0
    for r in 0..3 {
        var j := 0
        for j < n {
            d := ` + call + `
            sum = sum ^ u64(d[0]) ^ (u64(d[7]) << 8) ^ (u64(d[31]) << 16)
            j += 32
        }
        buf[r] = buf[r] ^ Trunc[u8](sum)
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
`
}

// TestSTDHASHMOD_Blake3OneShotBuildsNoState: the one-shot Blake3 no
// longer builds a Blake3State, whose New zero-fills the 1,728-byte
// chaining-value stack (a fill of 0x6c0 to 0x6e0 bytes) and whose literal
// is copied (0x738 bytes), once per call. Checked three ways: Blake3's body
// names no Blake3State; a release build of a loop of 32-byte one-shots
// passes no such length in its disassembly, while the same loop through a
// Blake3State does (the control: the scan sees the cost); and both
// programs print the same nonzero value.
func TestSTDHASHMOD_Blake3OneShotBuildsNoState(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("hash", "blake3.mk"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	i := strings.Index(text, "\nexport Blake3(")
	if i < 0 {
		t.Fatal("no export Blake3 in hash/blake3.mk")
	}
	body := text[i:]
	if j := strings.Index(body, "\n}"); j >= 0 {
		body = body[:j]
	}
	if strings.Contains(body, "Blake3State") {
		t.Errorf("the one-shot Blake3 builds a Blake3State again:\n%s", body)
	}
	od := hashModObjdump()
	if od == "" {
		t.Skip("llvm-objdump not found (MK_LLVM, C:\\Program Files\\LLVM\\bin, PATH)")
	}
	dis := func(exe string) string {
		t.Helper()
		out, err := exec.Command(od, "-d", "--no-show-raw-insn", exe).Output()
		if err != nil {
			t.Fatalf("llvm-objdump %s: %v", exe, err)
		}
		return string(out)
	}
	oneExe := hashModBuild(t, hashModB3Short(true), "-release")
	stExe := hashModBuild(t, hashModB3Short(false), "-release")
	// The fill or copy of the state passes its size, 1,536 to 2,047 bytes
	// (0x6c0 for the stack alone, 0x6e0 with the fields beside it, 0x738
	// for the whole literal), as the third argument of the memset or
	// memcpy loop (%r8 on Windows x64, %rdx on System V).
	big := regexp.MustCompile(`\$0x[67][0-9a-f]{2}, %(r8d|r8|edx|rdx)\b`)
	one, st := dis(oneExe), dis(stExe)
	if m := big.FindAllString(one, -1); len(m) > 0 {
		t.Errorf("the one-shot loop's disassembly passes a Blake3State-sized length to a fill or copy: %v", m)
	}
	if !big.MatchString(st) {
		t.Errorf("control: the Blake3State loop's disassembly passes no length of 0x600 to 0x7ff bytes; the scan no longer sees the cost it checks for")
	}
	a, c := hashModExec(t, oneExe), hashModExec(t, stExe)
	if strings.TrimSpace(a) == "0" {
		t.Errorf("the probe printed 0: its input repeats, so it checks nothing")
	}
	if a != c {
		t.Errorf("one-shot printed %q, the state printed %q", a, c)
	}
}

// TestSTDHASHMOD_Blake3TreeShapes checks the one-shot's subtree
// recursion and the state on every tree shape near the chunk counts 1 to
// 40, 64, 65, 128, 129 and 1,024 (lengths k * 1,024 - 1, k * 1,024 and
// k * 1,024 + 1, so the left subtree is every power of two and the right
// one is a lone chunk, a lone byte or a full subtree), against the Go
// model, in dev and release builds.
func TestSTDHASHMOD_Blake3TreeShapes(t *testing.T) {
	var ks []int
	for k := 1; k <= 40; k++ {
		ks = append(ks, k)
	}
	ks = append(ks, 64, 65, 128, 129, 1024)
	var lens []int
	for _, k := range ks {
		lens = append(lens, k*1024-1, k*1024, k*1024+1)
	}
	var lit []string
	for _, n := range lens {
		lit = append(lit, fmt.Sprint(n))
	}
	maxLen := lens[len(lens)-1]
	src := hashModPrelude + fmt.Sprintf(`
const TreeLens: [%d]int = [%s]

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var r := Rng(s: 11)
    buf := try sys.arena.Slice[u8](%d)
    Fill(mut r, buf)
    for k in 0..%d {
        n := TreeLens[k]
        a := hash.Blake3(buf[0..n])
        var s := hash.Blake3State.New()
        s.Bytes(buf[0..n / 3])
        s.Bytes(buf[n / 3..n])
        e := s.Sum()
        try Print(mut out, "{n} ", (n: n))
        try Hex(mut out, a[..])
        try Print(mut out, " ")
        try Hex(mut out, e[..])
        try Print(mut out, "\n")
    }
}
`, len(lens), strings.Join(lit, ", "), maxLen, len(lens))
	buf := make([]byte, maxLen)
	r := hmSplitmix{s: 11}
	r.fill(buf)
	var want strings.Builder
	for _, n := range lens {
		d := hmBlake3(buf[:n])
		h := hex.EncodeToString(d[:])
		fmt.Fprintf(&want, "%d %s %s\n", n, h, h)
	}
	for _, flags := range [][]string{nil, {"-release"}} {
		exe := hashModBuild(t, src, flags...)
		hashModCompare(t, hashModExec(t, exe), want.String())
	}
}

// TestSTDHASHMOD_ShortAndStreamRows: the digests and the states have
// rows on the inputs their users pass: every 64-byte message
// through the one-shot SHA-256, SHA-512 and BLAKE3, and each of the four
// states fed 1,500-byte pieces (not a multiple of any block, so the
// partial-block path of Bytes runs). Each row has an Mk column calling the
// function named, the std columns that exist (Go has no BLAKE3), and both
// checksums; the stream row's quick checksum equals hash-crc32's, since
// both print the CRCs of the same bytes.
func TestSTDHASHMOD_ShortAndStreamRows(t *testing.T) {
	type row struct {
		Name     string            `json:"name"`
		Args     []string          `json:"args"`
		Checksum string            `json:"checksum"`
		Quick    string            `json:"quick_checksum"`
		Sources  map[string]string `json:"sources"`
	}
	base := filepath.Join("..", "tests", "bench-lanes", "std-hashmod")
	load := func(dir string) row {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(base, dir, "bench.json"))
		if err != nil {
			t.Fatal(err)
		}
		var r row
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		return r
	}
	cases := []struct {
		dir   string
		calls []string
		goCol bool
	}{
		{"sha256-short", []string{"hash.Sha256(buf[j..j + 64])"}, true},
		{"sha512-short", []string{"hash.Sha512(buf[j..j + 64])"}, true},
		{"blake3-short", []string{"hash.Blake3(buf[j..j + 64])"}, false},
		{"crc32-stream", []string{"hash.Crc32State.New()", "s.Bytes(buf[j..e])", "s.Sum()", "j + 1500"}, true},
		{"sha256-stream", []string{"hash.Sha256State.New()", "s.Bytes(buf[j..e])", "s.Sum()", "j + 1500"}, true},
		{"sha512-stream", []string{"hash.Sha512State.New()", "s.Bytes(buf[j..e])", "s.Sum()", "j + 1500"}, true},
		{"blake3-stream", []string{"hash.Blake3State.New()", "s.Bytes(buf[j..e])", "s.Sum()", "j + 1500"}, false},
	}
	for _, c := range cases {
		r := load(c.dir)
		if r.Name != "hash-"+c.dir {
			t.Errorf("%s: name %q, want %q", c.dir, r.Name, "hash-"+c.dir)
		}
		if r.Checksum == "" || r.Quick == "" {
			t.Errorf("%s: a checksum is empty (checksum %q, quick %q)", c.dir, r.Checksum, r.Quick)
		}
		if len(r.Args) != 3 || r.Args[0] != "64" {
			t.Errorf("%s: args %v, want 64 MiB and reps and a seed", c.dir, r.Args)
		}
		if r.Sources["mk"] != "mk" || r.Sources["zig"] == "" {
			t.Errorf("%s: sources %v, want an Mk and a Zig column", c.dir, r.Sources)
		}
		if _, ok := r.Sources["go"]; ok != c.goCol {
			t.Errorf("%s: Go column present %v, want %v", c.dir, ok, c.goCol)
		}
		src, err := os.ReadFile(filepath.Join(base, c.dir, "mk", r.Name+".mk"))
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range c.calls {
			if !strings.Contains(string(src), call) {
				t.Errorf("%s: the Mk program does not contain %q", c.dir, call)
			}
		}
	}
	if s, l := load("crc32-stream"), load("crc32"); s.Quick != l.Quick {
		t.Errorf("hash-crc32-stream quick checksum %s differs from hash-crc32's %s", s.Quick, l.Quick)
	}
}
