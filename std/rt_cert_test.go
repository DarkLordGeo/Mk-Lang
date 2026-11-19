// Why: rt.CertVerify on Windows must trust what the platform verifier trusts;
// Go's crypto/x509 with no Roots calls that verifier, so it is the oracle.
// What: TestRt_CertRoots, which runs the RT8-Cert-Roots block
// (std/task/tests_cert.mk).
// How: each root Go verifies, as a one-certificate chain, is written to a file
// for the block, which must find every one trusted (verdict 0).

//go:build windows

package std

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rt8StoreRoots lists the DER of every certificate in the current user's
// ROOT store (the logical store, the machine's roots included), read by
// Windows PowerShell, so the test needs no unsafe import (archtest's
// TestNoUnsafe).
func rt8StoreRoots(t *testing.T) [][]byte {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`Get-ChildItem Cert:\CurrentUser\Root | ForEach-Object { [Convert]::ToBase64String($_.RawData) }`)
	// A PSModulePath inherited from PowerShell 7 hides Windows PowerShell's
	// own Cert: provider, so the child gets the system's default.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "PSMODULEPATH=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("reading the ROOT store through powershell.exe: %v: %s", err, stderr.String())
	}
	var all [][]byte
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		der, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			t.Fatalf("store line %q: %v", line, err)
		}
		all = append(all, der)
	}
	return all
}

// TestRt_CertRoots: crypto/x509's Verify with no Roots on Windows calls
// CertGetCertificateChain and the SSL policy itself. Every root of the current
// user's ROOT store that Go verifies for server authentication now, as its own
// one-certificate chain, is written to a file in rt.CertVerify's record form,
// and the RT8-Cert-Roots block must find each one trusted (verdict 0).
func TestRt_CertRoots(t *testing.T) {
	rtsNative(t)
	var out []byte
	kept := 0
	for _, der := range rt8StoreRoots(t) {
		if kept >= 20 {
			break
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			continue
		}
		if _, err := c.Verify(x509.VerifyOptions{CurrentTime: time.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			continue
		}
		if len(out)+4+len(der) > 16384 { // the block's buffer
			break
		}
		out = binary.LittleEndian.AppendUint32(out, uint32(len(der)))
		out = append(out, der...)
		kept++
	}
	if kept == 0 {
		t.Skip("no root of this user's store verifies for server authentication under Go")
	}
	file := filepath.Join(t.TempDir(), "roots.bin")
	if err := os.WriteFile(file, out, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d roots, %d bytes", kept, len(out))
	for _, profile := range []string{"dev", "release"} {
		exe, all := rtsBuild(t, profile)
		found := false
		for _, b := range all {
			if !strings.HasPrefix(b.name, "RT8-Cert-Roots") {
				continue
			}
			found = true
			r := rtsRun(t, exe, b.index, 120*time.Second, "MK_RT8_ROOTS="+file)
			rtsJudge(t, b, r, "")
		}
		if !found {
			t.Fatal("no RT8-Cert-Roots block")
		}
	}
}
