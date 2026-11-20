// Why: an open set (an alias of error types, or an enum, written open) may
// grow, so matches outside its package need a `_` arm; mk apidiff, mk fix and the
// cache must all agree on which sets are open.
// What: mk apidiff on the word open and the older doc-line mark, mk fix's
// added `_` arm, and the rechecks a cached build does when a set becomes
// open or closed, directly or through an open alias that holds its members.
// How: builds small std or package trees in temporary directories, compares
// apidiff severities by line, and compares each cached verdict with a
// fresh cache's (one that holds only std's check results).

package driver

import (
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestAPIDiffReadsBothMarks: a set an older std marks with its doc
// line is as open to mk apidiff as one written open, so additions to those
// open sets stay additions; and a release that turns the doc line into the word
// breaks nothing.
func TestAPIDiffReadsBothMarks(t *testing.T) {
	got := stdChanges(t, apiStd(t, "doc", apiFsV1, apiTlsV1), apiStd(t, "word", apiFsV2, apiTlsV1))
	for _, k := range []string{"std.fs.Error members added", "std.fs.Kind members added", "std.fs.Open signature: errors added to an open set"} {
		if got[k] != Compatible {
			t.Errorf("%s: %q, want compatible", k, got[k])
		}
	}
	for k, s := range stdChanges(t, apiStd(t, "doc", apiFsV1, apiTlsV1), apiStd(t, "word", apiFsV1, apiTlsV1)) {
		if s == Breaking {
			t.Errorf("the doc line turned into the word: breaking %s", k)
		}
	}
}

// TestAPIDiffWordChange: writing open on a closed set breaks every
// exhaustive match outside its package, so mk apidiff calls it breaking;
// removing it is reported. Both directions, for an alias and an enum; and mk
// query sig spells the word.
func TestAPIDiffWordChange(t *testing.T) {
	closed, open := apiStd(t, "", apiFsV1, apiTlsV1), apiStd(t, "word", apiFsV1, apiTlsV1)
	opened := stdChanges(t, closed, open)
	shut := stdChanges(t, open, closed)
	for _, k := range []string{"std.fs.Error open added", "std.fs.Kind open added"} {
		if opened[k] != Breaking {
			t.Errorf("closed to open: %s %q, want breaking (%v)", k, opened[k], opened)
		}
	}
	for _, k := range []string{"std.fs.Error open removed", "std.fs.Kind open removed"} {
		if shut[k] != Reported {
			t.Errorf("open to closed: %s %q, want reported (%v)", k, shut[k], shut)
		}
	}
	for _, m := range []map[string]Severity{opened, shut} {
		for k := range m {
			if strings.Contains(k, " declaration") {
				t.Errorf("the word alone changed a declaration fact: %s", k)
			}
		}
	}
	// A change's declaration lines still show the word.
	res, err := APIDiffStd(Config{}, open, apiStd(t, "word", apiFsV2, apiTlsV1))
	if err != nil {
		t.Fatal(err)
	}
	shown := false
	for _, c := range res.Changes {
		if c.Key == "std.fs.Error" && c.What == "members added" {
			shown = strings.HasPrefix(c.Old, "export open type Error = ") && strings.HasPrefix(c.New, "export open type Error = ")
		}
		// A member added to a set already written open is an addition.
		if (c.Key == "std.fs.Error" || c.Key == "std.fs.Kind") && c.Severity != Compatible {
			t.Errorf("open set grown: %s %s %v, want compatible", c.Key, c.What, c.Severity)
		}
	}
	if !shown {
		t.Errorf("std.fs.Error's members added does not show the word:\n%s", res.Text())
	}
	dir := testPkg(t, map[string]string{"main.mk": "import std.fs\n\nexport F(k: fs.Kind): int {\n    return 1\n}\n"})
	s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), StdDir: open, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]string{"fs.Error": "export open type Error = ", "fs.Kind": "export open enum Kind"} {
		a, res, err := s.Query("sig", []string{q})
		if err != nil || a == nil {
			t.Fatalf("query sig %s: %v\n%s", q, err, res.Report.Text(res.Sources))
		}
		if !strings.HasPrefix(a.Text(), want) {
			t.Errorf("query sig %s:\n%s\nwant a line starting %q", q, a.Text(), want)
		}
	}
}

