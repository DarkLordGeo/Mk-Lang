// Why: the app table's rules have holes that a plain row does not show: a
// kind that hides what a cell links, cargo keys that change a build, a data
// name that escapes its directory.
// What: one test per hole, from TestAppGenDataNames to
// TestRenderAppRowDigestNamesPkg.
// How: each test builds a row (and its Mk package or crate) in a temporary
// tree and asserts the refusal, the command or the rendered text it names.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestAppGenDataNames: a data file name of "." or "..", or one with a
// separator, is refused before anything is removed, and removeDataDir
// removes only a directory one element below the data directory. The
// data directory sits two levels inside t.TempDir(), with a canary above
// it, so a regression deletes only the test's own files.
func TestAppGenDataNames(t *testing.T) {
	for _, kind := range []string{"words", "tree"} {
		for _, name := range []string{"..", ".", "", "a/b", `a\b`, "../x", `..\x`} {
			base := t.TempDir()
			parent := filepath.Join(base, "bench")
			data := filepath.Join(parent, "data")
			writeFile(t, filepath.Join(parent, "canary.txt"), "above the data directory\n")
			writeFile(t, filepath.Join(data, "other.txt"), "another row's data\n")
			if _, err := genData(data, dataSpec{Kind: kind, File: name, Bytes: 64, Seed: 1}); err == nil {
				t.Errorf("kind %s, file %q: accepted", kind, name)
			}
			for _, f := range []string{filepath.Join(parent, "canary.txt"), filepath.Join(data, "other.txt")} {
				if _, err := os.Stat(f); err != nil {
					t.Fatalf("kind %s, file %q: %s is gone", kind, name, f)
				}
			}
		}
	}
	base := t.TempDir()
	data := filepath.Join(base, "bench", "data")
	writeFile(t, filepath.Join(data, "t", "f.txt"), "x\n")
	for _, p := range []string{data, filepath.Join(data, ".."), filepath.Join(data, "t", "f.txt"), base} {
		if err := removeDataDir(data, p); err == nil {
			t.Errorf("removeDataDir(%s) accepted", p)
		}
	}
	if err := removeDataDir(data, filepath.Join(data, "t")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "t")); err == nil {
		t.Fatal("the tree was not removed")
	}
	// A plain name still works for both kinds.
	if _, err := genData(data, dataSpec{Kind: "tree", File: "tr", Bytes: 1 << 16, Seed: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := genData(data, dataSpec{Kind: "words", File: "w.txt", Bytes: 64, Seed: 1}); err != nil {
		t.Fatal(err)
	}
}

// TestAppKindAgainstLinks: a cell's kind is checked against what it
// links. A crate whose lockfile holds a registry package is eco, with libs
// naming its direct dependencies; a vendored library is C's only, with C
// eco; and the Mk package, always std, links no C, assembly or library.
func TestAppKindAgainstLinks(t *testing.T) {
	root := appRepo(t, map[string]string{
		"evidence/vendor/z/z.c":            "int z;\n",
		"tests/bench/app/r/r.c":            "// r\n",
		"tests/bench/app/r/rs/Cargo.toml":  appCrateToml,
		"tests/bench/app/r/rs/Cargo.lock":  appCrateLock,
		"tests/bench/app/r/rs/src/main.rs": appCrateMain,
		"tests/bench/app/r/mk/pkg.mk":      appPkg,
		"tests/bench/app/r/mk/main.mk":     "Main() {}\n",
		// A lockfile with a second registry package, not a direct
		// dependency: libs naming only it misses cfg-if.
		"tests/bench/app/r/rs2/Cargo.toml":  appCrateToml,
		"tests/bench/app/r/rs2/Cargo.lock":  appCrateLock + "\n[[package]]\nname = \"other\"\nversion = \"1.0.0\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"00\"\n",
		"tests/bench/app/r/rs2/src/main.rs": appCrateMain,
	})
	dir := filepath.Join(root, "tests", "bench", "app", "r")
	load := func(body string) error {
		p := filepath.Join(dir, "bench.json")
		writeFile(t, p, body)
		_, err := loadRow(p, "")
		return err
	}
	const cfgIf = `[{"name": "cfg-if", "version": "1.0.0", "lang": "Rust", "inside": "none"}]`
	const other = `[{"name": "other", "version": "1.0.0", "lang": "Rust", "inside": "none"}]`
	const zlib = `{"c": [{"name": "z", "version": "1", "lang": "C", "inside": "C"}]}`
	const vendor = `"vendor": {"dir": "vendor/z", "files": ["z.c"], "c_flags": [], "for": ["c"]}`
	for _, c := range []struct {
		what, body, want string
	}{
		{"a crate linking cfg-if as std", `{"table": "app", "sources": {"rust": "rs"}, "kinds": {"rust": "std"}}`, "its cell is eco"},
		{"libs naming a library the lock lacks", `{"table": "app", "sources": {"rust": "rs"}, "kinds": {"rust": "eco"}, "libs": {"rust": ` + cfgIf[:len(cfgIf)-1] + `, ` + other[1:] + `}}`, "not in the crate's Cargo.lock"},
		{"libs not naming the direct dependency", `{"table": "app", "sources": {"rust": "rs2"}, "kinds": {"rust": "eco"}, "libs": {"rust": ` + other + `}}`, "depends on cfg-if"},
		{"a C cell given a vendored library as std", `{"table": "app", "sources": {"c": "r.c"}, "kinds": {"c": "std"}, ` + vendor + `}`, "C's cell is then eco"},
		{"a vendor with no for", `{"table": "app", "sources": {"mk": "mk", "c": "r.c"}, "kinds": {"mk": "std", "c": "eco"}, "libs": ` + zlib + `, "vendor": {"dir": "vendor/z", "files": ["z.c"], "c_flags": []}}`, `"for": ["c"]`},
		{"a vendor for mk and c", `{"table": "app", "sources": {"mk": "mk", "c": "r.c"}, "kinds": {"mk": "std", "c": "eco"}, "libs": ` + zlib + `, "vendor": {"dir": "vendor/z", "files": ["z.c"], "c_flags": [], "for": ["mk", "c"]}}`, `"for": ["c"]`},
	} {
		err := load(c.body)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.what, err, c.want)
		}
	}
	if err := load(`{"table": "app", "sources": {"rust": "rs"}, "kinds": {"rust": "eco"}, "libs": {"rust": ` + cfgIf + `}}`); err != nil {
		t.Fatalf("a well-labelled crate: %v", err)
	}
	if err := load(`{"table": "app", "sources": {"c": "r.c"}, "kinds": {"c": "eco"}, "libs": ` + zlib + `, ` + vendor + `}`); err != nil {
		t.Fatalf("a well-labelled vendored C cell: %v", err)
	}
	pkg := filepath.Join(dir, "mk", "pkg.mk")
	for _, field := range []string{`c_sources: ["fast.c"]`, `asm_sources: ["memchr.s"]`, `c_imports: ["zlib.h"]`} {
		writeFile(t, pkg, strings.Replace(appPkg, "unsafe_modules: []", field+", unsafe_modules: []", 1))
		if err := load(`{"table": "app", "sources": {"mk": "mk"}, "kinds": {"mk": "std"}}`); err == nil || !strings.Contains(err.Error(), "links no code but Mk's own") {
			t.Errorf("%s in the Mk package: %v", field, err)
		}
	}
	writeFile(t, pkg, strings.Replace(appPkg, "links: []", `links: [pkg.Link(name: "version.dll", kind: "dynamic", path: "", target: "x86_64-windows")]`, 1))
	if err := load(`{"table": "app", "sources": {"mk": "mk"}, "kinds": {"mk": "std"}}`); err == nil || !strings.Contains(err.Error(), "sets links") {
		t.Errorf("links in the Mk package: %v", err)
	}
	writeFile(t, pkg, strings.Replace(appPkg, "unsafe_modules: []", "c_sources: [], asm_sources: [], c_imports: [], deps: [], unsafe_modules: []", 1))
	if err := load(`{"table": "app", "sources": {"mk": "mk"}, "kinds": {"mk": "std"}}`); err != nil {
		t.Fatalf("empty fields refused: %v", err)
	}
	// A path dependency's own c_sources and unsafe
	// modules link into the program, so the Mk package has no deps.
	writeFile(t, pkg, strings.Replace(appPkg, "unsafe_modules: []", `deps: [pkg.Dep(name: "fastc", path: "../fastc", url: "", hash: "")], unsafe_modules: []`, 1))
	if err := load(`{"table": "app", "sources": {"mk": "mk"}, "kinds": {"mk": "std"}}`); err == nil || !strings.Contains(err.Error(), "the Mk package sets deps") {
		t.Errorf("deps in the Mk package: %v", err)
	}
	writeFile(t, pkg, appPkg)
	// A variant's Mk package is checked as the row's is.
	writeFile(t, filepath.Join(dir, "mk2", "pkg.mk"), strings.Replace(appPkg, "unsafe_modules: []", `c_sources: ["fast.c"], unsafe_modules: []`, 1))
	writeFile(t, filepath.Join(dir, "mk2", "main.mk"), "Main() {}\n")
	variant := `{"table": "app", "args": ["x"], "sources": {"mk": "mk"}, "kinds": {"mk": "std"}, "variants": [{"name": "v", "args": ["y"], "sources": {"mk": "mk2"}}]}`
	if err := load(variant); err == nil || !strings.Contains(err.Error(), "variant v: the Mk package sets c_sources") {
		t.Errorf("c_sources in a variant's Mk package: %v", err)
	}
	writeFile(t, filepath.Join(dir, "mk2", "pkg.mk"), appPkg)
	if err := load(variant); err != nil {
		t.Errorf("a clean variant Mk package: %v", err)
	}
}

