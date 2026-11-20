// Why: the install layout (toolchains/<v>, the current link) must survive
// switches, removals and interrupted unpacks.
// What: TestMakeLink, TestUseListRemove, TestFindInstall and
// TestUnpackInterrupted.
// How: the current link is a junction on Windows; every root is under the
// test's temporary directory.

package release

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func exeName() string {
	if runtime.GOOS == "windows" {
		return "mk.exe"
	}
	return "mk"
}

// hostHere is this machine's host, or Linux's where releases carry none:
// the tests only need its archive layout.
func hostHere() string {
	if h := HostOf(runtime.GOOS, runtime.GOARCH); h != "" {
		return h
	}
	return HostLinux
}

// fakeVersion makes toolchains/<v>/bin/mk with a marker file.
func fakeVersion(t *testing.T, in Install, v string) {
	t.Helper()
	bin := filepath.Join(in.Dir(v), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, exeName()), []byte("mk "+v), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestMakeLink: the link reads back its target, its files are reachable
// through it, and removing it leaves the target whole.
func TestMakeLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(target, "f.txt"), []byte("kept"), 0o644)
	link := filepath.Join(root, "link")
	if err := MakeLink(link, target); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadLink(link); err != nil || !samePath(got, target) {
		t.Errorf("ReadLink = %q %v, want %q", got, err, target)
	}
	if b, err := os.ReadFile(filepath.Join(link, "f.txt")); err != nil || string(b) != "kept" {
		t.Errorf("through the link: %q %v", b, err)
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
		t.Errorf("Lstat mode %v %v: not a link", fi, err)
	}
	if err := MakeLink(link, target); err == nil {
		t.Error("MakeLink over an existing link")
	}
	if err := RemoveLink(link); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(target, "f.txt")); err != nil || string(b) != "kept" {
		t.Errorf("removing the link touched the target: %q %v", b, err)
	}
	if err := RemoveLink(target); err == nil {
		t.Error("RemoveLink removed a real directory")
	}
	if err := MakeLink(filepath.Join(root, "l2"), filepath.Join(root, "absent")); err == nil {
		t.Error("a link to nothing")
	}
}

func TestUseListRemove(t *testing.T) {
	in := Install{Root: t.TempDir()}
	if v, err := in.Current(); err != nil || v != "" {
		t.Errorf("empty root: current %q %v", v, err)
	}
	for _, v := range []string{"0.3.0", "0.10.0", "0.2.0"} {
		fakeVersion(t, in, v)
	}
	os.MkdirAll(filepath.Join(in.Toolchains(), ".unpack-0.4.0-123"), 0o755)
	vs, err := in.Versions()
	if err != nil || !slices.Equal(vs, []string{"0.2.0", "0.3.0", "0.10.0"}) {
		t.Errorf("Versions = %v %v", vs, err)
	}
	if err := in.Use("0.3.0"); err != nil {
		t.Fatal(err)
	}
	if v, _ := in.Current(); v != "0.3.0" {
		t.Errorf("current %q", v)
	}
	if b, err := os.ReadFile(filepath.Join(in.CurrentLink(), "bin", exeName())); err != nil || string(b) != "mk 0.3.0" {
		t.Errorf("through current: %q %v", b, err)
	}
	if err := in.Use("0.10.0"); err != nil {
		t.Fatal(err)
	}
	if v, _ := in.Current(); v != "0.10.0" {
		t.Errorf("current after the move %q", v)
	}
	if err := in.Use("9.9.9"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("use of an absent version: %v", err)
	}
	if v, _ := in.Current(); v != "0.10.0" {
		t.Errorf("a failed use moved current to %q", v)
	}
	// No leftovers of the move.
	des, _ := os.ReadDir(in.Root)
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	if !slices.Equal(names, []string{"current", "toolchains"}) {
		t.Errorf("root holds %v", names)
	}
	if err := in.Remove("0.10.0", ""); err == nil {
		t.Error("removed current")
	}
	if err := in.Remove("0.2.0", in.Dir("0.2.0")); err == nil {
		t.Error("removed the running mk")
	}
	if err := in.Remove("0.3.0", in.Dir("0.2.0")); err != nil {
		t.Fatal(err)
	}
	if vs, _ := in.Versions(); !slices.Equal(vs, []string{"0.2.0", "0.10.0"}) {
		t.Errorf("after remove: %v", vs)
	}
	if err := in.Remove("0.3.0", ""); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("remove twice: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(in.CurrentLink(), "bin", exeName())); err != nil || string(b) != "mk 0.10.0" {
		t.Errorf("remove disturbed current: %q %v", b, err)
	}
}

func TestFindInstall(t *testing.T) {
	in := Install{Root: t.TempDir()}
	fakeVersion(t, in, "0.3.0")
	exe := filepath.Join(in.Dir("0.3.0"), "bin", exeName())
	if root, ok := FindInstall(exe); !ok || !samePath(root, in.Root) {
		t.Errorf("FindInstall(%s) = %s %v", exe, root, ok)
	}
	if v := in.VersionOf(exe); v != "0.3.0" {
		t.Errorf("VersionOf = %q", v)
	}
	if root, ok := FindInstall(filepath.Join(in.Root, "current", "bin", exeName())); !ok || !samePath(root, in.Root) {
		t.Errorf("through current: %s %v", root, ok)
	}
	if _, ok := FindInstall(filepath.Join(t.TempDir(), "mk-0.3.0-x86_64-windows", "bin", exeName())); ok {
		t.Error("a self-contained archive found as an install")
	}
}

// TestUnpackInterrupted: an unpack stopped part way leaves no
// toolchains/<version> and no temporary directory, and current where it
// was; a whole one lands under its version.
func TestUnpackInterrupted(t *testing.T) {
	in := Install{Root: t.TempDir()}
	fakeVersion(t, in, "0.2.0")
	if err := in.Use("0.2.0"); err != nil {
		t.Fatal(err)
	}
	host := hostHere()
	arc := filepath.Join(t.TempDir(), ArchiveName("0.3.0", host))
	f, _ := os.Create(arc)
	err := WriteArchive(f, TopDir("0.3.0", host), []Entry{
		{Name: "bin/" + exeName(), Data: []byte("mk 0.3.0"), Exec: true},
		{Name: "llvm/bin/clang", Data: []byte("clang")},
	}, host == HostWindows)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("interrupted")
	n := 0
	err = in.Unpack(arc, "0.3.0", host, func(string) error {
		if n++; n == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("unpack: %v", err)
	}
	des, _ := os.ReadDir(in.Toolchains())
	if len(des) != 1 || des[0].Name() != "0.2.0" {
		t.Errorf("an interrupted unpack left %v", des)
	}
	if v, _ := in.Current(); v != "0.2.0" {
		t.Errorf("current moved to %q", v)
	}
	if err := in.Unpack(arc, "0.3.0", host, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(in.Dir("0.3.0"), "bin", exeName())); err != nil || string(b) != "mk 0.3.0" {
		t.Errorf("unpacked: %q %v", b, err)
	}
	if err := in.Unpack(arc, "0.3.0", host, nil); err == nil {
		t.Error("unpacked over an installed version")
	}
}
