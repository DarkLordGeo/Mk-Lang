// Why: mk upgrade must install only releases signed by the project, so the
// trusted public keys are compiled into mk.
// What: trusted and Trusted, the keys mk accepts for a release's
// SHA256SUMS.sig.
// How: the current key and, during a rotation, the next one; the test key is
// never listed, and an empty list fails every verification closed.

package release

// trusted is the current release key and, optionally, the next one (a release
// signed with the current key may introduce the next; mk upgrade accepts
// either). It is empty until the release maintainer runs `go run
// ./tools/release keygen -out <file outside the repository>` and pastes the
// printed line here. With it empty, every verification fails closed
// (ErrNoKeys). The test key is never listed here (TestTrustedKeys).
var trusted = []Key{}

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