// TestAPIDiffKindChangeHasNoOpenLine: an open alias that
// becomes an error, and an open enum that becomes a struct, lose the open
// fact with their kind; the declaration's line says so, breaking, and no
// "open added" or "open removed" line is printed.
func TestAPIDiffKindChangeHasNoOpenLine(t *testing.T) {
	a := apiStd(t, "word", apiFsV1+"\n@open\nexport type Set = NotFound | Denied\n\n@open\nexport enum Shape {\n    Round\n}\n", apiTlsV1)
	b := apiStd(t, "word", apiFsV1+"\nexport error Set\n\nexport struct Shape {\n    n: int\n}\n", apiTlsV1)
	got := stdChanges(t, a, b)
	for k, s := range got {
		if strings.HasPrefix(k, "std.fs.Set open") || strings.HasPrefix(k, "std.fs.Shape open") {
			t.Errorf("kind change printed %q (%v)", k, s)
		}
	}
	for _, k := range []string{"std.fs.Set declaration", "std.fs.Shape declaration"} {
		if got[k] != Breaking {
			t.Errorf("%s: %q, want breaking (%v)", k, got[k], got)
		}
	}
}

// TestFixAddsFutureArm: a match outside std that names every member
// of an open set is E0306 with reason open, and mk fix adds the `_` arm,
// which panics, in value position and where every arm returns; the package
// then checks clean.
func TestFixAddsFutureArm(t *testing.T) {
	std := apiStd(t, "word", apiFsV1, apiTlsV1)
	src := `import std.fs

Code(e: fs.Error): int {
    return match e {
        fs.NotFound { 1 }
        fs.Denied { 2 }
    }
}

Name(k: fs.Kind): str {
    match k {
        .File {
            return "file"
        }
        .Dir {
            return "dir"
        }
    }
}
`
	want := `import std.fs

Code(e: fs.Error): int {
    return match e {
        fs.NotFound { 1 }
        fs.Denied { 2 }
        _ {
            Panic("fs.Error gained a member this match does not handle")
        }
    }
}

Name(k: fs.Kind): str {
    match k {
        .File {
            return "file"
        }
        .Dir {
            return "dir"
        }
        _ {
            Panic("fs.Kind gained a member this match does not handle")
        }
    }
}
`
	dir := testPkg(t, map[string]string{"main.mk": src})
	s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), StdDir: std, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stopped != "clean" || len(res.Rounds) != 1 || len(res.Rounds[0].Applied) != 2 {
		t.Fatalf("stopped %q, rounds %+v\n%s", res.Stopped, res.Rounds, res.Final.Report.Text(res.Final.Sources))
	}
	if got := readFile(t, filepath.Join(dir, "main.mk")); got != want {
		t.Fatalf("fixed file:\n%s\nwant:\n%s", got, want)
	}
}

// TestFixArmBraceOnArmLine: when the match's closing brace
// shares the last arm's line, which mk fmt keeps, the proven arm still
// applies: the brace moves to a line of its own, and the package checks
// clean.
func TestFixArmBraceOnArmLine(t *testing.T) {
	std := apiStd(t, "word", apiFsV1, apiTlsV1)
	src := "import std.fs\n\nS(k: fs.Kind): int {\n    var n := 0\n    match k {\n        .File { n = 1 }\n        .Dir { n = 2 } }\n    return n\n}\n"
	want := "import std.fs\n\nS(k: fs.Kind): int {\n    var n := 0\n    match k {\n        .File { n = 1 }\n        .Dir { n = 2 }\n        _ {\n            Panic(\"fs.Kind gained a member this match does not handle\")\n        }\n    }\n    return n\n}\n"
	dir := testPkg(t, map[string]string{"main.mk": src})
	s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), StdDir: std, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stopped != "clean" || len(res.Rounds) != 1 || len(res.Rounds[0].Applied) != 1 {
		t.Fatalf("stopped %q, rounds %+v\n%s", res.Stopped, res.Rounds, res.Final.Report.Text(res.Final.Sources))
	}
	if got := readFile(t, filepath.Join(dir, "main.mk")); got != want {
		t.Fatalf("fixed file:\n%s\nwant:\n%s", got, want)
	}
}

