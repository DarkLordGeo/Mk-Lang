// Why: std.x509 is granted unsafe for one local proof (x509.SystemPool's kept
// copy); the grant covers the whole module, so the module must stay as proven.
// What: TestX509_UnsafeBlocksPinned, TestX509_UnsafeBlocksPinMutants and
// TestX509_KeptTypesPinned.
// How: a text scan pins the three unsafe blocks in system.mk and the fields of
// every type the kept block holds; a change fails until the proof is reread.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// x509OtherUnsafe are the four forms other than an unsafe block that
// unsafe_modules admits (internal/driver's E1102, check's section clause
// and extern var, and borrow's E1102 for an `ends` primitive): an unsafe
// impl, an extern var, a section clause, and a signature's `ends` clause,
// which in a granted module may end nothing.
var x509OtherUnsafe = regexp.MustCompile(`\bunsafe\s+impl\b|\bextern\s+var\b|\bsection\s+"|\)[^{]*\bends\s+[A-Za-z_]`)

// x509CopyLit is a use of PoolCopy as a constructor or a pattern; a
// pattern is `PoolCopy(...) :=`.
var x509CopyLit = regexp.MustCompile(`\bPoolCopy\(`)

// x509UnsafeForms reads the .mk files of dir: the unsafe blocks per file,
// and as "file:line: text" every other unsafe form and every PoolCopy
// literal (a PoolCopy( that is not a destructuring pattern), comments
// left out.
func x509UnsafeForms(dir string) (blocks map[string]int, other, lits []string, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	blocks = map[string]int{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mk") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, nil, nil, err
		}
		if k := len(unsafeBlock.FindAllIndex(b, -1)); k > 0 {
			blocks[e.Name()] = k
		}
		for i, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
			code, _, _ := strings.Cut(line, "//")
			at := fmt.Sprintf("%s:%d: %s", e.Name(), i+1, strings.TrimSpace(code))
			if x509OtherUnsafe.MatchString(code) {
				other = append(other, at)
			}
			for _, m := range x509CopyLit.FindAllStringIndex(code, -1) {
				rest := code[m[1]:]
				if close := strings.Index(rest, ")"); close >= 0 && strings.HasPrefix(strings.TrimSpace(rest[close+1:]), ":=") {
					continue
				}
				lits = append(lits, at)
			}
		}
	}
	sort.Strings(other)
	sort.Strings(lits)
	return blocks, other, lits, nil
}

// x509PinErrors is what the pin finds wrong with dir.
func x509PinErrors(dir string) ([]string, error) {
	blocks, other, lits, err := x509UnsafeForms(dir)
	if err != nil {
		return nil, err
	}
	var errs []string
	if len(blocks) != 1 || blocks["system.mk"] != 3 {
		errs = append(errs, fmt.Sprintf("unsafe blocks: got %v, want map[system.mk:3] (MakeCopy, FreeCopy, KeepCopy; a new one needs a stated reason)", blocks))
	}
	if len(other) > 0 {
		errs = append(errs, fmt.Sprintf("other unsafe forms: got %q, want none (std.x509 has unsafe for KeepCopy's proof only)", other))
	}
	if len(lits) != 1 || !strings.HasPrefix(lits[0], "system.mk:") || !strings.Contains(lits[0], "return some(PoolCopy(") {
		errs = append(errs, fmt.Sprintf("PoolCopy literals: got %q, want MakeCopy's one in system.mk (KeepCopy's premise (d))", lits))
	}
	return errs, nil
}

// TestX509_UnsafeBlocksPinned: std/x509 has exactly three unsafe blocks,
// all in system.mk (MakeCopy's writes, FreeCopy's free, which only the
// tests call, and KeepCopy's cast), none of the four other forms the grant
// admits, and one PoolCopy literal, MakeCopy's (the proof's premise (d):
// only MakeCopy makes a PoolCopy). A new one fails here until the proof
// covers it and this pin moves with it.
func TestX509_UnsafeBlocksPinned(t *testing.T) {
	errs, err := x509PinErrors("x509")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range errs {
		t.Errorf("std/x509's %s", e)
	}
}