// TestAppLockfileSources: every package of the lockfile but the root
// comes from the registry with a checksum.
func TestAppLockfileSources(t *testing.T) {
	dir := t.TempDir()
	cfgIf := []lib{{Name: "cfg-if", Version: "1.0.0", Lang: "Rust", Inside: "none"}}
	for i, lock := range []string{
		strings.Replace(appCrateLock, "source = \"registry+https://github.com/rust-lang/crates.io-index\"\n", "", 1),
		strings.Replace(appCrateLock, "checksum = \"baf1de4339761588bc0619e3cbc0120ee582ebb74b53b4efbf79117bd2da40fd\"\n", "", 1),
		strings.Replace(appCrateLock, "registry+https://github.com/rust-lang/crates.io-index", "git+https://example.invalid/cfg-if#abc", 1),
	} {
		d := filepath.Join(dir, string(rune('a'+i)))
		appCrate(t, filepath.Join(d, "rs"), map[string]string{"Cargo.toml": appCrateToml, "Cargo.lock": lock, "src/main.rs": appCrateMain})
		r := &row{Name: "r", Table: "app", Dir: d, Sources: map[string]string{"rust": "rs"}, Kinds: map[string]string{"rust": "eco"}, Libs: map[string][]lib{"rust": cfgIf}}
		if err := checkCargoSource(r); err == nil || !strings.Contains(err.Error(), "every library comes from the registry") {
			t.Errorf("lock %d: %v", i, err)
		}
	}
	ps := parseCargoLock(strings.ReplaceAll(appCrateLock, "\n", "\r\n"))
	if len(ps) != 2 || ps[0].Name != "cfg-if" || ps[0].Checksum == "" || ps[1].Name != "probe" || !slices.Equal(ps[1].Deps, []string{"cfg-if"}) {
		t.Fatalf("parseCargoLock: %+v", ps)
	}
}

