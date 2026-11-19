// Why: the std-crypto bench rows must do the same work on every side and
// label their differences, or their numbers mislead.
// What: checks of the rows in tests/bench-lanes/std-crypto: work per column,
// X25519 ladders per step, Zig's verify and Equal rows, the random row.
// How: reads each row's bench.json and column sources; tools/bench's own
// contract test covers what all rows share.

package std

import (
	"crypto/ecdh"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cryptoBenchRow is the part of a bench.json these tests read.
type cryptoBenchRow struct {
	Name     string            `json:"name"`
	Args     []string          `json:"args"`
	Quick    []string          `json:"quick"`
	Checksum string            `json:"checksum"`
	QuickSum string            `json:"quick_checksum"`
	Sources  map[string]string `json:"sources"`
	Skip     map[string]string `json:"skip"`
	Notes    []string          `json:"notes"`
	Variants []struct {
		Name     string   `json:"name"`
		Args     []string `json:"args"`
		Quick    []string `json:"quick"`
		Checksum string   `json:"checksum"`
		QuickSum string   `json:"quick_checksum"`
	} `json:"variants"`
}

const cryptoBenchDir = "../tests/bench-lanes/std-crypto"

func cryptoLoadRow(t *testing.T, dir string) cryptoBenchRow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cryptoBenchDir, dir, "bench.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r cryptoBenchRow
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	return r
}