// TestX509_UnsafeBlocksPinMutants runs the pin over copies of std/x509,
// each with one addition the pin must refuse (each of the forms other
// than a block, the ends primitive among them), and over the unchanged
// copy, which it must pass.
func TestX509_UnsafeBlocksPinMutants(t *testing.T) {
	ents, err := os.ReadDir("x509")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, file, text string }{
		{"none", "", ""},
		{"a fourth block", "zz.mk", "Zz() {\n    unsafe \"zz\" {\n    }\n}\n"},
		{"an unsafe impl", "zz.mk", "struct ZzRaw {\n    p: int\n}\n\nunsafe impl Share for ZzRaw {}\n"},
		{"an extern var", "zz.mk", "extern var ZzEnd: u8\n"},
		{"a section clause", "zz.mk", "var ZzSec: u8 = 0 section \".zz\"\n"},
		{"an ends primitive", "zz.mk", "export struct ZzHold {\n    export r: ?&i32\n}\n\nexport ZzHold.Forget(mut self) ends self {}\n"},
		{"a second PoolCopy literal", "zz.mk", "ZzCopy(q: *u8): PoolCopy {\n    return PoolCopy(p: q, size: 0, owner: raw.Of(rt.ProcessHeap().Alloc()))\n}\n"},
		{"a positional PoolCopy literal", "zz.mk", "ZzCopy(q: *u8, o: *Alloc): PoolCopy {\n    return PoolCopy(q, 0, o)\n}\n"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join("x509", e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if c.file != "" {
			if err := os.WriteFile(filepath.Join(dir, c.file), []byte(c.text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		errs, err := x509PinErrors(dir)
		if err != nil {
			t.Fatal(err)
		}
		if (c.file == "") != (len(errs) == 0) {
			t.Errorf("%s: the pin said %q", c.name, errs)
		}
	}
}

// keptTypes is each type the kept block holds, by file, with its fields
// as declared (export dropped), in order.
var keptTypes = []struct {
	file, name string
	fields     []string
}{
	{"x509/pool.mk", "Pool", []string{"certs: []Certificate", "system: bool"}},
	{"x509/x509.mk", "Certificate", []string{
		"data: []u8",
		"tbs_span: Span", "sig_span: Span", "issuer_span: Span", "subject_span: Span",
		"spki_span: Span", "san_span: Span", "permitted_span: Span", "excluded_span: Span",
		"sig_alg: u8", "valid_from: time.Time", "valid_to: time.Time",
		"flags: u32", "path_len: int", "key_usage: u32",
	}},
	{"x509/x509.mk", "Span", []string{"at: int", "end: int"}},
	{"time/wall.mk", "Time", []string{"nanos: i64"}},
}

// TestX509_KeptTypesPinned: the fields of every type the kept block holds
// (Pool, Certificate, Span, time.Time), names and types. A new field fails
// here until Moved and KeptInBlock rebase or check it and the proof's
// premises (b) and (c) (no reference outside the block, no
// interior-mutable field) are read again.
func TestX509_KeptTypesPinned(t *testing.T) {
	for _, k := range keptTypes {
		b, err := os.ReadFile(filepath.FromSlash(k.file))
		if err != nil {
			t.Fatal(err)
		}
		b = []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
		head := regexp.MustCompile(`(?m)^(export )?struct ` + k.name + ` \{\n`)
		loc := head.FindIndex(b)
		if loc == nil {
			t.Errorf("std/%s: no struct %s", k.file, k.name)
			continue
		}
		body, _, ok := strings.Cut(string(b[loc[1]:]), "\n}")
		if !ok {
			t.Errorf("std/%s: struct %s has no end", k.file, k.name)
			continue
		}
		var got []string
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "//") {
				continue
			}
			got = append(got, strings.TrimPrefix(line, "export "))
		}
		if strings.Join(got, "; ") != strings.Join(k.fields, "; ") {
			t.Errorf("std/%s: struct %s's fields changed (KeepCopy's proof, Moved and KeptInBlock hold for these only)\n got: %s\nwant: %s",
				k.file, k.name, strings.Join(got, "; "), strings.Join(k.fields, "; "))
		}
	}
}