// TestWordRechecksDependents: the word open is part of the set's
// public facts, so writing it in a std that a cached build already checked
// rechecks the dependent package: its match, clean against the closed set,
// is E0306 reason open against the open one, with one cache directory.
func TestWordRechecksDependents(t *testing.T) {
	std := apiStd(t, "", apiFsV1, apiTlsV1)
	src := "import std.fs\n\nName(k: fs.Kind): int {\n    return match k {\n        .File { 1 }\n        .Dir { 2 }\n    }\n}\n"
	dir := testPkg(t, map[string]string{"main.mk": src})
	cache := t.TempDir()
	check := func() Result {
		s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), StdDir: std, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		return s.Check()
	}
	if res := check(); len(res.Report.Diags) != 0 {
		t.Fatalf("closed set: %s", res.Report.Text(res.Sources))
	}
	open := apiStd(t, "word", apiFsV1, apiTlsV1)
	writeFiles(t, std, map[string]string{"fs/fs.mk": readFile(t, filepath.Join(open, "fs", "fs.mk"))})
	res := check()
	text := res.Report.Text(res.Sources)
	if len(res.Report.Diags) != 1 || !strings.Contains(text, "E0306") || !strings.Contains(text, "reason: open") {
		t.Fatalf("open set after a cached closed build: %s", text)
	}
}

