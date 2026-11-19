// Why: name folding and case forms decide which near-miss names resolve
// suggests, so they must be exact.
// What: tests of folding, case forms, recasing, the exact-then-folded rule,
// the synonym table and ImportsOf.
// How: table-driven checks of the helpers in names.go and synonym.go.

package resolve

import (
	"sort"
	"testing"
)

func TestFoldAndCaseForms(t *testing.T) {
	t.Parallel()
	folds := []struct{ in, want string }{
		{"MaxUsers", "maxusers"}, {"max_users", "maxusers"}, {"MAX_USERS", "maxusers"},
		{"x", "x"}, {"Some", "some"}, {"_", ""}, {"a_b_c", "abc"}, {"ok", "ok"},
	}
	for _, c := range folds {
		if got := Fold(c.in); got != c.want {
			t.Errorf("Fold(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	pascal := map[string]bool{"Point": true, "HTTPServer": true, "P2": true, "point": false, "Max_Users": false, "": false, "_X": false}
	for s, want := range pascal {
		if IsPascal(s) != want {
			t.Errorf("IsPascal(%q) != %v", s, want)
		}
	}
	snake := map[string]bool{"max_users": true, "x": true, "x2_y": true, "Max": false, "maxUsers": false, "": false, "_x": false}
	for s, want := range snake {
		if IsSnake(s) != want {
			t.Errorf("IsSnake(%q) != %v", s, want)
		}
	}
}

func TestE0207_RecaseHelpers(t *testing.T) {
	t.Parallel()
	snake := []struct{ in, want string }{
		{"MaxUsers", "max_users"}, {"HTTPServer", "http_server"}, {"maxUsers", "max_users"},
		{"Point", "point"}, {"X", "x"}, {"already_snake", "already_snake"}, {"Utf8Error", "utf8_error"},
		{"IOError", "io_error"}, {"A_B", "a_b"},
	}
	for _, c := range snake {
		if got := ToSnake(c.in); got != c.want {
			t.Errorf("ToSnake(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	pascal := []struct{ in, want string }{
		{"max_users", "MaxUsers"}, {"maxUsers", "MaxUsers"}, {"point", "Point"}, {"Point", "Point"}, {"a_b_c", "ABC"},
	}
	for _, c := range pascal {
		if got := ToPascal(c.in); got != c.want {
			t.Errorf("ToPascal(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range snake {
		if got := ToSnake(c.in); !IsSnake(got) {
			t.Errorf("ToSnake(%q) = %q is not snake_case", c.in, got)
		}
	}
	for _, c := range pascal {
		if got := ToPascal(c.in); !IsPascal(got) {
			t.Errorf("ToPascal(%q) = %q is not PascalCase", c.in, got)
		}
	}
}

func TestLookupExactBeforeFolded(t *testing.T) {
	t.Parallel()
	cands := []string{"Len", "len", "Push", "push_back", "PushBack"}
	if m := Lookup("len", cands); m.Exact != 1 || m.Folded != nil {
		t.Errorf("exact len: %+v", m)
	}
	if m := Lookup("LEN", cands); m.Exact != -1 || len(m.Folded) != 2 {
		t.Errorf("folded LEN: %+v", m)
	}
	if m := Lookup("push", cands); m.Exact != -1 || len(m.Folded) != 1 || m.Folded[0] != 2 {
		t.Errorf("folded push: %+v", m)
	}
	if m := Lookup("pushback", cands); m.Exact != -1 || len(m.Folded) != 2 {
		t.Errorf("folded pushback: %+v", m)
	}
	if m := Lookup("Pop", cands); m.Exact != -1 || len(m.Folded) != 0 {
		t.Errorf("miss: %+v", m)
	}
}

func TestSynonymTableSorted(t *testing.T) {
	t.Parallel()
	s := Synonyms()
	if !sort.SliceIsSorted(s, func(i, j int) bool { return s[i].Foreign < s[j].Foreign }) {
		t.Error("Synonyms not sorted by Foreign")
	}
	names := map[string]string{"Vec": "List", "String": "Text", "HashMap": "Map", "usize": "int", "Rc": "sync.Shared", "Arc": "sync.Shared", "fmt.Println": "Print"}
	for f, mk := range names {
		row, ok := synonymFor(f)
		if !ok || row.MkName != mk || row.Member {
			t.Errorf("row %s: %+v %v", f, row, ok)
		}
	}
	for _, r := range s {
		if r.Member != (r.Foreign[0] == '.') {
			t.Errorf("row %s: Member %v", r.Foreign, r.Member)
		}
		if (r.MkName == "sync.Shared") != (r.Import == "import std.sync") {
			t.Errorf("row %s: import %q", r.Foreign, r.Import)
		}
	}
	if _, ok := synonymFor(".unwrap"); ok {
		t.Error("member row returned as a name row")
	}
	if _, ok := synonymFor("Box"); ok {
		t.Error("Box is a prelude name (S-C40), not a synonym")
	}
}

func TestImportsOf(t *testing.T) {
	t.Parallel()
	p := parseOne(t, "import std.fs\nimport t = std.time\nimport a.b.c\n\nF() {}\n")
	got := ImportsOf(p.files[0], p.in)
	want := []struct{ bind, path string }{{"fs", "std.fs"}, {"t", "std.time"}, {"c", "a.b.c"}}
	if len(got) != len(want) {
		t.Fatalf("got %d imports", len(got))
	}
	for i, w := range want {
		if p.in.Str(got[i].Binding) != w.bind || got[i].Path != w.path {
			t.Errorf("import %d: %s %s", i, p.in.Str(got[i].Binding), got[i].Path)
		}
		if got[i].Node == 0 || !got[i].At.IsValid() || !got[i].PathSpan.IsValid() {
			t.Errorf("import %d: missing node or spans", i)
		}
	}
}