func cryptoRowSource(t *testing.T, dir, lang string, r cryptoBenchRow) string {
	t.Helper()
	p := filepath.Join(cryptoBenchDir, dir, r.Sources[lang])
	if lang == "mk" {
		p = filepath.Join(p, r.Name+".mk")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSTDCRYPTO_BenchRowsStateWorkPerColumn: every row's first note states
// the work per step and names each column's call (a difference in work is
// written where the reader of the table finds it),
// and every language is a column or a labelled skip.
func TestSTDCRYPTO_BenchRowsStateWorkPerColumn(t *testing.T) {
	ents, err := os.ReadDir(cryptoBenchDir)
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
		r := cryptoLoadRow(t, e.Name())
		if len(r.Notes) == 0 || !strings.HasPrefix(r.Notes[0], "Work per step:") {
			t.Errorf("%s: the first note does not state the work per step", e.Name())
			continue
		}
		for lang, word := range names {
			if _, ok := r.Sources[lang]; ok && !strings.Contains(r.Notes[0], word) {
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
	if rows != 8 {
		t.Errorf("%d rows, want 8 (hmac256, chacha20poly1305, x25519, ed25519-keygen, -sign, -verify, equal, random)", rows)
	}
}

// cryptoX25519Chain is the x25519 row's checksum by crypto/ecdh: with
// both, pub = X25519(k, 9) folded in as the row does; without, the chain
// of shared secrets alone (the row's older form).
func cryptoX25519Chain(t *testing.T, n int, seed uint64, both bool) string {
	t.Helper()
	k := make([]byte, 32)
	st := seed
	for w := 0; w < 4; w++ {
		st += 0x9e3779b97f4a7c15
		z := st
		z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
		z = (z ^ z>>27) * 0x94d049bb133111eb
		binary.LittleEndian.PutUint64(k[8*w:], z^z>>31)
	}
	u := make([]byte, 32)
	u[0] = 9
	c := ecdh.X25519()
	var acc uint64
	for i := 0; i < n; i++ {
		priv, err := c.NewPrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		if both {
			acc ^= binary.LittleEndian.Uint64(priv.PublicKey().Bytes()[:8])
		}
		pub, err := c.NewPublicKey(u)
		if err != nil {
			t.Fatal(err)
		}
		r, err := priv.ECDH(pub)
		if err != nil {
			t.Fatal(err)
		}
		u, k = k, r
	}
	return strconv.FormatUint(binary.LittleEndian.Uint64(k[:8])^acc, 10)
}

// TestSTDCRYPTO_X25519RowTwoLaddersPerStep: Go's crypto/ecdh runs a
// ladder in NewPrivateKey (the public key) and one in ECDH, and has no
// single-ladder form, so the row's task is the exchange step: two ladders
// on every side. The public key is in the checksum (the recorded quick and
// publish checksums are the crypto/ecdh model's, and differ from the
// shared-secret chain alone), so a column that skipped a ladder fails
// tools/bench verify; and the Mk and Zig sources call the two ladders.
func TestSTDCRYPTO_X25519RowTwoLaddersPerStep(t *testing.T) {
	r := cryptoLoadRow(t, "x25519")
	check := func(args []string, want string) {
		n, err1 := strconv.Atoi(args[0])
		seed, err2 := strconv.ParseUint(args[1], 10, 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("args %v", args)
		}
		got := cryptoX25519Chain(t, n, seed, true)
		if got != want {
			t.Errorf("args %v: the crypto/ecdh model gives %s, the row records %q", args, got, want)
		}
		if one := cryptoX25519Chain(t, n, seed, false); one == got {
			t.Errorf("args %v: the checksum does not depend on the public key", args)
		}
	}
	check(r.Quick, r.QuickSum)
	if !testing.Short() {
		check(r.Args, r.Checksum)
	}
	if !strings.Contains(r.Notes[0], "two X25519 ladders") {
		t.Error("the work note does not say two ladders per step")
	}
	mk := cryptoRowSource(t, "x25519", "mk", r)
	if strings.Count(mk, "crypto.X25519(") != 2 {
		t.Error("the Mk column does not call crypto.X25519 twice per step")
	}
	zig := cryptoRowSource(t, "x25519", "zig", r)
	if !strings.Contains(zig, "X25519.recoverPublicKey(k)") || !strings.Contains(zig, "X25519.scalarmult(k, u)") {
		t.Error("the Zig column does not run the base-point and the shared-secret ladders")
	}
	gosrc := cryptoRowSource(t, "x25519", "go", r)
	if !strings.Contains(gosrc, "priv.PublicKey().Bytes()") {
		t.Error("the Go column does not fold its public key into the checksum")
	}
}

// TestSTDCRYPTO_VerifyRowZigCofactorless: the Zig column of
// crypto-ed25519-verify uses the cofactorless Signature.verifyStrict, as
// Mk and Go verify, and the row labels what Zig still does more (it
// decodes R and refuses small-order A and R).
func TestSTDCRYPTO_VerifyRowZigCofactorless(t *testing.T) {
	r := cryptoLoadRow(t, "ed25519-verify")
	zig := cryptoRowSource(t, "ed25519-verify", "zig", r)
	if !strings.Contains(zig, "sig.verifyStrict(") || strings.Contains(zig, "sig.verify(") {
		t.Error("the Zig column must call Signature.verifyStrict, not the cofactored Signature.verify")
	}
	for _, w := range []string{"cofactorless", "decodes R", "identity A", "non-canonical R"} {
		if !strings.Contains(r.Notes[0], w) {
			t.Errorf("the work note does not label %q", w)
		}
	}
}

// TestSTDCRYPTO_EqualRowZig: Zig's std has a constant-time compare
// over runtime-length slices, timing_safe.compare, so the crypto-equal
// row has a Zig column through it (no "no standard facility" skip), and
// the row labels that it computes an ordering.
func TestSTDCRYPTO_EqualRowZig(t *testing.T) {
	r := cryptoLoadRow(t, "equal")
	if _, ok := r.Skip["zig"]; ok {
		t.Error("Zig is skipped, but timing_safe.compare is its standard facility")
	}
	if r.Sources["zig"] == "" {
		t.Fatal("no Zig column")
	}
	zig := cryptoRowSource(t, "equal", "zig", r)
	if !strings.Contains(zig, "std.crypto.timing_safe.compare(u8, a, b, .little) == .eq") {
		t.Error("the Zig column does not use timing_safe.compare")
	}
	if !strings.Contains(r.Notes[0], "ordering") {
		t.Error("the work note does not label that Zig computes an ordering")
	}
}

// TestSTDCRYPTO_RandomRow: crypto.Random has a row (32-byte draws for
// the cost per call, 64 KiB draws for the throughput) whose checksum is
// the bytes drawn, n * size, so every column agrees whatever the random
// bytes are; Mk calls crypto.Random, Go crypto/rand.Read, Zig Io.random.
func TestSTDCRYPTO_RandomRow(t *testing.T) {
	r := cryptoLoadRow(t, "random")
	got := map[string]bool{}
	for _, v := range r.Variants {
		got[v.Name] = true
		for _, p := range []struct {
			args []string
			sum  string
		}{{v.Args, v.Checksum}, {v.Quick, v.QuickSum}} {
			n, err1 := strconv.ParseUint(p.args[0], 10, 64)
			size, err2 := strconv.ParseUint(p.args[1], 10, 64)
			if err1 != nil || err2 != nil {
				t.Fatalf("variant %s: args %v", v.Name, p.args)
			}
			if want := strconv.FormatUint(n*size, 10); p.sum != want {
				t.Errorf("variant %s args %v: checksum %q, want n * size = %s", v.Name, p.args, p.sum, want)
			}
		}
	}
	if !got["32"] || !got["64k"] {
		t.Errorf("variants %v, want 32 and 64k", got)
	}
	for lang, call := range map[string]string{"mk": "crypto.Random(sys.io", "go": "rand.Read(buf)", "zig": "init.io.random(buf)"} {
		if !strings.Contains(cryptoRowSource(t, "random", lang, r), call) {
			t.Errorf("the %s column does not call %s", lang, call)
		}
	}
}
