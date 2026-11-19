// Why: url dependencies are downloaded from servers the user does not control,
// so hashes, URLs and unpacking must refuse anything unsafe.
// What: tests of hashes, URLs, tar.gz and zip unpacking, Windows names, the
// store, offline use and size limits, and FuzzUnpack.
// How: archives served by httptest; the hash is checked before the store.

package fetch

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"mk/internal/cache"
)

// entry is one archive entry for the builders: a regular file unless
// link or dir is set.
type entry struct {
	name, body string
	link       string // a symlink to link
	hard       bool   // with link: a hard link
	dir        bool
}

func tarBytes(t testing.TB, es []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range es {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			h.Typeflag, h.Size, h.Mode = tar.TypeDir, 0, 0o755
		case e.link != "" && e.hard:
			h.Typeflag, h.Linkname, h.Size = tar.TypeLink, e.link, 0
		case e.link != "":
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TarGz is a .tar.gz of es (exported to the driver's tests through a copy
// of this builder there; kept here for this package's tests).
func tarGz(t testing.TB, es []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(tarBytes(t, es)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipBytes(t testing.TB, es []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range es {
		if e.dir {
			if _, err := zw.Create(strings.TrimSuffix(e.name, "/") + "/"); err != nil {
				t.Fatal(err)
			}
			continue
		}
		fh := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.link != "" {
			fh.SetMode(os.ModeSymlink | 0o777)
		} else {
			fh.SetMode(0o644)
		}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}
		body := e.body
		if e.link != "" {
			body = e.link
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func paths(tr *Tree) []string {
	var out []string
	for _, f := range tr.Files {
		out = append(out, f.Path+"="+string(f.Data))
	}
	return out
}

func reason(err error) string {
	var fe *Error
	if errors.As(err, &fe) {
		return fe.Reason
	}
	if err == nil {
		return ""
	}
	return "other: " + err.Error()
}

func TestHashParse(t *testing.T) {
	h := Sum([]byte("abc"))
	s := h.String()
	if !strings.HasPrefix(s, "sha256:") || len(s) != 7+64 {
		t.Fatalf("String: %q", s)
	}
	for _, in := range []string{s, "sha256:" + strings.ToUpper(s[7:])} {
		if got, ok := ParseHash(in); !ok || got != h {
			t.Errorf("ParseHash(%q) = %v, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "sha256:", s[:len(s)-1], "b3:" + s[7:], "sha256:" + strings.Repeat("g", 64), s + "0"} {
		if _, ok := ParseHash(bad); ok {
			t.Errorf("ParseHash(%q) accepted", bad)
		}
	}
}

func TestCheckURL(t *testing.T) {
	for _, ok := range []string{"https://example.com/a.tar.gz", "http://127.0.0.1:8080/a.zip", "http://localhost/a.tar",
		"http://[::1]:9/x.tar.gz"} {
		if err := CheckURL(ok); err != nil {
			t.Errorf("CheckURL(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "example.com/a.tar.gz", "http://example.com/a.tar.gz", "ftp://example.com/a",
		"file:///etc/passwd", "https:///nohost", "mailto:x@y"} {
		if err := CheckURL(bad); reason(err) != ReasonInvalid {
			t.Errorf("CheckURL(%q) = %v, want invalid", bad, err)
		}
	}
}

func TestUnpackTarGz(t *testing.T) {
	data := tarGz(t, []entry{{name: "pkg.mk", body: "m"}, {name: "src/", dir: true}, {name: "./src/a.mk", body: "a"}})
	tr, err := Unpack(data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(paths(tr), ","), "pkg.mk=m,src/a.mk=a"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// A plain tar too.
	tr, err = Unpack(tarBytes(t, []entry{{name: "pkg.mk", body: "m"}}), Limits{})
	if err != nil || len(tr.Files) != 1 {
		t.Fatalf("tar: %v %v", tr, err)
	}
}

func TestUnpackZip(t *testing.T) {
	data := zipBytes(t, []entry{{name: "src/", dir: true}, {name: "src/b.mk", body: "b"}, {name: "pkg.mk", body: "m"}})
	tr, err := Unpack(data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(paths(tr), ","), "pkg.mk=m,src/b.mk=b"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestUnpackStripsTopDir(t *testing.T) {
	data := tarGz(t, []entry{{name: "ini-1.2/", dir: true}, {name: "ini-1.2/pkg.mk", body: "m"}, {name: "ini-1.2/src/a.mk", body: "a"}})
	tr, err := Unpack(data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(paths(tr), ","), "pkg.mk=m,src/a.mk=a"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// Two top-level entries: nothing stripped.
	tr, err = Unpack(zipBytes(t, []entry{{name: "a/x.mk", body: "x"}, {name: "b/y.mk", body: "y"}}), Limits{})
	if err != nil || strings.Join(paths(tr), ",") != "a/x.mk=x,b/y.mk=y" {
		t.Errorf("got %v %v", paths(tr), err)
	}
}

func TestUnpackRefuses(t *testing.T) {
	big := strings.Repeat("x", 100)
	cases := []struct {
		name string
		data func() []byte
		lim  Limits
	}{
		{"dotdot", func() []byte { return tarGz(t, []entry{{name: "../evil.mk", body: "x"}}) }, Limits{}},
		{"inner dotdot", func() []byte { return tarGz(t, []entry{{name: "a/../../evil.mk", body: "x"}}) }, Limits{}},
		{"absolute", func() []byte { return tarGz(t, []entry{{name: "/etc/evil", body: "x"}}) }, Limits{}},
		{"drive", func() []byte { return zipBytes(t, []entry{{name: "C:/evil.mk", body: "x"}}) }, Limits{}},
		{"backslash", func() []byte { return zipBytes(t, []entry{{name: `a\..\..\evil.mk`, body: "x"}}) }, Limits{}},
		{"zip dotdot", func() []byte { return zipBytes(t, []entry{{name: "../../evil.mk", body: "x"}}) }, Limits{}},
		{"symlink", func() []byte { return tarGz(t, []entry{{name: "a.mk", link: "/etc/passwd"}}) }, Limits{}},
		{"hard link", func() []byte { return tarGz(t, []entry{{name: "a.mk", link: "../x", hard: true}}) }, Limits{}},
		{"zip symlink", func() []byte { return zipBytes(t, []entry{{name: "a.mk", link: "../x"}}) }, Limits{}},
		{"device", func() []byte { return tarGz(t, []entry{{name: "src/CON.mk", body: "x"}}) }, Limits{}},
		{"trailing dot", func() []byte { return tarGz(t, []entry{{name: "src./a.mk", body: "x"}}) }, Limits{}},
		{"case clash", func() []byte { return tarGz(t, []entry{{name: "A.mk", body: "x"}, {name: "a.mk", body: "y"}}) }, Limits{}},
		{"file and dir", func() []byte { return tarGz(t, []entry{{name: "a", body: "x"}, {name: "a/b.mk", body: "y"}}) }, Limits{}},
		{"too many bytes", func() []byte { return tarGz(t, []entry{{name: "a.mk", body: big}}) }, Limits{MaxBytes: 99}},
		{"too many files", func() []byte { return tarGz(t, []entry{{name: "a.mk"}, {name: "b.mk"}}) }, Limits{MaxFiles: 1}},
		{"not an archive", func() []byte { return []byte("this is no archive at all, just bytes") }, Limits{}},
		// pkgdeps-2: directory entries count against the entry limit.
		{"too many dirs", func() []byte { return tarGz(t, manyDirs(11, false)) }, Limits{MaxFiles: 10}},
		{"one dir repeated", func() []byte { return tarGz(t, manyDirs(11, true)) }, Limits{MaxFiles: 10}},
		{"zip too many dirs", func() []byte { return zipBytes(t, manyDirs(11, false)) }, Limits{MaxFiles: 10}},
		{"dirs and a file", func() []byte { return tarGz(t, append(manyDirs(10, false), entry{name: "a.mk", body: "x"})) }, Limits{MaxFiles: 10}},
		// pkgdeps-6: the characters Windows forbids in a name.
		{"question mark", func() []byte { return tarGz(t, []entry{{name: "src/a?b.mk", body: "x"}}) }, Limits{}},
		{"star", func() []byte { return tarGz(t, []entry{{name: "src/a*b.mk", body: "x"}}) }, Limits{}},
		{"less than", func() []byte { return tarGz(t, []entry{{name: "a<b", body: "x"}}) }, Limits{}},
		{"greater than", func() []byte { return zipBytes(t, []entry{{name: "a>b", body: "x"}}) }, Limits{}},
		{"pipe", func() []byte { return tarGz(t, []entry{{name: "a|b", body: "x"}}) }, Limits{}},
		{"quote", func() []byte { return tarGz(t, []entry{{name: `a"b.mk`, body: "x"}}) }, Limits{}},
		{"dir with star", func() []byte { return tarGz(t, []entry{{name: "a*/", dir: true}}) }, Limits{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr, err := Unpack(c.data(), c.lim)
			if reason(err) != ReasonUnsafeArchive {
				t.Fatalf("Unpack = %v, %v; want unsafe-archive", tr, err)
			}
		})
	}
}

// manyDirs is n directory entries and no file: d0/ ... d<n-1>/, or n
// copies of same/.
func manyDirs(n int, same bool) []entry {
	out := make([]entry, n)
	for i := range out {
		out[i] = entry{name: "d" + strconv.Itoa(i) + "/", dir: true}
		if same {
			out[i].name = "same/"
		}
	}
	return out
}

// TestUnpackEntryLimit: exactly MaxFiles entries, directories included,
// unpack; one more is refused (pkgdeps-2).
func TestUnpackEntryLimit(t *testing.T) {
	es := append(manyDirs(9, false), entry{name: "a.mk", body: "x"})
	if _, err := Unpack(tarGz(t, es), Limits{MaxFiles: 10}); err != nil {
		t.Fatalf("10 entries at MaxFiles 10: %v", err)
	}
	es = append(es, entry{name: "d99/", dir: true})
	if _, err := Unpack(tarGz(t, es), Limits{MaxFiles: 10}); reason(err) != ReasonUnsafeArchive {
		t.Fatalf("11 entries at MaxFiles 10: %v", err)
	}
}

// TestCheckPathWindowsNames: every character Windows forbids in a file
// name is refused, so a tree that unpacks also writes on Windows
// (pkgdeps-6).
func TestCheckPathWindowsNames(t *testing.T) {
	for _, c := range `<>"|?*` {
		if err := CheckPath("src/a" + string(c) + "b.mk"); reason(err) != ReasonUnsafeArchive {
			t.Errorf("CheckPath with %q: %v", c, err)
		}
	}
	if err := CheckPath("src/a-b_c+d=e,f;g'h[i]j{k}l~m!n@o#p$q%r&s(t)u.mk"); err != nil {
		t.Errorf("ordinary punctuation refused: %v", err)
	}
}

func TestTreeWrite(t *testing.T) {
	tr := &Tree{Files: []File{{Path: "pkg.mk", Data: []byte("m")}, {Path: "src/a.mk", Data: []byte("a")}}}
	dir := filepath.Join(t.TempDir(), "out")
	if err := tr.Write(dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "src", "a.mk")); err != nil || string(b) != "a" {
		t.Fatalf("read back: %q %v", b, err)
	}
	if err := tr.Write(dir); err == nil {
		t.Error("Write over an existing directory succeeded")
	}
	evil := &Tree{Files: []File{{Path: "../evil", Data: nil}}}
	if err := evil.Write(filepath.Join(t.TempDir(), "e")); reason(err) != ReasonUnsafeArchive {
		t.Errorf("Write of ../evil: %v", err)
	}
}

// server serves body at /a.tar.gz and counts requests.
func server(t *testing.T, body []byte) (*httptest.Server, *atomic.Int64) {
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.URL.Path != "/a.tar.gz" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func store(t *testing.T) *cache.Store {
	st, err := cache.Open(t.TempDir(), cache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestArchiveFetchVerifyStore(t *testing.T) {
	body := tarGz(t, []entry{{name: "pkg.mk", body: "m"}})
	srv, n := server(t, body)
	st := store(t)
	f := &Fetcher{Store: st}
	got, fetched, err := f.Archive(srv.URL+"/a.tar.gz", Sum(body))
	if err != nil || !fetched || !bytes.Equal(got, body) {
		t.Fatalf("first: fetched=%v err=%v", fetched, err)
	}
	srv.Close()
	off := &Fetcher{Store: st, Offline: true}
	got, fetched, err = off.Archive(srv.URL+"/a.tar.gz", Sum(body))
	if err != nil || fetched || !bytes.Equal(got, body) {
		t.Fatalf("from the store: fetched=%v err=%v", fetched, err)
	}
	if n.Load() != 1 {
		t.Errorf("requests: %d, want 1", n.Load())
	}
	// A 404 is a fetch failure.
	srv2, _ := server(t, body)
	if _, _, err := (&Fetcher{}).Archive(srv2.URL+"/missing.tar.gz", Sum(body)); reason(err) != ReasonFetch {
		t.Errorf("404: %v", err)
	}
}

func TestArchiveHashMismatch(t *testing.T) {
	body := tarGz(t, []entry{{name: "pkg.mk", body: "m"}})
	srv, _ := server(t, body)
	st := store(t)
	wrong := Sum([]byte("something else"))
	if _, _, err := (&Fetcher{Store: st}).Archive(srv.URL+"/a.tar.gz", wrong); reason(err) != ReasonHashMismatch {
		t.Fatalf("got %v, want hash-mismatch", err)
	}
	if _, ok, _ := st.Get(cache.KindFetch, storeKey(wrong)); ok {
		t.Error("a mismatched archive was stored")
	}
	// A store entry whose bytes do not hash to the key is a miss.
	if err := st.Put(cache.KindFetch, storeKey(wrong), body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&Fetcher{Store: st, Offline: true}).Archive(srv.URL+"/a.tar.gz", wrong); reason(err) != ReasonOffline {
		t.Errorf("tampered entry: %v, want offline (a miss)", err)
	}
}

func TestArchiveOffline(t *testing.T) {
	_, n := server(t, nil)
	if _, _, err := (&Fetcher{Store: store(t), Offline: true}).Archive("https://example.com/a.tar.gz", Sum(nil)); reason(err) != ReasonOffline {
		t.Fatalf("got %v, want offline", err)
	}
	if n.Load() != 0 {
		t.Error("offline fetch reached the server")
	}
}

func TestArchiveTooLarge(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 1000)
	srv, _ := server(t, body)
	if _, _, err := (&Fetcher{MaxArchive: 999}).Archive(srv.URL+"/a.tar.gz", Sum(body)); reason(err) != ReasonUnsafeArchive {
		t.Fatalf("got %v, want unsafe-archive", err)
	}
}

func FuzzUnpack(f *testing.F) {
	f.Add(tarGz(f, []entry{{name: "pkg.mk", body: "m"}, {name: "src/a.mk", body: "a"}}))
	f.Add(zipBytes(f, []entry{{name: "x/pkg.mk", body: "m"}}))
	f.Add(tarBytes(f, []entry{{name: "../a", body: "m"}}))
	f.Fuzz(func(t *testing.T, data []byte) {
		tr, err := Unpack(data, Limits{MaxFiles: 1000, MaxBytes: 1 << 20})
		if err != nil {
			return
		}
		for _, file := range tr.Files {
			if err := CheckPath(file.Path); err != nil {
				t.Fatalf("Unpack returned a refused path %q", file.Path)
			}
		}
	})
}
