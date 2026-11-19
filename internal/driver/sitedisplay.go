// Why: a panic site or a run-time error report should name a dependency the
// way the root package's manifest names it, not by its canonical id.
//
// What: displaySitePath, the driver's use of codegen.DisplaySitePath.
//
// How: a fetched package's "<cid>@<hex>/" prefix becomes "<name>@<hex>/" and
// a vendored one's "vendor/<cid>/" becomes "vendor/<name>/", through the
// world's display map; the canonical id stays in symbols and vendor/ only.

package driver

import "mk/internal/codegen"

// displaySitePath is a panic site's package-relative path p with a
// dependency's canonical id shown by its local name in names (the world's
// display map), codegen.DisplaySitePath: a fetched package's "<cid>@<hex>/"
// prefix becomes "<name>@<hex>/", and a vendored one's "vendor/<cid>/"
// becomes "vendor/<name>/". Lowered trace sites keep the canonical-id path
// (lower.Config.SitePath is w.sitePath); codegen maps them with the same
// function.
func displaySitePath(p string, names map[string]string) string {
	return codegen.DisplaySitePath(p, names)
}
