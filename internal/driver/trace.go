// Why: incremental-build tests and the corpus must see what a build
// actually did (what came from the cache, what was checked, evaluated,
// generated and compiled), not only what it printed.
// What: Trace, the per-build work log, and its sort.
// How: each stage appends what it did; sort orders every list, so a trace
// never depends on scheduling.

package driver

import (
	"slices"

	"mk/internal/types"
)

// Trace is the per-build work log. Every list is sorted (paths and keys
// bytewise) whatever the order the workers finished in, so it is
// deterministic; counts are derived from the lists.
type Trace struct {
	FilesParsed      []string    // source paths as diagnostics print them; std as std/...
	ModulesResolved  []string    // module paths that ran stage 3
	ModulesFromCache []string    // module paths loaded from an interface
	ItemsChecked     []types.Key // functions whose body ran stage 6
	ItemsFromCache   []types.Key
	Evaluations      []string    // evaluation keys executed
	GeneratorRuns    []types.Key // gen line keys executed
	PacksCompiled    []string    // pack names compiled by clang
	PacksFromCache   []string
	// PacksEmitted are the pack names whose IR codegen generated, the support
	// pack included (a pack whose pre-emission key hits is not).
	PacksEmitted []string
	Linked       bool
	NotStored    []string // units not written to the cache, with the reason
	// ManifestChecked are the modules the manifest step checked: std.pkg,
	// then each manifest module by its file path (std/pkg.mk, pkg.mk). Sorted,
	// never compacted: std.pkg appears exactly once per session.
	ManifestChecked []string
	// ModulesWalked are the modules whose roots the instance-limit walk
	// walked (walkcache.go skips one whose marker holds).
	ModulesWalked []string
}

// sort puts every list in its canonical order.
func (t *Trace) sort() {
	for _, l := range []*[]string{&t.FilesParsed, &t.ModulesResolved, &t.ModulesFromCache, &t.Evaluations,
		&t.PacksCompiled, &t.PacksFromCache, &t.PacksEmitted, &t.NotStored, &t.ModulesWalked} {
		slices.Sort(*l)
		*l = slices.Compact(*l)
	}
	slices.Sort(t.ManifestChecked)
	for _, l := range []*[]types.Key{&t.ItemsChecked, &t.ItemsFromCache, &t.GeneratorRuns} {
		slices.SortFunc(*l, func(a, b types.Key) int { return a.Compare(b) })
	}
}
