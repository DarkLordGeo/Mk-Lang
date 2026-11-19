// Why: two release variants need no new profile: a build for size, and a
// release build that keeps its symbols for profilers and debuggers.
//
// What: the size profile (mk build --profile size, Config.Size) and release
// with symbols (mk build --release --symbols, Config.Symbols), read by
// profileCompile and linkKeepsSymbols, which native_steps.go's packJob and
// native.go's linkJob call.
//
// How: the size profile is a release build (Profile is Release: release
// semantics and checks) whose every pack, the support pack included,
// compiles at -Oz; the Mk packs stay ThinLTO bitcode and the link is
// release's. lld has no size level for its LTO backend (--lto-O0 to O3
// only), so the link's LTO keeps release's level: on hello world, -Oz
// bitcode under the release link is smaller than release, and smaller than
// -Oz native packs with no LTO. Release with symbols changes no object:
// every build already emits line tables, and the link keeps the symbol table
// and writes the debug information (the PDB on Windows, DWARF kept on Linux)
// exactly as a --debug link does, while codegen's full debug info and its
// effects on optimization (no leaf inlining, loopversion.go; no
// small-function set, PackFacts) stay off. Both reach the cache keys through
// the jobs: the compile level is in objKey and the pack prekey, the link
// job's Debug in the link key.

package driver

import "mk/internal/toolchain"

// profileCompile is a pack's compile level and LTO under the profile:
// -Oz for the size profile, with compileOpts' LTO, else what compileOpts
// chose.
func (w *world) profileCompile(opt toolchain.OptLevel, lto bool) (toolchain.OptLevel, bool) {
	if w.cfg.Size {
		return toolchain.Oz, lto
	}
	return opt, lto
}

// linkKeepsSymbols is the link job's Debug: --debug, or --symbols.
func (w *world) linkKeepsSymbols() bool { return w.cfg.Debug || w.cfg.Symbols }
