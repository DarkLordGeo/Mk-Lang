// Why: a release archive must be reproducible, so anyone can rebuild it and
// compare bytes, and unpacking one must never write outside its target,
// whatever the archive holds.
//
// What: Entry, WriteArchive (tar.gz or zip), CheckName, Limits, ErrUnsafe
// and Extract.
//
// How: the writer sorts entries and fixes times (EntryTime), owners and
// modes, so two runs give the same bytes. The reader refuses anything but
// plain files and directories under the archive's one top directory, and
// any name that is unsafe on some system (absolute, "..", a Windows device
// name).

package release

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EntryTime is every entry's modification time: the earliest a zip header
// can hold, so a rebuild never depends on the clock or the file times.
var EntryTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Entry is one regular file of an archive. Name is its '/' separated path
// under the top directory (Writer adds TopDir); the bytes come from Path on
// disk, or from Data when Path is "". Exec marks an executable (mode 0755,
// else 0644).
type Entry struct {
	Name string
	Path string
	Data []byte
	Exec bool
}

func (e Entry) open() (io.ReadCloser, int64, error) {
	if e.Path == "" {
		return io.NopCloser(strings.NewReader(string(e.Data))), int64(len(e.Data)), nil
	}
	f, err := os.Open(e.Path)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("release: %s is not a regular file", e.Path)
	}
	return f, fi.Size(), nil
}

