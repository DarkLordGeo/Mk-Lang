// Why: an aligned wide load from a byte-aligned address is undefined
// behaviour, and std.core reads text and control bytes a word at a time.
// What: TestStdColl_NoAlignedWideByteLoads, a source scan of std/core.
// How: no raw.Read takes a cast from *u8 to a wider type, and LoadWord and
// GroupAt go through raw.ReadUnaligned. The IR half lives in internal/stdtest.

package std

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// wideRead matches raw.Read(raw.PtrCast[u8, T](...)) or raw.Cast with T
// wider than a byte, across line breaks.
var wideRead = regexp.MustCompile(`raw\.Read\(\s*raw\.(?:PtrCast|Cast)\[\s*u8\s*,\s*([^\]]+)\]`)

// TestStdColl_NoAlignedWideByteLoads scans std.core, where text and
// the Swiss table's control bytes are read: no raw.Read takes a raw.PtrCast
// or raw.Cast from *u8 to a wider type as its argument, and the word loads
// (LoadWord, which LoadU64, IndexByteIn, Words.Next, Hasher.Bytes and SipStr
// use, and the table's GroupAt) go through raw.ReadUnaligned. The IR half
// (every 8-byte load of the probe, str.From and Words.Next is align 1) needs
// the driver's OnLL hook, which package std cannot import; it is
// internal/stdtest's. std/sys and std/rt read OS structures at their natural
// alignment through the same cast, so they are outside this scan.
func TestStdColl_NoAlignedWideByteLoads(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("core", "*.mk"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no std/core files: %v", err)
	}
	unaligned := map[string]int{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, m := range wideRead.FindAllStringSubmatchIndex(text, -1) {
			to := strings.TrimSpace(text[m[2]:m[3]])
			if to == "u8" || to == "i8" || to == "bool" {
				continue
			}
			line := strings.Count(text[:m[0]], "\n") + 1
			t.Errorf("%s:%d: raw.Read of a *u8 cast to %s: an aligned load at a byte address; use raw.ReadUnaligned", f, line, to)
		}
		unaligned[filepath.Base(f)] += strings.Count(text, "raw.ReadUnaligned(")
	}
	// The two word loads the text and table paths use.
	for _, want := range []struct{ file, fn string }{{"core_str.mk", "LoadWord(b: []u8, i: int): u64"}, {"mem_table.mk", "GroupAt("}} {
		b, err := os.ReadFile(filepath.Join("core", want.file))
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		i := strings.Index(text, want.fn)
		if i < 0 {
			t.Fatalf("%s: %s not found", want.file, want.fn)
		}
		body := text[i:]
		if j := strings.Index(body, "\n}\n"); j >= 0 {
			body = body[:j]
		}
		if !strings.Contains(body, "raw.ReadUnaligned(raw.PtrCast[u8, u64]") {
			t.Errorf("%s: %s does not read its word with raw.ReadUnaligned", want.file, want.fn)
		}
	}
	t.Logf("raw.ReadUnaligned calls in std/core: %v", unaligned)
}
