// Why: some rows need third-party C, such as the sqlite amalgamation, that
// is never kept in the repository; and rows under tests/bench-lanes join
// the tables only once each has a verdict.
//
// What: the "vendor" row field (vendorSpec, resolveVendor, vendorCopy,
// vendorCArgs, checkVendorFlags) and the verdicts on tests/bench-lanes rows
// (laneReview, loadLaneReview, applyLaneReview).
//
// How: vendored files come from $MK_EVIDENCE/<dir> (or Mk-evidence beside
// the repository) into a build copy of the Mk package, and into the C
// column's clang line with -I<dir> and the row's c_flags. A row whose files
// are absent does not load, so it is listed with the reason and set aside
// like any broken row under tests/bench-lanes.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// vendorSpec is a row's "vendor": files under the evidence directory that
// both the Mk package (as its own c_sources and headers) and the C column
// build from.
type vendorSpec struct {
	Dir    string   `json:"dir"`
	Files  []string `json:"files"`
	CFlags []string `json:"c_flags"`
	// Differ names the -D macros the two sides set differently on purpose
	// (each side's own OS layer or size call), exempt from vendorFlagDiff.
	Differ []string `json:"differ,omitempty"`
	// For names the languages that build the vendored files: "mk" and
	// "c" when absent (the sqlite rows, whose Mk package compiles the
	// amalgamation as its own c_sources). An app row whose C cell alone
	// links a library (zlib for an eco C cell) says ["c"]: the Mk package
	// is then built as it is, with no copy and none of the files in its
	// stamp.
	For []string `json:"for,omitempty"`
	// Include is directories under Dir that the C line gets as -I, beside
	// Dir itself (a library with its headers in subdirectories, zstd's
	// lib/common).
	Include []string `json:"include,omitempty"`
	// Path is the absolute directory the files were found in (loadRow).
	Path string `json:"-"`
	// files is Files with each pattern expanded (resolveVendor): paths
	// relative to Path, slash-separated, sorted within each pattern.
	files []string
}

// vendorFor reports whether language lang builds row r's vendored files.
func vendorFor(r *row, lang string) bool {
	if r.Vendor == nil {
		return false
	}
	if isMkLang(lang) {
		lang = "mk"
	}
	if len(r.Vendor.For) == 0 {
		return lang == "mk" || lang == "c"
	}
	return slices.Contains(r.Vendor.For, lang)
}

// vendorRel checks a path of the vendor field: relative to the evidence
// directory, slash-separated, without "..".
func vendorRel(p string) error {
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || slices.Contains(strings.Split(p, "/"), "..") {
		return fmt.Errorf("vendor path %q must be relative and slash-separated, without ..", p)
	}
	return nil
}

// pathDir is a slash path's directory part ("" for none).
func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// evidenceDir is $MK_EVIDENCE, else Mk-evidence beside the repository.
func evidenceDir(root string) string {
	if e := os.Getenv("MK_EVIDENCE"); e != "" {
		return e
	}
	return filepath.Join(filepath.Dir(root), "Mk-evidence")
}

// resolveVendor checks a row's vendor field against the evidence directory
// and records where its files are.
func resolveVendor(v *vendorSpec, rowDir string) error {
	if v.Dir == "" || len(v.Files) == 0 {
		return errors.New("vendor needs a dir and files")
	}
	if filepath.IsAbs(v.Dir) || strings.Contains(filepath.ToSlash(v.Dir), "..") {
		return fmt.Errorf("vendor dir %q must be relative to the evidence directory, without ..", v.Dir)
	}
	root, err := repoRoot(rowDir)
	if err != nil {
		return err
	}
	dir := filepath.Join(evidenceDir(root), filepath.FromSlash(v.Dir))
	for _, l := range v.For {
		if l != "mk" && l != "c" {
			return fmt.Errorf("vendor for %q: only mk and c build vendored C", l)
		}
	}
	for _, inc := range v.Include {
		if err := vendorRel(inc); err != nil {
			return err
		}
	}
	// A file is a path under dir whose last element may be a pattern
	// ("lib/common/*.c"); each must match at least one file.
	var missing []string
	v.files = nil
	for _, f := range v.Files {
		if err := vendorRel(f); err != nil {
			return err
		}
		if !strings.ContainsAny(f, "*?[") {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
				missing = append(missing, f)
			}
			v.files = append(v.files, f)
			continue
		}
		if strings.ContainsAny(pathDir(f), "*?[") {
			return fmt.Errorf("vendor file %q: only the last element may be a pattern", f)
		}
		ms, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return fmt.Errorf("vendor file %q: %w", f, err)
		}
		if len(ms) == 0 {
			missing = append(missing, f)
		}
		slices.Sort(ms)
		for _, m := range ms {
			rel, _ := filepath.Rel(dir, m)
			v.files = append(v.files, filepath.ToSlash(rel))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("vendor files absent from %s: %s (the evidence directory is outside the repository; set MK_EVIDENCE)", dir, strings.Join(missing, ", "))
	}
	v.Path = dir
	return nil
}

