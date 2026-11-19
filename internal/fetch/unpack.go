// Why: an archive from the network may hold links, devices, path escapes,
// names that clash on Windows or macOS, or a zip bomb; none of that may reach
// the disk.
//
// What: Limits, File, Tree, Unpack (.tar.gz, .tar or .zip by its first bytes),
// CheckPath, and Tree.Write, which puts a checked tree on disk for mk vendor.
//
// How: the archive is read into memory, never onto disk, so a refused archive
// leaves nothing behind. unpacker counts entries and bytes against Limits,
// checks every name with CheckPath, then finish strips a lone top directory
// and refuses paths equal after case folding or a file that is a directory.

package fetch

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Limits bound what one archive may unpack to. The zero value means the
// defaults.
type Limits struct {
	MaxFiles int   // 0: DefaultMaxFiles; counts every file and directory entry
	MaxBytes int64 // 0: DefaultMaxBytes, counted while reading, never from headers
}

// Default limits of Unpack.
const (
	DefaultMaxFiles = 100_000
	DefaultMaxBytes = 1 << 30
)

// File is one regular file of a Tree: its path ('/' separated, relative,
// checked by CheckPath) and its bytes.
type File struct {
	Path string
	Data []byte
}

// Tree is an unpacked archive: its regular files in path order. No two
// paths are equal after ASCII case folding, and no path is a directory of
// another.
type Tree struct {
	Files []File
}

// Unpack reads the archive data (gzip-compressed tar, tar or zip, by its
// first bytes) into a Tree. Only regular files and directories are
// accepted; a link, a device, a FIFO or a path CheckPath refuses is
// ReasonUnsafeArchive, as is a tree over the limits. When every entry lies
// under one top-level directory, that directory is stripped.
func Unpack(data []byte, lim Limits) (*Tree, error) {
	if lim.MaxFiles <= 0 {
		lim.MaxFiles = DefaultMaxFiles
	}
	if lim.MaxBytes <= 0 {
		lim.MaxBytes = DefaultMaxBytes
	}
	u := &unpacker{lim: lim}
	var err error
	switch {
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		var zr *gzip.Reader
		zr, err = gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			err = u.tar(zr)
		}
	case len(data) >= 4 && string(data[:2]) == "PK" && (data[2] == 3 && data[3] == 4 || data[2] == 5 && data[3] == 6):
		err = u.zip(data)
	default:
		err = u.tar(bytes.NewReader(data))
	}
	if err != nil {
		var fe *Error
		if errors.As(err, &fe) {
			return nil, fe
		}
		return nil, errorf(ReasonUnsafeArchive, "the archive cannot be read: %v", err)
	}
	return u.finish(true)
}

// unpacker collects one archive's entries.
type unpacker struct {
	lim     Limits
	files   []File
	dirs    []string
	entries int // file and directory entries read, under lim.MaxFiles
	bytes   int64
}

// count admits one more file or directory entry, or refuses the archive
// over the entry limit (a directory header costs nothing to compress, so it
// counts as a file does).
func (u *unpacker) count() error {
	if u.entries >= u.lim.MaxFiles {
		return errorf(ReasonUnsafeArchive, "the archive holds more than %d entries", u.lim.MaxFiles)
	}
	u.entries++
	return nil
}

func (u *unpacker) tar(r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeReg, 0: // and the old NUL type of a regular file
			if err := u.file(h.Name, tr); err != nil {
				return err
			}
		case tar.TypeDir:
			if err := u.dir(h.Name); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			// Metadata; archive/tar applies the long-name ones itself.
		case tar.TypeSymlink, tar.TypeLink:
			return errorf(ReasonUnsafeArchive, "the archive holds a link, %s -> %s; links are not unpacked", h.Name, h.Linkname)
		default:
			return errorf(ReasonUnsafeArchive, "the archive entry %s is not a regular file or a directory", h.Name)
		}
	}
}

func (u *unpacker) zip(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := u.dir(f.Name); err != nil {
				return err
			}
		case mode.IsRegular():
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = u.file(f.Name, rc)
			rc.Close()
			if err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			return errorf(ReasonUnsafeArchive, "the archive holds a link, %s; links are not unpacked", f.Name)
		default:
			return errorf(ReasonUnsafeArchive, "the archive entry %s is not a regular file or a directory", f.Name)
		}
	}
	return nil
}

// name checks and cleans one entry name: "./" and a trailing "/" dropped.
func (u *unpacker) name(raw string) (string, error) {
	p := strings.TrimSuffix(strings.TrimPrefix(raw, "./"), "/")
	if err := CheckPath(p); err != nil {
		return "", err
	}
	return p, nil
}

func (u *unpacker) dir(raw string) error {
	if strings.TrimSuffix(strings.TrimPrefix(raw, "./"), "/") == "" || raw == "." {
		return nil // the archive's own root
	}
	if err := u.count(); err != nil {
		return err
	}
	p, err := u.name(raw)
	if err != nil {
		return err
	}
	u.dirs = append(u.dirs, p)
	return nil
}

func (u *unpacker) file(raw string, r io.Reader) error {
	p, err := u.name(raw)
	if err != nil {
		return err
	}
	if err := u.count(); err != nil {
		return err
	}
	left := u.lim.MaxBytes - u.bytes
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, left+1))
	if err != nil {
		return err
	}
	return u.add(p, buf.Bytes(), n)
}

// add records one checked, counted file of n bytes, or refuses the tree
// over the byte limit.
func (u *unpacker) add(p string, data []byte, n int64) error {
	if n > u.lim.MaxBytes-u.bytes {
		return errorf(ReasonUnsafeArchive, "the archive unpacks to more than %d bytes", u.lim.MaxBytes)
	}
	u.bytes += n
	u.files = append(u.files, File{Path: p, Data: data})
	return nil
}

