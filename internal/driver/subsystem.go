// Why: Mk struct literals name every field, so when `pkg.Bin` gained
// `subsystem` and `pkg.Package` gained `links`, an existing manifest's call
// became E0305 with the new field missing. mk fix must repair it.
//
// What: manifestFix and the manifestFixes table, addManifestFixes, which
// attaches the fixes to the diagnostics, and the helpers fieldInsert,
// onlyMissing and pkgCallAt.
//
// How: each fix writes the value that links exactly what the manifest linked
// before the field existed, so the fix is proven and `mk fix` applies it.

package driver

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// manifestFix is one proven E0305 fix: a `pkg.<Struct>(...)` call whose
// only missing field is Field gets Text after its last argument.
type manifestFix struct {
	Struct, Field, Text, Why string
}

// manifestFixes are the fields added to std.pkg's stable structs, each
// with the value that keeps a manifest's build what it was.
var manifestFixes = []manifestFix{
	{"Bin", "subsystem", `, subsystem: "console"`,
		"`subsystem: \"console\"` links the console program this bin was before the field existed"},
	{"Package", "links", `, links: []`,
		"`links: []` links exactly what the package linked before the field existed"},
}

// addManifestFixes gives each E0305 of a manifest module whose only
// missing field is one manifestFixes names, at that struct's
// `pkg.<Struct>(...)` call, its proven fix. A call missing anything else
// keeps the checker's stubs.
func addManifestFixes(b *build, in *source.Interner, ds []*diag.Diagnostic) {
	for _, d := range ds {
		if d.Code != diag.E0305 || len(d.Fixes) > 0 {
			continue
		}
		for _, mf := range manifestFixes {
			if !onlyMissing(d, mf.Field) {
				continue
			}
			for _, m := range b.mods {
				if m.std {
					continue
				}
				for _, f := range m.files {
					if f.ast == nil || f.src == nil || f.src.ID != d.At.File {
						continue
					}
					if c := pkgCallAt(f.ast, in, d, mf.Struct); c != nil && len(c.Args) > 0 {
						at, text := fieldInsert(f.src.Text, c.Args[len(c.Args)-1].Span(), mf.Text)
						d.Fix(diag.Fix{Class: diag.Proven, Edits: []diag.Edit{{Span: source.Span{File: d.At.File, Start: at, End: at}, Text: text}},
							Why: mf.Why})
					}
				}
			}
		}
	}
}

// fieldInsert places the new field after a call's last argument last. On
// one line it is text (", name: value") right after the argument. When the
// argument ends its line with a trailing comma, as a manifest written one
// field per line does, the field goes on a line of its own after that
// comma, at the argument's indentation, with its own trailing comma, so
// the result is what mk fmt writes.
func fieldInsert(src string, last source.Span, text string) (uint32, string) {
	blank := func(c byte) bool { return c == ' ' || c == '\t' }
	i := int(last.End)
	for i < len(src) && blank(src[i]) {
		i++
	}
	if i >= len(src) || src[i] != ',' {
		return last.End, text
	}
	j := i + 1
	for j < len(src) && blank(src[j]) {
		j++
	}
	if j >= len(src) || src[j] != '\n' {
		return last.End, text
	}
	ls := int(last.Start)
	for ls > 0 && src[ls-1] != '\n' {
		ls--
	}
	indent := ls
	for indent < len(src) && blank(src[indent]) {
		indent++
	}
	return uint32(i + 1), "\n" + src[ls:indent] + strings.TrimPrefix(text, ", ") + ","
}

// onlyMissing reports whether d's `missing` fact is exactly [name].
func onlyMissing(d *diag.Diagnostic, name string) bool {
	f, ok := d.FactValue("missing")
	return ok && len(f.List) == 1 && f.List[0].Str == name
}

// pkgCallAt finds the `pkg.<name>(...)` call whose span is d's.
func pkgCallAt(file *ast.File, in *source.Interner, d *diag.Diagnostic, name string) *ast.CallExpr {
	var out *ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil || out != nil {
			return false
		}
		c, ok := n.(*ast.CallExpr)
		if !ok || c.Span() != d.At {
			return true
		}
		if fe, ok := c.Fun.(*ast.FieldExpr); ok && fe.Name != nil && in.Str(fe.Name.Name) == name {
			if x, ok := fe.X.(*ast.Ident); ok && in.Str(x.Name) == "pkg" {
				out = c
			}
		}
		return out == nil
	})
	return out
}
