// Why: the commands mk cache size, mk cache clean and mk profile merge
// work on the cache and on profiles outside any build, so they need entry
// points of their own.
//
// What: CacheDirFor, CacheSize and its CacheReport (KindSize per kind),
// CacheClean, MergeProfiles and ProfileWarnings.
//
// How: thin wrappers over internal/cache (the store's walk, its GC lock and
// Store.Clean) and internal/pgo (pgo.Merge, which sorts by function key, so
// a merged profile does not depend on the order of its inputs).

package driver

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"mk/internal/cache"
	"mk/internal/pgo"
)

// CacheDirFor is the cache directory of the build rooted at root: the
// cacheDir argument when given, else MK_CACHE, else <package root>/.mk-cache.
// The error is a *UsageError.
func CacheDirFor(root, cacheDir string) (string, error) {
	cfg, err := Config{Root: root, CacheDir: cacheDir}.normalize()
	if err != nil {
		return "", err
	}
	if cfg.CacheDir != "" {
		return cfg.CacheDir, nil
	}
	ri, err := findRoot(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(ri.dir, ".mk-cache"), nil
}

// KindSize is one kind's line of mk cache size.
type KindSize struct {
	Kind    string
	Entries int64
	Bytes   int64
}

// CacheReport is what mk cache size prints: one line per kind of the store
// in cache.Stats.SortedKinds order (bytewise, so the output is
// deterministic), the executables under the store's bin that mk run, mk test
// and the corpus link, and the store's total, bin included. Kinds holds
// every kind but bin.
type CacheReport struct {
	Kinds    []KindSize
	Total    int64
	BinFiles int64
	BinBytes int64
	// Packages are the fetched package trees under <cache>/pkg, apart
	// from the store and outside Total.
	Packages PackageCache
}

// CacheSize reports the cache under dir. A directory that does not exist is
// an empty cache, never an error, and is not created. The executables are
// the store's cache.BinDir, which its walk counts like every Dir,
// so Total is the size GC holds to the budget.
func CacheSize(dir string) (CacheReport, error) {
	var rep CacheReport
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return rep, nil
	}
	st, err := cache.Open(dir, cache.Options{ReadOnly: true})
	if err != nil {
		return rep, err
	}
	stats, err := st.Stats()
	if err != nil {
		return rep, err
	}
	for _, k := range stats.SortedKinds() {
		if k == cache.BinDir {
			rep.BinFiles, rep.BinBytes = stats.Entries[k], stats.Bytes[k]
			continue
		}
		rep.Kinds = append(rep.Kinds, KindSize{Kind: string(k), Entries: stats.Entries[k], Bytes: stats.Bytes[k]})
	}
	rep.Total = stats.Total
	rep.Packages, err = PackageCacheSize(dir)
	return rep, err
}

// Text renders the report: "<kind> <entries> entries <bytes> bytes" per
// kind, then "bin", then "total".
func (r CacheReport) Text() string {
	var b bytes.Buffer
	for _, k := range r.Kinds {
		fmt.Fprintf(&b, "%-8s %d entries %d bytes\n", k.Kind, k.Entries, k.Bytes)
	}
	fmt.Fprintf(&b, "%-8s %d entries %d bytes\n", "bin", r.BinFiles, r.BinBytes)
	fmt.Fprintf(&b, "%-8s %d bytes\n", "total", r.Total)
	fmt.Fprintf(&b, "%-8s %d trees %d bytes\n", "packages", r.Packages.Trees, r.Packages.Bytes)
	return b.String()
}

// CacheClean empties the cache under dir: every store entry under the
// store's GC lock (Store.Clean), the ThinLTO directory and the executables
// of bin included, and the work directories in the store's tmp that are
// older than cache.Grace (a running mk's younger one is left to it). An
// executable that is running (Windows refuses to delete it) stays, and is
// named in the error; the rest is removed. A <cache>/bin beside the store,
// where older versions of mk put executables, is removed too. A missing
// directory is already clean.
func CacheClean(dir string) error {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	st, err := cache.Open(dir, cache.Options{})
	if err != nil {
		return err
	}
	errs := []error{st.Clean()}
	if err := os.RemoveAll(filepath.Join(dir, "bin")); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// MergeProfiles reads every .mkprof of inputs, sums them with pgo.Merge and
// writes the result to out atomically (a temporary file beside out, then a
// rename). pgo.Merge sorts by function key, so the bytes do not depend on
// the order of the inputs. An unreadable or malformed input is an error
// naming it; out is then not written. The warnings are ProfileWarnings of
// the merged profile; out is written anyway.
func MergeProfiles(out string, inputs []string) ([]string, error) {
	if len(inputs) == 0 {
		return nil, errors.New("no input profiles")
	}
	ps := make([]*pgo.Profile, 0, len(inputs))
	for _, in := range inputs {
		f, err := os.Open(in)
		if err != nil {
			return nil, err
		}
		p, err := pgo.Read(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", in, err)
		}
		ps = append(ps, p)
	}
	m, err := pgo.Merge(ps...)
	if err != nil {
		return nil, err
	}
	b, err := m.Bytes()
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".mkprof-*")
	if err != nil {
		return nil, err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	return ProfileWarnings(m), nil
}

// ProfileWarnings names each function key of p that has records under more
// than one checksum, in key order: profiles of different builds of that
// function were merged. A build applies only the record whose checksum
// matches its MIR, but every record's entry count is in the profile's hot
// and cold thresholds, so a stale record still shifts them. p must be
// canonical (sorted by key, as pgo.Merge and pgo.Read give it): records of
// one key are found as neighbours.
func ProfileWarnings(p *pgo.Profile) []string {
	var out []string
	for i := 0; i < len(p.Records); {
		j := i + 1
		for j < len(p.Records) && p.Records[j].Key == p.Records[i].Key {
			j++
		}
		if n := j - i; n > 1 {
			out = append(out, fmt.Sprintf("%s has records for %d checksums (profiles of different builds); only the one matching the build is applied, but all of them count toward the hot and cold thresholds", p.Records[i].Key, n))
		}
		i = j
	}
	return out
}
