// Why: versions and sources are written by hand in manifests, and both the
// version choice and the cache depend on reading them exactly.
//
// What: semver (parseSemver, String, Tag, compareSemver), checkSource and
// sourceMajor.
//
// How: a `Dep.version` is X.Y.Z with an optional pre-release, semantic
// versioning 2.0's syntax, compared by its precedence rules; a
// `Dep.source` is host/path, the host holding a dot, every element passing
// the fetcher's path check, with no scheme, no query and no trailing ".git".

package driver

import (
	"fmt"
	"strings"

	"mk/internal/fetch"
)

// semver is a parsed X.Y.Z[-pre] version (no build metadata: a manifest
// writes none, and a tag's is not read).
type semver struct {
	Major, Minor, Patch uint64
	Pre                 []string // dot-separated identifiers; nil for a release
}

// parseSemver reads "X.Y.Z" or "X.Y.Z-pre" (semantic versioning 2.0: a
// numeric identifier has no leading zero; a pre-release identifier is
// [0-9A-Za-z-]+, numeric ones with no leading zero).
func parseSemver(s string) (semver, bool) {
	var v semver
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	var ok bool
	if v.Major, ok = numericID(parts[0]); !ok {
		return v, false
	}
	if v.Minor, ok = numericID(parts[1]); !ok {
		return v, false
	}
	if v.Patch, ok = numericID(parts[2]); !ok {
		return v, false
	}
	if !hasPre {
		return v, true
	}
	for _, id := range strings.Split(pre, ".") {
		if id == "" {
			return v, false
		}
		digits := true
		for _, c := range []byte(id) {
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				digits = false
			default:
				return v, false
			}
		}
		if digits && len(id) > 1 && id[0] == '0' {
			return v, false
		}
		v.Pre = append(v.Pre, id)
	}
	return v, true
}

// String is the version as a manifest writes it.
func (v semver) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Tag is the git tag of v, "v" and its text.
func (v semver) Tag() string { return "v" + v.String() }

// compareSemver orders a and b by semantic versioning 2.0's precedence: the
// numbers, then a pre-release before its release, then the pre-release
// identifiers left to right (numeric ones numerically and before
// alphanumeric ones, which compare by bytes; a shorter list first).
func compareSemver(a, b semver) int {
	if c := compareVersions([3]uint64{a.Major, a.Minor, a.Patch}, [3]uint64{b.Major, b.Minor, b.Patch}); c != 0 {
		return c
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		x, y := a.Pre[i], b.Pre[i]
		xn, xok := numericID(x)
		yn, yok := numericID(y)
		switch {
		case xok && yok:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xok:
			return -1
		case yok:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	return len(a.Pre) - len(b.Pre)
}

// checkSource reports why s is not a valid `Dep.source`, or nil.
func checkSource(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("a source is host/path")
	case strings.Contains(s, "://"):
		return fmt.Errorf("a source has no scheme: write %q", s[strings.Index(s, "://")+3:])
	case strings.ContainsAny(s, "?#"):
		return fmt.Errorf("a source has no query or fragment")
	case strings.HasSuffix(strings.ToLower(s), ".git"):
		return fmt.Errorf("a source has no trailing .git: write %q", s[:len(s)-4])
	case strings.HasSuffix(s, "/"):
		return fmt.Errorf("a source has no trailing slash")
	}
	host, path, ok := strings.Cut(s, "/")
	if !ok || path == "" {
		return fmt.Errorf("a source is host/path, with a path after the host")
	}
	if !strings.Contains(host, ".") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return fmt.Errorf("the host %q holds no dot", host)
	}
	for _, c := range []byte(host) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return fmt.Errorf("the host %q is not a host name", host)
		}
	}
	if err := fetch.CheckPath(path); err != nil {
		return fmt.Errorf("the path %q is refused: %v", path, err)
	}
	return nil
}

// sourceMajor is the identity of a source package: its source and major
// version.
func sourceMajor(source string, v semver) string { return fmt.Sprintf("%s@v%d", source, v.Major) }
