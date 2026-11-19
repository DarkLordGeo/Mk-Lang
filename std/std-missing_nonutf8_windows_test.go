// Why: a Windows file name can hold an unpaired surrogate, which is not
// UTF-8, and fs.Dir must list it so it can be reopened.
// What: TestSTDMISS_DirUnpairedSurrogateWindows on a file named a,
// U+D800, b, created through CreateFileW.
// How: dir.mk gives the name as WTF-8, as Go's os.ReadDir does: Str() is
// none, Format shows U+FFFD, and fs.OpenPath reopens it from those bytes.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

func TestSTDMISS_DirUnpairedSurrogateWindows(t *testing.T) {
	exe := missBuild(t, missR6Prog)
	dir := t.TempDir()
	prefix := syscall.StringToUTF16(dir + `\`)
	name := append(prefix[:len(prefix)-1], 'a', 0xD800, 'b', 0)
	h, err := syscall.CreateFile(&name[0], syscall.GENERIC_WRITE, 0, nil, syscall.CREATE_NEW, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("CreateFileW with an unpaired surrogate: %v", err)
	}
	syscall.CloseHandle(h)
	if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := missR6Lines(missRunArgs(t, exe, dir))
	want := []string{
		"n 61eda08062 f 61efbfbdefbfbdefbfbd62 s0",
		"n 706c61696e f 706c61696e s1 stat-ok",
		"r 61eda08062 size 0",
	}
	if !slices.Equal(got, want) {
		t.Errorf("listing:\ngot  %q\nwant %q", got, want)
	}
	// The same entry set as os.ReadDir: Go gives the unpaired surrogate as
	// WTF-8 (ED A0 80) too, the bytes the listing above shows.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, fmt.Sprintf("%x", e.Name()))
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"61eda08062", "706c61696e"}) {
		t.Errorf("os.ReadDir gives %q", names)
	}
}