// TestAppCargoManifestKeys: the manifest keys that change the build or
// bring in code outside libs are refused in every spelling.
func TestAppCargoManifestKeys(t *testing.T) {
	dir := t.TempDir()
	cfgIf := []lib{{Name: "cfg-if", Version: "1.0.0", Lang: "Rust", Inside: "none"}}
	n := 0
	for _, c := range []struct{ what, toml string }{
		{"a build script under another name", strings.Replace(appCrateToml, "edition = \"2024\"\n", "edition = \"2024\"\nbuild = \"b.rs\"\n", 1)},
		{"a top-level dotted profile key", "profile.release.package.\"*\".opt-level = 0\n" + appCrateToml},
		{"a profile table", appCrateToml + "\n[profile.release]\nopt-level = 0\n"},
		{"a workspace", appCrateToml + "\n[workspace]\nmembers = [\"helper\"]\n"},
		{"a path dependency", appCrateToml + "helper = { path = \"helper\" }\n"},
		{"a git dependency", appCrateToml + "helper = { git = \"https://example.invalid/h\" }\n"},
		{"a [lib] target", appCrateToml + "\n[lib]\npath = \"src/l.rs\"\n"},
		{"a [patch]", appCrateToml + "\n[patch.crates-io]\ncfg-if = { version = \"1\" }\n"},
		{"a [replace]", appCrateToml + "\n[replace]\n\"cfg-if:1.0.0\" = { version = \"1\" }\n"},
		{"a [[bin]]", appCrateToml + "\n[[bin]]\nname = \"x\"\n"},
		// TOML's other spellings of the same keys, each of which cargo
		// honours.
		{"an inline-table profile", "profile = { release = { package = { \"*\" = { opt-level = 0 } } } }\n" + appCrateToml},
		{"an inline-table profile after the package", strings.Replace(appCrateToml, "[dependencies]\n", "profile = { release = { opt-level = 0 } }\n\n[dependencies]\n", 1)},
		{"a quoted profile header", appCrateToml + "\n[\"profile\".release.package.\"*\"]\nopt-level = 0\n"},
		{"a literal-quoted dotted profile key", "'profile'.release.package.\"*\".opt-level = 0\n" + appCrateToml},
		{"a quoted build key", strings.Replace(appCrateToml, "edition = \"2024\"\n", "edition = \"2024\"\n\"build\" = \"b.rs\"\n", 1)},
		{"a quoted workspace header", appCrateToml + "\n[\"workspace\"]\n"},
		{"a links key", strings.Replace(appCrateToml, "edition = \"2024\"\n", "edition = \"2024\"\nlinks = \"z\"\n", 1)},
		{"a dotted package key", strings.Replace(appCrateToml, "edition = \"2024\"\n", "edition = \"2024\"\npackage.build = \"b.rs\"\n", 1)},
		{"a dependency table header", appCrateToml + "\n[dependencies.helper]\npath = \"helper\"\n"},
		{"a target dependency", appCrateToml + "\n[target.'cfg(windows)'.dependencies]\nhelper = \"1\"\n"},
		{"a dev-dependency", appCrateToml + "\n[dev-dependencies]\nhelper = \"1\"\n"},
		{"a features table", appCrateToml + "\n[features]\ndefault = []\n"},
		{"a workspace dependency", appCrateToml + "helper = { workspace = true }\n"},
		{"a renamed package", appCrateToml + "helper = { version = \"1\", package = \"other\" }\n"},
		{"a dotted dependency key", appCrateToml + "helper.path = \"helper\"\n"},
		{"a multi-line string", strings.Replace(appCrateToml, "edition = \"2024\"\n", "description = \"\"\"\nx\"\"\"\n", 1)},
		{"an escaped string", strings.Replace(appCrateToml, "edition = \"2024\"\n", "edition = \"20\\u0032\"\n", 1)},
		{"an inline table over two lines", appCrateToml + "helper = { version = \"1\",\n  path = \"h\" }\n"},
	} {
		n++
		d := filepath.Join(dir, fmt.Sprint(n))
		appCrate(t, filepath.Join(d, "rs"), map[string]string{"Cargo.toml": c.toml, "Cargo.lock": appCrateLock, "src/main.rs": appCrateMain})
		r := &row{Name: "r", Table: "app", Dir: d, Sources: map[string]string{"rust": "rs"}, Kinds: map[string]string{"rust": "eco"}, Libs: map[string][]lib{"rust": cfgIf}}
		if err := checkCargoSource(r); err == nil {
			t.Errorf("%s: accepted", c.what)
		}
	}
	// The form's own spellings are accepted: comments, blank lines, the
	// allowed [package] keys, an inline dependency table of version,
	// features and default-features, and a registry name that contains
	// "path" (which a word blacklist would refuse).
	for _, toml := range []string{
		appCrateToml,
		"# a comment, the crate's own\n[package] # the package\nname = \"probe\" # its name\nversion = \"0.1.0\"\nedition = \"2024\"\npublish = false\ndescription = \"a # in a string\"\nlicense = \"MIT\"\n\n[dependencies]\ncfg-if = { version = \"=1.0.0\", default-features = false, features = [\"a\", \"b\"] }\n",
		"[package]\r\nname = \"probe\"\r\nversion = \"0.1.0\"\r\n\r\n[dependencies]\r\ncfg-if = { version = \"=1.0.0\", features = [] }\r\nrelative-path = \"=1.9.3\"\r\n",
	} {
		if name, err := cargoManifest(toml); err != nil || name != "probe" {
			t.Errorf("a well-formed manifest: %q %v\n%s", name, err, toml)
		}
	}
	for _, toml := range []string{"", "[dependencies]\ncfg-if = \"1\"\n[package]\nname = \"probe\"\n", "[package]\nversion = \"0.1.0\"\n"} {
		if _, err := cargoManifest(toml); err == nil {
			t.Errorf("accepted:\n%s", toml)
		}
	}
}

