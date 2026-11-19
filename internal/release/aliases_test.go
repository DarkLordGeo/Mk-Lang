// Why: installing, switching and removing toolchains changes files the user
// relies on, so an alias, a stray link or a renamed path must never delete or
// point at the wrong toolchain.
// What: TestRemoveUseRefuseAliases, TestUseRetargetsInPlace,
// TestNamesWindowsRewrites and TestKeyRotation.
// How: every root is under the test's temporary directory.

package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRemoveUseRefuseAliases: a spelling of a version that is not
// exactly a name Versions lists is refused by Use and Remove, so a case
// alias or a trailing dot (one directory on Windows) never deletes
// current; the running toolchain is compared by file identity, not text.
func TestRemoveUseRefuseAliases(t *testing.T) {
	for _, alias := range []string{"0.3.0-RC1", "0.3.0-rc1.", "0.3.0-rc1..", "v0.3.0-rc1"} {
		t.Run(alias, func(t *testing.T) {
			in := Install{Root: t.TempDir()}
			fakeVersion(t, in, "0.3.0-rc1")
			fakeVersion(t, in, "0.2.0")
			if err := in.Use("0.3.0-rc1"); err != nil {
				t.Fatal(err)
			}
			if err := in.Remove(alias, ""); err == nil {
				t.Errorf("Remove(%q) accepted", alias)
			}
			if _, err := os.Stat(filepath.Join(in.Dir("0.3.0-rc1"), "bin", exeName())); err != nil {
				t.Errorf("Remove(%q) deleted current's mk: %v", alias, err)
			}
			if err := in.Use("0.2.0"); err != nil {
				t.Fatal(err)
			}
			if err := in.Use(alias); err == nil {
				t.Errorf("Use(%q) accepted", alias)
			}
			if cur, _ := in.Current(); cur != "0.2.0" {
				t.Errorf("current %q after a refused Use", cur)
			}
		})
	}
	// The running toolchain, named by its directory in another spelling.
	in := Install{Root: t.TempDir()}
	fakeVersion(t, in, "0.2.0")
	fakeVersion(t, in, "0.3.0")
	if err := in.Use("0.3.0"); err != nil {
		t.Fatal(err)
	}
	running := in.Dir("0.2.0") + string(filepath.Separator) + "bin" + string(filepath.Separator) + ".."
	if runtime.GOOS == "windows" {
		running = strings.ToUpper(running)
	}
	if err := in.Remove("0.2.0", running); err == nil {
		t.Error("removed the running toolchain")
	}
	if _, err := os.Stat(in.Dir("0.2.0")); err != nil {
		t.Errorf("the running toolchain is gone: %v", err)
	}
	// Another version running: 0.2.0 is neither current nor running.
	if err := in.Remove("0.2.0", in.Dir("0.3.0")); err != nil {
		t.Errorf("remove of a version neither current nor running: %v", err)
	}
}

// TestUseRetargetsInPlace: Use changes the existing current link's
// target rather than replacing it, so current never goes missing; it
// makes one when there is none (or it dangles), and it removes stray
// .current-* links a crash left.
func TestUseRetargetsInPlace(t *testing.T) {
	in := Install{Root: t.TempDir()}
	fakeVersion(t, in, "0.3.0")
	fakeVersion(t, in, "0.4.0")
	if err := in.Use("0.3.0"); err != nil {
		t.Fatal(err)
	}
	// Leftovers of a crash mid-switch: a new link and an old one.
	for _, n := range []string{".current-new-123", ".current-old-456"} {
		if err := MakeLink(filepath.Join(in.Root, n), in.Dir("0.4.0")); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.Lstat(in.CurrentLink())
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Use("0.4.0"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(in.CurrentLink())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && !os.SameFile(before, after) {
		t.Error("Use replaced the junction instead of retargeting it")
	}
	if cur, _ := in.Current(); cur != "0.4.0" {
		t.Errorf("current %q", cur)
	}
	if b, err := os.ReadFile(filepath.Join(in.CurrentLink(), "bin", exeName())); err != nil || string(b) != "mk 0.4.0" {
		t.Errorf("through current: %q %v", b, err)
	}
	des, _ := os.ReadDir(in.Root)
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".current-") {
			t.Errorf("stray link %s left", de.Name())
		}
	}
	// The targets of the stray links are whole.
	for _, v := range []string{"0.3.0", "0.4.0"} {
		if _, err := os.Stat(filepath.Join(in.Dir(v), "bin", exeName())); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	// current dangling (its version deleted by hand): Use still works.
	if err := os.RemoveAll(in.Dir("0.4.0")); err != nil {
		t.Fatal(err)
	}
	if err := in.Use("0.3.0"); err != nil {
		t.Fatalf("Use over a dangling current: %v", err)
	}
	if cur, _ := in.Current(); cur != "0.3.0" {
		t.Errorf("current %q", cur)
	}
	// No current at all: Use makes one.
	if err := RemoveLink(in.CurrentLink()); err != nil {
		t.Fatal(err)
	}
	if err := in.Use("0.3.0"); err != nil {
		t.Fatal(err)
	}
	if cur, _ := in.Current(); cur != "0.3.0" {
		t.Errorf("current %q after making it", cur)
	}
}

func fixTarGz(t *testing.T, names []string) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: n, Mode: 0o644, Size: 1}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte("x"))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

