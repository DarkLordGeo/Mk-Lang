// Why: a build that reads vendor/ must find each dependency's copy without
// fetching, and the mapping must be a plain, reviewable file.
//
// What: vendor/index.txt (vendorIndexFile): vendorEntry, vendorIndex with
// readVendorIndex, lookup, lookupSource and text, and depPkg.vendorKey.
//
// How: one sorted line per package of the build list, `<source or path>
// v<version> <canonical id> tree-sha256:<hex>`. A path package's line names
// its directory relative to the workspace root, with "/"; a url package's
// its url; a package with no version writes "-" in the version field; a
// source package's line names its source and version. A build finds a
// package by that key (a source package by its source and major) and reads
// vendor/<canonical id>/.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"mk/internal/fetch"
)

// vendorIndexFile is the index's name in vendor/.
const vendorIndexFile = "index.txt"

// vendorEntry is one line of vendor/index.txt.
type vendorEntry struct {
	key, version, cid string
	hash              fetch.TreeHash
}

// vendorIndex is a read vendor/index.txt; the zero value is empty.
type vendorIndex struct{ entries []vendorEntry }

// readVendorIndex reads dir's index.txt: an empty index when there is none.
func readVendorIndex(dir string) (*vendorIndex, error) {
	b, err := os.ReadFile(filepath.Join(dir, vendorIndexFile))
	if os.IsNotExist(err) {
		return &vendorIndex{}, nil
	}
	if err != nil {
		return nil, err
	}
	vi := &vendorIndex{}
	for i, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, " ")
		if len(f) != 4 {
			return nil, fmt.Errorf("line %d: want four fields: source or path, version, canonical id, tree hash", i+1)
		}
		h, ok := fetch.ParseTreeHash(f[3])
		if !ok || !canonicalIDForm(f[2]) {
			return nil, fmt.Errorf("line %d: %q is not a canonical id and tree-sha256 hash", i+1, f[2]+" "+f[3])
		}
		v := strings.TrimPrefix(f[1], "v")
		vi.entries = append(vi.entries, vendorEntry{key: f[0], version: v, cid: f[2], hash: h})
	}
	return vi, nil
}

// canonicalIDForm reports whether s has a canonical id's form,
// `<identifier>_<8 lower hex>`, so it is one directory name under vendor/
// and never a reserved first segment such as std.
func canonicalIDForm(s string) bool {
	i := strings.LastIndexByte(s, '_')
	if i <= 0 || !isHex(s[i+1:], 8) || !validDepName(s[:i]) {
		return false
	}
	return !slices.Contains(reservedSegments, s) && s != "main"
}

// empty reports whether the index has no line (no index.txt: the older
// vendor/<name> layout, or no vendor/).
func (vi *vendorIndex) empty() bool { return vi == nil || len(vi.entries) == 0 }

// lookup is the entry of p's identity.
func (vi *vendorIndex) lookup(p *depPkg) (vendorEntry, bool) {
	if vi == nil {
		return vendorEntry{}, false
	}
	key := p.vendorKey()
	url := strings.HasPrefix(p.identity, "url ")
	for _, e := range vi.entries {
		if url && key != strings.Fields(p.identity)[1] && e.cid != urlCID(strings.Fields(p.identity)[1]) {
			continue // a redacted key: the canonical id tells two urls apart
		}
		if e.key == key || p.rel != "" && foldPath(e.key) == foldPath(key) {
			return e, true
		}
	}
	return vendorEntry{}, false
}

// lookupSource is the entry of a source package of the given major: its
// line names the source and a version of that major (two majors of one
// source are two lines).
func (vi *vendorIndex) lookupSource(source string, major uint64) (vendorEntry, bool) {
	if vi == nil {
		return vendorEntry{}, false
	}
	for _, e := range vi.entries {
		if v, ok := parseSemver(e.version); ok && e.key == source && v.Major == major {
			return e, true
		}
	}
	return vendorEntry{}, false
}

// vendorKey is p's first field in vendor/index.txt: its directory relative
// to the workspace root, its source, or its url without its credentials
// (redactURL; vendor/ is committed).
func (p *depPkg) vendorKey() string {
	if p.rel != "" {
		return p.rel
	}
	if p.source != "" {
		return p.source
	}
	return redactURL(strings.Fields(p.identity)[1])
}

// text is the index's content, sorted by key, then version (two majors of
// one source).
func (vi *vendorIndex) text() string {
	es := slices.Clone(vi.entries)
	slices.SortFunc(es, func(a, b vendorEntry) int {
		if c := strings.Compare(a.key, b.key); c != 0 {
			return c
		}
		av, _ := parseSemver(a.version)
		bv, _ := parseSemver(b.version)
		return compareSemver(av, bv)
	})
	var b strings.Builder
	for _, e := range es {
		v := "-"
		if e.version != "-" && e.version != "" {
			v = "v" + e.version
		}
		fmt.Fprintf(&b, "%s %s %s %s\n", e.key, v, e.cid, e.hash)
	}
	return b.String()
}
