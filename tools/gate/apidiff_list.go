// Why: the apidiff step fails on any breaking change to std's public API
// that the release does not announce, so an accidental break cannot ship.
//
// What: apidiffBreaking, the names this release announces as breaking,
// compared with the std of apidiffBase.
//
// How: namesItem matches each breaking entry of `mk apidiff --std` against
// these names by qualified name; a name without a module is std.core's
// (`Method.Flows` lists std.core.Method.Flows). The first release has no
// earlier std, so the list is empty; the next release lists every item its
// notes announce as breaking.

package main

// apidiffBreaking is this release's breaking list.
var apidiffBreaking []string
