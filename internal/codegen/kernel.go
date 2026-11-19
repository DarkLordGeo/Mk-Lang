// Why: kernel and freestanding code shares globals with C and linker
// scripts by name, places data in named sections, and must keep SSE
// registers out of code that runs before the FPU state is saved.
//
// What: symbolName, externGlobal, defineExternGlobal (extern and export
// extern vars), sectionConst (a const with a section clause), groFeatures,
// groAttrs, withFeatures and featuresOf (general-regs-only builds and cpu
// clauses).
//
// How: an extern var is an external global named by its own name with no
// initializer; a const with a section clause is a named constant global in
// that section, kept in @llvm.used whether or not code reads it. (A var's
// section is set where it is defined, aggregate.go's defineGlobal; a
// function's in func.go.)

package codegen

import (
	"strings"

	"mk/internal/llvm"
	"mk/internal/source"
	"mk/internal/types"
)

// symbolName is an item's own name, the last part of its key: the C name
// an extern var's symbol has.
func symbolName(k types.Key) string {
	s := k.String()
	if i := strings.LastIndexAny(s, ".:"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// externGlobal declares extern var item: an external global @name of T's
// memory size, default visibility, no initializer. Every pack that
// reads it declares it; none defines it.
func (g *packGen) externGlobal(item types.ItemID, d *types.GlobalDecl) llvm.Value {
	if v, ok := g.globals[item]; ok {
		return v
	}
	r := g.reprOf(d.Type)
	v := g.m.GlobalRef(g.m.AddGlobal(symbolName(g.items.Get(item).Key), llvm.GlobalAttrs{Linkage: llvm.External,
		Visibility: llvm.Default, Align: r.align, Type: g.m.Array(max(r.size, 1), g.m.I(8))}))
	g.globals[item] = v
	return v
}

// unitGlobal is item's entry in the unit's Globals, if the driver listed it.
func (g *packGen) unitGlobal(item types.ItemID) (Global, bool) {
	for _, gl := range g.u.Globals {
		if gl.Item == item {
			return gl, true
		}
	}
	return Global{}, false
}

// defineExternGlobal defines export extern var item in its pack
// (Global.Define): @name, its C symbol, zero-initialized, of T's memory size
// and alignment, external with default visibility (protected on ELF: a .so
// binds its own references to it, -Bsymbolic, so a host's copy relocation
// would split it silently; protected makes the linker refuse that host and
// a GOT host shares the one definition), never internal and
// never moved to a padded static section, and kept in @llvm.used, so
// neither the optimizer nor ThinLTO's internalization nor --gc-sections
// drops it or its stores: a debugger or C code reads it unseen. Other packs
// declare it (externGlobal), with the same name and type.
func (g *packGen) defineExternGlobal(item types.ItemID, d *types.GlobalDecl) llvm.Value {
	if v, ok := g.globals[item]; ok {
		return v
	}
	r := g.reprOf(d.Type)
	t := g.m.Array(max(r.size, 1), g.m.I(8))
	vis := llvm.Protected
	if os := g.cfg.Target.OS; os == types.OSWindows || os == types.OSMacos {
		vis = llvm.Default
	}
	v := g.m.GlobalRef(g.m.AddGlobal(symbolName(g.items.Get(item).Key), llvm.GlobalAttrs{Linkage: llvm.External,
		Visibility: vis, DSOLocal: true, Align: r.align, Type: t, Init: g.m.ZeroInit(t)}))
	g.m.Used(v)
	g.globals[item] = v
	return v
}

// sectionConst defines const item with a section clause once in this pack:
// a constant global "mk.<key>" holding its value, in its section, external
// (a linker script may name it) and in @llvm.used (it stays linked, read or
// not). A const with no section is no global of its own.
func (g *packGen) sectionConst(item types.ItemID, d *types.ConstDecl) {
	if d.Section == "" {
		return
	}
	if _, ok := g.globals[item]; ok {
		return
	}
	it := g.items.Get(item)
	if !d.HasValue {
		source.ICEf(it.Span, "codegen: section const %s has no value", it.Key)
	}
	cv := g.env.Consts().Get(d.Value)
	init := g.constInit(cv)
	r := g.reprOf(d.Type)
	vis := llvm.Hidden
	if g.cfg.Target.OS == types.OSWindows {
		vis = llvm.Default
	}
	gl := g.m.AddGlobal("mk."+it.Key.String(), llvm.GlobalAttrs{Linkage: llvm.External, Visibility: vis, DSOLocal: true,
		Constant: true, Align: max(r.align, cv.Align, 1), Type: init.Ty, Init: init, Section: d.Section})
	v := g.m.GlobalRef(gl)
	g.m.Used(v)
	g.globals[item] = v
}

// groFeatures is the general-regs-only feature string: what
// clang -### --target=x86_64-unknown-none-elf -mgeneral-regs-only passes
// (clang 21.1.8: exactly
// -target-feature -x87 -target-feature -mmx -target-feature -sse; LLVM's
// -sse turns off sse2 and everything that implies sse with it).
// TestGeneralRegsOnlyString pins it against the clang on PATH.
const groFeatures = "-x87,-mmx,-sse"

// groAttrs adds "target-features"=groFeatures to a in a general-regs-only
// bin. Every function codegen defines starts from it (the
// fnAttrs of packGen and of the support pack): the support pack's memcpy,
// memmove and memset and every emitted helper (an s.go thunk, a wrapper
// taken outside a function, mk.cpu.detect, the support intrinsics' bodies)
// get no SSE register, as the reference promises for everything the bin
// reaches (without it, memcpy, memmove and memset were vectorized with
// movups). A function with a cpu clause replaces it (withFeatures).
func groAttrs(a llvm.Attrs, cfg *Config) llvm.Attrs {
	if cfg.GeneralRegsOnly {
		return a.Str("target-features", groFeatures)
	}
	return a
}

// withFeatures is a with its "target-features" replaced by tf.
func withFeatures(a llvm.Attrs, tf string) llvm.Attrs {
	return a.Without("target-features").Str("target-features", tf)
}

// featuresOf is Funcs[fi]'s "target-features": its feature set
// (its cpu clause's closure and what it inherits); in a
// general-regs-only bin, groFeatures, or for a function whose set holds
// sse2 (it saves and restores the FPU state itself) that string with its
// set's +sse, +sse2 and further features in place of -sse.
func (g *packGen) featuresOf(fi int) string {
	tf := g.targetFeatures(g.cpuOf(fi))
	if !g.cfg.GeneralRegsOnly {
		return tf
	}
	if strings.Contains(","+tf+",", ",+sse2,") {
		return "-x87,-mmx," + tf
	}
	return groFeatures
}
