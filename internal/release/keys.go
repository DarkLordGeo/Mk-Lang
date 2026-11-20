// Why: mk upgrade must install only releases signed by the project, so the
// trusted public keys are compiled into mk.
// What: trusted and Trusted, the keys mk accepts for a release's
// SHA256SUMS.sig.
// How: the current key and, during a rotation, the next one; the test key is
// never listed, and an empty list fails every verification closed.

package release

// trusted is the current release key and, optionally, the next one (a release
// signed with the current key may introduce the next; mk upgrade accepts
// either). With it empty, every verification fails closed (ErrNoKeys), and
// TestReleaseBuildHasKey fails a release build, so no tag ships an mk that
// can never upgrade. The test key is never listed here (TestTrustedKeys).
//
// RELEASE KEY: the release maintainer fills this list once, before the
// first tag. Run, from the repository root,
//
//	go run ./tools/release keygen -out <file outside every repository>
//
// and paste the line it prints between the braces below. Put the same key
// in tools/install/install.ps1 ($MkTrustedKeys) and tools/install/install.sh
// (trusted_keys); TestInstallKeys checks that the three agree. Store the
// private key file as the secret MK_RELEASE_KEY of the GitHub environment
// "release", which admits only v* tags (release.yml's sign job is its only
// user), not as a repository secret, and keep two offline copies.
var trusted = []Key{
	{ID: "7f12cb260ba04bea", Public: "ykIH33Kzl3Uno3l9vL2v5oOUy/H0toVHc5giQVn4BC8="},
}

// Trusted is the keys built into this mk, never the test key.
func Trusted() []Key {
	var ks []Key
	for _, k := range trusted {
		if !IsTestKey(k) {
			ks = append(ks, k)
		}
	}
	return ks
}
