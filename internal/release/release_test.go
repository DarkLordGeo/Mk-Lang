// Why: a release archive is what users download and run, so it must be
// reproducible, safe to unpack and signed.
// What: TestVersions, TestWriteArchiveDeterministic, TestExtractRoundTrip,
// TestExtractRefuses, TestSums, TestSignVerify, TestTrustedKeys, TestKeyFile.
// How: sums, sign and verify round-trip with testdata/test.key, while a
// tampered file, a wrong key and a truncated signature are each refused.

package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// testKey is the test key of testdata/test.key.
func testKey(t testing.TB) ed25519.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "test.key"))
	if err != nil {
		t.Fatal(err)
	}
	k, err := ParsePrivate(data)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		cmp  int
	}{
		{"0.3.0", "0.2.0", 1}, {"0.2.0", "0.3.0", -1}, {"v0.3.0", "0.3.0", 0},
		{"0.10.0", "0.9.9", 1}, {"0.3.0-rc1", "0.3.0", -1}, {"0.3.0-rc2", "0.3.0-rc1", 1},
	} {
		a, err := ParseVersion(c.a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ParseVersion(c.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.Compare(b); got != c.cmp {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.cmp)
		}
	}
	for _, bad := range []string{"", "0.3", "0.3.0.1", "0.03.0", "0.3.x", "0.3.0-", "0.3.0-a/b", "-0.3.0"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) accepted", bad)
		}
	}
	for _, h := range Hosts {
		n := ArchiveName("0.3.0", h)
		v, host, ok := ParseArchiveName(n)
		if !ok || v != "0.3.0" || host != h {
			t.Errorf("ParseArchiveName(%s) = %s %s %v", n, v, host, ok)
		}
	}
	if ArchiveName("0.3.0", HostWindows) != "mk-0.3.0-x86_64-windows.zip" || ArchiveName("0.3.0", HostLinux) != "mk-0.3.0-x86_64-linux.tar.gz" {
		t.Error("archive names")
	}
	if ArchiveName("0.3.0", HostMacOS) != "mk-0.3.0-x86_64-macos.tar.gz" || ArchiveName("0.3.0", HostMacOSARM64) != "mk-0.3.0-aarch64-macos.tar.gz" {
		t.Error("macOS archive names")
	}
	for _, n := range []string{"SHA256SUMS", "mk-0.3-x86_64-linux.tar.gz", "mk-0.3.0-x86_64-linux.zip", "mk-0.3.0-arm64-linux.tar.gz",
		"mk-0.3.0-aarch64-linux.tar.gz", "mk-0.3.0-aarch64-macos.zip", "mk-0.3.0-arm64-macos.tar.gz", "mk-0.3.0-x86_64-darwin.tar.gz"} {
		if _, _, ok := ParseArchiveName(n); ok {
			t.Errorf("ParseArchiveName(%s) accepted", n)
		}
	}
	if got := FileURL("https://h/o/Mk/releases/", "0.3.0", SumsFile); got != "https://h/o/Mk/releases/download/v0.3.0/SHA256SUMS" {
		t.Errorf("FileURL = %s", got)
	}
	if got := FileURL("https://h/o/Mk/releases", "", SumsFile); got != "https://h/o/Mk/releases/latest/download/SHA256SUMS" {
		t.Errorf("FileURL latest = %s", got)
	}
	for _, c := range []struct{ goos, goarch, host string }{
		{"windows", "amd64", HostWindows}, {"linux", "amd64", HostLinux},
		{"darwin", "amd64", HostMacOS}, {"darwin", "arm64", HostMacOSARM64},
		{"linux", "arm64", ""}, {"windows", "arm64", ""}, {"freebsd", "amd64", ""},
	} {
		if got := HostOf(c.goos, c.goarch); got != c.host {
			t.Errorf("HostOf(%s, %s) = %q, want %q", c.goos, c.goarch, got, c.host)
		}
	}
}

// sampleEntries is a small bundle: an executable, a header in a deep
// directory, and an in-memory file, given out of order.
func sampleEntries(t *testing.T) []Entry {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "mk.exe")
	hdr := filepath.Join(dir, "stddef.h")
	if err := os.WriteFile(exe, bytes.Repeat([]byte("MZ binary "), 1000), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hdr, []byte("#define NULL 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return []Entry{
		{Name: "llvm/lib/clang/21/include/stddef.h", Path: hdr},
		{Name: "RELEASE.txt", Data: []byte("mk 0.3.0\n")},
		{Name: "bin/mk.exe", Path: exe, Exec: true},
	}
}

