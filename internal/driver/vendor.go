// Why: a package that commits its dependencies builds offline and from
// exactly the bytes it reviewed; `mk vendor` writes that copy.
//
// What: VendorDep, VendorResult and Session.Vendor, with writeVendored,
// dirTreeHash, copyPackage and previousVendorIDs.
//
// How: every dependency of the build is walked from the original sources
// (vendor/ ignored) and copied into vendor/<canonical id>/, with
// vendor/index.txt naming each (vendor_index.go); later builds read it
// instead of fetching. Only directories the previous index named are ever
// removed. An older vendor/<name> layout gets a note and stays.

package driver

import (
	"cmp"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/fetch"
)

// VendorDep is one dependency mk vendor wrote: its name, its source as the
// manifest gives it (a path or a url), and the directory written,
// "vendor/<canonical id>".
type VendorDep struct{ Name, From, Dir string }

// VendorResult is mk vendor's outcome: the dependencies written, in name
// order, or the diagnostics that stopped it (Result.Report).
type VendorResult struct {
	Deps   []VendorDep
	Result Result
}

// Vendor runs the manifest step and the dependency walk with vendor/
// ignored, then replaces vendor/<name> for every dependency: a path
// dependency's files are copied (hidden directories and its own vendor/
// skipped), a fetched one's unpacked tree written. Each is written to
// vendor/.tmp-<name> first and renamed into place. The error is an I/O
// failure or a usage error (a root that is no package).
func (s *Session) Vendor() (VendorResult, error) {
	if s.root.kind != rootPackage {
		return VendorResult{}, usagef("mk vendor needs a package (a directory holding pkg.mk)")
	}
	w := newWorld(s.cfg)
	s.w = w
	p := &pipeline{w: w}
	s.p, s.noVendor = p, true
	defer func() { s.noVendor = false }()
	var deps []*depPkg
	p.guard("vendor "+s.root.name, func() {
		stdT := w.stdTree()
		pkgT := w.diskTree(s.root.dir, s.root.prefix)
		ms := w.newManifestStep(stdT)
		p.step(StageManifest, func() []*diag.Diagnostic {
			_, ds := ms.load(stdT, stdLayout.Manifest, "")
			if len(ds) > 0 {
				return ds
			}
			man, ds := ms.load(pkgT, "pkg.mk", "main")
			if len(ds) > 0 {
				return ds
			}
			deps, ds = s.resolveDeps(ms, pkgT, man, false)
			return ds
		})
	})
	res := VendorResult{Result: p.result(w, s.cfg.MaxDiags)}
	if len(res.Result.Report.Diags) > 0 {
		return res, nil
	}
	slices.SortFunc(deps, func(a, b *depPkg) int { return strings.Compare(a.cid, b.cid) })
	vendor := filepath.Join(s.root.dir, "vendor")
	old := previousVendorIDs(vendor)
	idx := &vendorIndex{}
	keep := map[string]bool{}
	if err := os.MkdirAll(vendor, 0o755); err != nil {
		return res, err // the index is written even with no dependency
	}
	// A workspace member is the user's own code, read from its directory;
	// it is never vendored (its id is its manifest name, no canonical id
	// the index may hold).
	deps = slices.DeleteFunc(deps, func(dp *depPkg) bool { return dp.member })
	for _, dp := range deps {
		h, err := writeVendored(vendor, dp)
		if err != nil {
			return res, err
		}
		keep[dp.cid] = true
		// A source package's line holds its version.
		idx.entries = append(idx.entries, vendorEntry{key: dp.vendorKey(), version: cmp.Or(dp.version, "-"), cid: dp.cid, hash: h})
		res.Deps = append(res.Deps, VendorDep{Name: dp.name, From: redactURL(dp.from), Dir: "vendor/" + dp.cid})
	}
	if err := writeFileAtomic(filepath.Join(vendor, vendorIndexFile), []byte(idx.text())); err != nil {
		return res, err
	}
	// Only what the previous vendor/index.txt named, and this build no
	// longer has, is removed: never a directory mk vendor did not write,
	// never a package's own source. An older-layout vendor/<name> gets a
	// note and stays.
	sources := map[string]bool{}
	for _, dp := range deps {
		if dp.osDir != "" {
			sources[pathKey(dp.osDir)] = true
		}
	}
	for _, cid := range old {
		dir := filepath.Join(vendor, cid)
		if keep[cid] || sources[pathKey(dir)] || !validDepName(cid) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return res, err
		}
	}
	if len(old) == 0 {
		for _, dp := range deps {
			for _, n := range dp.names {
				dir := filepath.Join(vendor, n)
				if !keep[n] && !sources[pathKey(dir)] && hasManifest(dir) {
					res.Result.Notes = append(res.Result.Notes, fmt.Sprintf(
						"note: vendor/%s is in the older layout and is no longer read; mk vendor wrote vendor/%s, so vendor/%s may be removed by hand", n, dp.cid, n))
				}
			}
		}
	}
	return res, nil
}

// writeVendored replaces vendor/<canonical id> with dp's files and
// returns their tree hash.
func writeVendored(vendor string, dp *depPkg) (fetch.TreeHash, error) {
	if err := os.MkdirAll(vendor, 0o755); err != nil {
		return fetch.TreeHash{}, err
	}
	tmp := filepath.Join(vendor, ".tmp-"+dp.cid)
	if err := os.RemoveAll(tmp); err != nil {
		return fetch.TreeHash{}, err
	}
	var err error
	switch {
	case dp.archive != nil:
		err = dp.archive.Write(tmp)
	case dp.treeDir != "":
		err = copyPackage(dp.treeDir, tmp) // a source package from the package cache
	default:
		err = copyPackage(dp.osDir, tmp)
	}
	if err != nil {
		os.RemoveAll(tmp)
		return fetch.TreeHash{}, err
	}
	h, err := dirTreeHash(tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return fetch.TreeHash{}, err
	}
	final := filepath.Join(vendor, dp.cid)
	if err := os.RemoveAll(final); err != nil {
		return fetch.TreeHash{}, err
	}
	return h, os.Rename(tmp, final)
}

// dirTreeHash is the package tree hash (fetch.TreeHash) of the regular
// files under dir.
func dirTreeHash(dir string) (fetch.TreeHash, error) {
	var t fetch.Tree
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		t.Files = append(t.Files, fetch.File{Path: filepath.ToSlash(rel), Data: b})
		return nil
	})
	return t.Hash(), err
}

// copyPackage copies the regular files of the package in src to dst,
// skipping hidden directories (.git, .mk-cache) and src's own vendor/.
func copyPackage(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || rel == "vendor") {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}

// previousVendorIDs are the canonical ids the existing vendor/index.txt
// names, read leniently (a malformed line is skipped): the directories a
// new mk vendor may remove.
func previousVendorIDs(vendor string) []string {
	b, err := os.ReadFile(filepath.Join(vendor, vendorIndexFile))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 4 {
			out = append(out, f[2])
		}
	}
	return out
}
