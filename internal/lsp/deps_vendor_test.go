// Why: a vendor/index.txt id is joined into a path, so a bad id must be refused.
// What: TestLSPVendorIndexChecked, TestLSPVendorIndexSourceLine.
// How: writes index files with valid and invalid ids and checks what
// vendorIndexOf accepts.

package lsp

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestLSPVendorIndexChecked(t *testing.T) {
	sum := sha256.Sum256([]byte("../util"))
	good := "util_" + hex.EncodeToString(sum[:4])
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := "tree-sha256:" + hex.EncodeToString(make([]byte, 32))
	index := "../util - " + good + " " + h + "\n" +
		"../evil - .. " + h + "\n" +
		"../std - std " + h + "\n" +
		"../stale - stale_00000000 " + h + "\n"
	if err := os.WriteFile(filepath.Join(dir, "vendor", "index.txt"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	got := vendorIndexOf(dir)
	if len(got) != 1 || got["../util"] != good {
		t.Errorf("index %v, want only ../util -> %s", got, good)
	}
}

// A 1.3 source package's index line,
// `<source> v<version> <canonical id> tree-sha256:`, whose id hashes
// "<source>@v<major>", is read; and a source dependency's canonical id is
// the driver's.
func TestLSPVendorIndexSourceLine(t *testing.T) {
	src := "example.com/someone/toml"
	sum := sha256.Sum256([]byte(src + "@v1"))
	good := "toml_" + hex.EncodeToString(sum[:4])
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := "tree-sha256:" + hex.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(filepath.Join(dir, "vendor", "index.txt"), []byte(src+" v1.2.0 "+good+" "+h+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := vendorIndexOf(dir); got[src+"@v1"] != good {
		t.Errorf("index %v, want %s@v1 -> %s", got, src, good)
	}
	if got := sourceCIDOf(src, "1.2.0"); got != good {
		t.Errorf("sourceCIDOf %q, want %q", got, good)
	}
}
