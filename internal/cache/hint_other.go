// Why: the stat hint needs a changeIdentity on every system it builds on.
// What: an empty changeIdentity for systems that are only unit-tested
// (macOS).
// How: with no change identity, size, mtime and the racy rule decide alone.

//go:build !windows && !linux

package cache

import "os"

// changeIdentity is empty here.
func changeIdentity(fi os.FileInfo) [2]uint64 { return [2]uint64{} }
