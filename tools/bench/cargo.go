// Why: the app table's Rust cells are real crates with dependencies, and a
// cargo build may change what it compiles through a manifest key, a
// configuration file or a fetched crate; the table must time a pinned build.
//
// What: the Rust cells of the app table that build a crate
// (tests/bench/README.md "Applications"): the allowed manifest form, the
// lockfile check against the cell's kind, cratesConfig and cargoCommand.
// `sources.rust` names a directory with a Cargo.toml and a Cargo.lock.
//
// How: tools/bench builds it with `cargo build --release --offline --locked`
// and the rust column's profile. Nothing is fetched at bench time: the
// libraries come from the local cargo registry, or, for a row that names
// `crates`, from a `cargo vendor` tree in the evidence directory by
// directory source replacement. From the registry, the lockfile's checksum
// pins each crate. From a vendored tree, cargo compares the lockfile's
// checksum only with the "package" field each package's .cargo-checksum.json
// declares, and each file only with that JSON's own "files" map, which
// guards against accidental edits only; so tools/bench also checks every
// vendored file against the package's crates.io archive kept beside the
// tree, and the archive against the lockfile's checksum (cratespin.go).

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// cargoTarget is the target triple of every cargo build: with --target,
// RUSTFLAGS reach the program's crates only, not build scripts or
// procedural macros, which run on the build machine.
const cargoTarget = "x86_64-pc-windows-msvc"

// cargoEnvCleared is the environment a caller could change a cargo build
// through, removed from every cargo build's environment (an entry ending
// in "*" is a prefix, buildEnv): every CARGO_PROFILE_, CARGO_BUILD_ and
// CARGO_TARGET_ variable (CARGO_TARGET_DIR is then set per column), the
// flags variables (CARGO_ENCODED_RUSTFLAGS, the highest-precedence one,
// is set again with the column's flags), the wrappers, the rustc and
// rustup's toolchain choice. CARGO_HOME stays, for the registry.
var cargoEnvCleared = []string{"RUSTFLAGS", "CARGO_ENCODED_RUSTFLAGS", "RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER", "RUSTC", "RUSTDOC",
	"CARGO_INCREMENTAL", "CARGO_PROFILE_*", "CARGO_BUILD_*", "CARGO_TARGET_*", "RUSTUP_TOOLCHAIN"}

var (
	cargoLockEntry = regexp.MustCompile(`(?m)^name = "([^"]+)"\nversion = "([^"]+)"`)
	cargoBareKey   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	cargoString    = regexp.MustCompile(`^"[^"\\]*"$`)
)

// The one manifest form a row's crate may have: a [package] table, then
// optionally [dependencies], with no key before [package] and no other
// table; in [package] only the keys below, each a bare key with a plain
// value; in [dependencies] a bare name with a version string, or an inline
// table of version, features and default-features. Everything else is
// refused: a profile, a build script, a target section, a workspace, a
// patch, a path or git dependency, in any spelling TOML has (a dotted,
// quoted or inline-table key, a quoted table header), since each would
// change the build away from the rust column's flags or bring in code that
// libs and the program's own files do not show. A quoted key, a literal
// string and a multi-line value are refused outright, so no spelling reaches
// cargo unread.
var (
	cargoPackageKeys = map[string]string{"name": "string", "version": "string", "edition": "string",
		"publish": "bool", "description": "string", "license": "string"}
	cargoDepKeys = map[string]string{"version": "string", "features": "strings", "default-features": "bool"}
)

// cargoManifest checks a crate's Cargo.toml against that form and returns
// its package name.
func cargoManifest(toml string) (string, error) {
	table, name := "", ""
	for i, l := range strings.Split(strings.ReplaceAll(toml, "\r\n", "\n"), "\n") {
		bad := func(what string) error {
			return fmt.Errorf("the Rust crate's Cargo.toml line %d (%q) is %s: tools/bench builds a [package] with [dependencies] from the registry only, each key bare and each value one plain string, boolean, array of strings or dependency table", i+1, strings.TrimSpace(l), what)
		}
		l, err := cargoStripComment(l)
		if err != nil {
			return "", bad(err.Error())
		}
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "[") {
			h := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(l, "["), "]"))
			if !strings.HasSuffix(l, "]") || (h != "package" && h != "dependencies") || (h == "dependencies" && table == "") {
				return "", bad("a table other than [package] and, after it, [dependencies]")
			}
			table = h
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case !ok || !cargoBareKey.MatchString(k):
			return "", bad("not a bare key = value (a dotted or quoted key, or no value)")
		case table == "":
			return "", bad("a key before [package]")
		case table == "package":
			kind, known := cargoPackageKeys[k]
			if !known {
				return "", bad("a [package] key tools/bench does not allow (a build script, a target, a workspace or another setting)")
			}
			if !cargoValue(kind, v) {
				return "", bad("not a plain " + kind)
			}
			if k == "name" {
				name = strings.Trim(v, `"`)
			}
		case table == "dependencies":
			if !cargoValue("string", v) && !cargoDepTable(v) {
				return "", bad("a dependency that is neither a version string nor a table of version, features and default-features (a path, git or workspace dependency, or a renamed package)")
			}
		}
	}
	if name == "" {
		return "", errors.New("the Rust crate's Cargo.toml names no [package]")
	}
	return name, nil
}

