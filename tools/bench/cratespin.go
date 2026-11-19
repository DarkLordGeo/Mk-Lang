// Why: cargo's own checks of a vendored tree compare Cargo.lock's checksum
// only with the "package" field each .cargo-checksum.json declares for
// itself, and each file only with that JSON's "files" map, so a file edited
// together with its entry builds.
//
// What: the pin of an app row's crates tree: checkCratesPinned,
// checkCratePinned and cargoVendorLeavesOut.
//
// How: tools/bench keeps each package's crates.io archive beside the tree
// and, before every cargo build, checks the archive's SHA-256 against
// Cargo.lock and every vendored file against the archive.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// cratesArchives is the directory, inside a crates directory, that holds
// the crates.io archive of every vendored package as
// <name>-<version>.crate, the file cargo downloads and Cargo.lock's
// checksum is the SHA-256 of.
const cratesArchives = "archives"

// cargoVendorLeavesOut reports whether a package file, as a path relative
// to the package's directory, may be missing from the tree: a .gitignore
// at any depth, or a .gitattributes or unpack marker at the package's top,
// which no build reads. cargo 1.99.1's vendor dropped a top .gitignore from
// each of the 8 of the rows' 15 archives that have one, and
// unicode-ident 1.0.27's tests/fst/.gitignore (app/jsonl's tree), so a
// .gitignore below the top is allowed too; app/archive's tree lost top
// .gitattributes files as well. The vendor kept .ignore, Cargo.toml.orig
// and .cargo_vcs_info.json. .cargo-ok is allowed by its kind, not by an
// observation. No other archive file may be missing from the tree.
func cargoVendorLeavesOut(rel string) bool {
	switch rel {
	case ".gitattributes", ".cargo-ok":
		return true
	}
	return path.Base(rel) == ".gitignore"
}

// checkCratesPinned checks the vendored directory of a row's crates
// directory (cratesDir) against the archives beside it and the row's
// Cargo.lock: every registry package of the lockfile has
// archives/<name>-<version>.crate, whose SHA-256 is the lockfile's
// checksum; its vendored directory <vendored>/<name>-<version> (the
// --versioned-dirs layout) holds exactly the archive's regular files, byte
// for byte, beside its .cargo-checksum.json, missing only what
// cargoVendorLeavesOut names; and the vendored directory holds those
// packages' directories and nothing else. So the code cargo compiles is
// the code whose archive Cargo.lock pins, whatever the .cargo-checksum.json
// files say.
func checkCratesPinned(r *row, dir, vendored string) error {
	crate := cargoDir(r)
	if crate == "" {
		return errors.New("crates: the row builds no Rust crate")
	}
	lock, err := os.ReadFile(filepath.Join(crate, "Cargo.lock"))
	if err != nil {
		return fmt.Errorf("crates: the Rust crate's lockfile: %w", err)
	}
	vdir := filepath.Join(dir, filepath.FromSlash(vendored))
	want := map[string]string{} // <name>-<version> to the lockfile's checksum
	for _, p := range parseCargoLock(string(lock)) {
		if !strings.HasPrefix(p.Source, "registry+") {
			continue
		}
		if p.Checksum == "" {
			return fmt.Errorf("crates: Cargo.lock's %s %s has no checksum", p.Name, p.Version)
		}
		want[p.Name+"-"+p.Version] = p.Checksum
	}
	ents, err := os.ReadDir(vdir)
	if err != nil {
		return fmt.Errorf("crates %s: %w", r.Crates, err)
	}
	for _, e := range ents {
		if _, ok := want[e.Name()]; !ok || !e.IsDir() {
			return fmt.Errorf("crates %s: %s/%s is not a package of the crate's Cargo.lock (the vendored directory holds exactly the lockfile's registry packages, one <name>-<version> directory each, as `cargo vendor --versioned-dirs` writes them)", r.Crates, vendored, e.Name())
		}
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := checkCratePinned(filepath.Join(dir, cratesArchives, id+".crate"), want[id], id, filepath.Join(vdir, id)); err != nil {
			return fmt.Errorf("crates %s: %s: %w", r.Crates, id, err)
		}
	}
	return nil
}

// checkCratePinned checks one package: the archive's SHA-256 against sum,
// and the package directory pkg against the archive's files under id/.
func checkCratePinned(archive, sum, id, pkg string) error {
	b, err := os.ReadFile(archive)
	if err != nil {
		return fmt.Errorf("its archive: %w (each vendored package's crates.io archive is kept in %s/)", err, cratesArchives)
	}
	if h := sha256.Sum256(b); hex.EncodeToString(h[:]) != sum {
		return fmt.Errorf("%s/%s.crate has SHA-256 %s, and Cargo.lock's checksum is %s", cratesArchives, id, hex.EncodeToString(h[:]), sum)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%s/%s.crate: %w", cratesArchives, id, err)
	}
	tr := tar.NewReader(zr)
	inArchive := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%s/%s.crate: %w", cratesArchives, id, err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		rel, ok := strings.CutPrefix(h.Name, id+"/")
		rel = strings.TrimSuffix(rel, "/")
		if !ok || strings.Contains(rel, `\`) || rel != "" && (path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel)) {
			return fmt.Errorf("%s/%s.crate: entry %q is not a path inside %s/", cratesArchives, id, h.Name, id)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("%s/%s.crate: entry %q is not a regular file or a directory", cratesArchives, id, h.Name)
		}
		if rel == "" || rel == ".cargo-checksum.json" || inArchive[rel] {
			return fmt.Errorf("%s/%s.crate: entry %q is repeated or reserved", cratesArchives, id, h.Name)
		}
		inArchive[rel] = true
		body, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("%s/%s.crate: %s: %w", cratesArchives, id, h.Name, err)
		}
		got, err := os.ReadFile(filepath.Join(pkg, filepath.FromSlash(rel)))
		switch {
		case errors.Is(err, fs.ErrNotExist) && cargoVendorLeavesOut(rel):
		case err != nil:
			return fmt.Errorf("the vendored %s is missing or unreadable: %w", rel, err)
		case !bytes.Equal(got, body):
			return fmt.Errorf("the vendored %s differs from its file in %s/%s.crate", rel, cratesArchives, id)
		}
	}
	return filepath.WalkDir(pkg, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(pkg, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			return fmt.Errorf("the vendored %s is not a regular file", rel)
		}
		if rel != ".cargo-checksum.json" && !inArchive[rel] {
			return fmt.Errorf("the vendored %s is not in %s/%s.crate", rel, cratesArchives, id)
		}
		return nil
	})
}