// layout checks the entries' names and returns every directory they imply
// and the entries, each list sorted by name.
func layout(entries []Entry) ([]string, []Entry, error) {
	es := append([]Entry(nil), entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	dirs := map[string]bool{}
	seen := map[string]bool{}
	for _, e := range es {
		if err := CheckName(e.Name); err != nil {
			return nil, nil, err
		}
		key := strings.ToLower(e.Name)
		if seen[key] {
			return nil, nil, fmt.Errorf("release: two entries named %q", e.Name)
		}
		seen[key] = true
		for d := path.Dir(e.Name); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	var ds []string
	for d := range dirs {
		if seen[strings.ToLower(d)] {
			return nil, nil, fmt.Errorf("release: %q is a file and a directory", d)
		}
		ds = append(ds, d)
	}
	sort.Strings(ds)
	return ds, es, nil
}

// WriteArchive writes the entries under top/ as a zip (zip true) or a
// gzip-compressed tar to w. The bytes are a function of the entries alone:
// directories first, then files, each sorted by name, every time
// EntryTime, no owner, modes 0755 for directories and executables and 0644
// otherwise.
func WriteArchive(w io.Writer, top string, entries []Entry, isZip bool) error {
	if err := CheckName(top); err != nil || strings.Contains(top, "/") {
		return fmt.Errorf("release: bad top directory %q", top)
	}
	dirs, files, err := layout(entries)
	if err != nil {
		return err
	}
	dirs = append([]string{""}, dirs...)
	full := func(name string) string {
		if name == "" {
			return top
		}
		return top + "/" + name
	}
	if isZip {
		zw := zip.NewWriter(w)
		for _, d := range dirs {
			h := &zip.FileHeader{Name: full(d) + "/", Method: zip.Store, Modified: EntryTime}
			h.SetMode(os.ModeDir | 0o755)
			if _, err := zw.CreateHeader(h); err != nil {
				return err
			}
		}
		for _, e := range files {
			h := &zip.FileHeader{Name: full(e.Name), Method: zip.Deflate, Modified: EntryTime}
			h.SetMode(fileMode(e.Exec))
			fw, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			if err := copyEntry(fw, e); err != nil {
				return err
			}
		}
		return zw.Close()
	}
	gz, err := gzip.NewWriterLevel(w, gzip.DefaultCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	for _, d := range dirs {
		h := &tar.Header{Typeflag: tar.TypeDir, Name: full(d) + "/", Mode: 0o755, ModTime: EntryTime, Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
	}
	for _, e := range files {
		r, size, err := e.open()
		if err != nil {
			return err
		}
		h := &tar.Header{Typeflag: tar.TypeReg, Name: full(e.Name), Mode: int64(fileMode(e.Exec).Perm()), Size: size,
			ModTime: EntryTime, Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			r.Close()
			return err
		}
		n, err := io.Copy(tw, r)
		r.Close()
		if err != nil {
			return err
		}
		if n != size {
			return fmt.Errorf("release: %s changed while it was read", e.Path)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func fileMode(exec bool) os.FileMode {
	if exec {
		return 0o755
	}
	return 0o644
}

func copyEntry(w io.Writer, e Entry) error {
	r, size, err := e.open()
	if err != nil {
		return err
	}
	defer r.Close()
	n, err := io.Copy(w, r)
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("release: %s changed while it was read", e.Path)
	}
	return nil
}

// CheckName refuses an entry name that could leave the directory it is
// unpacked into, or that Windows reads otherwise: empty, absolute, a drive
// or a backslash, a '.' or '..' or empty element, a ':' (an alternate data
// stream), a control character, an element ending in '.' or a space, or a
// Windows device name (CON, PRN, AUX, NUL, COM1-9, LPT1-9, any extension).
func CheckName(name string) error {
	bad := func(why string) error { return fmt.Errorf("release: unsafe entry name %q (%s)", name, why) }
	if name == "" {
		return bad("empty")
	}
	if strings.ContainsAny(name, `\:`) {
		return bad("backslash or colon")
	}
	if strings.HasPrefix(name, "/") {
		return bad("absolute")
	}
	for _, el := range strings.Split(name, "/") {
		if el == "" || el == "." || el == ".." {
			return bad("empty, '.' or '..' element")
		}
		// Windows drops a trailing '.' or space, so `a.` is `a` and `.. `
		// is `..` there.
		if strings.HasSuffix(el, ".") || strings.HasSuffix(el, " ") {
			return bad("an element ending in '.' or a space")
		}
		if reservedDevice(el) {
			return bad("a Windows device name")
		}
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			return bad("control character")
		}
	}
	return nil
}

// reservedDevice reports whether a path element names a Windows device
// (CON, PRN, AUX, NUL, COM1 to COM9, LPT1 to LPT9) with or without an
// extension, in any case: Windows opens the device, not a file.
func reservedDevice(el string) bool {
	stem, _, _ := strings.Cut(el, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		return true
	}
	return false
}

// Limits bound what Extract writes. The zero value means the defaults.
type Limits struct {
	MaxFiles int   // 0: 200,000 entries
	MaxBytes int64 // 0: 8 GiB, counted while writing, never from headers
}

// ErrUnsafe is the error class of an archive Extract refuses.
var ErrUnsafe = errors.New("unsafe archive")

// Extract unpacks the archive file (a .zip by its name, else a .tar.gz)
// into dest, which must not exist, taking only the entries under top/ and
// stripping that prefix. It refuses (ErrUnsafe) a link, a device, any
// other entry type, a name CheckName refuses, an entry outside top/, two
// entries whose names fold to one, and an archive over the limits. Files
// get mode 0755 when the archive marks them executable, else 0644. onFile,
// when not nil, is called before each file is written, and its error stops
// the unpack (tests use it to interrupt one). On any error dest may hold a
// part of the tree: the caller unpacks under a temporary name and removes
// it on failure.
func Extract(archive, dest, top string, lim Limits, onFile func(name string) error) error {
	if lim.MaxFiles <= 0 {
		lim.MaxFiles = 200_000
	}
	if lim.MaxBytes <= 0 {
		lim.MaxBytes = 8 << 30
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("release: %s already exists", dest)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	x := &extractor{dest: dest, top: top + "/", lim: lim, onFile: onFile, seen: map[string]bool{}}
	if strings.HasSuffix(strings.ToLower(archive), ".zip") {
		return x.zip(archive)
	}
	return x.tarGz(archive)
}

type extractor struct {
	dest   string
	top    string
	lim    Limits
	onFile func(string) error
	seen   map[string]bool
	files  int
	bytes  int64
}

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsafe, fmt.Sprintf(format, args...))
}

// name maps an archive name to its path under dest, or "" for the top
// directory itself.
func (x *extractor) name(raw string, dir bool) (string, error) {
	x.files++
	if x.files > x.lim.MaxFiles {
		return "", refuse("more than %d entries", x.lim.MaxFiles)
	}
	n := raw
	if dir {
		n = strings.TrimSuffix(n, "/")
	}
	if n+"/" == x.top {
		return "", nil
	}
	rest, ok := strings.CutPrefix(n, x.top)
	if !ok {
		return "", refuse("%q is outside %s", raw, x.top)
	}
	if err := CheckName(rest); err != nil {
		return "", refuse("%v", err)
	}
	key := strings.ToLower(rest)
	if x.seen[key] {
		return "", refuse("two entries named %q", rest)
	}
	x.seen[key] = true
	return filepath.Join(x.dest, filepath.FromSlash(rest)), nil
}

func (x *extractor) file(raw string, r io.Reader, exec bool) error {
	p, err := x.name(raw, false)
	if err != nil {
		return err
	}
	if p == "" {
		return refuse("%q is a file", raw)
	}
	if x.onFile != nil {
		if err := x.onFile(strings.TrimPrefix(raw, x.top)); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode(exec))
	if err != nil {
		return err
	}
	left := x.lim.MaxBytes - x.bytes
	n, err := io.Copy(f, io.LimitReader(r, left+1))
	x.bytes += n
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > left {
		return refuse("more than %d bytes", x.lim.MaxBytes)
	}
	return nil
}

func (x *extractor) dir(raw string) error {
	p, err := x.name(raw, true)
	if err != nil || p == "" {
		return err
	}
	return os.MkdirAll(p, 0o755)
}

func (x *extractor) tarGz(archive string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(bufio.NewReader(f))
	if err != nil {
		return refuse("not a gzip file: %v", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return refuse("bad tar: %v", err)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = x.dir(h.Name)
		case tar.TypeReg:
			err = x.file(h.Name, tr, h.Mode&0o111 != 0)
		default:
			err = refuse("%q is not a file or a directory (type %q)", h.Name, h.Typeflag)
		}
		if err != nil {
			return err
		}
	}
}

func (x *extractor) zip(archive string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return refuse("bad zip: %v", err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		m := zf.Mode()
		switch {
		case m.IsDir():
			err = x.dir(zf.Name)
		case m.IsRegular():
			var r io.ReadCloser
			if r, err = zf.Open(); err == nil {
				err = x.file(zf.Name, r, m.Perm()&0o111 != 0)
				if cerr := r.Close(); err == nil && cerr != nil {
					err = refuse("%s: %v", zf.Name, cerr)
				}
			}
		default:
			err = refuse("%q is not a file or a directory (mode %v)", zf.Name, m)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
