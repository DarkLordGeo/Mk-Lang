// Why: the release tool, the install scripts and mk upgrade must agree on
// how a release's files are named and versions compared.
//
// What: the hosts (Hosts, HostOf), TopDir, ArchiveName and ParseArchiveName; Version with
// ParseVersion, String and Compare; Base, the compiled-in release address.
//
// How: the hosts are x86_64 Windows, x86_64 and aarch64 Linux and both
// macOS architectures; an archive is mk-<version>-<host>.zip on Windows and
// .tar.gz elsewhere; a version is Major.Minor.Patch with an optional pre-release
// suffix, which sorts before its release.

// Package release holds what a toolchain release is on disk and on the
// wire: the archive names and layout (archive.go), the deterministic
// archive writer and the safe reader, SHA256SUMS (sums.go), the Ed25519
// signature SHA256SUMS.sig (sign.go) and the public keys mk trusts
// (keys.go). tools/release writes releases with it and mk upgrade reads
// them. It imports only the standard library (its DAG row allows source).
package release

import (
	"fmt"
	"strconv"
	"strings"
)

// Files of a release besides its archives.
const (
	SumsFile = "SHA256SUMS"
	SigFile  = "SHA256SUMS.sig"
)

// The hosts a release has an archive for.
const (
	HostWindows    = "x86_64-windows"
	HostLinux      = "x86_64-linux"
	HostLinuxARM64 = "aarch64-linux"
	HostMacOS      = "x86_64-macos"  // Intel Macs
	HostMacOSARM64 = "aarch64-macos" // Apple silicon
)

// Hosts lists every host a release has an archive for.
var Hosts = []string{HostWindows, HostLinux, HostLinuxARM64, HostMacOS, HostMacOSARM64}

// HostOf is the release host of a Go GOOS and GOARCH, or "" when releases
// carry no archive for it.
func HostOf(goos, goarch string) string {
	switch goos + "/" + goarch {
	case "windows/amd64":
		return HostWindows
	case "linux/amd64":
		return HostLinux
	case "linux/arm64":
		return HostLinuxARM64
	case "darwin/amd64":
		return HostMacOS
	case "darwin/arm64":
		return HostMacOSARM64
	}
	return ""
}

// TopDir is the one directory an archive holds everything under:
// mk-<version>-<host>.
func TopDir(version, host string) string { return "mk-" + version + "-" + host }

// ArchiveName is the host's archive file name: mk-<version>-x86_64-windows.zip
// on Windows, mk-<version>-<host>.tar.gz on Linux and macOS.
func ArchiveName(version, host string) string {
	if host == HostWindows {
		return TopDir(version, host) + ".zip"
	}
	return TopDir(version, host) + ".tar.gz"
}

// ParseArchiveName reads a version and host back from an archive file name,
// ok=false for any other name.
func ParseArchiveName(name string) (version, host string, ok bool) {
	for _, h := range Hosts {
		rest, found := strings.CutPrefix(name, "mk-")
		if !found {
			return "", "", false
		}
		if v, found := strings.CutSuffix(rest, strings.TrimPrefix(ArchiveName("", h), "mk-")); found {
			if _, err := ParseVersion(v); err == nil {
				return v, h, true
			}
		}
	}
	return "", "", false
}

// Version is a release version, Major.Minor.Patch with an optional
// pre-release suffix after '-' ("0.1.0", "0.2.0-rc1").
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// ParseVersion reads a version; a leading 'v' (a tag) is accepted. The
// numbers are decimal without a sign or a leading zero, and the suffix
// holds letters, digits and dots, neither starting nor ending with a dot
// and never with two together.
func ParseVersion(s string) (Version, error) {
	var v Version
	t := strings.TrimPrefix(s, "v")
	core, pre, hasPre := strings.Cut(t, "-")
	if hasPre {
		if pre == "" || strings.Trim(pre, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.") != "" ||
			strings.HasPrefix(pre, ".") || strings.HasSuffix(pre, ".") || strings.Contains(pre, "..") {
			return v, fmt.Errorf("release: bad version %q", s)
		}
		v.Pre = pre
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("release: bad version %q (want major.minor.patch)", s)
	}
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') || strings.Trim(p, "0123456789") != "" {
			return v, fmt.Errorf("release: bad version %q", s)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("release: bad version %q", s)
		}
		*[]*int{&v.Major, &v.Minor, &v.Patch}[i] = n
	}
	return v, nil
}

// String is "0.1.0" or "0.2.0-rc1".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare is -1, 0 or +1 as v is older than, equal to or newer than w. A
// pre-release is older than its release; two suffixes compare as text.
func (v Version) Compare(w Version) int {
	for _, d := range []int{v.Major - w.Major, v.Minor - w.Minor, v.Patch - w.Patch} {
		if d != 0 {
			if d < 0 {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.Pre == w.Pre:
		return 0
	case v.Pre == "":
		return 1
	case w.Pre == "":
		return -1
	}
	return strings.Compare(v.Pre, w.Pre)
}

// base is the release address compiled into a release build of mk:
// go build -ldflags "-X mk/internal/release.base=https://github.com/zerodayokay/Mk-Lang/releases".
// It is "" in every other build, so an mk that nobody configured never
// reaches the network; MK_RELEASES overrides it (the driver reads it).
var base string

// Base is the compiled-in release address, "" outside a release build.
func Base() string { return base }

// ReleaseBuild reports whether this binary is a release build: one with a
// compiled-in release address. A release build refuses the test key
// (sign.go).
func ReleaseBuild() bool { return base != "" }

// FileURL is the address of one file of a release under a base address in
// GitHub's release layout: <base>/download/v<version>/<file>, or, for the
// newest release (version ""), <base>/latest/download/<file>.
func FileURL(baseURL, version, file string) string {
	b := strings.TrimRight(baseURL, "/")
	if version == "" {
		return b + "/latest/download/" + file
	}
	return b + "/download/v" + strings.TrimPrefix(version, "v") + "/" + file
}
