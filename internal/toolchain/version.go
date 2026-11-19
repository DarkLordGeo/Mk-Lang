// Why: the emitted IR uses LLVM 21's syntax, so an older clang must be
// refused with a clear error instead of failing on the first compile.
//
// What: MinMajor, MaxTestedMajor, Version, ParseVersion and VersionError.
//
// How: the first line of `clang --version` is parsed to Major.Minor.Patch;
// Version.Supported compares it with MinMajor.

package toolchain

import (
	"fmt"
	"strconv"
	"strings"
)

// MinMajor is the oldest LLVM major version the emitted IR is written for:
// 21, so codegen may use LLVM 21's IR (getelementptr nuw
// and nusw, range attributes, captures, samesign, nneg, disjoint). A clang
// below it is refused, and on Linux DefaultDirs looks only in
// /usr/lib/llvm-21/bin before /usr/local/bin and /usr/bin.
const MinMajor = 21

// MaxTestedMajor is the newest LLVM major version the smoke tests have run
// on. A newer clang is accepted; the driver may say it is untested.
const MaxTestedMajor = 21

// Version is clang's version. Line is the first line of `clang --version`,
// verbatim, and is what reaches the toolchain identity and E0001 facts.
type Version struct {
	Major, Minor, Patch int
	Line                string
}

// String is "21.1.8".
func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Supported reports whether the version is at least MinMajor.
func (v Version) Supported() bool { return v.Major >= MinMajor }

// ParseVersion reads the first line of `clang --version` output: the text
// after "clang version " up to the first character that is neither a digit
// nor a dot gives Major.Minor.Patch ("Ubuntu clang version 18.1.3
// (1ubuntu1)", "clang version 21.1.8"). A missing Minor or Patch is 0. The
// error says the line is not a clang version line.
func ParseVersion(output string) (Version, error) {
	line := output
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	v := Version{Line: line}
	const marker = "clang version "
	i := strings.Index(line, marker)
	if i < 0 {
		return v, fmt.Errorf("not a clang version line: %q", line)
	}
	rest := line[i+len(marker):]
	end := 0
	for end < len(rest) && (rest[end] == '.' || rest[end] >= '0' && rest[end] <= '9') {
		end++
	}
	parts := strings.Split(rest[:end], ".")
	nums := []*int{&v.Major, &v.Minor, &v.Patch}
	for k, p := range parts {
		if k == len(nums) {
			break
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			if k == 0 {
				return v, fmt.Errorf("no version number in %q", line)
			}
			break
		}
		*nums[k] = n
	}
	return v, nil
}

// VersionError is a clang older than MinMajor. The driver reports it as
// E0001 with `reason: unsupported-version`.
type VersionError struct {
	Path    string
	Version Version
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("%s is LLVM %s; LLVM %d or newer is required (%s)",
		e.Path, e.Version, MinMajor, e.Version.Line)
}
