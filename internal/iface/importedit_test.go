// Why: a cache-loaded module's quote bindings come from its import lines
// (resolve.Session.LoadWith), so an edit of a generator module's imports must
// miss its module record.
// What: TestImportEditMovesModulePrefix and
// TestDecodeIntoChangedImportsIsStale.
// How: pins the two places the cache sees imports: each file's Outside hash in
// the module prefix (the driver fills it with the file's token hash, import
// lines included; resolve's TestGeneratorImportEditMovesHash shows the real hash moves with an
// import edit), and the interface record's import paths against the build's.

package iface

import (
	"errors"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// TestImportEditMovesModulePrefix: a moved Outside hash moves the
// module prefix, with every item's token hash held equal (an alias that
// only a quote names changes no item's tokens). Outside is the one file
// field the driver fills (frontcache.go: the whole file's token hash);
// FileTokens.Imports is left zero there, so it is not what keeps a hit's
// import lines current and is not tested here. The hash below stands in
// for the file hash of each edit, which TestGeneratorImportEditMovesHash in resolve shows
// distinct.
func TestImportEditMovesModulePrefix(t *testing.T) {
	var build source.Hash
	build[0] = 1
	variants := []struct {
		name    string
		imports [][2]string
	}{
		{"base", [][2]string{{"core", "std.core"}, {"raw", "std.raw"}}},
		{"added", [][2]string{{"core", "std.core"}, {"raw", "std.raw"}, {"simd", "std.simd"}}},
		{"removed", [][2]string{{"core", "std.core"}}},
		{"aliased", [][2]string{{"core", "std.core"}, {"r", "std.raw"}}},
		{"realiased", [][2]string{{"core", "std.core"}, {"q", "std.raw"}}},
	}
	seen := map[source.Hash]string{}
	for _, v := range variants {
		// As the driver: Outside set, Imports zero.
		f := FileTokens{Path: "gen/main.mk", Outside: ImportsHash(v.imports),
			Items: []ItemTokens{{Key: types.ItemKey("gen", "Matcher")}}}
		p := ModulePrefix(build, "gen", []FileTokens{f}, nil)
		if prev, dup := seen[p]; dup {
			t.Errorf("%s and %s share a module prefix", prev, v.name)
		}
		seen[p] = v.name
	}
}

// TestDecodeIntoChangedImportsIsStale: an interface decoded into a
// module whose Module.Imports (the build's import graph) differ from the
// record's import paths is ErrStale, and leaves no item behind.
func TestDecodeIntoChangedImportsIsStale(t *testing.T) {
	a, f := ifaceSession(false)
	m := geoInterface(a, f)
	enc := EncodeInterface(a.env, a.h, m)
	b := loadSession(bodyText)
	extra := &types.Module{Path: "std.simd"}
	b.items.AddModule(extra)
	// The record's own imports, as b's modules, then one more.
	into := &types.Module{Path: "geo"}
	for _, id := range m.Module.Imports {
		got, ok := moduleByPath(b.items, a.items.Module(id).Path)
		if !ok {
			t.Fatalf("import %s missing from the loading session", a.items.Module(id).Path)
		}
		into.Imports = append(into.Imports, got)
	}
	into.Imports = append(into.Imports, extra.ID)
	b.items.AddModule(into)
	if _, err := DecodeInterfaceInto(b.env, enc, into); !errors.Is(err, ErrStale) {
		t.Errorf("an added import: %v, want ErrStale", err)
	}
	if _, ok := b.items.ByKey(types.ItemKey("geo", "Point")); ok {
		t.Error("a failed decode left an item")
	}
}
