// Why: the trapped call (internal/codegen/trapform.go) is right only for some
// builds, and the choice belongs to the driver, which knows the bin's kind,
// profile and flags.
//
// What: trapForm, which decides codegen's Config.TrapForm.
//
// How: on only for an x86_64-windows executable built optimized, without
// --debug and not general-regs-only; there a small function's cold call of a
// never function becomes a trap the runtime's vectored handler resumes into
// that call, so the function keeps no Win64 frame for it.

package driver

import "mk/internal/types"

// trapForm reports whether a bin's packs take the trapped form: an
// x86_64-windows executable built optimized (release, checked, size)
// without --debug and not general-regs-only. A library keeps the call form
// (its handler is installed by its start export, after code of its own has
// run, and removed at unload), as do dev builds, every other target and
// the TestHooks.NoTrapForm builds tests compare with.
func trapForm(c Config, lib, gro bool) bool {
	return c.Target.Name == types.X86_64Windows.Name && c.Profile.Optimized() && !c.Debug && !lib && !gro &&
		!c.TestHooks.NoTrapForm
}