func writeArchive(t *testing.T, entries []Entry, isZip bool) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := WriteArchive(&b, "mk-0.3.0-x86_64-linux", entries, isZip); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestWriteArchiveDeterministic: the same entries in another order, with
// other file times, give the same bytes, in both formats.
func TestWriteArchiveDeterministic(t *testing.T) {
	for _, isZip := range []bool{true, false} {
		es := sampleEntries(t)
		a := writeArchive(t, es, isZip)
		for _, e := range es {
			if e.Path != "" {
				later := time.Now().Add(48 * time.Hour)
				if err := os.Chtimes(e.Path, later, later); err != nil {
					t.Fatal(err)
				}
			}
		}
		rev := []Entry{es[2], es[0], es[1]}
		b := writeArchive(t, rev, isZip)
		if !bytes.Equal(a, b) {
			t.Errorf("zip=%v: two writes differ", isZip)
		}
	}
	if err := WriteArchive(&bytes.Buffer{}, "top", []Entry{{Name: "a", Data: nil}, {Name: "A", Data: nil}}, true); err == nil {
		t.Error("two names that fold to one were written")
	}
	if err := WriteArchive(&bytes.Buffer{}, "top", []Entry{{Name: "../a"}}, true); err == nil {
		t.Error("an escaping name was written")
	}
}

// TestExtractRoundTrip: what WriteArchive wrote unpacks to the same files
// under dest, the top directory stripped, and the executable keeps its mode
// where the host has one.
func TestExtractRoundTrip(t *testing.T) {
	for _, isZip := range []bool{true, false} {
		es := sampleEntries(t)
		name := "a.tar.gz"
		if isZip {
			name = "a.zip"
		}
		arc := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(arc, writeArchive(t, es, isZip), 0o644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "out")
		if err := Extract(arc, dest, "mk-0.3.0-x86_64-linux", Limits{}, nil); err != nil {
			t.Fatalf("zip=%v: %v", isZip, err)
		}
		for _, e := range es {
			got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(e.Name)))
			if err != nil {
				t.Fatal(err)
			}
			want := e.Data
			if e.Path != "" {
				want, _ = os.ReadFile(e.Path)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("zip=%v: %s differs", isZip, e.Name)
			}
		}
		if runtime.GOOS != "windows" {
			fi, err := os.Stat(filepath.Join(dest, "bin", "mk.exe"))
			if err != nil || fi.Mode().Perm()&0o111 == 0 {
				t.Errorf("zip=%v: the executable lost its mode: %v %v", isZip, fi, err)
			}
		}
		if err := Extract(arc, dest, "mk-0.3.0-x86_64-linux", Limits{}, nil); err == nil {
			t.Error("Extract into an existing directory")
		}
		// An interruption stops the unpack with the hook's error.
		stop := errors.New("interrupted")
		if err := Extract(arc, filepath.Join(t.TempDir(), "x"), "mk-0.3.0-x86_64-linux", Limits{}, func(string) error { return stop }); !errors.Is(err, stop) {
			t.Errorf("zip=%v: onFile's error lost: %v", isZip, err)
		}
	}
}

type rawEntry struct {
	name     string
	typeflag byte
	mode     os.FileMode
	link     string
	data     string
}

func rawTarGz(t *testing.T, es []rawEntry) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range es {
		h := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o644, Linkname: e.link, Size: int64(len(e.data))}
		if e.typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte(e.data))
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func rawZip(t *testing.T, es []rawEntry) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, e := range es {
		h := &zip.FileHeader{Name: e.name}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.data))
	}
	zw.Close()
	return b.Bytes()
}

