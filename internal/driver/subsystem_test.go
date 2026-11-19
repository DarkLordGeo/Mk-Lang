// Why: pkg.Bin's subsystem decides whether a Windows executable opens a
// console, and manifests written before the field must still build.
// What: TestOmittedSubsystemAndWindowsLinksGUI and peSubsystem.
// How: a manifest without the field builds with it supplied empty; a
// "windows" bin links as a GUI program (PE subsystem 2), a console bin or
// one with no subsystem as a console program (3), read from each PE32+
// optional header.

package driver

import (
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
	"mk/std"
)

// copyStd copies the embedded std to a directory, through edit when set,
// with the extra files added, and returns the directory.
func copyStd(t *testing.T, extra map[string]string, edit func(p string, b []byte) []byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "std")
	write := func(p string, b []byte) error {
		out := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	}
	err := fs.WalkDir(std.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := std.FS.ReadFile(p)
		if err != nil {
			return err
		}
		if edit != nil {
			b = edit(p, b)
		}
		return write(p, b)
	})
	if err != nil {
		t.Fatal(err)
	}
	for p, text := range extra {
		if err := write(p, []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const t15Manifest = `const Package = pkg.Package(
    name: "app",
    bins: [pkg.Bin(name: "app", root: "src", link_script: "", cpu: "")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`

func TestOmittedSubsystemAndWindowsLinksGUI(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"pkg.mk":      t15Manifest,
		"src/main.mk": "export Main(sys: &System): WriteError!void {\n    try sys.Print(\"hi\\n\")\n}\n",
	})
	cache := t.TempDir()
	session := func() *Session {
		s, err := NewSession(Config{Root: dir, CacheDir: cache, Target: types.X86_64Windows})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	res, err := session().Fix(FixOptions{})
	if err != nil || res.Stopped != "clean" || len(res.Rounds) != 0 {
		t.Fatalf("mk fix: %v %q %+v", err, res.Stopped, res.Rounds)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pkg.mk"))
	if string(got) != t15Manifest {
		t.Fatalf("pkg.mk after mk fix:\n%s", got)
	}
	needTools(t)
	for _, c := range []struct {
		sub  string
		want uint16 // IMAGE_SUBSYSTEM_WINDOWS_GUI 2, _CUI 3
	}{{"", 3}, {"console", 3}, {"windows", 2}} {
		text := string(got)
		if c.sub != "" {
			text = strings.Replace(text, `cpu: "")`, `cpu: "", subsystem: "`+c.sub+`")`, 1)
		}
		if err := os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "app.exe")
		r, paths, err := session().Build(out)
		if err != nil || len(r.Report.Diags) != 0 || len(paths) != 1 {
			t.Fatalf("build %s: %v %v\n%s", c.sub, err, paths, diagLines(r))
		}
		if s := peSubsystem(t, paths[0]); s != c.want {
			t.Errorf("subsystem %q: PE subsystem %d, want %d", c.sub, s, c.want)
		}
	}
}

// peSubsystem reads the Subsystem field of a PE32+ optional header.
func peSubsystem(t *testing.T, path string) uint16 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 0x40 {
		t.Fatalf("read %s: %v", path, err)
	}
	pe := int(binary.LittleEndian.Uint32(b[0x3c:]))
	at := pe + 4 + 20 + 68 // signature, COFF header, Subsystem's offset
	if at+2 > len(b) || string(b[pe:pe+4]) != "PE\x00\x00" {
		t.Fatalf("%s: not a PE file", path)
	}
	return binary.LittleEndian.Uint16(b[at:])
}