// TestAppCargoConfigAtBuild: a cargo configuration
// in CARGO_HOME, which is the machine's and not a row's, fails the crate's
// build only: every row still loads, and cargoCommand refuses that crate.
func TestAppCargoConfigAtBuild(t *testing.T) {
	root := appRepo(t, map[string]string{
		"tests/bench/app/a/rs/Cargo.toml":  appCrateToml,
		"tests/bench/app/a/rs/Cargo.lock":  appCrateLock,
		"tests/bench/app/a/rs/src/main.rs": appCrateMain,
		"tests/bench/app/a/bench.json":     `{"table": "app", "args": ["x"], "sources": {"rust": "rs"}, "kinds": {"rust": "eco"}, "libs": {"rust": [{"name": "cfg-if", "version": "1.0.0", "lang": "Rust", "inside": "none"}]}}`,
		"tests/bench/same/s/s.c":           "// s\nint main(void) { return 0; }\n",
		"tests/bench/same/s/bench.json":    `{"table": "same", "args": ["x"], "sources": {"c": "s.c"}}`,
	})
	home := filepath.Join(t.TempDir(), "cargo-home")
	t.Setenv("CARGO_HOME", home)
	writeFile(t, filepath.Join(home, "config.toml"), "[net]\noffline = true\n")
	rows, err := discoverTree(t, root)
	if err != nil || len(rows) != 2 {
		t.Fatalf("a config.toml in CARGO_HOME: discover gave %d rows, %v", len(rows), err)
	}
	var app *row
	for _, r := range rows {
		if r.Table == "app" {
			app = r
		}
	}
	tc := &toolchain{Rustc: "rustc", Cargo: "cargo", Work: t.TempDir()}
	c, _ := columnByID("rust")
	if _, err := tc.command(app, c, filepath.Join(tc.Work, "a.exe")); err == nil || !strings.Contains(err.Error(), "cargo would read "+filepath.Join(home, "config.toml")) {
		t.Errorf("the crate's build with a cargo configuration in CARGO_HOME: %v", err)
	}
	os.Remove(filepath.Join(home, "config.toml"))
	if _, err := tc.command(app, c, filepath.Join(tc.Work, "a.exe")); err != nil {
		t.Errorf("the crate's build with a clean CARGO_HOME: %v", err)
	}
}