// TestExtractRefuses: links, devices, escaping or absolute names, names
// outside the top directory, names that fold to one, and archives over the
// limits are each ErrUnsafe.
func TestExtractRefuses(t *testing.T) {
	const top = "mk-0.3.0-x86_64-linux"
	reg := func(n string) rawEntry { return rawEntry{name: n, typeflag: tar.TypeReg, data: "x"} }
	tars := map[string][]rawEntry{
		"symlink":   {{name: top + "/bin/mk", typeflag: tar.TypeSymlink, link: "/etc/passwd"}},
		"hardlink":  {{name: top + "/bin/mk", typeflag: tar.TypeLink, link: top + "/x"}},
		"device":    {{name: top + "/dev", typeflag: tar.TypeChar}},
		"dotdot":    {reg(top + "/../evil")},
		"absolute":  {reg("/" + top + "/evil")},
		"backslash": {reg(top + `/a\..\..\evil`)},
		"outside":   {reg("other/bin/mk")},
		"colon":     {reg(top + "/a:stream")},
		"fold":      {reg(top + "/A"), reg(top + "/a")},
	}
	for name, es := range tars {
		arc := filepath.Join(t.TempDir(), "a.tar.gz")
		os.WriteFile(arc, rawTarGz(t, es), 0o644)
		err := Extract(arc, filepath.Join(t.TempDir(), "out"), top, Limits{}, nil)
		if !errors.Is(err, ErrUnsafe) {
			t.Errorf("tar %s: %v, want ErrUnsafe", name, err)
		}
	}
	zips := map[string][]rawEntry{
		"symlink": {{name: top + "/bin/mk", mode: os.ModeSymlink | 0o777, data: "/etc/passwd"}},
		"dotdot":  {{name: top + "/../evil", data: "x"}},
		"outside": {{name: "evil", data: "x"}},
	}
	for name, es := range zips {
		arc := filepath.Join(t.TempDir(), "a.zip")
		os.WriteFile(arc, rawZip(t, es), 0o644)
		err := Extract(arc, filepath.Join(t.TempDir(), "out"), top, Limits{}, nil)
		if !errors.Is(err, ErrUnsafe) {
			t.Errorf("zip %s: %v, want ErrUnsafe", name, err)
		}
	}
	arc := filepath.Join(t.TempDir(), "big.tar.gz")
	os.WriteFile(arc, rawTarGz(t, []rawEntry{{name: top + "/a", typeflag: tar.TypeReg, data: strings.Repeat("x", 100)}}), 0o644)
	if err := Extract(arc, filepath.Join(t.TempDir(), "out"), top, Limits{MaxBytes: 10}, nil); !errors.Is(err, ErrUnsafe) {
		t.Errorf("over MaxBytes: %v", err)
	}
	os.WriteFile(arc, rawTarGz(t, []rawEntry{reg(top + "/a"), reg(top + "/b"), reg(top + "/c")}), 0o644)
	if err := Extract(arc, filepath.Join(t.TempDir(), "out"), top, Limits{MaxFiles: 2}, nil); !errors.Is(err, ErrUnsafe) {
		t.Errorf("over MaxFiles: %v", err)
	}
	os.WriteFile(arc, []byte("not an archive"), 0o644)
	if err := Extract(arc, filepath.Join(t.TempDir(), "out"), top, Limits{}, nil); !errors.Is(err, ErrUnsafe) {
		t.Errorf("garbage: %v", err)
	}
}

func TestSums(t *testing.T) {
	s := Sums{"mk-0.3.0-x86_64-windows.zip": strings.Repeat("a", 64), "mk-0.3.0-x86_64-linux.tar.gz": strings.Repeat("b", 64)}
	text := s.Format()
	want := strings.Repeat("b", 64) + "  mk-0.3.0-x86_64-linux.tar.gz\n" + strings.Repeat("a", 64) + "  mk-0.3.0-x86_64-windows.zip\n"
	if string(text) != want {
		t.Errorf("Format:\n%s", text)
	}
	back, err := ParseSums(text)
	if err != nil || len(back) != 2 || back["mk-0.3.0-x86_64-windows.zip"] != strings.Repeat("a", 64) {
		t.Errorf("ParseSums: %v %v", back, err)
	}
	h := strings.Repeat("c", 64)
	for _, bad := range []string{"", h + "  a", h + " a\n", h + "  a\n" + h + "  a\n", strings.ToUpper(h) + "  a\n",
		h[:63] + "  a\n", h + "  dir/a\n", h + "  *a\n", h + "  a\n\n"} {
		if _, err := ParseSums([]byte(bad)); err == nil {
			t.Errorf("ParseSums(%q) accepted", bad)
		}
	}
}

