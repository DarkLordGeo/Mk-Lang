// Why: profile-guided optimization needs two builds to agree on one
// profile file, and the profile must reach every cache key it affects.
//
// What: the driver half of PGO: DefaultProfile (default.mkprof in the
// package root, as Go's default.pgo) and the choice of profile for a build.
//
// How: a release build with --pgo-gen is instrumented
// (codegen.Config.PGOGen); one with --pgo=<file>, or with default.mkprof
// unless --pgo=off, is optimized with that profile (codegen.Config.PGO).
// Dev and test builds use no profile. The profile reaches every pack's IR
// (entry counts, branch weights, hot and cold), so the object key, which
// holds the IR, holds the profile too.

package driver

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"mk/internal/codegen"
	"mk/internal/diag"
	"mk/internal/pgo"
	"mk/internal/types"
)

// DefaultProfile is the profile a release build of a package uses unless
// --pgo names another or off (as Go's default.pgo).
const DefaultProfile = "default.mkprof"

// pgoConfig sets cfg's PGO fields for the build. A profile that cannot be
// read or is malformed is E0001 tool fs, naming the file.
func (w *world) pgoConfig(cfg *codegen.Config, pkgRoot string) *diag.Diagnostic {
	if w.cfg.Profile != types.Release {
		return nil
	}
	path := ""
	switch w.cfg.PGO {
	case PGOGen:
		cfg.PGOGen = true
		return nil
	case PGOOff:
		return nil
	case PGOUse:
		path = w.cfg.PGOFile
	case PGODefault:
		if pkgRoot == "" {
			return nil
		}
		path = filepath.Join(pkgRoot, DefaultProfile)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return diag.Toolchain("fs", "read "+filepath.ToSlash(path), pathErrText(err), "cannot read the profile "+filepath.ToSlash(path))
	}
	p, err := pgo.Read(bytes.NewReader(b))
	if err != nil {
		return diag.Toolchain("fs", "read "+filepath.ToSlash(path), err.Error(), "the profile "+filepath.ToSlash(path)+" is not a valid .mkprof")
	}
	cfg.PGO = p
	return nil
}