// TestAppCargoEnvironment: buildEnv clears cargo's variables by
// prefix and keeps CARGO_HOME; a cargo configuration or toolchain file in
// a parent directory, or in CARGO_HOME, is found.
func TestAppCargoEnvironment(t *testing.T) {
	base := []string{"CARGO_HOME=h", "CARGO_PROFILE_RELEASE_STRIP=x", "CARGO_BUILD_RUSTC=r", "CARGO_TARGET_X86_64_PC_WINDOWS_MSVC_LINKER=l",
		"CARGO_TARGET_DIR=d", "RUSTUP_TOOLCHAIN=nightly", "RUSTC_WRAPPER=w", "CARGO_ENCODED_RUSTFLAGS=f", "PATH=p"}
	if got := buildEnv(base, cargoEnvCleared); !slices.Equal(got, []string{"CARGO_HOME=h", "PATH=p"}) {
		t.Fatalf("buildEnv kept %q", got)
	}
	root := t.TempDir()
	crate := filepath.Join(root, "a", "b", "rs")
	writeFile(t, filepath.Join(crate, "Cargo.toml"), appCrateToml)
	t.Setenv("CARGO_HOME", filepath.Join(root, "home"))
	if p := cargoConfigAbove(crate); p != "" {
		t.Fatalf("a clean tree: %s", p)
	}
	for _, f := range []string{filepath.Join(root, "a", ".cargo", "config.toml"), filepath.Join(root, "rust-toolchain.toml"), filepath.Join(root, "home", "config.toml")} {
		writeFile(t, f, "\n")
		if p := cargoConfigAbove(crate); p != f {
			t.Errorf("%s: found %q", f, p)
		}
		os.Remove(f)
	}
	tc := &toolchain{Rustc: `C:\r\rustc.exe`, Cargo: "cargo", Work: t.TempDir()}
	writeFile(t, filepath.Join(crate, "Cargo.lock"), appCrateLock)
	c, _ := columnByID("rust")
	cmd, err := tc.cargoCommand(&row{Name: "r", Table: "app", Dir: filepath.Dir(crate)}, c, crate)
	if err != nil || !slices.Contains(cmd.Env, `RUSTC=C:\r\rustc.exe`) {
		t.Fatalf("cargo runs another rustc than Deps records: %v %q", err, cmd.Env)
	}
}

