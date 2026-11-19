// Why: the std-crypto2 bench rows must do the same work on every side and
// label their differences, or their numbers mislead.
// What: checks of the rows in tests/bench-lanes/std-crypto2, the twin of
// std-crypto_benchrows_test.go: work per column, DER signatures, notes.
// How: reads each row's bench.json and column sources and checks them against
// what the row's notes and Go's source say.

package std

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cr2BenchDir = "../tests/bench-lanes/std-crypto2"

func cr2LoadRow(t *testing.T, dir string) cryptoBenchRow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cr2BenchDir, dir, "bench.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r cryptoBenchRow
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	return r
}

func cr2RowSource(t *testing.T, dir, lang string, r cryptoBenchRow) string {
	t.Helper()
	p := filepath.Join(cr2BenchDir, dir, r.Sources[lang])
	if lang == "mk" {
		p = filepath.Join(p, r.Name+".mk")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSTDCRYPTO_TlsBenchRowsStateWorkPerColumn: every row has a note that
// states the work per step and names each column's call, and every
// language is a column or a labelled skip; nine rows.
func TestSTDCRYPTO_TlsBenchRowsStateWorkPerColumn(t *testing.T) {
	ents, err := os.ReadDir(cr2BenchDir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{"mk": "Mk", "zig": "Zig", "go": "Go"}
	rows := 0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		rows++
		r := cr2LoadRow(t, e.Name())
		work := ""
		for _, n := range r.Notes {
			if strings.HasPrefix(n, "Work per step:") {
				work = n
			}
		}
		if work == "" {
			t.Errorf("%s: no note states the work per step", e.Name())
			continue
		}
		for lang, word := range names {
			if _, ok := r.Sources[lang]; ok && !strings.Contains(work, word) {
				t.Errorf("%s: the work note does not name the %s column's call", e.Name(), lang)
			}
		}
		for _, lang := range []string{"c", "rust", "zig", "go"} {
			_, col := r.Sources[lang]
			why, skip := r.Skip[lang]
			if col == skip || (skip && why == "") {
				t.Errorf("%s: %s must be a column or a skip with a reason, not both or neither", e.Name(), lang)
			}
		}
	}
	if rows != 9 {
		t.Errorf("%d rows, want 9 (aesgcm128, aesgcm256-short, hkdf256, hmac384, p256-ecdh, p256-sign, p256-verify, p384-verify, rsa2048-verify)", rows)
	}
}

// TestSTDCRYPTO_SignatureRowsDerEverywhere: the ECDSA rows sign and
// verify DER in every column, so no column does an encoding the others
// skip: Mk names SigFormat.Der and never Fixed; Go neither imports
// encoding/asn1 nor math/big, and verifies with VerifyASN1, not the
// big.Int ecdsa.Verify (which re-encodes r and s to DER); Zig reads the
// signature with Signature.fromDer.
func TestSTDCRYPTO_SignatureRowsDerEverywhere(t *testing.T) {
	for _, dir := range []string{"p256-sign", "p256-verify", "p384-verify"} {
		r := cr2LoadRow(t, dir)
		mk := cr2RowSource(t, dir, "mk", r)
		if !strings.Contains(mk, "SigFormat.Der") || strings.Contains(mk, "SigFormat.Fixed") {
			t.Errorf("%s: the Mk column is not DER only", dir)
		}
		gosrc := cr2RowSource(t, dir, "go", r)
		for _, bad := range []string{`"encoding/asn1"`, `"math/big"`, "ecdsa.Verify("} {
			if strings.Contains(gosrc, bad) {
				t.Errorf("%s: the Go column uses %s", dir, bad)
			}
		}
		if strings.HasSuffix(dir, "verify") && !strings.Contains(gosrc, "ecdsa.VerifyASN1(") {
			t.Errorf("%s: the Go column does not verify with VerifyASN1", dir)
		}
		if _, ok := r.Sources["zig"]; ok && !strings.Contains(cr2RowSource(t, dir, "zig", r), "Signature.fromDer(") {
			t.Errorf("%s: the Zig column does not read DER", dir)
		}
		if !strings.Contains(r.Notes[0]+strings.Join(r.Notes, " "), "DER") {
			t.Errorf("%s: no note says the row is DER", dir)
		}
	}
}

// TestSTDCRYPTO_BenchNotesMatchGoSource: the two claims about Go's
// work that Go 1.27.3's source contradicts are gone (Go makes R^2 per RSA
// verification; Go's deterministic ECDSA signing does not recompute the
// public point per call), and the corrected statements are there.
func TestSTDCRYPTO_BenchNotesMatchGoSource(t *testing.T) {
	ents, err := os.ReadDir(cr2BenchDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		notes := strings.Join(cr2LoadRow(t, e.Name()).Notes, "\n")
		for _, wrong := range []string{"R^2 cached in the key", "which recomputes the public point"} {
			if strings.Contains(notes, wrong) {
				t.Errorf("%s: a note still says %q", e.Name(), wrong)
			}
		}
	}
	if n := strings.Join(cr2LoadRow(t, "rsa2048-verify").Notes, "\n"); !strings.Contains(n, "R^2 mod n made per verification too") {
		t.Errorf("rsa2048-verify: the note does not say Go makes R^2 per verification")
	}
	if n := strings.Join(cr2LoadRow(t, "p256-sign").Notes, "\n"); !strings.Contains(n, "privateKeyCache") || !strings.Contains(n, "no scalar multiplication") {
		t.Errorf("p256-sign: the note does not say Go caches the converted key")
	}
}
