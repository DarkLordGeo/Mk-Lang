// Why: users must be able to tell a real release from a tampered one, with
// a key that never enters the repository.
//
// What: the keygen, sums, sign and verify subcommands: keygen, writeSums,
// signDir and verifyDir over a release directory's archives.
//
// How: SHA256SUMS lists each archive's SHA-256 and SHA256SUMS.sig is an
// Ed25519 signature of it; verify checks the signature against the trusted
// keys (internal/release/keys.go) and every archive against the list. The
// test key never signs a release, and keygen refuses a path in a git tree.

package main

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"mk/internal/release"
)

// runKeygen writes a new key pair. The private key goes to -out, which must
// not exist and must lie outside every git work tree (the release key is
// kept outside the repository, two copies offline); the public key is printed as
// the line to add to internal/release/keys.go.
func runKeygen(args []string, stdout, stderr io.Writer) int {
	fs := flags("keygen", stderr)
	out := fs.String("out", "", "the private key file to write (outside the repository)")
	if !parse(fs, args, stderr, "out") {
		return 2
	}
	pub, err := keygen(*out)
	if err != nil {
		return fail(stderr, "keygen", err)
	}
	fmt.Fprintf(stdout, "wrote the private key to %s; keep it outside the repository, two copies offline\n", *out)
	fmt.Fprintln(stdout, "add this line to trusted in internal/release/keys.go:")
	fmt.Fprintf(stdout, "\t{ID: %q, Public: %q},\n", pub.ID, pub.Public)
	fmt.Fprintln(stdout, "and this line to $MkTrustedKeys in tools/install/install.ps1 (in quotes) and trusted_keys in tools/install/install.sh:")
	fmt.Fprintf(stdout, "\t%s %s\n", pub.ID, pub.Public)
	return 0
}

func keygen(out string) (release.Key, error) {
	p, err := filepath.Abs(out)
	if err != nil {
		return release.Key{}, err
	}
	if wt := gitWorkTree(filepath.Dir(p)); wt != "" {
		return release.Key{}, fmt.Errorf("%s lies in the git work tree %s; write the private key outside every repository", p, wt)
	}
	priv, pub, err := release.GenerateKey()
	if err != nil {
		return release.Key{}, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return release.Key{}, err
	}
	if _, err := f.Write(priv); err != nil {
		f.Close()
		return release.Key{}, err
	}
	return pub, f.Close()
}

// gitWorkTree is the first directory at or above dir holding .git, or "".
func gitWorkTree(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// archives lists the release archives in dir (names ParseArchiveName
// reads), sorted.
func archives(dir string) ([]string, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, de := range des {
		if _, _, ok := release.ParseArchiveName(de.Name()); ok && de.Type().IsRegular() {
			names = append(names, de.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func runSums(args []string, stdout, stderr io.Writer) int {
	fs := flags("sums", stderr)
	dir := fs.String("dir", "", "the release directory")
	if !parse(fs, args, stderr, "dir") {
		return 2
	}
	n, err := writeSums(*dir)
	if err != nil {
		return fail(stderr, "sums", err)
	}
	fmt.Fprintf(stdout, "wrote %s over %d archives\n", filepath.Join(*dir, release.SumsFile), n)
	return 0
}

func writeSums(dir string) (int, error) {
	names, err := archives(dir)
	if err != nil {
		return 0, err
	}
	if len(names) == 0 {
		return 0, errors.New("no release archive (mk-<version>-<host>.zip or .tar.gz) in " + dir)
	}
	sums := release.Sums{}
	for _, n := range names {
		if sums[n], err = release.SumFile(filepath.Join(dir, n)); err != nil {
			return 0, err
		}
	}
	return len(names), os.WriteFile(filepath.Join(dir, release.SumsFile), sums.Format(), 0o644)
}

func runSign(args []string, stdout, stderr io.Writer) int {
	fs := flags("sign", stderr)
	key := fs.String("key", "", "the private key file keygen wrote")
	dir := fs.String("dir", "", "the release directory holding SHA256SUMS")
	if !parse(fs, args, stderr, "key", "dir") {
		return 2
	}
	data, err := os.ReadFile(*key)
	if err != nil {
		return fail(stderr, "sign", err)
	}
	priv, err := release.ParsePrivate(data)
	if err != nil {
		return fail(stderr, "sign", err)
	}
	k, err := signDir(priv, *dir, false)
	if err != nil {
		return fail(stderr, "sign", err)
	}
	fmt.Fprintf(stdout, "wrote %s, signed by key %s\n", filepath.Join(*dir, release.SigFile), k.ID)
	return 0
}

// signDir writes dir's SHA256SUMS.sig. It refuses the test key unless
// allowTest, which only this tool's tests pass.
func signDir(priv ed25519.PrivateKey, dir string, allowTest bool) (release.Key, error) {
	k := release.PublicOf(priv)
	if release.IsTestKey(k) && !allowTest {
		return k, fmt.Errorf("key %s is the test key (internal/release/testdata), which signs no release", k.ID)
	}
	sums, err := os.ReadFile(filepath.Join(dir, release.SumsFile))
	if err != nil {
		return k, err
	}
	if _, err := release.ParseSums(sums); err != nil {
		return k, err
	}
	return k, os.WriteFile(filepath.Join(dir, release.SigFile), release.Sign(priv, sums), 0o644)
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flags("verify", stderr)
	dir := fs.String("dir", "", "the release directory")
	if !parse(fs, args, stderr, "dir") {
		return 2
	}
	k, n, err := verifyDir(*dir, release.Trusted())
	if err != nil {
		fmt.Fprintf(stderr, "release verify: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "ok: %s signed by key %s; %d archives match\n", release.SumsFile, k.ID, n)
	return 0
}

// verifyDir checks dir's SHA256SUMS.sig against keys and every archive in
// dir against SHA256SUMS: each listed file must exist with its hash, and
// every archive in dir must be listed. The test key is refused unless keys
// is a test's (Verify refuses it in a release build; verify passes the
// built-in keys, which never hold it).
func verifyDir(dir string, keys []release.Key) (release.Key, int, error) {
	sums, err := os.ReadFile(filepath.Join(dir, release.SumsFile))
	if err != nil {
		return release.Key{}, 0, err
	}
	sig, err := os.ReadFile(filepath.Join(dir, release.SigFile))
	if err != nil {
		return release.Key{}, 0, err
	}
	k, err := release.Verify(sums, sig, keys)
	if err != nil {
		return k, 0, err
	}
	listed, err := release.ParseSums(sums)
	if err != nil {
		return k, 0, err
	}
	var names []string
	for n := range listed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		got, err := release.SumFile(filepath.Join(dir, n))
		if err != nil {
			return k, 0, err
		}
		if got != listed[n] {
			return k, 0, fmt.Errorf("%s: SHA-256 %s, %s says %s", n, got, release.SumsFile, listed[n])
		}
	}
	present, err := archives(dir)
	if err != nil {
		return k, 0, err
	}
	for _, n := range present {
		if _, ok := listed[n]; !ok {
			return k, 0, fmt.Errorf("%s is not listed in %s", n, release.SumsFile)
		}
	}
	return k, len(names), nil
}
