// Why: the library items KnownItems names must stay visible and usable where
// the reference says they are, or generators and Map keys break.
// What: TestGeneratorAPIIsPrelude and TestRefKeysHaveEqHash.
// How: reads doc/reference.md, doc/std/core.md, doc/memory.md and std's Map
// declaration, and checks the prelude list, the generator API sentence and
// the Eq, Ord and Hash rule for &T.

package types

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mk/internal/source"
)

func readDoc(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "doc", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// generatorAPI ties each generator API name (doc/reference.md, "Compile
// time") to the KnownItems field the rules read it through. TypeInfo is the
// item for the name Type.
var generatorAPI = []struct {
	name  string
	field func(*KnownItems) *ItemID
}{
	{"Gen", func(k *KnownItems) *ItemID { return &k.Gen }},
	{"Type", func(k *KnownItems) *ItemID { return &k.TypeInfo }},
	{"Code", func(k *KnownItems) *ItemID { return &k.Code }},
	{"Block", func(k *KnownItems) *ItemID { return &k.Block }},
	{"Field", func(k *KnownItems) *ItemID { return &k.Field }},
	{"Variant", func(k *KnownItems) *ItemID { return &k.Variant }},
	{"Method", func(k *KnownItems) *ItemID { return &k.Method }},
	{"Param", func(k *KnownItems) *ItemID { return &k.Param }},
	{"TypeKind", func(k *KnownItems) *ItemID { return &k.TypeKind }},
	{"Receiver", func(k *KnownItems) *ItemID { return &k.Receiver }},
	{"Flow", func(k *KnownItems) *ItemID { return &k.Flow }},
	{"FlowKind", func(k *KnownItems) *ItemID { return &k.FlowKind }},
}

// TestGeneratorAPIIsPrelude: every generator API name KnownItems holds is
// in the prelude list of doc/reference.md's "Module map" (that list is
// exactly what user code sees, so a name missing from it is E0201 in every
// generator module), is named by the section's generator-API sentence, is
// not a universe name (std could not declare it, E0102), and each maps to
// its own KnownItems field. The generator signature uses Gen and Type.
func TestGeneratorAPIIsPrelude(t *testing.T) {
	t.Parallel()
	ref := readDoc(t, "reference.md")
	m := regexp.MustCompile("Prelude, no import needed: `([^`]*)`").FindStringSubmatch(ref)
	if m == nil {
		t.Fatal("prelude line not found in doc/reference.md")
	}
	prelude := map[string]bool{}
	for _, n := range strings.Fields(m[1]) {
		prelude[n] = true
	}
	g := regexp.MustCompile("`([^`]*)` are the generator API of section 11").FindStringSubmatch(ref)
	if g == nil {
		t.Fatal("generator API sentence not found in the reference's generator API")
	}
	stated := strings.Fields(g[1])
	if len(stated) != len(generatorAPI) {
		t.Errorf("the reference names %d generator API types %q, KnownItems has %d", len(stated), stated, len(generatorAPI))
	}
	in := source.NewInterner()
	var k KnownItems
	seen := map[*ItemID]string{}
	for i, e := range generatorAPI {
		if !prelude[e.name] {
			t.Errorf("generator API name %s is not in the reference's prelude list", e.name)
		}
		if i < len(stated) && stated[i] != e.name {
			t.Errorf("generator API sentence name %d is %s, want %s", i, stated[i], e.name)
		}
		if source.IsUniverse(in.Intern(e.name)) {
			t.Errorf("generator API name %s is a universe name", e.name)
		}
		f := e.field(&k)
		if other, dup := seen[f]; dup {
			t.Errorf("%s and %s share one KnownItems field", other, e.name)
		}
		seen[f] = e.name
	}
	if !strings.Contains(ref, "(g: mut Gen, t: Type)") {
		t.Error("the reference's generator no longer takes (g: mut Gen, t: Type); recheck this table")
	}
}

// TestRefKeysHaveEqHash: one of doc/memory.md's hard cases keys a Map
// by a reference and std's Map declares its keys `K: Hash + Eq`, so the
// prelude's page (doc/std/core.md) must give Eq, Ord and Hash to &T
// whenever T has them, as it gives Format.
func TestRefKeysHaveEqHash(t *testing.T) {
	t.Parallel()
	decl, err := os.ReadFile(filepath.Join("..", "..", "std", "core", "coll_map.mk"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^export view struct Map\[K: Hash \+ Eq, V\]`).Match(decl) {
		t.Fatal("std's Map no longer declares K: Hash + Eq; recheck this test")
	}
	core := readDoc(t, filepath.Join("std", "core.md"))
	found := false
	for _, para := range strings.Split(core, "\n\n") {
		if strings.Contains(para, "`&T`") && strings.Contains(para, "`Eq`") && strings.Contains(para, "`Ord`") && strings.Contains(para, "`Hash`") && strings.Contains(para, "`Map[&K, V]`") {
			found = true
			break
		}
	}
	if !found {
		t.Error("doc/std/core.md has no paragraph giving Eq, Ord and Hash to &T so that Map[&K, V] works")
	}
	mm := readDoc(t, "memory.md")
	if !regexp.MustCompile("(?m)^\\| H\\d+ \\| `Map\\.Remove` on `Map\\[&K, &V\\]`").MatchString(mm) {
		t.Error("doc/memory.md has no hard case keying a Map by a reference; recheck this test")
	}
}