// TestSetHoldingAliasRechecks: a set is open when it holds every member
// of an exported open alias of its members' package, so writing or removing
// the word on lib.QErr, or on an alias of lib with QErr's members, rechecks
// Direct with one cache directory, and Code too, since mid.PErr = lib.QErr |
// Mine holds them. An alias of lib that keeps its name and kind and becomes
// open, `type X = int` to `open type X = A | B`, rechecks too. mid and ext
// are other top directories, so the word on PErr, on mid's own alias of
// lib.A | lib.B and on ext's is E0706 at the word, and leaves both matches
// accepted. Each cached verdict is compared with a fresh cache's. Code
// matches through mid only; Direct names lib.QErr in its signature.
func TestSetHoldingAliasRechecks(t *testing.T) {
	mid := "import lib\n\nexport error Mine\n\nexport type PErr = lib.QErr | Mine\n"
	ext := "import lib\n\nexport open type X = lib.A | lib.B\n"
	src := "import mid\nimport lib\n\nCode(e: mid.PErr): int {\n    return match e {\n        lib.A { 1 }\n        lib.B { 2 }\n        mid.Mine { 3 }\n    }\n}\n\nDirect(e: lib.QErr): int {\n    return match e {\n        lib.A { 1 }\n        lib.B { 2 }\n    }\n}\n"
	closed := "export error A\nexport error B\nexport error C\n\nexport type QErr = A | B\n"
	open := strings.Replace(closed, "export type", "export open type", 1)
	// Wide is open and holds C too, so no set of the program holds it whole.
	wide := closed + "\nexport open type Wide = A | B | C\n"
	added := closed + "\nexport open type Other = A | B\n"
	dir := testPkg(t, map[string]string{"pkg.mk": depManifest("app"), "src/main.mk": src, "mid/mid.mk": mid, "lib/lib.mk": closed})
	cache := t.TempDir()
	check := func(cache string) []string {
		s, err := NewSession(Config{Root: dir, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		res := s.Check()
		var lines []string
		for _, e := range res.Report.Diags {
			lines = append(lines, e.Diag.Code.String()+" "+strconv.Itoa(int(res.Sources.Position(e.Diag.At.StartPos()).Line)))
		}
		sort.Strings(lines)
		return lines
	}
	lb := func(s string) map[string]string { return map[string]string{"lib/lib.mk": s} }
	md := func(s string) map[string]string { return map[string]string{"mid/mid.mk": s} }
	steps := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"closed", lb(closed), nil},
		{"open", lb(open), []string{"E0306 13", "E0306 5"}},
		{"closed again", lb(closed), nil},
		// Wide is open and holds C too: no set of the program is Wide.
		{"wide", lb(wide), nil},
		{"wide shrunk to A | B", lb(strings.Replace(wide, "A | B | C", "A | B", 1)), []string{"E0306 13", "E0306 5"}},
		{"closed, then a new open alias", lb(closed), nil},
		{"a new open alias", lb(added), []string{"E0306 13", "E0306 5"}},
		{"closed, before a private alias", lb(closed), nil},
		// A private alias is no promise to another package, so it does
		// not make the set open, cached or fresh.
		{"a new private open alias", lb(closed + "\nopen type Priv = A | B\n"), nil},
		// The name and the kind stay, only the alias's type
		// and its word change.
		{"an alias of int", lb(closed + "\nexport type X = int\n"), nil},
		{"the alias of int written open over A | B", lb(closed + "\nexport open type X = A | B\n"), []string{"E0306 13", "E0306 5"}},
		{"the open alias back to int", lb(closed + "\nexport type X = int\n"), nil},
		{"closed, before mid's word", lb(closed), nil},
		{"mid writes open on PErr", md(strings.Replace(mid, "export type", "export open type", 1)), []string{"E0706 5"}},
		{"mid closed again", md(mid), nil},
		// mid imports ext privately, so the import closure grows by a module
		// whose open alias holds lib.A | lib.B, and no export of mid
		// changes.
		{"mid's private import of ext", map[string]string{"ext/ext.mk": ext, "mid/mid.mk": "import lib\nimport ext\n\nexport error Mine\n\nexport type PErr = lib.QErr | Mine\n\nHidden(v: ext.X): int {\n    return 0\n}\n"}, []string{"E0706 3"}},
		{"mid without ext, ext closed", map[string]string{"mid/mid.mk": mid, "ext/ext.mk": strings.Replace(ext, "export open type", "export type", 1)}, nil},
		{"mid's own open alias of lib.A | lib.B", md(mid + "\nexport open type MX = lib.A | lib.B\n"), []string{"E0706 7"}},
		{"mid re-exports ext's open alias", map[string]string{"ext/ext.mk": ext, "mid/mid.mk": "import lib\nimport ext\n\nexport error Mine\n\nexport type PErr = lib.QErr | Mine\n\nexport type Ext = ext.X\n"}, []string{"E0706 3"}},
	}
	// The fresh cache holds std's check results alone: the package is
	// checked from nothing at every step.
	seed := stdSeedCheck(t, Config{})
	for i, st := range steps {
		writeFiles(t, dir, st.files)
		got := check(cache)
		fresh := check(seededCache(t, seed))
		if !slices.Equal(got, st.want) || !slices.Equal(fresh, st.want) {
			t.Fatalf("step %d (%s): cached %v, fresh %v, want %v", i, st.name, got, fresh, st.want)
		}
	}
}

