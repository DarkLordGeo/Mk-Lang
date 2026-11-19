// Why: a Windows name with an unpaired surrogate must list as its WTF-8 and
// reopen from those bytes.
// What: TestRt_Wtf8Names, which runs the RT8-Oracle-Wtf8 block
// (std/task/tests_oracle.mk); Go's os.ReadDir gives the same WTF-8 bytes.
// How: names made with raw UTF-16 are listed, stated and removed by their
// listed bytes; each must match os.ReadDir, and only the directory is left.

//go:build windows

package std

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRt_Wtf8Names makes a file a<U+D800>b, a directory d<U+DC00> holding a
// file, and a plain file, and runs the block, which lists the directory, stats
// each name by its whole path in bytes, lists the directory with the
// surrogate inside its parent, and removes each file by its listed name. Each
// N line must be one of os.ReadDir's names, each S and R line must succeed,
// and after the run os.ReadDir finds only the directory left.
func TestRt_Wtf8Names(t *testing.T) {
	rtsNative(t)
	dir := t.TempDir()
	prefix := syscall.StringToUTF16(dir + `\`)
	prefix = prefix[:len(prefix)-1]
	file := append(slices.Clone(prefix), 'a', 0xD800, 'b', 0)
	h, err := syscall.CreateFile(&file[0], syscall.GENERIC_WRITE, 0, nil, syscall.CREATE_NEW, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("CreateFileW with an unpaired surrogate: %v", err)
	}
	syscall.CloseHandle(h)
	sub := append(slices.Clone(prefix), 'd', 0xDC00, 0)
	if err := syscall.CreateDirectory(&sub[0], nil); err != nil {
		t.Fatalf("CreateDirectoryW with an unpaired surrogate: %v", err)
	}
	inner := append(slices.Clone(sub[:len(sub)-1]), '\\', 'i', 'n', 0)
	h, err = syscall.CreateFile(&inner[0], syscall.GENERIC_WRITE, 0, nil, syscall.CREATE_NEW, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("CreateFileW inside it: %v", err)
	}
	syscall.CloseHandle(h)
	if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, e := range ents {
		want[hex.EncodeToString([]byte(e.Name()))] = e.IsDir()
	}
	if len(want) != 3 || want["61eda08062"] || !want["64edb080"] {
		t.Fatalf("os.ReadDir gives %v, not the three names this test made", want)
	}
	defer os.RemoveAll(dir)
	for _, profile := range []string{"dev", "release"} {
		if profile == "release" {
			// The dev run removed the files: make them again.
			h, err := syscall.CreateFile(&file[0], syscall.GENERIC_WRITE, 0, nil, syscall.CREATE_NEW, syscall.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal(err)
			}
			syscall.CloseHandle(h)
			if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("y"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		exe, all := rtsBuild(t, profile)
		found := false
		for _, b := range all {
			if !strings.HasPrefix(b.name, "RT8-Oracle-Wtf8") {
				continue
			}
			found = true
			r := rtsRun(t, exe, b.index, 60*time.Second, "MK_RT8_WTF8="+dir)
			rtsJudge(t, b, r, "")
			rt8CheckWtf8(t, profile, want, r.stderr)
		}
		if !found {
			t.Fatal("no RT8-Oracle-Wtf8 block")
		}
		left, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(left) != 1 || hex.EncodeToString([]byte(left[0].Name())) != "64edb080" {
			t.Errorf("%s: after the block os.ReadDir gives %d entries, want only the directory", profile, len(left))
		}
	}
}

// rt8CheckWtf8 checks the block's lines against os.ReadDir's names.
func rt8CheckWtf8(t *testing.T, profile string, want map[string]bool, out string) {
	t.Helper()
	seen := map[string]bool{}
	var inner []string
	opens := 0
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		switch f[0] {
		case "N":
			isDir, ok := want[f[1]]
			if !ok {
				t.Errorf("%s: the block listed %s, which os.ReadDir does not", profile, f[1])
			}
			if (f[2] == "2") != isDir {
				t.Errorf("%s: %q: kind differs from os.ReadDir's", profile, line)
			}
			seen[f[1]] = true
		case "O":
			opens++
			if f[2] != "1" {
				t.Errorf("%s: %q: rt.OpenBytes did not open the listed name", profile, line)
			}
		case "S", "R":
			if f[2] != "1" {
				t.Errorf("%s: %q: the listed name did not reopen", profile, line)
			}
		case "I":
			inner = append(inner, f[2])
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s: the block did not list %s", profile, name)
		}
	}
	if opens != 2 {
		t.Errorf("%s: %d O lines, want 2 (rt.OpenBytes of each file)", profile, opens)
	}
	if !slices.Equal(inner, []string{"696e"}) {
		t.Errorf("%s: inside the directory the block listed %v, want [696e]", profile, inner)
	}
}