// cargoStripComment is line l without its comment, trimmed, or an error
// for a spelling the manifest form refuses: a literal string or quoted key
// ('...'), a multi-line string, or an unclosed one. The key and value
// checks refuse each of them too; this one names them.
func cargoStripComment(l string) (string, error) {
	in := false
scan:
	for i := 0; i < len(l); i++ {
		switch l[i] {
		case '"':
			in = !in
		case '#':
			if !in {
				l = l[:i]
				break scan
			}
		}
	}
	switch {
	case strings.Contains(l, `"""`) || strings.Contains(l, `'`):
		return "", errors.New("a literal or multi-line string, or a key quoted with '")
	case in:
		return "", errors.New("an unclosed string")
	}
	return strings.TrimSpace(l), nil
}

// cargoValue reports whether v is a plain value of kind: a string with no
// escape, true or false, or an array of such strings.
func cargoValue(kind, v string) bool {
	switch kind {
	case "string":
		return cargoString.MatchString(v)
	case "bool":
		return v == "true" || v == "false"
	case "strings":
		inner, ok := strings.CutPrefix(v, "[")
		if inner, ok = strings.CutSuffix(inner, "]"); !ok {
			return false
		}
		for _, e := range strings.Split(inner, ",") {
			if e = strings.TrimSpace(e); e != "" && !cargoString.MatchString(e) {
				return false
			}
		}
		return true
	}
	return false
}

