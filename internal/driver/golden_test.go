// Why: the --json diagnostic object is a public format; doc/tools.md shows
// an example, and the driver must emit that object for that program.
// What: TestExampleObject, with golden testdata/e0501-example.golden.
// How: checks testdata/e0501-example.mk, compares the whole report with the
// golden, and checks the object's key properties on their own.

package driver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestExampleObject: doc/tools.md's example program (testdata/
// e0501-example.mk, placed as src/lex.mk with the example's lines: Next's
// signature on line 12, the loan on 40, the conflict at 41:5, the use on
// 43) gives one E0501 whose fixes are [] (there is no clause synthesis).
// The whole report is a golden; its key properties are checked on their
// own so that a golden refresh cannot drop them. The remaining differences
// from the documented object (message, kept_live_by, the signature named,
// alternatives) are the emitter's.
func TestExampleObject(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "e0501-example.mk"))
	if err != nil {
		t.Fatal(err)
	}
	root := writeTree(t, pkgFiles(map[string]string{"src/lex.mk": string(src)}))
	r := check(t, Config{Root: root})
	got := r.ReportJSON() // the --json document, version first

	var rep struct {
		Version     int    `json:"version"`
		Phase       string `json:"phase"`
		Diagnostics []struct {
			Code    string            `json:"code"`
			Name    string            `json:"name"`
			Phase   string            `json:"phase"`
			Root    int               `json:"root"`
			At      map[string]any    `json:"at"`
			Facts   map[string]any    `json:"facts"`
			Fixes   []json.RawMessage `json:"fixes"`
			Explain string            `json:"explain"`
		} `json:"diagnostics"`
		Omitted int `json:"omitted"`
	}
	if err := json.Unmarshal(got, &rep); err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if rep.Version != 1 || !bytes.HasPrefix(got, []byte(`{"version":1,"phase":`)) {
		t.Errorf("the object does not begin with \"version\": 1:\n%s", got)
	}
	if rep.Phase != "flow" || len(rep.Diagnostics) != 1 || rep.Omitted != 0 {
		t.Fatalf("want one flow diagnostic, got\n%s", got)
	}
	d := rep.Diagnostics[0]
	if d.Code != "E0501" || d.Name != "exclusive-conflict" || d.Phase != "flow" || d.Root != 1 ||
		d.Explain != "mk explain E0501" {
		t.Errorf("code %s name %s phase %s root %d explain %q", d.Code, d.Name, d.Phase, d.Root, d.Explain)
	}
	if d.At["file"] != "src/lex.mk" || d.At["line"] != float64(41) || d.At["col"] != float64(5) {
		t.Errorf("at %v, want src/lex.mk:41:5", d.At)
	}
	if d.Fixes == nil || len(d.Fixes) != 0 || !bytes.Contains(got, []byte(`"fixes":[]`)) {
		t.Errorf(`fixes %v, want "fixes": [] (DG177)`, d.Fixes)
	}
	if d.Facts["holder"] != "a" || d.Facts["loan"] == nil || d.Facts["signature"] == nil {
		t.Errorf("facts %v, want loan, holder a and signature", d.Facts)
	}

	golden := filepath.Join("testdata", "e0501-example.golden")
	if *update {
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test -run TestExampleObject -update and read the result)", err)
	}
	if !bytes.Equal(append(got, '\n'), want) {
		t.Errorf("report differs from %s:\n got %s\nwant %s", golden, got, want)
	}
}
