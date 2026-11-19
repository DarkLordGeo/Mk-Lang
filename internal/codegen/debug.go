// Why: every build carries line tables, so a debugger, a profiler and a
// panic trace can map code to source lines; the paths must not depend on
// where the build ran.
//
// What: debugUnit, diFile, lineCol, subprogram, setLoc and enter.
//
// How: one DICompileUnit per pack, a DIFile per source file with the
// package-relative path and directory ".", a DISubprogram per function, a
// DILocation on every instruction of a function, calls included. Debug Info
// Version 3, Dwarf Version 4 on ELF, CodeView 1 on COFF. Full debug info is
// debug_full.go and dityp.go.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// debugUnit creates the pack's compile unit and module flags. The
// unit's file is the pack name, which is also source_filename.
func (g *packGen) debugUnit(name string) {
	md := g.m.Metadata()
	md.DebugFlags(g.cfg.Target.OS == types.OSWindows)
	if g.cfg.Debug {
		g.dbgCU = md.DICompileUnitFull(md.DIFile(name, "."), g.cfg.Profile.Optimized())
		g.dbgTypes, g.dbgNS = map[types.TypeID]llvm.MD{}, map[string]llvm.MD{}
		return
	}
	g.dbgCU = md.DICompileUnit(md.DIFile(name, "."), g.cfg.Profile.Optimized())
}

// diFile is the DIFile of a source file's package-relative path, one per
// path per pack.
func (g *packGen) diFile(path string) llvm.MD {
	if f, ok := g.dbgFiles[path]; ok {
		return f
	}
	f := g.m.Metadata().DIFile(path, ".")
	g.dbgFiles[path] = f
	return f
}

// lineCol is the 1-based line and column of a span's start, and the
// span's file path; zero when the span names no file.
func (g *packGen) lineCol(sp source.Span) (string, int, int) {
	if !sp.File.IsValid() {
		return "", 0, 0
	}
	lc := g.env.LineCol(source.Pos{File: sp.File, Off: sp.Start})
	return g.env.Path(sp.File), int(lc.Line), int(lc.Col)
}

// subprogram attaches the DISubprogram of a function definition named key
// (the readable name) with symbol, at span sp (the prologue takes the
// function's span).
func (fg *fnGen) subprogram(key string, sp source.Span) {
	g := fg.g
	if g.dbgCU.IsZero() {
		return
	}
	path, line, _ := g.lineCol(sp)
	file := g.diFile(path)
	fg.dbgSP = g.m.Metadata().DISubprogram(key, fg.fn.Name(), file, line, g.dbgCU, g.cfg.Profile.Optimized())
	fg.fn.SetDbg(fg.dbgSP)
}

// setLoc makes span sp current: run-time checks take its site, and every
// later instruction of the current block, and of every block created while
// it is current, takes its DILocation.
func (fg *fnGen) setLoc(sp mir.SpanID) {
	fg.span = sp
	if fg.dbgSP.IsZero() {
		return
	}
	_, line, col := fg.g.lineCol(fg.spanOf(sp))
	scope := fg.dbgSP
	if !fg.dbgScope.IsZero() {
		scope = fg.dbgScope
	}
	fg.dbgLoc = fg.g.m.Metadata().DILocation(line, col, scope)
	if fg.cur != nil {
		fg.cur.SetDebugLoc(fg.dbgLoc)
	}
}

// enter makes b the current block under the current location. With
// Config.Debug the MIR block being entered (fg.bi) sets the scope of the
// locations that follow.
func (fg *fnGen) enter(b *llvm.Block) {
	fg.cur = b
	if fg.dbgScopes != nil && fg.bi < len(fg.dbgScopes) {
		fg.dbgScope = fg.dbgScopes[fg.bi]
	}
	if !fg.dbgLoc.IsZero() {
		b.SetDebugLoc(fg.dbgLoc)
	}
}