// cargoDepTable reports whether v is an inline dependency table of
// cargoDepKeys only, with a version.
func cargoDepTable(v string) bool {
	inner, ok := strings.CutPrefix(v, "{")
	if inner, ok = strings.CutSuffix(inner, "}"); !ok {
		return false
	}
	seen := map[string]bool{}
	depth, start := 0, 0
	var parts []string
	for i, ch := range inner {
		switch ch {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, inner[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, inner[start:])
	for _, p := range parts {
		k, v, ok := strings.Cut(p, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		kind, known := cargoDepKeys[k]
		if !ok || !known || seen[k] || !cargoValue(kind, v) {
			return false
		}
		seen[k] = true
	}
	return seen["version"]
}

// lockPackage is one [[package]] of a Cargo.lock.
type lockPackage struct {
	Name, Version, Source, Checksum string
	Deps                            []string // as written: "name" or "name version"
}

// parseCargoLock reads a Cargo.lock's packages (the version 3 and 4 form
// cargo writes: one key per line, dependencies one per line).
func parseCargoLock(lock string) []lockPackage {
	var out []lockPackage
	lock = strings.ReplaceAll(lock, "\r\n", "\n")
	for _, block := range strings.Split(lock, "[[package]]")[1:] {
		var p lockPackage
		inDeps := false
		for _, l := range strings.Split(block, "\n") {
			l = strings.TrimSpace(l)
			if inDeps {
				if l == "]" {
					inDeps = false
				} else if s := strings.Trim(strings.TrimSuffix(l, ","), `"`); s != "" {
					p.Deps = append(p.Deps, s)
				}
				continue
			}
			k, v, ok := strings.Cut(l, "=")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`)
			switch k {
			case "name":
				p.Name = v
			case "version":
				p.Version = v
			case "source":
				p.Source = v
			case "checksum":
				p.Checksum = v
			case "dependencies":
				inDeps = v == "["
			}
		}
		out = append(out, p)
	}
	return out
}

// cargoConfigAbove is a cargo configuration or toolchain file in a parent
// directory of dir, or in CARGO_HOME, that cargo or rustup would read for
// a build in dir, or "".
func cargoConfigAbove(dir string) string {
	var places []string
	for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
		places = append(places, filepath.Join(d, ".cargo", "config"), filepath.Join(d, ".cargo", "config.toml"),
			filepath.Join(d, "rust-toolchain"), filepath.Join(d, "rust-toolchain.toml"))
		if filepath.Dir(d) == d {
			break
		}
	}
	home := os.Getenv("CARGO_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".cargo")
		}
	}
	if home != "" {
		places = append(places, filepath.Join(home, "config"), filepath.Join(home, "config.toml"))
	}
	for _, p := range places {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// cargoDir is the crate directory of an app row's Rust source, or "" when
// the source is a single file built by rustc.
func cargoDir(r *row) string {
	src, ok := r.Sources["rust"]
	if !ok {
		return ""
	}
	p := filepath.Join(r.Dir, src)
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p
	}
	return ""
}

// checkCargoSource checks a row's crate when the row is loaded: it has a
// Cargo.toml of cargoManifest's form, naming one package, and a
// Cargo.lock, no build script, cargo configuration or toolchain file of
// its own, and every library its libs pin appears in the lockfile at
// exactly that version.
func checkCargoSource(r *row) error {
	dir := cargoDir(r)
	if dir == "" {
		return nil
	}
	toml, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		return fmt.Errorf("the Rust crate: %w", err)
	}
	root, err := cargoManifest(string(toml))
	if err != nil {
		return err
	}
	for _, f := range []string{"build.rs", ".cargo", "rust-toolchain", "rust-toolchain.toml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return fmt.Errorf("the Rust crate has %s: no build script, cargo configuration or toolchain file of its own", f)
		}
	}
	// A cargo configuration above the crate, or in CARGO_HOME, is the
	// machine's, not the row's: cargoCommand refuses the crate's build
	// then, a failed build of that cell like any other, and every row
	// still loads.
	lock, err := os.ReadFile(filepath.Join(dir, "Cargo.lock"))
	if err != nil {
		return fmt.Errorf("the Rust crate has no lockfile (cargo builds with --locked): %w", err)
	}
	have := map[string][]string{}
	for _, m := range cargoLockEntry.FindAllSubmatch([]byte(strings.ReplaceAll(string(lock), "\r\n", "\n")), -1) {
		have[string(m[1])] = append(have[string(m[1])], string(m[2]))
	}
	for _, l := range r.Libs["rust"] {
		if !slices.Contains(have[l.Name], l.Version) {
			return fmt.Errorf("library %s %s is not in the crate's Cargo.lock (it has %q)", l.Name, l.Version, have[l.Name])
		}
	}
	// The lockfile against the cell's kind: every package but
	// the root comes from the registry with a checksum, a crate that
	// links any is an eco cell, and libs names each direct dependency.
	var direct []string
	others := 0
	for _, p := range parseCargoLock(string(lock)) {
		if p.Name == root && p.Source == "" {
			for _, d := range p.Deps {
				name, _, _ := strings.Cut(d, " ")
				direct = append(direct, name)
			}
			continue
		}
		others++
		if !strings.HasPrefix(p.Source, "registry+") || p.Checksum == "" {
			return fmt.Errorf("Cargo.lock's %s %s has source %q and checksum %q: every library comes from the registry, pinned by its checksum", p.Name, p.Version, p.Source, p.Checksum)
		}
	}
	if others > 0 && r.Kinds["rust"] != "eco" {
		return fmt.Errorf("the Rust crate links %d registry packages (Cargo.lock): its cell is eco, with libs naming its dependencies", others)
	}
	for _, d := range direct {
		if !slices.ContainsFunc(r.Libs["rust"], func(l lib) bool { return l.Name == d }) {
			return fmt.Errorf("the Rust crate depends on %s (Cargo.lock), which libs does not name", d)
		}
	}
	return nil
}

// cratesConfigLine is the form of each line of a crates directory's
// .cargo/config.toml, in order (what `cargo vendor` prints): it replaces
// crates-io with the vendored directory and says nothing else.
var cratesConfigLine = []*regexp.Regexp{
	regexp.MustCompile(`^\[source\.crates-io\]$`),
	regexp.MustCompile(`^replace-with\s*=\s*"vendored-sources"$`),
	regexp.MustCompile(`^\[source\.vendored-sources\]$`),
	regexp.MustCompile(`^directory\s*=\s*"([^"\\]+)"$`),
}

// cratesDir is the directory a row's crates field names, under the
// evidence directory.
func cratesDir(r *row) (string, error) {
	if err := vendorRel(r.Crates); err != nil {
		return "", fmt.Errorf("crates: %w", err)
	}
	root, err := repoRoot(r.Dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(evidenceDir(root), filepath.FromSlash(r.Crates)), nil
}

// cratesConfig is the configuration file of a row's crates directory
// (row.Crates), checked: the directory is under the evidence
// directory, its .cargo/config.toml has exactly the four lines of
// cratesConfigLine (blank lines aside), and the vendored directory it names
// is relative, without "..", and present, and that directory is pinned by
// the crates.io archives beside it (checkCratesPinned). cargo reads a
// relative directory against the parent of the .cargo directory, so the
// tree may move with the evidence directory.
func cratesConfig(r *row) (string, error) {
	dir, err := cratesDir(r)
	if err != nil {
		return "", err
	}
	cfg := filepath.Join(dir, ".cargo", "config.toml")
	b, err := os.ReadFile(cfg)
	if err != nil {
		return "", fmt.Errorf("crates %s: %w (the evidence directory is outside the repository; set MK_EVIDENCE)", r.Crates, err)
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != len(cratesConfigLine) {
		return "", fmt.Errorf("%s: %d lines; it may only replace crates-io with a vendored directory (%d lines)", cfg, len(lines), len(cratesConfigLine))
	}
	var vendored string
	for i, re := range cratesConfigLine {
		m := re.FindStringSubmatch(lines[i])
		if m == nil {
			return "", fmt.Errorf("%s: line %q: it may only replace crates-io with a vendored directory", cfg, lines[i])
		}
		if len(m) > 1 {
			vendored = m[1]
		}
	}
	if err := vendorRel(vendored); err != nil {
		return "", fmt.Errorf("%s: directory: %w", cfg, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(vendored))); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s: the vendored directory %s is absent", cfg, vendored)
	}
	if err := checkCratesPinned(r, dir, vendored); err != nil {
		return "", err
	}
	return cfg, nil
}

// cargoCommand is the build of a crate into the column's executable: the
// rust column's flags as cargo's release profile (opt-level 3, one codegen
// unit, fat LTO, panic abort, overflow checks on for rust-ovf only) set by
// --config, which wins over every configuration file, and the CPU target
// and static CRT through CARGO_ENCODED_RUSTFLAGS. Each column has its own
// CARGO_TARGET_DIR under the work directory; CARGO_HOME stays the user's,
// for the registry. A cargo configuration or toolchain file cargo would
// find above the crate or in CARGO_HOME refuses the build: a failed build
// of this cell, which publish and verify report per cell and compare
// stops at (cargoConfigAbove).
func (t *toolchain) cargoCommand(r *row, c column, dir string) (buildCmd, error) {
	if t.Cargo == "" {
		return buildCmd{}, errors.New("cargo not found")
	}
	toml, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		return buildCmd{}, err
	}
	name, err := cargoManifest(string(toml))
	if err != nil {
		return buildCmd{}, err
	}
	if p := cargoConfigAbove(dir); p != "" {
		return buildCmd{}, fmt.Errorf("cargo would read %s for this build: tools/bench builds with no cargo configuration or toolchain file but its own flags", p)
	}
	cpu := cpus[r.cpu()]
	ovf := "false"
	if c.ID == "rust-ovf" {
		ovf = "true"
	}
	target := filepath.Join(t.Work, "cargo", strings.ReplaceAll(r.id(), "/", "_"), c.ID)
	args := []string{t.Cargo, "build", "--release", "--offline", "--locked", "--target", cargoTarget,
		"--manifest-path", filepath.Join(dir, "Cargo.toml"),
		"--config", "profile.release.opt-level=3",
		"--config", "profile.release.codegen-units=1",
		"--config", `profile.release.lto="fat"`,
		"--config", `profile.release.panic="abort"`,
		"--config", "profile.release.overflow-checks=" + ovf,
		"--config", "profile.release.debug-assertions=false",
		"--config", "profile.release.incremental=false",
	}
	if r.Crates != "" {
		// The crates come from the evidence directory by directory source
		// replacement; the build is still --offline --locked.
		// cratesConfig checks every vendored file against its package's
		// archive and each archive against Cargo.lock, since cargo's own
		// .cargo-checksum.json check refuses only an accidental edit (the
		// file header).
		cfg, err := cratesConfig(r)
		if err != nil {
			return buildCmd{}, err
		}
		args = append(args, "--config", cfg)
	}
	env := []string{"CARGO_TARGET_DIR=" + target,
		"CARGO_ENCODED_RUSTFLAGS=" + strings.Join([]string{"-Ctarget-cpu=" + cpu.Rust, "-Ctarget-feature=+crt-static"}, "\x1f")}
	if t.Rustc != "" {
		// The rustc Deps records is the one cargo runs.
		env = append(env, "RUSTC="+t.Rustc)
	}
	return buildCmd{Dir: dir, Env: env, Unset: cargoEnvCleared, Args: args,
		Deps:    t.version("rust", t.Rustc) + "; " + t.version("cargo", t.Cargo),
		Product: filepath.Join(target, cargoTarget, "release", name+".exe")}, nil
}