// vendorCopy makes the build copy of an Mk package that names vendored
// files: the package directory and the vendor files, under the work
// directory, refreshed on every call so an edit to either is built.
func vendorCopy(work string, r *row, lang string) (string, error) {
	src := filepath.Join(r.Dir, r.Sources[lang])
	dst := filepath.Join(work, "vendor", strings.ReplaceAll(r.id(), "/", "_"), lang)
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		return copyVendorFile(p, filepath.Join(dst, rel))
	})
	if err != nil {
		return "", err
	}
	for _, f := range r.Vendor.files {
		to := filepath.Join(dst, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return "", err
		}
		if err := copyVendorFile(filepath.Join(r.Vendor.Path, filepath.FromSlash(f)), to); err != nil {
			return "", err
		}
	}
	return dst, nil
}

func copyVendorFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// vendorFiles is the absolute paths of a row's vendored files, for the
// build stamp of a language that builds them (vendorFor).
func vendorFiles(r *row, lang string) []string {
	if !vendorFor(r, lang) {
		return nil
	}
	var out []string
	for _, f := range r.Vendor.files {
		out = append(out, filepath.Join(r.Vendor.Path, filepath.FromSlash(f)))
	}
	return out
}

// vendorCArgs is what the C column's clang line adds for a vendored row:
// -I<dir> and each include directory, the c_flags, and every vendored .c
// and assembly (.S, .s) file.
func vendorCArgs(r *row) []string {
	if !vendorFor(r, "c") {
		return nil
	}
	args := []string{"-I" + r.Vendor.Path}
	for _, inc := range r.Vendor.Include {
		args = append(args, "-I"+filepath.Join(r.Vendor.Path, filepath.FromSlash(inc)))
	}
	args = append(args, r.Vendor.CFlags...)
	for _, f := range r.Vendor.files {
		if strings.HasSuffix(f, ".c") || strings.HasSuffix(f, ".S") || strings.HasSuffix(f, ".s") {
			args = append(args, filepath.Join(r.Vendor.Path, filepath.FromSlash(f)))
		}
	}
	return args
}

var (
	csourceFlags = regexp.MustCompile(`pkg\.CSource\(path:\s*"([^"]+)",\s*flags:\s*\[([^\]]*)\]`)
	quoted       = regexp.MustCompile(`"([^"]*)"`)
)

