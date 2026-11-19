// Why: fuzz findings live in the source tree, so each test needs a corpus
// directory of its own that never collides with another test's and can be
// created on every target, including Windows with its device names and
// case-insensitive paths.
// What: fuzzDirOf, fuzzDirMap, fuzzCorpusDir and fuzzCorpusNote for the
// directories, fuzzSignature and savedSignatures for one saved input per
// failure.
// How: a name that is a device name or has a case-folded twin gets a "~"
// suffix from a SHA-256 of the module path and test name; failures are
// deduplicated by a signature with every number masked.

package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// windowsDevices are the names Windows reserves in every directory, with
// or without an extension.
var windowsDevices = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// windowsDevice reports whether a directory named n would be a device.
func windowsDevice(n string) bool {
	base, _, _ := strings.Cut(n, ".")
	return windowsDevices[strings.ToUpper(strings.TrimRight(base, " "))]
}

// fuzzDirOf is the corpus directory name of test tc among tests (the
// package's): FuzzDirName(tc.name) when that is the name itself, no
// Windows device name, and no other test's directory compares equal to it
// ignoring case (Windows' rule, so one tree means one thing on both
// targets); otherwise that name, "_" first for a device name, with "~"
// and the first 8 hex digits of
// the SHA-256 of the module path, a NUL byte and the test name, so two
// tests never share a directory and none is unwritable. A test whose name
// gains a twin later moves to its suffixed directory: the driver says so
// when the plain one still holds files (fuzzCorpusNote).
func fuzzDirOf(tc testCase, tests []testCase) string {
	return fuzzDirMap(tests)[fuzzKey{tc.module, tc.name}]
}

// fuzzKey is a test's identity: its module and its name.
type fuzzKey struct{ module, name string }

// fuzzDirMap is fuzzDirOf for every test at once, in time linear in the
// number of tests (one scan of the others per test is quadratic, too slow
// for large test suites): the tests are counted by their directory name
// folded to lower case, and a name counted once is unique.
func fuzzDirMap(tests []testCase) map[fuzzKey]string {
	folded := map[string]map[fuzzKey]bool{}
	for _, t := range tests {
		f := strings.ToLower(FuzzDirName(t.name))
		if folded[f] == nil {
			folded[f] = map[fuzzKey]bool{}
		}
		folded[f][fuzzKey{t.module, t.name}] = true
	}
	out := make(map[fuzzKey]string, len(tests))
	for _, t := range tests {
		k := fuzzKey{t.module, t.name}
		base := FuzzDirName(t.name)
		if base == t.name && !windowsDevice(base) && len(folded[strings.ToLower(base)]) == 1 {
			out[k] = base
			continue
		}
		if windowsDevice(base) {
			base = "_" + base // COM1.txt~... is still COM1 to Windows
		}
		sum := sha256.Sum256([]byte(t.module + "\x00" + t.name))
		out[k] = base + "~" + hex.EncodeToString(sum[:4])
	}
	return out
}

// fuzzDir is test tc's corpus directory name: from opt's map, made once
// per run by Session.Test, else made now.
func (s *Session) fuzzDir(tc testCase, opt TestOptions) string {
	if d, ok := opt.fuzzDirs[fuzzKey{tc.module, tc.name}]; ok {
		return d
	}
	return fuzzDirOf(tc, s.w.packageTests(s.testModules()))
}

// fuzzCorpusDir is test tc's corpus directory under the package root.
func (s *Session) fuzzCorpusDir(tc testCase, opt TestOptions) string {
	return filepath.Join(s.root.dir, "testdata", "fuzz", s.fuzzDir(tc, opt))
}

// fuzzCorpusNote is the test's note when its directory is a suffixed one
// but the plain directory of its name holds files: they are not read,
// because another test shares that name (the report shows the note
// whatever the result, TestCase.Note).
func (s *Session) fuzzCorpusNote(tc testCase, opt TestOptions) string {
	d := s.fuzzDir(tc, opt)
	plain := FuzzDirName(tc.name)
	if d == plain {
		return ""
	}
	if files := corpusFiles(filepath.Join(s.root.dir, "testdata", "fuzz", plain)); len(files) > 0 && !windowsDevice(plain) {
		return "testdata/fuzz/" + plain + "/ is not read: this test's corpus is testdata/fuzz/" + d + "/ (its name is shared or not a plain file name)"
	}
	return ""
}

// fuzzSiteRe finds a panic report's site, "at <file>:<line>:<col>", and
// fuzzNumRe its numbers, hexadecimal ones first.
var (
	fuzzSiteRe = regexp.MustCompile(` at (\S+:\d+:\d+)\s*$`)
	fuzzNumRe  = regexp.MustCompile(`0[xX][0-9A-Fa-f]+|[0-9]+`)
)

// fuzzSignature is a failure's identity for deduplication: its reason, and
// for a panic the report's first "panic:" line, its message with every
// number (decimal or 0x hexadecimal) written # and its site: so a bounds
// panic's index does not make a new failure, while two different messages
// raised at one site (a helper that panics for its callers) are two
// failures. A hang has no message and no site, so every hang of a target is
// one signature: a second, different hang is logged, not saved, until the
// first is fixed.
func fuzzSignature(r fuzzRun) string {
	sig := r.reason
	if strings.HasPrefix(sig, "hang") {
		return "hang"
	}
	for _, l := range strings.Split(strings.ReplaceAll(r.output, "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(l)
		msg, ok := strings.CutPrefix(l, "panic:")
		if !ok {
			continue
		}
		site := ""
		if m := fuzzSiteRe.FindStringSubmatchIndex(msg); m != nil {
			site = " at " + msg[m[2]:m[3]]
			msg = msg[:m[0]]
		}
		return sig + ": " + fuzzNumRe.ReplaceAllString(strings.TrimSpace(msg), "#") + site
	}
	return sig
}

// savedSignatures are the signature lines of dir's .report files, by
// signature, each with its input's file name.
func savedSignatures(dir string) map[string]string {
	out := map[string]string{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		n, ok := strings.CutSuffix(e.Name(), ".report")
		if !ok {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if sig, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), "signature: "); ok {
				if _, seen := out[sig]; !seen {
					out[sig] = n
				}
				break
			}
		}
	}
	return out
}