// finish strips a lone top-level directory (an archive's, when strip is
// set; a git tree has none), sorts, and refuses paths that clash: equal
// after case folding (one file on Windows or macOS), or a file that is
// also a directory.
func (u *unpacker) finish(strip bool) (*Tree, error) {
	if top := u.loneTop(); strip && top != "" {
		for i := range u.files {
			u.files[i].Path = u.files[i].Path[len(top)+1:]
		}
		dirs := u.dirs[:0]
		for _, d := range u.dirs {
			if d != top {
				dirs = append(dirs, d[len(top)+1:])
			}
		}
		u.dirs = dirs
	}
	slices.SortFunc(u.files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	folded := map[string]string{}
	dirOf := map[string]bool{} // folded directories implied by the files and listed
	dirs := map[string]bool{}  // the same, as written
	for _, d := range u.dirs {
		dirOf[strings.ToLower(d)] = true
		dirs[d] = true
	}
	for _, f := range u.files {
		for d := f.Path; ; {
			i := strings.LastIndexByte(d, '/')
			if i < 0 {
				break
			}
			d = d[:i]
			dirOf[strings.ToLower(d)] = true
			dirs[d] = true
		}
	}
	// The limits as the git walk counts them (internal/git's tree.go):
	// every file and every directory once, and their paths' bytes with the
	// files' bytes, so a git tree and an archive of the same files meet
	// the same limits.
	if n := len(u.files) + len(dirs); n > u.lim.MaxFiles {
		return nil, errorf(ReasonUnsafeArchive, "the archive holds more than %d entries (%d files and directories)", u.lim.MaxFiles, n)
	}
	total := u.bytes
	for _, f := range u.files {
		total += int64(len(f.Path))
	}
	for d := range dirs {
		total += int64(len(d))
	}
	if total > u.lim.MaxBytes {
		return nil, errorf(ReasonUnsafeArchive, "the archive unpacks to more than %d bytes, its paths counted", u.lim.MaxBytes)
	}
	for _, f := range u.files {
		k := strings.ToLower(f.Path)
		if first, ok := folded[k]; ok {
			return nil, errorf(ReasonUnsafeArchive, "the archive holds %s and %s, which differ only in case", first, f.Path)
		}
		folded[k] = f.Path
		if dirOf[k] {
			return nil, errorf(ReasonUnsafeArchive, "the archive holds %s both as a file and as a directory", f.Path)
		}
	}
	return &Tree{Files: u.files}, nil
}

// loneTop is the one top-level directory every file lies under, or "".
func (u *unpacker) loneTop() string {
	top := ""
	for _, f := range u.files {
		first, _, ok := strings.Cut(f.Path, "/")
		if !ok || top != "" && first != top {
			return ""
		}
		top = first
	}
	for _, d := range u.dirs {
		if first, _, _ := strings.Cut(d, "/"); first != top {
			return ""
		}
	}
	return top
}

// windowsDevices are the names Windows maps to devices in any directory,
// with any extension.
var windowsDevices = []string{"con", "prn", "aux", "nul", "conin$", "conout$",
	"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9", "com¹", "com²", "com³",
	"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9", "lpt¹", "lpt²", "lpt³"}

// CheckPath accepts a relative '/'-separated path that names the same file
// on every supported OS and stays inside the directory it is joined to: no
// empty, "." or ".." segment, no leading '/', no '\', ':' or control
// character (a drive letter, an alternate data stream, a NUL), none of the
// other characters Windows forbids in a name (< > " | ? *), no segment
// ending in '.' or ' ' (Windows drops those), no Windows device name, and
// no segment ".git" or its short name "git~1" in any case (git refuses to
// check them out, and internal/git's walk refuses the entry).
func CheckPath(p string) error {
	bad := func(why string) error {
		return errorf(ReasonUnsafeArchive, "the archive path %q is refused: %s", p, why)
	}
	if p == "" {
		return bad("it is empty")
	}
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c < 0x20 || c == 0x7f:
			return bad("it holds a control character")
		case c == '\\':
			return bad("it holds a backslash")
		case c == ':':
			return bad("it holds a colon (a drive or a stream)")
		case strings.IndexByte(`<>"|?*`, c) >= 0:
			return bad("it holds " + string(c) + ", which Windows forbids in a file name")
		}
	}
	if p[0] == '/' {
		return bad("it is absolute")
	}
	for seg := range strings.SplitSeq(p, "/") {
		switch {
		case seg == "":
			return bad("it has an empty segment")
		case seg == "." || seg == "..":
			return bad("it leaves its directory")
		case strings.EqualFold(seg, ".git") || strings.EqualFold(seg, "git~1"):
			return bad("it names a .git directory, which git itself never checks out")
		case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
			return bad("a segment ends in a dot or a space")
		}
		base, _, _ := strings.Cut(strings.ToLower(seg), ".")
		if slices.Contains(windowsDevices, strings.TrimRight(base, " ")) {
			return bad("it names a Windows device")
		}
	}
	return nil
}

// Write writes the tree under dir, which must not exist yet: directories
// 0755, files 0644. Each path is checked again, and the joined path must
// stay under dir.
func (t *Tree) Write(dir string) error {
	if _, err := os.Lstat(dir); err == nil {
		return errors.New("fetch: " + filepath.Base(dir) + " already exists")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root := filepath.Clean(dir) + string(filepath.Separator)
	for _, f := range t.Files {
		if err := CheckPath(f.Path); err != nil {
			return err
		}
		full := filepath.Join(dir, filepath.FromSlash(f.Path))
		if !strings.HasPrefix(full, root) {
			return errorf(ReasonUnsafeArchive, "the archive path %q leaves its directory", f.Path)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, f.Data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