// vendorFlagDiff is Request 17: the flags of the Mk package's CSource for
// each vendored .c file against the C column's c_flags. The two must agree
// on -march, every -O, the builtin flags and every -D, except a -D whose
// macro name the allowed list names (each side's own OS layer or size
// call). It returns one line per difference, "" for none.
func vendorFlagDiff(pkgText string, cflags, allowed []string) []string {
	relevant := func(f string) (string, bool) {
		switch {
		case strings.HasPrefix(f, "-march="), strings.HasPrefix(f, "-O"), f == "-fno-builtin", f == "-ffreestanding", strings.HasPrefix(f, "-fno-builtin-"):
			return f, true
		case strings.HasPrefix(f, "-D"):
			name, _, _ := strings.Cut(strings.TrimPrefix(f, "-D"), "=")
			if slices.Contains(allowed, name) {
				return "", false
			}
			return f, true
		}
		return "", false
	}
	set := func(flags []string) []string {
		var out []string
		for _, f := range flags {
			if k, ok := relevant(f); ok {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	c := set(cflags)
	var diffs []string
	for _, m := range csourceFlags.FindAllStringSubmatch(pkgText, -1) {
		var mk []string
		for _, q := range quoted.FindAllStringSubmatch(m[2], -1) {
			mk = append(mk, q[1])
		}
		// The driver compiles every CSource with -ffreestanding
		// -fno-builtin, so the Mk side has them whether or not the
		// package names them; -O follows the build profile (release: -O3,
		// the C column's level).
		mk = append(mk, "-fno-builtin")
		ms := set(mk)
		ms = slices.DeleteFunc(ms, func(f string) bool { return f == "-ffreestanding" })
		cs := slices.DeleteFunc(slices.Clone(c), func(f string) bool { return f == "-ffreestanding" || strings.HasPrefix(f, "-O") })
		ms = slices.DeleteFunc(ms, func(f string) bool { return strings.HasPrefix(f, "-O") })
		for _, f := range ms {
			if !slices.Contains(cs, f) {
				diffs = append(diffs, fmt.Sprintf("%s: %s is on the Mk CSource's clang line and not in the C column's c_flags", m[1], f))
			}
		}
		for _, f := range cs {
			if !slices.Contains(ms, f) {
				diffs = append(diffs, fmt.Sprintf("%s: %s is in the C column's c_flags and not on the Mk CSource's clang line", m[1], f))
			}
		}
	}
	return diffs
}

// checkVendorFlags refuses a vendored row whose Mk CSource and C column
// would compile the vendored C differently (Request 17), before anything is
// built or timed.
func checkVendorFlags(r *row) error {
	if !vendorFor(r, "mk") || !vendorFor(r, "c") {
		return nil
	}
	_, hasC := r.Sources["c"]
	mkSrc, hasMk := r.Sources["mk"]
	if !hasC || !hasMk {
		return nil
	}
	pkgText, err := os.ReadFile(filepath.Join(r.Dir, mkSrc, "pkg.mk"))
	if err != nil {
		return fmt.Errorf("%s: %w", r.id(), err)
	}
	cpu := cpus[r.cpu()]
	cflags := append([]string{"-O3", "-march=" + cpu.C}, r.Vendor.CFlags...)
	if d := vendorFlagDiff(string(pkgText), cflags, r.Vendor.Differ); len(d) > 0 {
		return fmt.Errorf("%s: the vendored C is not compiled alike in the mk and c columns (name a deliberate -D difference in vendor.differ):\n  %s", r.id(), strings.Join(d, "\n  "))
	}
	return nil
}

// laneReview is tests/bench/lane-review.json: the verdict on each row under
// tests/bench-lanes against the contract (fold the fair ones into the
// tables, hold the rest). Each entry matches a row id
// ("std-hash/maphash"), a bench.json's id before its variants
// ("std-simd/dot"), or a lane prefix ending in "/" ("std-text2/"); the
// longest match decides. "fold" adds its note to the row's notes (so
// RESULTS.md carries the label); "hold" sets the row aside like a broken
// one, with the note as the reason. Exempt lists the contract findings of
// TestLaneRowsMeetTheContract accepted for that row, each with
// its reason in the note.
type laneReview struct {
	Rows []laneVerdict `json:"rows"`
}

type laneVerdict struct {
	Match   string   `json:"match"`
	Verdict string   `json:"verdict"` // "fold" or "hold"
	Note    string   `json:"note"`
	Exempt  []string `json:"exempt,omitempty"`
}

// reviewFile is where the verdicts live: tests/bench/lane-review.json.
func reviewFile(root string) string {
	return filepath.Join(root, "tests", "bench", "lane-review.json")
}

func loadLaneReview(root string) (*laneReview, error) {
	b, err := os.ReadFile(reviewFile(root))
	if errors.Is(err, fs.ErrNotExist) {
		return &laneReview{}, nil
	}
	if err != nil {
		return nil, err
	}
	var lr laneReview
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lr); err != nil {
		return nil, fmt.Errorf("%s: %w", reviewFile(root), err)
	}
	for _, v := range lr.Rows {
		if v.Match == "" || (v.Verdict != "fold" && v.Verdict != "hold") || (v.Verdict == "hold" && v.Note == "") {
			return nil, fmt.Errorf("%s: entry %q needs a match, a verdict fold or hold, and a note for a hold", reviewFile(root), v.Match)
		}
	}
	return &lr, nil
}

// verdictFor is the entry that decides row r, or nil.
func (lr *laneReview) verdictFor(r *row) *laneVerdict {
	base := r.Lane + "/" + r.base()
	var best *laneVerdict
	for i := range lr.Rows {
		v := &lr.Rows[i]
		ok := v.Match == r.id() || v.Match == base || (strings.HasSuffix(v.Match, "/") && strings.HasPrefix(r.id(), v.Match))
		if ok && (best == nil || len(v.Match) > len(best.Match)) {
			best = v
		}
	}
	return best
}

// applyLaneReview labels or sets aside each lane row by its verdict.
func applyLaneReview(lr *laneReview, rows []*row) {
	for _, r := range rows {
		if r.Lane == "" || r.Broken != "" {
			continue
		}
		v := lr.verdictFor(r)
		switch {
		case v == nil:
			r.Notes = append(slices.Clone(r.Notes), "Not reviewed against the contract (tests/bench/lane-review.json has no entry for it).")
		case v.Verdict == "hold":
			r.Broken = "held out of the published tables by the contract review (tests/bench/lane-review.json): " + v.Note
		case v.Note != "":
			r.Notes = append(slices.Clone(r.Notes), "the contract review: "+v.Note)
		}
	}
}