// TestAppVariantCrate: a variant's crate is refused off the app table
// and checked on it.
func TestAppVariantCrate(t *testing.T) {
	root := appRepo(t, map[string]string{
		"tests/bench/idiomatic/q/q.rs":           "// q\nfn main() {}\n",
		"tests/bench/idiomatic/q/rs/Cargo.toml":  appCrateToml,
		"tests/bench/idiomatic/q/rs/Cargo.lock":  appCrateLock,
		"tests/bench/idiomatic/q/rs/src/main.rs": appCrateMain,
		"tests/bench/idiomatic/q/bench.json":     `{"table": "idiomatic", "args": ["x"], "sources": {"rust": "q.rs"}, "variants": [{"name": "v", "args": ["x"], "sources": {"rust": "rs"}}]}`,
		"tests/bench/app/a/a.rs":                 "// a\nfn main() {}\n",
		"tests/bench/app/a/rs/Cargo.toml":        appCrateToml,
		"tests/bench/app/a/rs/src/main.rs":       appCrateMain,
		"tests/bench/app/a/bench.json":           `{"table": "app", "args": ["x"], "sources": {"rust": "a.rs"}, "kinds": {"rust": "eco"}, "libs": {"rust": [{"name": "cfg-if", "version": "1.0.0", "lang": "Rust", "inside": "none"}]}, "variants": [{"name": "v", "args": ["x"], "sources": {"rust": "rs"}}]}`,
	})
	if _, err := loadRow(filepath.Join(root, "tests", "bench", "idiomatic", "q", "bench.json"), ""); err == nil || !strings.Contains(err.Error(), "only the app table builds") {
		t.Errorf("a variant crate on idiomatic: %v", err)
	}
	if _, err := loadRow(filepath.Join(root, "tests", "bench", "app", "a", "bench.json"), ""); err == nil || !strings.Contains(err.Error(), "variant v: the Rust crate has no lockfile") {
		t.Errorf("an app variant crate with no lockfile: %v", err)
	}
	writeFile(t, filepath.Join(root, "tests", "bench", "app", "a", "rs", "Cargo.lock"), appCrateLock)
	if _, err := loadRow(filepath.Join(root, "tests", "bench", "app", "a", "bench.json"), ""); err != nil {
		t.Errorf("an app variant crate with its lockfile: %v", err)
	}
}

// TestAppVectorBanRenamed: the vector-code ban follows an import that
// renames std.simd.
func TestAppVectorBanRenamed(t *testing.T) {
	for _, c := range []struct {
		code string
		bad  bool
	}{
		{"import std.simd\nF() { var v: simd.Vec[u8, 16] }\n", true},
		{"import v = std.simd\nF() { var x: v.Vec[u8, 16] = v.Vec[u8, 16].Zero() }\n", true},
		{"import v = std.simd\nF() { v.Dispatch(t) }\n", true},
		{"import v = std.simd\nF() { v.IndexByte(b, 10) }\n", false},
		{"import std.simd\nF() { simd.Count(b, 10) }\n", false},
	} {
		if got := appMkVectorCode(c.code); (len(got) > 0) != c.bad {
			t.Errorf("%q: %q", c.code, got)
		}
	}
}

