// Why: mk apidiff decides what a std release may change without breaking
// users, so its rules for open sets, options types and Experimental modules
// must hold.
// What: tests of the rules in apidiff_rules.go: additions to open sets,
// fields added to options types, Experimental modules, the wildcard arm mk
// fix offers, and std.pkg in mk apidiff --std.
// How: builds two stand-in std trees (apiStd, apiFsV2) and compares the
// severity stdChanges reports for each changed item.

package driver

import (
	"path/filepath"
	"strings"
	"testing"
)

// apiStd is the stand-in std with an fs module in the shape of 1.2's and an
// Experimental tls module; mark says how fs's open sets are marked: "word"
// with the word open, "doc" with 1.2's doc line, "" not at all.
func apiStd(t *testing.T, mark string, fs, tls string) string {
	t.Helper()
	files := fakeStdFiles()
	switch mark {
	case "word":
		fs = strings.ReplaceAll(fs, "@open\nexport ", "export open ")
	case "doc":
		fs = strings.ReplaceAll(fs, "@open\n", "/// May gain members.\n")
	default:
		fs = strings.ReplaceAll(fs, "@open\n", "")
	}
	files["fs/fs.mk"] = "export ReadText(): int {\n    return 1\n}\n\n" + fs
	files["tls/tls.mk"] = tls
	return writeTree(t, files)
}

const apiFsV1 = `export error NotFound
export error Denied

@open
export type Error = NotFound | Denied

/// Not open: a set with no doc line.
export type Closed = NotFound | Denied

@open
export enum Kind {
    File
    Dir
}

export Open(p: str): Error!int {
    return 1
}

export struct Options {
    export read: bool
    sealed: int
}

export const DefaultOptions: Options = Options(read: true, sealed: 0)

export struct Info {
    export is_dir: bool
}
`

const apiTlsV1 = "export Version(): int {\n    return 3\n}\n"

// apiFsV2 adds a member to each set, a variant to Kind, a field to Options
// and one to Info.
var apiFsV2 = strings.NewReplacer(
	"export error Denied\n", "export error Denied\nexport error Unsupported\n",
	"export type Error = NotFound | Denied", "export type Error = NotFound | Denied | Unsupported",
	"export type Closed = NotFound | Denied", "export type Closed = NotFound | Denied | Unsupported",
	"    Dir\n", "    Dir\n    Link\n",
	"    export read: bool\n", "    export read: bool\n    export write: bool\n",
	"Options(read: true, sealed: 0)", "Options(read: true, write: false, sealed: 0)",
	"    export is_dir: bool\n", "    export is_dir: bool\n    export kind: Kind\n",
).Replace(apiFsV1)

const apiTlsV2 = "export Version(major: int): int {\n    return major\n}\n"

func stdChanges(t *testing.T, a, b string) map[string]Severity {
	t.Helper()
	res, err := APIDiffStd(Config{}, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Incomplete {
		t.Fatalf("incomplete:\n%s\n%s", res.A.Report.Text(res.A.Sources), res.B.Report.Text(res.B.Sources))
	}
	out := map[string]Severity{}
	for _, c := range res.Changes {
		out[c.Key+" "+c.What] = c.Severity
	}
	t.Logf("%s", res.Text())
	return out
}

// TestAPIDiffOpenSetAddition: a member added to a set the older
// release declared open is an addition, in the set and in every signature
// that spells it; a set with no doc line still breaks; a field added to an
// options type is an addition, to another struct breaking (fs.Info); an
// Experimental module's change is reported, not breaking.
func TestAPIDiffOpenSetAddition(t *testing.T) {
	v1 := apiStd(t, "word", apiFsV1, apiTlsV1)
	v2 := apiStd(t, "word", apiFsV2, apiTlsV2)
	got := stdChanges(t, v1, v2)
	want := map[string]Severity{
		"std.fs.Error members added":                         Compatible,
		"std.fs.Kind members added":                          Compatible,
		"std.fs.Open errors: errors added to an open set":    Compatible,
		"std.fs.Open signature: errors added to an open set": Compatible,
		"std.fs.Options fields added":                        Compatible,
		"std.fs.Unsupported added":                           Compatible,
		"std.fs.Closed declaration":                          Breaking,
		"std.fs.Info fields":                                 Breaking,
		"std.tls.Version signature":                          Reported,
	}
	for k, s := range want {
		if got[k] != s {
			t.Errorf("%s: %q, want %q", k, got[k], s)
		}
	}
	for k, s := range got {
		if s == Breaking && want[k] != Breaking && !strings.HasSuffix(k, " size") && !strings.HasSuffix(k, " abi") {
			t.Errorf("unexpected breaking change %s", k)
		}
	}
}

// TestAPIDiffSetsOpenedInThisRelease: the members a release adds to
// sets the older release did not declare open (1.2's additions to 1.1's
// sets) are breaking.
func TestAPIDiffSetsOpenedInThisRelease(t *testing.T) {
	v1 := apiStd(t, "", apiFsV1, apiTlsV1)
	v2 := apiStd(t, "word", apiFsV2, apiTlsV1)
	got := stdChanges(t, v1, v2)
	for _, k := range []string{"std.fs.Error declaration", "std.fs.Kind fields", "std.fs.Open errors", "std.fs.Open signature", "std.fs.Info fields"} {
		if got[k] != Breaking {
			t.Errorf("%s: %q, want breaking", k, got[k])
		}
	}
	if got["std.fs.Options fields added"] != Compatible {
		t.Errorf("an options type's added field: %q, want compatible", got["std.fs.Options fields added"])
	}
}

// TestFixOffersWildcardArm: mk fix applies nothing to a match on a
// std set that misses a member: the `_` arm is an alternative, never a
// proven fix, and the report left carries it with the open-set note.
func TestFixOffersWildcardArm(t *testing.T) {
	std := apiStd(t, "word", apiFsV1, apiTlsV1)
	src := "import std.fs\n\nCode(e: fs.Error): int {\n    return match e {\n        fs.NotFound { 1 }\n    }\n}\n"
	dir := testPkg(t, map[string]string{"main.mk": src})
	s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), StdDir: std, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rounds) != 0 || len(res.Files) != 0 || readFile(t, filepath.Join(dir, "main.mk")) != src {
		t.Fatalf("mk fix changed the file: %d rounds, files %v", len(res.Rounds), res.Files)
	}
	text := res.Final.Report.Text(res.Final.Sources)
	for _, w := range []string{"E0306", "note: fs.Error may gain members: keep a `_` arm", "try: a `_` arm: `_ { ... }`"} {
		if !strings.Contains(text, w) {
			t.Errorf("report lacks %q:\n%s", w, text)
		}
	}
}

