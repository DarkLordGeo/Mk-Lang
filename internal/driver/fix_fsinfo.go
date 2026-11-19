// Why: release 1.2 replaced fs.Info's fields is_dir and modified_unix_nanos
// with kind and modified; code that reads the old fields can be moved
// mechanically.
//
// What: the fs.Info round of `mk fix`: fsInfoFixes, with fsInfoItem,
// hasField, notRead, needsParens, importName and importEdit.
//
// How: a read of fs.Info's 1.1 fields becomes a read of their 1.2 form,
// `info.is_dir` `info.kind == fs.Kind.Dir` and `info.modified_unix_nanos`
// `info.modified.UnixNanos()`, which std/fs/meta.mk states they equal. The
// round runs while std still has the old fields beside the new (the
// package checks clean: the rewrite is kept when it still does) and after
// std removed them (each read is E0203: the rewrite is kept when the fix
// loop sees progress), so mk fix moves 1.1 code in either state. A file
// that reads is_dir and does not import std.fs gains the import. Writes
// (an Info literal, a destructure, an assignment to the field, a `&` or
// `mut` of it) are left: the old fields are no part of 1.2's Info, so such
// code needs the programmer, and a rewritten write would not check, which
// would cost the file's reads their rewrite too.

package driver

import (
	"sort"
	"strings"

	"mk/internal/ast"
	checker "mk/internal/check"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// fsInfoPhase is the round's name in FixRound.Phase and its fixes' Why.
const fsInfoPhase = "fs.Info"

// fsInfoWhy is the AppliedFix reason of the round's rewrite.
const fsInfoWhy = "fs.Info's 1.1 fields are read as kind and modified"

// fsInfoFixes are the round's candidates, one per file: every read of
// is_dir or modified_unix_nanos on a value of std.fs's Info.
func (s *Session) fsInfoFixes(res Result, ov *overlay) []fixCand {
	if s == nil || s.w == nil || s.w.in == nil {
		return nil
	}
	isDir, ok1 := s.w.in.Lookup("is_dir")
	nanos, ok2 := s.w.in.Lookup("modified_unix_nanos")
	if !ok1 && !ok2 {
		return nil
	}
	type fileEdits struct {
		edits  []diag.Edit
		at     source.Span
		file   *ast.File
		needFs bool
		// removed: std's Info no longer has the field, so the read is an
		// unknown member and the fix is reported as that diagnostic's.
		removed bool
	}
	byPath := map[string]*fileEdits{}
	s.unitBodies(ov, res, func(b *checker.Body, path, text string) {
		var stack []ast.Node // the ancestors of the node visit sees
		// isDirRead is fe's Info when fe reads is_dir of one.
		isDirRead := func(e ast.Expr) (*ast.FieldExpr, types.ItemID) {
			fe, ok := e.(*ast.FieldExpr)
			if !ok || !ok1 || fe.X == nil || fe.Name == nil || fe.Name.Name != isDir {
				return nil, types.NoItem
			}
			return fe, s.fsInfoItem(b.TypeOf(fe.X.ID()))
		}
		visit := func(n ast.Node) bool {
			if _, lit := n.(*ast.FnLit); lit && n != b.Node {
				return false // a literal is a unit of its own, typed in its own Body
			}
			var fe *ast.FieldExpr
			var info types.ItemID
			op, whole := " == ", ast.Node(nil)
			switch x := n.(type) {
			case *ast.UnaryExpr:
				// `!info.is_dir` reads best as `info.kind != fs.Kind.Dir`.
				if x.Op == ast.TokBang {
					if fe, info = isDirRead(x.X); info != types.NoItem {
						op, whole = " != ", x
					}
				}
			case *ast.FieldExpr:
				if fe, info = isDirRead(x); info == types.NoItem && ok2 && x.X != nil && x.Name != nil && x.Name.Name == nanos {
					fe, info = x, s.fsInfoItem(b.TypeOf(x.X.ID()))
				}
				if info != types.NoItem && notRead(stack, x) {
					return true
				}
			}
			if info == types.NoItem {
				return true
			}
			fd := byPath[path]
			if fd == nil {
				fd = &fileEdits{at: fe.Span(), file: b.File, removed: !s.hasField(info, fe.Name.Name)}
				byPath[path] = fd
			}
			if fe.Name.Name == nanos {
				fd.edits = append(fd.edits, diag.Edit{Span: fe.Name.Span(), Text: "modified.UnixNanos()"})
				return true
			}
			fsName := importName(s.w.in, b.File, "std.fs")
			if fsName == "" {
				fsName, fd.needFs = "fs", true
			}
			if whole == nil {
				whole = fe
			}
			repl := text[fe.X.Span().Start:fe.X.Span().End] + ".kind" + op + fsName + ".Kind.Dir"
			if len(stack) > 0 && needsParens(stack[len(stack)-1], whole.(ast.Expr)) {
				repl = "(" + repl + ")"
			}
			fd.edits = append(fd.edits, diag.Edit{Span: whole.Span(), Text: repl})
			return false // the receiver is copied whole into the rewrite
		}
		ast.Inspect(b.Node, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			if !visit(n) {
				return false // Inspect calls no f(nil) for it
			}
			stack = append(stack, n)
			return true
		})
	})
	var paths []string
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []fixCand
	for _, p := range paths {
		fd := byPath[p]
		if fd.needFs {
			fd.edits = append(fd.edits, importEdit(fd.file, "std.fs"))
		}
		c := fixCand{path: p, edits: fd.edits, applied: AppliedFix{At: posText(res, fd.at), Why: fsInfoWhy}}
		if fd.removed {
			// The read is check's diagnostic (an unknown member): the fix
			// is reported as that diagnostic's, with its code.
			for _, e := range res.Report.Diags {
				if e.Diag.At.File == fd.at.File && e.Diag.At.Start >= fd.at.Start && e.Diag.At.End <= fd.at.End {
					c.d, c.applied.Code = e.Diag, e.Diag.Code
					break
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// fsInfoItem is std.fs's Info when t, under references, is it; else NoItem.
func (s *Session) fsInfoItem(t types.TypeID) types.ItemID {
	tab := s.w.tab
	for t != types.NoType && t != types.Poison {
		ty := tab.Get(t)
		switch ty.Kind {
		case types.KRef, types.KMut:
			t = ty.Elem
			continue
		case types.KNamed:
			it := s.w.items.Get(ty.Item)
			if s.w.in.Str(it.Name) == "Info" && s.w.items.Module(it.Module).Path == "std.fs" {
				return ty.Item
			}
		}
		return types.NoItem
	}
	return types.NoItem
}

// hasField reports whether the struct item declares a field named name.
func (s *Session) hasField(id types.ItemID, name source.Symbol) bool {
	if d, ok := s.w.items.Get(id).Decl.(*types.StructDecl); ok {
		for _, f := range d.Fields {
			if f.Name == name {
				return true
			}
		}
	}
	return false
}

// notRead reports whether fe, under the ancestors stack (its parent last),
// is no read of its value: an assignment's target, or the operand of `&` or
// `mut`, through any parentheses.
func notRead(stack []ast.Node, fe *ast.FieldExpr) bool {
	var child ast.Node = fe
	for i := len(stack) - 1; i >= 0; i-- {
		switch p := stack[i].(type) {
		case *ast.ParenExpr:
			child = p
			continue
		case *ast.AssignStmt:
			return p.Target == child
		case *ast.UnaryExpr:
			return p.Op == ast.TokAmp || p.Op == ast.TokMut
		}
		return false
	}
	return false
}

// needsParens reports whether a comparison written where child stands under
// parent needs parentheses: under a unary operator, a binary operator that
// binds tighter than && and ||, or as the receiver of a field, call or
// index.
func needsParens(parent ast.Node, child ast.Expr) bool {
	switch p := parent.(type) {
	case *ast.UnaryExpr:
		return true
	case *ast.BinaryExpr:
		return p.Op != ast.TokAmpAmp && p.Op != ast.TokPipePipe
	case *ast.FieldExpr:
		return p.X == child
	case *ast.CallExpr:
		return p.Fun == child
	case *ast.IndexExpr:
		return p.X == child
	}
	return false
}

// importName is the name a file spells module path by: its alias or its
// last segment; "" when the file does not import it.
func importName(in *source.Interner, f *ast.File, path string) string {
	if f == nil {
		return ""
	}
	for _, it := range f.Items {
		im, ok := it.(*ast.Import)
		if !ok || im.Path == nil {
			continue
		}
		var segs []string
		for _, n := range im.Path.Names {
			segs = append(segs, in.Str(n.Name))
		}
		if strings.Join(segs, ".") != path {
			continue
		}
		if im.Alias != nil {
			return in.Str(im.Alias.Name)
		}
		return segs[len(segs)-1]
	}
	return ""
}

// importEdit inserts `import path` after the file's last import, or before
// its first item when it has none.
func importEdit(f *ast.File, path string) diag.Edit {
	var at uint32
	last := false
	for _, it := range f.Items {
		if im, ok := it.(*ast.Import); ok {
			at, last = im.Span().End, true
		}
	}
	if last {
		return diag.Edit{Span: source.Span{File: f.Span().File, Start: at, End: at}, Text: "\nimport " + path}
	}
	if len(f.Items) > 0 {
		at = f.Items[0].Span().Start
	}
	return diag.Edit{Span: source.Span{File: f.Span().File, Start: at, End: at}, Text: "import " + path + "\n\n"}
}
