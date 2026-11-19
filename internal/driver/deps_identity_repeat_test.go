// Why: a manifest may list one package twice under different spellings of
// its path, and the dependency walk compares identities, not text.
// What: TestDep_RepeatSameIdentityAccepted.
// How: checks manifests with a repeated Dep, accepted, and one name for
// two packages, refused.

package driver

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDep_RepeatSameIdentityAccepted(t *testing.T) {
	spellings := []string{"../util/", "./../util", "../util/."}
	if runtime.GOOS == "windows" {
		spellings = append(spellings, "../Util")
	}
	for _, sp := range spellings {
		t.Run(sp, func(t *testing.T) {
			ws := workspace(t, []string{pathDep("util", "../util"), pathDep("util", sp)}, nil)
			clean(t, depCheck(t, filepath.Join(ws, "app"), Config{}))
		})
	}
	// One name for two packages in one manifest stays refused.
	ws := workspace(t, []string{pathDep("util", "../util"), pathDep("util", "../other")},
		map[string]string{"other/pkg.mk": depManifest("other")})
	e0202(t, depCheck(t, filepath.Join(ws, "app"), Config{}), "invalid", "pkg.mk:")
}
