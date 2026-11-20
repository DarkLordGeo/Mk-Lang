// Why: a release mk that trusts no key can never install or upgrade
// anything, so a tag must not ship one.
// What: TestReleaseBuildHasKey.
// How: the release workflow runs it first, with the release address set by
// -ldflags as in the release build of mk; in any other build it passes.

package release

import "testing"

// TestReleaseBuildHasKey fails a release build (one with a compiled-in
// release address) while keys.go lists no key but the test key:
//
//	go test -ldflags "-X mk/internal/release.base=https://github.com/zerodayokay/Mk-Lang/releases" -run TestReleaseBuildHasKey ./internal/release
func TestReleaseBuildHasKey(t *testing.T) {
	if !ReleaseBuild() {
		t.Skip("SKIP: not a release build (no -X mk/internal/release.base)")
	}
	if len(Trusted()) == 0 {
		t.Fatal("internal/release/keys.go lists no release key: run `go run ./tools/release keygen -out <file>` and add the line it prints (see keys.go)")
	}
}