// TestFFI_APIDiffStdPkg: std.pkg, which the manifest step checks in tables
// of its own, is in mk apidiff --std. From a std.pkg without Link and
// Package's links field to the tree's: Package's fields break (a manifest
// names every field), Link is an addition, and nothing else of std.pkg
// changes.
func TestFFI_APIDiffStdPkg(t *testing.T) {
	cur := stdPkgText()
	old := strings.Replace(cur, "    export links: []Link\n", "", 1)
	// Cut from the doc comment of struct Link (the "///" lines above it) to
	// the end: Link is the file's last declaration.
	if i := strings.Index(old, "\nexport struct Link {"); i >= 0 {
		j := i
		for {
			k := strings.LastIndex(old[:j], "\n")
			if k < 0 || !strings.HasPrefix(old[k+1:j], "///") {
				break
			}
			j = k
		}
		old = old[:j+1]
	}
	if old == cur || strings.Contains(old, "struct Link") || strings.Contains(old, "[]Link") {
		t.Fatal("std/pkg/pkg.mk no longer has the links field and the Link struct this test removes")
	}
	v1 := apiStd(t, "word", apiFsV1, apiTlsV1)
	v2 := apiStd(t, "word", apiFsV1, apiTlsV1)
	man := readFile(t, filepath.Join(v1, "pkg.mk"))
	writeFiles(t, v1, map[string]string{"pkg/pkg.mk": old, "pkg.mk": strings.Replace(man, "    links: [],\n", "", 1)})
	got := stdChanges(t, v1, v2)
	// A field added to a std.pkg struct is compatible: a manifest may leave
	// it out.
	want := map[string]Severity{"std.pkg.Package fields added": Compatible, "std.pkg.Link added": Compatible}
	for k, sev := range want {
		if got[k] != sev {
			t.Errorf("%s: %q, want %q", k, got[k], sev)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok && strings.HasPrefix(k, "std.pkg.") {
			t.Errorf("unexpected change %s", k)
		}
	}
}

// TestAPIDiffOpenSetInParameter: a member added to an open set is an
// addition wherever a signature spells the set: a callback's result (std's
// http.Serve shape), a callback beside a result that names the set too, a
// plain parameter. Each signature is compatible; a parameter of a set with
// no doc line that gained the same member still breaks, beside the open one
// too, though both flatten to the same text.
func TestAPIDiffOpenSetInParameter(t *testing.T) {
	const fns = `
export Serve(n: int, handle: fn(n: int): Error!void): Error!void {
    return
}

export Each(handle: fn(n: int): Error!void): int {
    return 0
}

export Report(e: Error): int {
    return 0
}

export Pick(e: Error, f: fn(e: Error): int): Error!int {
    return 1
}

export Shut(e: Closed): int {
    return 0
}

export Mixed(e: Error, c: Closed): int {
    return 0
}
`
	v1 := apiStd(t, "word", apiFsV1+fns, apiTlsV1)
	v2 := apiStd(t, "word", apiFsV2+fns, apiTlsV1)
	got := stdChanges(t, v1, v2)
	for _, f := range []string{"Serve", "Each", "Report", "Pick"} {
		k := "std.fs." + f + " signature: errors added to an open set"
		if got[k] != Compatible {
			t.Errorf("%s: %q, want compatible", k, got[k])
		}
	}
	for _, k := range []string{"std.fs.Shut signature", "std.fs.Mixed signature"} {
		if got[k] != Breaking {
			t.Errorf("%s: %q, want breaking", k, got[k])
		}
	}
	for k, s := range got {
		if s == Breaking && k != "std.fs.Closed declaration" && k != "std.fs.Info fields" && k != "std.fs.Shut signature" && k != "std.fs.Mixed signature" && !strings.HasSuffix(k, " size") && !strings.HasSuffix(k, " abi") {
			t.Errorf("unexpected breaking change %s", k)
		}
	}
}