// TestAppGridKinds: the app table's summary grid prints each cell's
// kind and no overflow class; the other tables' grid is unchanged.
func TestAppGridKinds(t *testing.T) {
	r := &row{Name: "z", Table: "app", Kinds: map[string]string{"mk": "std", "c": "eco", "go": "std"}}
	var cs []cell
	for col, med := range map[string]float64{"mk": 3.3, "c": 1.0, "go": 1.5} {
		c, _ := columnByID(col)
		ts := []float64{med * 0.99, med, med * 1.01}
		cs = append(cs, cell{Row: "z", Table: "app", Col: col, Class: appClass(r, c), Times: ts, Summary: summarize(ts)})
	}
	res := &results{Runs: []facts{{Commit: "abc", Versions: map[string]string{}}}, Columns: columns, Sweeps: 3, Options: hfOptions{Warmup: 3, Runs: 10},
		Rows:  []rowInfo{{ID: "z", Table: "app", Algorithm: "A task.", Args: []string{"c"}, Checksum: "x", Kinds: r.Kinds}},
		Cells: cs}
	md := renderResults(res, "2026-11-15")
	_, grid, _ := strings.Cut(md, `## Table "app"`)
	grid, _, _ = strings.Cut(grid, "### ")
	if !strings.Contains(grid, "| Row | mk | c | go |") || strings.Contains(grid, "Overflow class") {
		t.Errorf("app grid header:\n%s", grid)
	}
	if !strings.Contains(grid, "| z | 3.300 s, x3.30, std | 1.000 s, x1.00, eco | 1.500 s, x1.50, std |") {
		t.Errorf("app grid row:\n%s", grid)
	}
	if gridHead("same") != "| Row | Overflow class |" || gridLead("same", rowInfo{ID: "s"}) != "| s | - |" || gridKind("same", rowInfo{}, "c") != "" {
		t.Error("the other tables' grid changed")
	}
}