// TestNamesWindowsRewrites: an element ending in '.' or a space, and
// a Windows device name with any extension, are refused by CheckName, by
// the writer and by the reader, on every host.
func TestNamesWindowsRewrites(t *testing.T) {
	bad := []string{"a./evil", "bin/mk.exe.", "a /b", ".. /evil", "a/.. /evil", "... /x", "NUL", "con.txt",
		"a/aux", "Com1", "com9.log", "LPT1", "lpt9.tar.gz", "prn", "x/CON.", "Aux.h"}
	good := []string{"bin/mk.exe", "console.txt", "com10", "lpt", "a.b/c", "auxiliary/x", "nul2"}
	for _, n := range bad {
		if err := CheckName(n); err == nil {
			t.Errorf("CheckName(%q) accepted", n)
		}
		var w bytes.Buffer
		if err := WriteArchive(&w, "mk-0.3.0-x86_64-windows", []Entry{{Name: n, Data: []byte("x")}}, true); err == nil {
			t.Errorf("WriteArchive took %q", n)
		}
		arc := filepath.Join(t.TempDir(), "a.tar.gz")
		os.WriteFile(arc, fixTarGz(t, []string{"mk-0.3.0-x86_64-windows/" + n}), 0o644)
		dest := filepath.Join(t.TempDir(), "dest")
		if err := Extract(arc, dest, "mk-0.3.0-x86_64-windows", Limits{}, nil); !errors.Is(err, ErrUnsafe) {
			t.Errorf("Extract of %q: %v", n, err)
		}
	}
	for _, n := range good {
		if err := CheckName(n); err != nil {
			t.Errorf("CheckName(%q): %v", n, err)
		}
	}
}

// TestKeyRotation: with the current key and the next one listed, a
// release signed by the next verifies, one by a third key does not, a
// third key's signature under the next key's id does not, and a CRLF copy
// of SHA256SUMS does not.
func TestKeyRotation(t *testing.T) {
	_, curPub, _ := GenerateKey()
	nextText, nextPub, _ := GenerateKey()
	thirdText, _, _ := GenerateKey()
	next, _ := ParsePrivate(nextText)
	third, _ := ParsePrivate(thirdText)
	sums := []byte(strings.Repeat("a", 64) + "  mk-0.4.0-x86_64-linux.tar.gz\n")
	keys := []Key{curPub, nextPub}
	if k, err := Verify(sums, Sign(next, sums), keys); err != nil || k.ID != nextPub.ID {
		t.Errorf("signed by next: %v %v", k, err)
	}
	if _, err := Verify(sums, Sign(third, sums), keys); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("signed by a third key: %v", err)
	}
	forged := []byte(nextPub.ID + " " + strings.SplitN(string(Sign(third, sums)), " ", 2)[1])
	if _, err := Verify(sums, forged, keys); !errors.Is(err, ErrBadSignature) {
		t.Errorf("third's signature under next's id: %v", err)
	}
	crlf := bytes.ReplaceAll(sums, []byte("\n"), []byte("\r\n"))
	if _, err := Verify(crlf, Sign(next, sums), keys); err == nil {
		t.Error("CRLF sums verified")
	}
}