// TestWordInInterfaceHash: the same across two modules of one package
// under different top directories (so the match is foreign), where only
// the declaring module's interface hash can tell the dependent to recheck.
func TestWordInInterfaceHash(t *testing.T) {
	lib := "export enum Color {\n    Red\n    Blue\n}\n"
	src := "import lib\n\nName(c: lib.Color): int {\n    return match c {\n        .Red { 1 }\n        .Blue { 2 }\n    }\n}\n"
	dir := testPkg(t, map[string]string{"pkg.mk": depManifest("app"), "src/main.mk": src, "lib/lib.mk": lib})
	cache := t.TempDir()
	check := func() Result {
		s, err := NewSession(Config{Root: dir, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		return s.Check()
	}
	if res := check(); len(res.Report.Diags) != 0 {
		t.Fatalf("closed enum: %s", res.Report.Text(res.Sources))
	}
	writeFiles(t, dir, map[string]string{"lib/lib.mk": strings.Replace(lib, "export enum", "export open enum", 1)})
	res := check()
	text := res.Report.Text(res.Sources)
	if len(res.Report.Diags) != 1 || !strings.Contains(text, "E0306") || !strings.Contains(text, "reason: open") {
		t.Fatalf("open enum after a cached closed build: %s", text)
	}
}

// TestPackageOpenSetRechecks: an open alias is written in a module that
// declares one of its members, so lib.sets's `open type QOpen = lib.A |
// lib.B` is E0706 at the word and opens nothing: Direct's match on lib.QErr
// is accepted whether or not mid imports lib.sets privately, and so is a
// match on sets.QOpen itself, whatever the import closure holds. Once
// lib.sets declares C, an open alias of lib.A | C there opens sets.AC, its
// twin, and writing, removing or retyping that alias rechecks the match on
// AC (the reads of lib.sets's namespace and of its aliases' signatures). A
// set that holds every member of an open alias is open, so an open alias of
// part of sets.ABC, in either module that declares a member of it, opens
// ABC. An open alias of lib's errors in another top directory is E0706 at
// the word and opens nothing. Each cached verdict is compared with a fresh
// cache's.
func TestPackageOpenSetRechecks(t *testing.T) {
	lib := "export error A\nexport error B\n\nexport type QErr = A | B\n"
	sets := "import lib\n\nexport open type QOpen = lib.A | lib.B\n"
	ac := "import lib\n\nexport error C\n\nexport type AC = lib.A | C\n"
	mid := "import lib\n\nexport error Mine\n\nexport type PErr = lib.QErr | Mine\n"
	midSets := "import lib\nimport lib.sets\n\nexport error Mine\n\nexport type PErr = lib.QErr | Mine\n\nHidden(v: sets.QOpen): int {\n    return 0\n}\n"
	src := "import lib\nimport mid\n\nDirect(e: lib.QErr): int {\n    return match e {\n        lib.A { 1 }\n        lib.B { 2 }\n    }\n}\n\nUse(m: mid.Mine): int {\n    return 0\n}\n"
	withSets := strings.Replace(src, "import mid\n", "import mid\nimport lib.sets\n", 1)
	onOpen := withSets + "\nOnOpen(e: sets.QOpen): int {\n    return match e {\n        lib.A { 1 }\n        lib.B { 2 }\n    }\n}\n"
	onAC := withSets + "\nOnAC(e: sets.AC): int {\n    return match e {\n        lib.A { 1 }\n        sets.C { 2 }\n    }\n}\n"
	abc := "import lib\n\nexport error C\n\nexport type ABC = lib.A | lib.B | C\n"
	onABC := withSets + "\nOnABC(e: sets.ABC): int {\n    return match e {\n        lib.A { 1 }\n        lib.B { 2 }\n        sets.C { 3 }\n    }\n}\n"
	dir := testPkg(t, map[string]string{"pkg.mk": depManifest("app"), "src/main.mk": src, "mid/mid.mk": mid, "lib/lib.mk": lib, "lib/sets/sets.mk": sets})
	cache := t.TempDir()
	check := func(cache string) []string {
		s, err := NewSession(Config{Root: dir, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		res := s.Check()
		var lines []string
		for _, e := range res.Report.Diags {
			p := res.Sources.Position(e.Diag.At.StartPos())
			lines = append(lines, e.Diag.Code.String()+" "+filepath.Base(res.Sources.File(e.Diag.At.File).Path)+":"+strconv.Itoa(int(p.Line)))
		}
		sort.Strings(lines)
		return lines
	}
	away := []string{"E0706 sets.mk:3"}
	ls := func(s string) map[string]string { return map[string]string{"lib/sets/sets.mk": s} }
	steps := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"QOpen with no member of lib.sets", nil, away},
		// A private import of lib.sets changes no verdict.
		{"mid's private import of lib.sets", map[string]string{"mid/mid.mk": midSets}, away},
		{"mid without lib.sets", map[string]string{"mid/mid.mk": mid}, away},
		{"src matches sets.QOpen", map[string]string{"src/main.mk": onOpen}, away},
		{"QOpen gains a member of lib.sets", ls("import lib\n\nexport error C\n\nexport open type QOpen = lib.A | lib.B | C\n"), []string{"E0306 main.mk:17"}},
		{"QOpen with no member again", ls(sets), away},
		{"a closed AC over lib.A and lib.sets's C", map[string]string{"src/main.mk": onAC, "lib/sets/sets.mk": ac}, nil},
		{"an open twin of AC in lib.sets", ls(ac + "\nexport open type Twin = lib.A | C\n"), []string{"E0306 main.mk:17"}},
		{"the twin closed", ls(ac + "\nexport type Twin = lib.A | C\n"), nil},
		{"the twin an alias of int", ls(ac + "\nexport type Twin = int\n"), nil},
		{"the alias of int written open over lib.A | C", ls(ac + "\nexport open type Twin = lib.A | C\n"), []string{"E0306 main.mk:17"}},
		{"AC itself open, no twin", ls(strings.Replace(ac, "export type", "export open type", 1)), []string{"E0306 main.mk:17"}},
		{"AC closed again", ls(ac), nil},
		// A set that holds every member of an open alias is
		// open, so an open alias of part of ABC opens it, in lib.sets or in
		// lib, the two modules that declare its members.
		{"a closed ABC over lib's errors and lib.sets's C", map[string]string{"src/main.mk": onABC, "lib/sets/sets.mk": abc}, nil},
		{"an open alias of lib.A | C beside ABC", ls(abc + "\nexport open type Sub = lib.A | C\n"), []string{"E0306 main.mk:17"}},
		{"Sub closed", ls(abc + "\nexport type Sub = lib.A | C\n"), nil},
		{"lib's own open alias of A | B", map[string]string{"lib/lib.mk": lib + "\nexport open type LAB = A | B\n"}, []string{"E0306 main.mk:17", "E0306 main.mk:6"}},
		{"lib closed again", map[string]string{"lib/lib.mk": lib}, nil},
		{"src back", map[string]string{"src/main.mk": src}, nil},
		// The mark over another package's errors (another top directory).
		{"ext's open alias of lib's errors", map[string]string{"ext/ext.mk": "import lib\n\nexport open type X = lib.A | lib.B\n", "mid/mid.mk": "import lib\nimport ext\n\nexport error Mine\n\nexport type PErr = lib.QErr | Mine\n\nHidden(v: ext.X): int {\n    return 0\n}\n"}, []string{"E0706 ext.mk:3"}},
		{"mid without ext, ext closed", map[string]string{"mid/mid.mk": mid, "ext/ext.mk": "import lib\n\nexport type X = lib.A | lib.B\n"}, nil},
	}
	// The fresh cache holds std's check results alone: the package is
	// checked from nothing at every step.
	seed := stdSeedCheck(t, Config{})
	for i, st := range steps {
		writeFiles(t, dir, st.files)
		got := check(cache)
		fresh := check(seededCache(t, seed))
		if !slices.Equal(got, st.want) || !slices.Equal(fresh, st.want) {
			t.Fatalf("step %d (%s): cached %v, fresh %v, want %v", i, st.name, got, fresh, st.want)
		}
	}
}

// TestAPIDiffOpenTwinAdded: a set is open when it holds every member of
// an open alias, so an added open alias whose members all exist in the older
// release opens every set that holds them, a dependent's own spelling
// included, and mk apidiff calls it breaking, unless an open alias of the
// older release has only members among them (Again, a twin of the open
// Error, and Sup, which holds Error's members and Busy; not Sub, a strict
// subset of Error), and reported in an Experimental module. An open alias
// with a member of its own, and a closed alias over existing members, are
// additions. Growing an open set is always an addition: every set that holds
// the grown members held the older ones, and was open, so W grown by Busy,
// which the older release has, Named, which names W, and W1 grown into the
// open OB are additions.
func TestAPIDiffOpenTwinAdded(t *testing.T) {
	fsA := apiFsV1 + "\nexport error Busy\n\nexport type NB = NotFound | Busy\n"
	tlsA := apiTlsV1 + "\nexport error Bad\n\nexport error Worse\n\nexport type Closed = Bad | Worse\n"
	a := apiStd(t, "word", fsA, tlsA)
	b := apiStd(t, "word", fsA+"\nexport open type Twin = NotFound | Busy\n\nexport type Shut = NotFound | Denied\n\nexport error Gone\n\nexport open type Grown = NotFound | Gone\n\nexport open type Again = NotFound | Denied\n\nexport open type Sup = NotFound | Denied | Busy\n\nexport open type Sub = NotFound\n", tlsA+"\nexport open type Twin = Bad | Worse\n")
	got := stdChanges(t, a, b)
	for k, want := range map[string]Severity{
		"std.fs.Twin added, open over existing members":  Breaking,
		"std.fs.Shut added":                              Compatible,
		"std.fs.Gone added":                              Compatible,
		"std.fs.Grown added":                             Compatible,
		"std.fs.Again added":                             Compatible,
		"std.fs.Sup added":                               Compatible,
		"std.fs.Sub added, open over existing members":   Breaking,
		"std.tls.Twin added, open over existing members": Reported,
	} {
		if got[k] != want {
			t.Errorf("%s: %q, want %q (%v)", k, got[k], want, got)
		}
	}
	for _, k := range []string{"std.fs.Twin added", "std.fs.Again added, open over existing members", "std.fs.Sup added, open over existing members", "std.tls.Twin added"} {
		if _, ok := got[k]; ok {
			t.Errorf("unexpected %q (%v)", k, got)
		}
	}
	// Growth: W and W1 are open; W grows by Busy, which the older release
	// has, into NotFound | Denied | Busy, which All spells there, open
	// already since it holds W; W1 grows into NotFound | Busy, which OB has
	// open there.
	grow := apiFsV1 + "\nexport error Busy\n\nexport error Mine\n\nexport type All = NotFound | Denied | Busy\n\n@open\nexport type W = NotFound | Denied\n\nexport type Named = W | Mine\n\n@open\nexport type W1 = NotFound\n\n@open\nexport type OB = NotFound | Busy\n"
	grown := strings.NewReplacer("export type W = NotFound | Denied", "export type W = NotFound | Denied | Busy", "export type W1 = NotFound", "export type W1 = NotFound | Busy").Replace(grow)
	got = stdChanges(t, apiStd(t, "word", grow, apiTlsV1), apiStd(t, "word", grown, apiTlsV1))
	for k, want := range map[string]Severity{
		"std.fs.W members added":                             Compatible,
		"std.fs.Named members added to an open set it names": Compatible,
		"std.fs.W1 members added":                            Compatible,
	} {
		if got[k] != want {
			t.Errorf("growth: %s: %q, want %q (%v)", k, got[k], want, got)
		}
	}
	for k, sev := range got {
		if strings.Contains(k, "open over existing members") || sev == Breaking {
			t.Errorf("growth: unexpected %q, %q (%v)", k, sev, got)
		}
	}
}
