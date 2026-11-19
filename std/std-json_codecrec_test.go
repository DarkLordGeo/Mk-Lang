// Why: a decoded record must outlive the parsed Value it came from, even when
// it holds strings inside a List, a slice or an optional.
// What: TestSTDJSON_CodecListOfRecord.
// How: the generated FromJson copies every string into its arena and is
// declared `from a`, so the record carries no loan of the Value.

package std

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSTDJSON_CodecListOfRecord round-trips a List, a slice, an optional
// List and a direct field of a record holding a str. Load parses into a
// local Value and returns the record `from a` alone, which only a FromJson
// whose result holds no loan of v allows. Under `from a, v` the List of
// Item was E0905 wrapping E0604 ("stores a loan of a into v").
func TestSTDJSON_CodecListOfRecord(t *testing.T) {
	src := `import js = std.json

struct Item {
    export name: str
    export n: i64
}

struct Bag {
    export items: List[Item]
    export more: []Item
    export opt: ?List[Item]
    export first: Item
}

gen js.Codec(Item)
gen js.Codec(Bag)

Load(a: &Arena, text: str into a): js.Error!Bag from a {
    v := try js.Parse(a, text)
    return try Bag.FromJson(a, &v)
}

export Main(sys: &System): js.Error|WriteError!void {
    b := try Load(sys.arena, "{\"items\":[{\"name\":\"a\",\"n\":1},{\"name\":\"b\\\"q\",\"n\":2}],\"more\":[{\"name\":\"\",\"n\":3}],\"opt\":[{\"name\":\"\\u00e9\",\"n\":4}],\"first\":{\"name\":\"f\",\"n\":5}}")
    var out := sys.Stdout()
    try b.FormatJson(mut out)
}
`
	root := t.TempDir()
	stdDir := jsonStd(t, root, nil)
	pkgDir := filepath.Join(root, "p")
	files := map[string]string{
		"main.mk": src,
		"pkg.mk": "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n" +
			"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n",
	}
	for p, text := range files {
		dst := filepath.Join(pkgDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	exe := filepath.Join(root, "p.exe")
	out, status := mkCmd(t, pkgDir, stdDir, "build", "-o", exe, ".")
	if status != 0 {
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	r := runNative(t, exe, "")
	wantRun(t, r, 0, `{"items":[{"name":"a","n":1},{"name":"b\"q","n":2}],"more":[{"name":"","n":3}],"opt":[{"name":"é","n":4}],"first":{"name":"f","n":5}}`)
}
