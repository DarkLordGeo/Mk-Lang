// Why: the cimport tests need clang, a temporary package and line checks.
// What: clangFor, translate, wantLines, dumpIf and TestCImport_MacroCascade.
// How: clang comes from the toolchain once per run; tests skip when it is
// missing. $MK_CIMPORT_DUMP keeps the bindings for a person to read.

package cimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mk/internal/toolchain"
)

var (
	toolsOnce sync.Once
	tools     *toolchain.Tools
	resDir    string
	toolsErr  error
)

// clangFor finds clang, or skips the test with the reason.
func clangFor(t *testing.T) (*toolchain.Tools, string) {
	t.Helper()
	toolsOnce.Do(func() {
		tools, toolsErr = toolchain.Discover(toolchain.Options{})
		if toolsErr == nil {
			resDir, toolsErr = tools.ResourceDir(context.Background())
		}
	})
	if toolsErr != nil || tools.Clang == "" {
		t.Skipf("clang is needed: %v", toolsErr)
	}
	return tools, resDir
}

// translate writes the files (name to text) into a temporary package
// directory and translates one import of header h for target.
func translate(t *testing.T, target string, files map[string]string, imports ...Import) *Result {
	t.Helper()
	tl, rd := clangFor(t)
	dir := t.TempDir()
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := Request{Target: target, Triple: ParseTriple(target), Imports: imports, Include: []string{dir},
		WorkDir: filepath.Join(dir, ".work")}
	res, err := Translate(context.Background(), tl, rd, r)
	if err != nil {
		if ce, ok := err.(*Error); ok {
			t.Fatalf("%v\n%s\n%s", ce, ce.Command, ce.Stderr)
		}
		t.Fatal(err)
	}
	return res
}

// wantLines checks that every line of want appears in text, in order.
func wantLines(t *testing.T, text string, want ...string) {
	t.Helper()
	at := 0
	for _, w := range want {
		i := strings.Index(text[at:], w)
		if i < 0 {
			t.Errorf("binding lacks %q after byte %d\n%s", w, at, text)
			return
		}
		at += i + len(w)
	}
}

const sampleHeader = `#include <stdint.h>
#include <stddef.h>
typedef struct S { int a; unsigned b:3, c:5; char d; union { int x; float y; } u; struct S *next; } S;
#pragma pack(push, 1)
struct P { char a; int b; };
#pragma pack(pop)
enum E { E0, E1 = 5, E2 };
typedef enum { RED, GREEN } Color;
#define K 42
#define KS "hi\n"
#define KF 1.5
#define KN (-7LL)
#define KU 0xFFFFFFFFu
#define KP ((void*)-1)
#define KT int
#define FM(x) ((x)+1)
static inline int add(int a, int b) { return a + b; }
int vf(const char *fmt, ...);
typedef int (*cb)(void *, int);
int use_cb(cb f, void *ctx);
extern int gv;
struct Flex { int n; char data[]; };
size_t my_strlen(const char *s);
typedef struct sqlite3 sqlite3;
int open_db(const char *name, sqlite3 **db);
struct tm2 { int tm_sec; int dwSize; uint64_t big; double d; int arr[4]; };
`

// dumpIf writes text to $MK_CIMPORT_DUMP/name when that is set, for a
// person reading a binding.
func dumpIf(t *testing.T, name, text string) {
	if d := os.Getenv("MK_CIMPORT_DUMP"); d != "" {
		_ = os.MkdirAll(d, 0o755)
		if err := os.WriteFile(filepath.Join(d, name), []byte(text), 0o644); err != nil {
			t.Log(err)
		}
	}
}

// TestCImport_MacroCascade: a macro whose body is a declaration fragment
// (braces, a semicolon, or one through another macro) is not probed, so
// it cannot throw Clang's parser off the probe lines after it: the plain
// constants defined after it still bind (windows.h's PROPSHEETPAGE fields
// macros hid about 5,800 constants this way). A macro naming a typedef is
// a type alias.
func TestCImport_MacroCascade(t *testing.T) {
	res := translate(t, "x86_64-linux", map[string]string{"m.h": `#define FIELDS int a; union { int b; char c; }
#define OPEN_ONLY {
#define VIA FIELDS
#define WRAP(x) { x }
#define WRAPPED WRAP(1)
#define AFTER_ONE 41
#define AFTER_TWO (AFTER_ONE + 1)
#define PAREN_STR ")"
#define CHAR_PAREN ('(' + 0)
typedef unsigned int word_t;
#define WORD_ALIAS word_t
`}, Import{Name: "m", Headers: []string{"m.h"}})
	wantLines(t, res.Modules[0], "export type WordT = u32", "export const AfterOne: i32 = 41", "export const AfterTwo: i32 = 42",
		"export const ParenStr: str = \")\"", "export const CharParen: i32 = 40", "export type WordAlias = WordT")
	if st := res.Stats[0]; st.Skipped["macro is no constant"] != 4 {
		t.Errorf("skipped %v, want 4 macros that are no constant (FIELDS, OPEN_ONLY, VIA, WRAPPED)", st.Skipped)
	}
}

// TestExprShaped: the body test, through macros and past literals.
func TestExprShaped(t *testing.T) {
	bodies := map[string]string{"A": "(1 + B)", "B": "2", "C": "D;", "D": "x", "E": "F(3)", "F": "{ x }",
		"G": `"{;"`, "H": "'}'", "I": "(1", "J": "1)", "K": "K + 1", "L": "a[2]"}
	want := map[string]bool{"A": true, "B": true, "C": false, "E": false, "G": true, "H": true, "I": false, "J": false, "K": true, "L": true}
	for name, w := range want {
		if got := exprShaped(name, bodies, map[string]bool{}); got != w {
			t.Errorf("exprShaped(%s = %q) = %v, want %v", name, bodies[name], got, w)
		}
	}
}