// TestSignVerify: sign and verify round-trip; a tampered SHA256SUMS, a
// wrong key, an untrusted key, a truncated or malformed signature and an
// empty key list are each refused; a release build refuses the test key.
func TestSignVerify(t *testing.T) {
	priv := testKey(t)
	test := PublicOf(priv)
	if test.ID != TestKeyID || test.Public != TestKeyPublic || !IsTestKey(test) {
		t.Fatalf("testdata/test.key is not the test key: %+v", test)
	}
	sums := []byte(strings.Repeat("a", 64) + "  mk-0.3.0-x86_64-linux.tar.gz\n")
	sig := Sign(priv, sums)
	if k, err := Verify(sums, sig, []Key{test}); err != nil || k.ID != TestKeyID {
		t.Fatalf("round trip: %v %v", k, err)
	}
	_, otherPub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), sums...)
	tampered[0] = 'b'
	if _, err := Verify(tampered, sig, []Key{test}); !errors.Is(err, ErrBadSignature) {
		t.Errorf("tampered sums: %v", err)
	}
	if _, err := Verify(sums, sig, []Key{otherPub}); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("untrusted key: %v", err)
	}
	// A wrong key under the right id: the trusted entry's bytes are another
	// key's.
	wrong := Key{ID: TestKeyID, Public: otherPub.Public}
	if _, err := Verify(sums, sig, []Key{wrong}); err == nil {
		t.Error("a wrong key verified")
	}
	if _, err := Verify(sums, sig[:len(sig)-10], []Key{test}); !errors.Is(err, ErrBadSignature) {
		t.Errorf("truncated signature: %v", err)
	}
	if _, err := Verify(sums, sig[:len(sig)-1], []Key{test}); !errors.Is(err, ErrBadSignature) {
		t.Errorf("no newline: %v", err)
	}
	if _, err := Verify(sums, []byte(TestKeyID+"\n"), []Key{test}); !errors.Is(err, ErrBadSignature) {
		t.Errorf("no signature: %v", err)
	}
	short := TestKeyID + " " + base64.StdEncoding.EncodeToString(make([]byte, 10)) + "\n"
	if _, err := Verify(sums, []byte(short), []Key{test}); !errors.Is(err, ErrBadSignature) {
		t.Errorf("short signature: %v", err)
	}
	if _, err := Verify(sums, sig, nil); !errors.Is(err, ErrNoKeys) {
		t.Errorf("no keys: %v", err)
	}
	// A release build refuses the test key, even when it is passed in.
	old := base
	base = "https://example.invalid/releases"
	defer func() { base = old }()
	if _, err := Verify(sums, sig, []Key{test}); !errors.Is(err, ErrTestKey) {
		t.Errorf("release build, test key: %v", err)
	}
	if _, err := Verify(sums, sig, []Key{otherPub}); !errors.Is(err, ErrTestKey) {
		t.Errorf("release build, test key not passed: %v", err)
	}
}

// TestTrustedKeys: keys.go never lists the test key, and each key it
// lists is well formed. With none, verification fails closed.
func TestTrustedKeys(t *testing.T) {
	for _, k := range trusted {
		if IsTestKey(k) {
			t.Errorf("keys.go lists the test key %s", k.ID)
		}
		pub, err := base64.StdEncoding.DecodeString(k.Public)
		if err != nil || len(pub) != ed25519.PublicKeySize || KeyID(pub) != k.ID {
			t.Errorf("keys.go key %s is malformed", k.ID)
		}
	}
	if len(trusted) > 2 {
		t.Errorf("keys.go lists %d keys; the rule is the current one and at most the next", len(trusted))
	}
	if len(Trusted()) == 0 {
		sig := Sign(testKey(t), []byte("x\n"))
		if _, err := Verify([]byte("x\n"), sig, Trusted()); !errors.Is(err, ErrNoKeys) {
			t.Errorf("no trusted keys: %v", err)
		}
	}
}

func TestKeyFile(t *testing.T) {
	text, pub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ParsePrivate(text)
	if err != nil || PublicOf(priv) != pub {
		t.Fatalf("key file round trip: %v", err)
	}
	for _, bad := range []string{"", "mk-release-key-v1", "other " + strings.Repeat("A", 44), "mk-release-key-v1 AAAA"} {
		if _, err := ParsePrivate([]byte(bad)); err == nil {
			t.Errorf("ParsePrivate(%q) accepted", bad)
		}
	}
}