// TestAppTimedRun: the cell run publish's sweep and rerun and
// compare's pairs share: {out} replaced by the column's path, that file
// deleted, the prepare that deletes it, and the row's environment (runEnv,
// the row's env, a concurrency row's threads); none for a row with no
// output. And publish.go and compare.go build a timed run no other way.
func TestAppTimedRun(t *testing.T) {
	work := t.TempDir()
	r := &row{Name: "z", Table: "app", Env: map[string]string{"MK_CARRIERS": "1"}, Output: &outputSpec{File: "o.z", Check: "zlib"}}
	b := &built{Row: r, Col: columns[0], Exe: filepath.Join(work, "bin", "mk.exe")}
	out := outPath(b)
	writeFile(t, out, "stale\n")
	run, err := timedRun(b, []string{"c", "in", outArg})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(run.Args, []string{"c", "in", out}) {
		t.Errorf("args %q", run.Args)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("the stale output survived")
	}
	if want, _ := prepareCmd(out, runtime.GOOS); run.Prepare != want || want == "" {
		t.Errorf("prepare %q, want %q", run.Prepare, want)
	}
	if v, ok := envValue(run.Env, "MK_CARRIERS"); !ok || v != "1" {
		t.Errorf("row env lost: %q", run.Env)
	}
	if v, _ := envValue(run.Env, "GOMAXPROCS"); v != "1" {
		t.Errorf("runEnv's pins lost: %q", run.Env)
	}
	cr := &row{Name: "k", Table: "idiomatic", Concurrency: &concSpec{Mask: "0xff", Threads: 4}}
	run, err = timedRun(&built{Row: cr, Col: columns[0], Exe: b.Exe}, []string{"a"})
	if err != nil || run.Prepare != "" || !slices.Equal(run.Args, []string{"a"}) {
		t.Fatalf("a row with no output: %+v %v", run, err)
	}
	if v, _ := envValue(run.Env, "GOMAXPROCS"); v != "4" {
		t.Errorf("a concurrency row's threads: %q", run.Env)
	}
	for _, f := range []string{"publish.go", "compare.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		if strings.Contains(s, "cellArgs(") || strings.Contains(s, "timedPrepare(") || strings.Contains(s, "freshOutput(") || strings.Count(s, "timedRun(") < 1 {
			t.Errorf("%s builds a timed run outside timedRun", f)
		}
	}
	// appCellFacts: class by kind, peak only on success, output bytes.
	ar := &row{Name: "z", Table: "app", Kinds: map[string]string{"c": "eco"}}
	ccol, _ := columnByID("c")
	c := &cell{}
	appCellFacts(c, ar, checkResult{B: &built{Row: ar, Col: ccol}, Peak: memPeak{RSS: 5}, OutBytes: 7})
	if c.Class != "eco (not classed)" || c.Peak == nil || c.Peak.RSS != 5 || c.OutBytes != 7 {
		t.Errorf("appCellFacts: %+v", c)
	}
	c = &cell{Class: "checked"}
	appCellFacts(c, &row{Table: "same"}, checkResult{B: &built{Col: ccol}, Peak: memPeak{RSS: 5}})
	if c.Class != "checked" || c.Peak != nil {
		t.Errorf("appCellFacts off the app table: %+v", c)
	}
}

// TestAppRowTextAgainstCode: env may not set MK_IO, which runEnv
// clears, and an app row has no mk-wrap, mk-keyed or mk-ctx column.
func TestAppRowTextAgainstCode(t *testing.T) {
	root := appRepo(t, map[string]string{
		"tests/bench/app/r/r.c":         "// r\n",
		"tests/bench/app/r/mk/pkg.mk":   appPkg,
		"tests/bench/app/r/wrap/pkg.mk": appPkg,
	})
	p := filepath.Join(root, "tests", "bench", "app", "r", "bench.json")
	for _, c := range []struct{ body, want string }{
		{`{"table": "app", "sources": {"c": "r.c"}, "kinds": {"c": "std"}, "env": {"MK_IO": "poll"}}`, "MK_IO"},
		{`{"table": "app", "sources": {"mk": "mk", "mk-wrap": "wrap"}, "kinds": {"mk": "std", "mk-wrap": "std"}}`, "no mk-wrap column"},
		{`{"table": "app", "sources": {"mk": "mk", "mk-keyed": "wrap"}, "kinds": {"mk": "std", "mk-keyed": "std"}}`, "no mk-keyed column"},
	} {
		writeFile(t, p, c.body)
		if _, err := loadRow(p, ""); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.body, err)
		}
	}
}

// TestAppPackageFactUnknown: when git fails, the package's commit
// and state are recorded as unknown and printed so, not as clean.
func TestAppPackageFactUnknown(t *testing.T) {
	f := packageFactWith(t.TempDir(), &pkgSpec{Path: "Dogfoods/x"}, filepath.Join(t.TempDir(), "no-such-git.exe"))
	if f.Unknown == "" || f.Commit != "" || f.Dirty {
		t.Fatalf("git failed: %+v", f)
	}
	var w strings.Builder
	res := &results{Rows: []rowInfo{{ID: "z", Table: "app", Package: f}}}
	renderAppRow(&w, res, res.Rows[0])
	if !strings.Contains(w.String(), "its commit and state are unknown (git failed") || strings.Contains(w.String(), "used unchanged") {
		t.Errorf("render: %s", w.String())
	}
}

// TestRenderAppRowDigestNamesPkg: a digest row with a package says the
// package directory is written as $PKG, as normalise writes it; a digest
// row without one does not.
func TestRenderAppRowDigestNamesPkg(t *testing.T) {
	for _, c := range []struct {
		pkg  *pkgFact
		want bool
	}{{&pkgFact{Path: "Dogfoods/x", Commit: "0123456789ab"}, true}, {nil, false}} {
		var w strings.Builder
		res := &results{Rows: []rowInfo{{ID: "z", Table: "app", Digest: true, Package: c.pkg}}}
		renderAppRow(&w, res, res.Rows[0])
		if got := strings.Contains(w.String(), "`$PKG`"); got != c.want || !strings.Contains(w.String(), "`$DATA`") {
			t.Errorf("package %v: $PKG named %v, want %v: %s", c.pkg != nil, got, c.want, w.String())
		}
	}
}
