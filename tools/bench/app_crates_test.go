// Why: an app row's Rust crates come from the evidence directory by cargo's
// directory source replacement (tests/bench/README.md, "Crates pinned in
// Mk-evidence"), and a build must never read a registry or a changed tree.
// What: TestAppCratesField, TestAppCratesBuild, TestAppCratesStamp,
// TestAppCratesPinned and TestAppCratesTreesPinned.
// How: the field's refusals, the configuration file's check, the tree's pin
// by the archives beside it, the command line, and a real offline build.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// cratesConfigText is what `cargo vendor` prints for a tree in "vendor".
const cratesConfigText = "[source.crates-io]\nreplace-with = \"vendored-sources\"\n\n[source.vendored-sources]\ndirectory = \"vendor\"\n"

// appCrateSum is the cfg-if 1.0.0 checksum in appCrateLock.
const appCrateSum = "baf1de4339761588bc0619e3cbc0120ee582ebb74b53b4efbf79117bd2da40fd"

// tarEntry is one entry of a test's crate archive.
type tarEntry struct {
	name, body string
	flag       byte // tar.TypeReg when 0
}

// crateArchive is a .crate file (a gzipped tar) of entries, in order.
func crateArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: e.flag}
		switch e.flag {
		case 0:
			h.Typeflag = tar.TypeReg
		case tar.TypeSymlink:
			h.Linkname, h.Size = e.body, 0
		case tar.TypeDir:
			h.Size = 0
		case tar.TypeXGlobalHeader:
			h = &tar.Header{Typeflag: e.flag, PAXRecords: map[string]string{"comment": e.body}}
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
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pinCrate adds to files (an appRepo map) the crates directory crates/c1
// with cfg-if 1.0.0 vendored from pkg (its files, by path in the package)
// and pinned: the package's archive in
// archives/cfg-if-1.0.0.crate, the vendored copy with a
// .cargo-checksum.json, and the crate at tests/bench/app/z/rs with
// appCrateLock's checksum set to the archive's SHA-256.
func pinCrate(t *testing.T, files, pkg map[string]string) {
	t.Helper()
	const id = "cfg-if-1.0.0"
	var ents []tarEntry
	for _, rel := range slices.Sorted(maps.Keys(pkg)) {
		ents = append(ents, tarEntry{name: id + "/" + rel, body: pkg[rel]})
		if rel != ".gitignore" {
			files["evidence/crates/c1/vendor/"+id+"/"+rel] = pkg[rel]
		}
	}
	arc := crateArchive(t, ents)
	files["evidence/crates/c1/archives/"+id+".crate"] = string(arc)
	files["evidence/crates/c1/vendor/"+id+"/.cargo-checksum.json"] = `{"files":{},"package":"` + sha256Hex(arc) + `"}`
	files["evidence/crates/c1/.cargo/config.toml"] = cratesConfigText
	if !strings.Contains(appCrateLock, appCrateSum) {
		t.Fatal("appCrateLock lost its cfg-if checksum")
	}
	files["tests/bench/app/z/rs/Cargo.lock"] = strings.Replace(appCrateLock, appCrateSum, sha256Hex(arc), 1)
}

func TestAppCratesField(t *testing.T) {
	files := map[string]string{
		"tests/bench/app/z/README.md":      "# z\n",
		"tests/bench/app/z/rs/Cargo.toml":  appCrateToml,
		"tests/bench/app/z/rs/src/main.rs": appCrateMain,
		"tests/bench/app/n/n.go":           "// n\npackage main\n",
		"tests/bench/idiomatic/q/q.go":     "// q\npackage main\n",
	}
	pinCrate(t, files, map[string]string{"Cargo.toml": "\n"})
	root := appRepo(t, files)
	load := func(dir, json string) (*row, error) {
		p := filepath.Join(root, "tests", "bench", filepath.FromSlash(dir), "bench.json")
		writeFile(t, p, json)
		return loadRow(p, "")
	}
	const eco = `"kinds": {"rust": "eco"}, "libs": {"rust": [{"name": "cfg-if", "version": "1.0.0", "lang": "Rust", "inside": "none"}]}`
	r, err := load("app/z", `{"table": "app", "sources": {"rust": "rs"}, `+eco+`, "crates": "crates/c1"}`)
	if err != nil {
		t.Fatalf("a row with crates: %v", err)
	}
	for what, c := range map[string][2]string{
		"crates off the app table": {"idiomatic/q", `{"table": "idiomatic", "sources": {"go": "q.go"}, "crates": "crates/c1"}`},
		"crates with no crate":     {"app/n", `{"table": "app", "sources": {"go": "n.go"}, "kinds": {"go": "std"}, "crates": "crates/c1"}`},
		"crates with ..":           {"app/z", `{"table": "app", "sources": {"rust": "rs"}, ` + eco + `, "crates": "crates/../c1"}`},
		"crates absolute":          {"app/z", `{"table": "app", "sources": {"rust": "rs"}, ` + eco + `, "crates": "/crates/c1"}`},
		"crates with a backslash":  {"app/z", `{"table": "app", "sources": {"rust": "rs"}, ` + eco + `, "crates": "crates\\c1"}`},
	} {
		if _, err := load(c[0], c[1]); err == nil {
			t.Errorf("%s: loaded", what)
		}
	}
	// The command line names the checked file; a row with no crates
	// names none.
	work := t.TempDir()
	tc := &toolchain{Rustc: "rustc", Cargo: "cargo", Work: work}
	c, _ := columnByID("rust")
	cmd, err := tc.command(r, c, filepath.Join(work, "rust.exe"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "evidence", "crates", "c1", ".cargo", "config.toml")
	if i := slices.Index(cmd.Args, "--config"); i < 0 || !slices.Contains(cmd.Args, cfg) {
		t.Fatalf("no --config %s in %q", cfg, cmd.Args)
	}
	for _, need := range []string{"--offline", "--locked"} {
		if !slices.Contains(cmd.Args, need) {
			t.Errorf("no %s in %q", need, cmd.Args)
		}
	}
	plain := *r
	plain.Crates = ""
	if cmd, err := tc.command(&plain, c, filepath.Join(work, "rust.exe")); err != nil || slices.ContainsFunc(cmd.Args, func(a string) bool { return strings.HasSuffix(a, "config.toml") }) {
		t.Fatalf("a row with no crates: %v %q", err, cmd.Args)
	}
	// The configuration file may only replace crates-io with a
	// vendored directory under its own directory, which must exist.
	for what, text := range map[string]string{
		"another key":          cratesConfigText + "[build]\nrustflags = [\"-Copt-level=0\"]\n",
		"a profile":            cratesConfigText + "[profile.release]\nopt-level = 0\n",
		"another source":       strings.Replace(cratesConfigText, "vendored-sources\"", "elsewhere\"", 1),
		"a git source":         strings.Replace(cratesConfigText, "directory = \"vendor\"", "git = \"https://example.com/x\"", 1),
		"an absolute vendor":   strings.Replace(cratesConfigText, "\"vendor\"", "\"C:/vendor\"", 1),
		"a vendor above":       strings.Replace(cratesConfigText, "\"vendor\"", "\"../vendor\"", 1),
		"a missing vendor":     strings.Replace(cratesConfigText, "\"vendor\"", "\"gone\"", 1),
		"no file":              "",
		"a line split":         strings.Replace(cratesConfigText, "replace-with = ", "replace-with =\n", 1),
		"the sections swapped": "[source.vendored-sources]\ndirectory = \"vendor\"\n[source.crates-io]\nreplace-with = \"vendored-sources\"\n",
	} {
		if text == "" {
			os.Remove(cfg)
		} else {
			writeFile(t, cfg, text)
		}
		if _, err := tc.command(r, c, filepath.Join(work, "rust.exe")); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
	// CR LF line ends, as an editor on Windows writes them, are the same
	// file.
	writeFile(t, cfg, strings.ReplaceAll(cratesConfigText, "\n", "\r\n"))
	if _, err := cratesConfig(r); err != nil {
		t.Fatalf("CR LF: %v", err)
	}
}

// TestAppCratesBuild builds the zlib-c row's crate with its crates from a
// `cargo vendor` tree in a temporary evidence directory and an empty
// CARGO_HOME, so no registry can serve them, runs the program, and checks
// its output by a round trip. The tree is pinned by the registry cache's
// archives. Then it edits one vendored file with its .cargo-checksum.json
// entry, which tools/bench must refuse against the archive, and changes
// one without its entry, which cargo must refuse.
func TestAppCratesBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("vendors and builds a crate with cargo")
	}
	if runtime.GOOS != "windows" {
		t.Skip("the app table's cargo builds target x86_64-pc-windows-msvc")
	}
	cargo, err1 := exec.LookPath("cargo")
	rustc, err2 := exec.LookPath("rustc")
	if err1 != nil || err2 != nil {
		t.Skip("no cargo or rustc on PATH")
	}
	wd, _ := os.Getwd()
	repo, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	crate := filepath.Join(repo, "tests", "bench", "app", "zlib-c", "rs")
	files := map[string]string{"tests/bench/app/zlib-c/README.md": "# zlib-c\n"}
	for _, f := range []string{"Cargo.toml", "Cargo.lock", "src/main.rs"} {
		b, err := os.ReadFile(filepath.Join(crate, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		files["tests/bench/app/zlib-c/rs/"+f] = string(b)
	}
	root := appRepo(t, files)
	evid := filepath.Join(root, "evidence", "crates", "flate2")
	dir := filepath.Join(root, "tests", "bench", "app", "zlib-c")
	// The tree, made from the user's registry, offline.
	vend := exec.Command(cargo, "vendor", "--offline", "--locked", "--versioned-dirs", "--manifest-path", filepath.Join(dir, "rs", "Cargo.toml"), filepath.Join(evid, "vendor"))
	vend.Env = append(os.Environ(), "CARGO_TARGET_DIR="+t.TempDir())
	if out, err := vend.CombinedOutput(); err != nil {
		t.Skipf("cargo vendor --offline (the registry lacks the row's crates?): %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(evid, ".cargo", "config.toml"), cratesConfigText)
	// The archives that pin the tree, from the same registry.
	home := os.Getenv("CARGO_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		home = filepath.Join(h, ".cargo")
	}
	lockText, err := os.ReadFile(filepath.Join(dir, "rs", "Cargo.lock"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parseCargoLock(string(lockText)) {
		if p.Source == "" {
			continue
		}
		id := p.Name + "-" + p.Version
		found, _ := filepath.Glob(filepath.Join(home, "registry", "cache", "*", id+".crate"))
		if len(found) == 0 {
			t.Skipf("the registry cache has no %s.crate", id)
		}
		b, err := os.ReadFile(found[0])
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(evid, cratesArchives, id+".crate"), string(b))
	}
	libs := []lib{{Name: "flate2", Version: "1.1.1", Lang: "Rust", Inside: "none"}}
	r := &row{Name: "zlib-c", Table: "app", Dir: dir, Sources: map[string]string{"rust": "rs"},
		Kinds: map[string]string{"rust": "eco"}, Libs: map[string][]lib{"rust": libs}, Crates: "crates/flate2"}
	if err := checkCargoSource(r); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO_HOME", t.TempDir())
	work := t.TempDir()
	tc := &toolchain{Rustc: rustc, Cargo: cargo, Work: work}
	c, _ := columnByID("rust")
	b := &built{Row: r, Col: c, Exe: exeName(work, "verify", r, c)}
	if b.Cmd, err = tc.command(r, c, b.Exe); err != nil {
		t.Fatal(err)
	}
	if out, err := buildOne(b, func(s string) { t.Log(s) }); err != nil {
		t.Fatalf("cargo build from the vendored tree: %v\n%s", err, out)
	}
	in := filepath.Join(work, "in.txt")
	plain := bytes.Repeat([]byte("the zlib row's crate, vendored\n"), 5000)
	if err := os.WriteFile(in, plain, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(work, "out.zz")
	got, _, err := runOnce(b.Exe, []string{in, out})
	if err != nil || strings.TrimSpace(got) != "155000" {
		t.Fatalf("the program printed %q (%v)", got, err)
	}
	zb, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(zb))
	if err != nil {
		t.Fatal(err)
	}
	back, err := io.ReadAll(zr)
	if err != nil || !bytes.Equal(back, plain) {
		t.Fatalf("the round trip: %v, %d bytes back of %d", err, len(back), len(plain))
	}
	if len(zb) < 2 || zb[0] != 0x78 || zb[1] != 0x9c {
		t.Fatalf("the stream's header is % x, want 78 9c (level 6)", zb[:min(len(zb), 2)])
	}
	// A vendored file edited together with its .cargo-checksum.json entry
	// passes cargo's own check, so the build stamp must see it: with no
	// stamp and no target directory removed by hand, the next build
	// rebuilds from an empty CARGO_TARGET_DIR, and the program shows the
	// edit, level 1 (FLEVEL 0: header 78 01). A stamp that did not hash the
	// tree would let cargo reuse its old build.
	pkgs, _ := filepath.Glob(filepath.Join(evid, "vendor", "flate2*"))
	if len(pkgs) != 1 {
		t.Fatalf("flate2 in the tree: %q", pkgs)
	}
	const level6 = "inner: Deflate::make(level, zlib_header, ffi::MZ_DEFAULT_WINDOW_BITS as u8),"
	mem := filepath.Join(pkgs[0], "src", "mem.rs")
	msrc, err := os.ReadFile(mem)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(msrc), level6); n != 1 {
		t.Fatalf("flate2's src/mem.rs has the level line %d times, want 1", n)
	}
	writeFile(t, mem, strings.Replace(string(msrc), level6, "inner: Deflate::make(Compression::new(1), zlib_header, ffi::MZ_DEFAULT_WINDOW_BITS as u8),", 1))
	setVendoredSum(t, pkgs[0], "src/mem.rs")
	// tools/bench refuses that edit against flate2's archive before cargo
	// runs. The build below keeps the command made before the edit, so it
	// shows the stamp's half: what a tree changed after its check would
	// build.
	if _, err := tc.command(r, c, b.Exe); err == nil || !strings.Contains(err.Error(), "src/mem.rs differs") {
		t.Fatalf("a vendored file edited with its .cargo-checksum.json entry: the command was made (%v)", err)
	}
	var logs []string
	logf := func(s string) { logs = append(logs, s); t.Log(s) }
	if out, err := buildOne(b, logf); err != nil {
		t.Fatalf("cargo build of the edited tree: %v\n%s", err, out)
	}
	if !slices.ContainsFunc(logs, func(s string) bool { return strings.HasPrefix(s, "built ") }) {
		t.Fatalf("an edited crates tree was not rebuilt (log %q)", logs)
	}
	if got, _, err := runOnce(b.Exe, []string{in, out}); err != nil || strings.TrimSpace(got) != "155000" {
		t.Fatalf("the rebuilt program printed %q (%v)", got, err)
	}
	if zb, err = os.ReadFile(out); err != nil {
		t.Fatal(err)
	}
	if len(zb) < 2 || zb[0] != 0x78 || zb[1] != 0x01 {
		t.Fatalf("the rebuilt program's header is % x, want 78 01: the edit did not reach the build", zb[:min(len(zb), 2)])
	}
	// A vendored file changed without its entry is refused by cargo's
	// checksum check; again nothing is removed by hand.
	ms, _ := filepath.Glob(filepath.Join(evid, "vendor", "flate2*", "src", "lib.rs"))
	if len(ms) != 1 {
		t.Fatalf("flate2's lib.rs in the tree: %q", ms)
	}
	src, err := os.ReadFile(ms[0])
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, ms[0], string(src)+"\n// changed\n")
	if out, err := buildOne(b, func(s string) { t.Log(s) }); err == nil {
		t.Fatalf("a changed vendored file was built:\n%s", out)
	} else if !strings.Contains(out+err.Error(), "checksum") {
		t.Fatalf("refused, but not by the checksum check: %v\n%s", err, out)
	}
}

// setVendoredSum writes file's SHA-256 into its package's
// .cargo-checksum.json "files" map, leaving "package" as it is, as an edit
// made with its entry would.
func setVendoredSum(t *testing.T, pkgDir, file string) {
	t.Helper()
	p := filepath.Join(pkgDir, ".cargo-checksum.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var sums struct {
		Files   map[string]string `json:"files"`
		Package string            `json:"package"`
	}
	if err := json.Unmarshal(b, &sums); err != nil {
		t.Fatal(err)
	}
	if _, ok := sums.Files[file]; !ok {
		t.Fatalf("%s has no entry for %s", p, file)
	}
	fb, err := os.ReadFile(filepath.Join(pkgDir, filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	sums.Files[file] = sha256Hex(fb)
	if b, err = json.Marshal(sums); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, string(b))
}

// TestAppCratesStamp: a Rust column's build stamp covers every file of the
// row's crates tree, and a cargo build that runs (its stamp changed or
// missing) starts from an empty CARGO_TARGET_DIR, while an unchanged one is
// not rebuilt. No cargo runs: the test binary stands in for it (BENCH_HELPER)
// and the product is a file beside.
func TestAppCratesStamp(t *testing.T) {
	files := map[string]string{
		"tests/bench/app/z/README.md":      "# z\n",
		"tests/bench/app/z/rs/Cargo.toml":  appCrateToml,
		"tests/bench/app/z/rs/src/main.rs": appCrateMain,
	}
	// The tree is pinned, so the command is made; the edits below, which
	// a later command would refuse (TestAppCratesPinned), are made to the
	// stamp's input after it.
	pinCrate(t, files, map[string]string{"src/lib.rs": "// one\n"})
	root := appRepo(t, files)
	p := filepath.Join(root, "tests", "bench", "app", "z", "bench.json")
	writeFile(t, p, `{"table": "app", "sources": {"rust": "rs"}, "kinds": {"rust": "eco"}, "libs": {"rust": [{"name": "cfg-if", "version": "1.0.0", "lang": "Rust", "inside": "none"}]}, "crates": "crates/c1"}`)
	r, err := loadRow(p, "")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	tc := &toolchain{Rustc: "rustc", Cargo: "cargo", Work: work}
	c, _ := columnByID("rust")
	exe := exeName(work, "verify", r, c)
	cmd, err := tc.command(r, c, exe)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := envValue(cmd.Env, "CARGO_TARGET_DIR")
	if !ok || target == "" {
		t.Fatalf("no CARGO_TARGET_DIR in %q", cmd.Env)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Args = append([]string{self}, cmd.Args[1:]...)
	cmd.Env = append(cmd.Env, "BENCH_HELPER=1")
	cmd.Product = filepath.Join(t.TempDir(), "product.exe")
	writeFile(t, cmd.Product, "the product\n")
	b := &built{Row: r, Col: c, Exe: exe, Cmd: cmd}
	stale := filepath.Join(target, "stale")
	lib := filepath.Join(root, "evidence", "crates", "c1", "vendor", "cfg-if-1.0.0", "src", "lib.rs")
	build := func(what string, want bool) {
		t.Helper()
		writeFile(t, stale, "an old build\n")
		var built bool
		if out, err := buildOne(b, func(s string) { built = built || strings.HasPrefix(s, "built ") }); err != nil {
			t.Fatalf("%s: %v\n%s", what, err, out)
		}
		_, err := os.Stat(stale)
		switch {
		case built != want:
			t.Fatalf("%s: built %v, want %v", what, built, want)
		case want && err == nil:
			t.Fatalf("%s: the build kept its old CARGO_TARGET_DIR", what)
		case !want && err != nil:
			t.Fatalf("%s: an unchanged build cleared its CARGO_TARGET_DIR: %v", what, err)
		}
	}
	build("the first build", true)
	build("an unchanged build", false)
	s1, err := stamp(r, c, b.Cmd)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, lib, "// two\n")
	if s2, err := stamp(r, c, b.Cmd); err != nil || s2 == s1 {
		t.Fatalf("a changed file of the crates tree left the stamp unchanged (%v)", err)
	}
	build("a changed crates tree", true)
	os.Remove(exe + ".stamp")
	build("a missing stamp", true)
	// A row with no crates hashes no tree: the same edit changes nothing.
	plain := *r
	plain.Crates = ""
	pcmd := b.Cmd
	pcmd.Args = slices.DeleteFunc(slices.Clone(pcmd.Args), func(a string) bool { return strings.HasSuffix(a, "config.toml") })
	p1, err := stamp(&plain, c, pcmd)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, lib, "// three\n")
	if p2, err := stamp(&plain, c, pcmd); err != nil || p2 != p1 {
		t.Fatalf("a row with no crates: the stamp moved with the evidence tree (%v)", err)
	}
}

// TestAppCratesPinned: a crates tree builds only when every vendored file
// equals its package's crates.io archive kept in archives/, and each
// archive's SHA-256 is Cargo.lock's checksum. A tamper of a file
// edited together with its .cargo-checksum.json entry, which cargo itself
// builds, is refused here. No cargo runs.
func TestAppCratesPinned(t *testing.T) {
	const id = "cfg-if-1.0.0"
	pkg := map[string]string{
		".gitignore": "/target\n", // cargo vendor leaves it out, so pinCrate does
		"Cargo.toml": "[package]\nname = \"cfg-if\"\nversion = \"1.0.0\"\n",
		"src/lib.rs": "// cfg-if\n",
	}
	setup := func(t *testing.T) (*row, string) {
		t.Helper()
		files := map[string]string{
			"tests/bench/app/z/README.md":      "# z\n",
			"tests/bench/app/z/rs/Cargo.toml":  appCrateToml,
			"tests/bench/app/z/rs/src/main.rs": appCrateMain,
		}
		pinCrate(t, files, pkg)
		root := appRepo(t, files)
		p := filepath.Join(root, "tests", "bench", "app", "z", "bench.json")
		writeFile(t, p, `{"table": "app", "sources": {"rust": "rs"}, "kinds": {"rust": "eco"}, "libs": {"rust": [{"name": "cfg-if", "version": "1.0.0", "lang": "Rust", "inside": "none"}]}, "crates": "crates/c1"}`)
		r, err := loadRow(p, "")
		if err != nil {
			t.Fatal(err)
		}
		return r, root
	}
	at := func(root, p string) string {
		return filepath.Join(root, "evidence", "crates", "c1", filepath.FromSlash(p))
	}
	// rearchive replaces the archive with entries and the lockfile's
	// checksum with the new archive's, so only the entries can be wrong.
	rearchive := func(t *testing.T, root string, ents []tarEntry) {
		t.Helper()
		arc := crateArchive(t, ents)
		writeFile(t, at(root, "archives/"+id+".crate"), string(arc))
		writeFile(t, filepath.Join(root, "tests", "bench", "app", "z", "rs", "Cargo.lock"), strings.Replace(appCrateLock, appCrateSum, sha256Hex(arc), 1))
	}
	whole := []tarEntry{{name: id + "/.gitignore", body: pkg[".gitignore"]}, {name: id + "/Cargo.toml", body: pkg["Cargo.toml"]}, {name: id + "/src/lib.rs", body: pkg["src/lib.rs"]}}

	r, root := setup(t)
	if _, err := cratesConfig(r); err != nil {
		t.Fatalf("a pinned tree: %v", err)
	}
	// Directory entries and a pax global header, as a real archive may
	// hold, change nothing.
	rearchive(t, root, append([]tarEntry{{name: id + "/", flag: tar.TypeDir}, {body: "a global header", flag: tar.TypeXGlobalHeader}, {name: id + "/src/", flag: tar.TypeDir}}, whole...))
	if _, err := cratesConfig(r); err != nil {
		t.Fatalf("an archive with directory entries: %v", err)
	}
	// A .gitignore below the package's top, which cargo vendor drops too
	// (unicode-ident 1.0.27's tests/fst/.gitignore), may be missing.
	rearchive(t, root, append(slices.Clone(whole), tarEntry{name: id + "/tests/fst/.gitignore", body: "/prop_list.rs\n"}))
	if _, err := cratesConfig(r); err != nil {
		t.Fatalf("an archive with a nested .gitignore the tree lacks: %v", err)
	}
	for _, c := range []struct {
		what, want string
		edit       func(t *testing.T, root string)
	}{
		{"no archive", "archives", func(t *testing.T, root string) { os.Remove(at(root, "archives/"+id+".crate")) }},
		{"an archive whose SHA-256 is not the lockfile's", "Cargo.lock's checksum", func(t *testing.T, root string) {
			b, _ := os.ReadFile(at(root, "archives/"+id+".crate"))
			writeFile(t, at(root, "archives/"+id+".crate"), string(b)+"\x00")
		}},
		{"a vendored file edited with its .cargo-checksum.json entry (the review's tamper)", "src/lib.rs differs", func(t *testing.T, root string) {
			p := at(root, "vendor/"+id)
			writeFile(t, filepath.Join(p, "src", "lib.rs"), "// cfg-if, changed\n")
			b, _ := os.ReadFile(at(root, "archives/"+id+".crate"))
			writeFile(t, filepath.Join(p, ".cargo-checksum.json"), `{"files":{"src/lib.rs":"`+sha256Hex([]byte("// cfg-if, changed\n"))+`"},"package":"`+sha256Hex(b)+`"}`)
		}},
		{"a vendored file added", "src/extra.rs is not in", func(t *testing.T, root string) {
			writeFile(t, at(root, "vendor/"+id+"/src/extra.rs"), "// extra\n")
		}},
		{"a vendored build script added", "build.rs is not in", func(t *testing.T, root string) {
			writeFile(t, at(root, "vendor/"+id+"/build.rs"), "fn main() {}\n")
		}},
		{"a vendored file removed", "src/lib.rs is missing", func(t *testing.T, root string) {
			os.Remove(at(root, "vendor/"+id+"/src/lib.rs"))
		}},
		{"a package directory removed", "Cargo.toml is missing", func(t *testing.T, root string) {
			os.RemoveAll(at(root, "vendor/"+id))
		}},
		{"a package directory the lockfile lacks", "vendor/other-1.0.0 is not a package", func(t *testing.T, root string) {
			writeFile(t, at(root, "vendor/other-1.0.0/Cargo.toml"), "\n")
		}},
		{"a file beside the packages", "vendor/README is not a package", func(t *testing.T, root string) {
			writeFile(t, at(root, "vendor/README"), "\n")
		}},
		{"an archive file the tree lacks, other than cargo vendor's", "Cargo.toml.orig is missing", func(t *testing.T, root string) {
			rearchive(t, root, append(slices.Clone(whole), tarEntry{name: id + "/Cargo.toml.orig", body: "\n"}))
		}},
		{"a nested .gitattributes the tree lacks (only a top one may be left out)", "src/.gitattributes is missing", func(t *testing.T, root string) {
			rearchive(t, root, append(slices.Clone(whole), tarEntry{name: id + "/src/.gitattributes", body: "\n"}))
		}},
		{"an archive entry outside its package", "not a path inside", func(t *testing.T, root string) {
			rearchive(t, root, append(slices.Clone(whole), tarEntry{name: "other-1.0.0/src/lib.rs", body: "\n"}))
		}},
		{"an archive entry with ..", "not a path inside", func(t *testing.T, root string) {
			rearchive(t, root, append(slices.Clone(whole), tarEntry{name: id + "/../x.rs", body: "\n"}))
		}},
		{"an archive entry repeated", "repeated", func(t *testing.T, root string) {
			rearchive(t, root, append(slices.Clone(whole), tarEntry{name: id + "/src/lib.rs", body: "// again\n"}))
		}},
		{"an archive symlink", "not a regular file", func(t *testing.T, root string) {
			rearchive(t, root, append(slices.Clone(whole), tarEntry{name: id + "/src/link.rs", body: "lib.rs", flag: tar.TypeSymlink}))
		}},
	} {
		t.Run(c.what, func(t *testing.T) {
			r, root := setup(t)
			c.edit(t, root)
			_, err := cratesConfig(r)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error with %q", err, c.want)
			}
			// The command is refused with it, so no cargo build runs.
			tc := &toolchain{Rustc: "rustc", Cargo: "cargo", Work: t.TempDir()}
			col, _ := columnByID("rust")
			if _, err := tc.command(r, col, filepath.Join(t.TempDir(), "rust.exe")); err == nil {
				t.Fatal("the cargo command was made")
			}
		})
	}
}

// TestAppCratesTreesPinned checks each row's crates tree that the evidence
// directory holds (MK_EVIDENCE) against its archives and the row's
// Cargo.lock. A tree not yet placed is reported and not checked; with none
// placed the test skips, and is then not run.
func TestAppCratesTreesPinned(t *testing.T) {
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := discoverTree(t, root)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, r := range rows {
		if r.Crates == "" {
			continue
		}
		dir, err := cratesDir(r)
		if err != nil {
			t.Errorf("%s: %v", r.id(), err)
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			t.Logf("%s: crates %s is not in %s, not checked", r.id(), r.Crates, evidenceDir(root))
			continue
		}
		if _, err := cratesConfig(r); err != nil {
			t.Errorf("%s: %v", r.id(), err)
		}
		checked++
	}
	if checked == 0 {
		t.Skipf("no row's crates tree is in %s", evidenceDir(root))
	}
	t.Logf("%d rows' crates trees checked", checked)
}
