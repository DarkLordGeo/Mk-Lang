// Why: every pass that depends on the build target or profile reads the
// same facts, and the compile-time constant Target must match them.
//
// What: Arch and OS (mirroring the prelude enums), Target and the supported
// targets (Targets, AllTargets, TargetByName), TargetField, and Profile
// (ContractsChecked, Optimized).
//
// How: plain values. Enum orders mirror std's declarations, and new members
// are appended, so no discriminant or cache key moves.

package types

import "strconv"

// Arch mirrors the prelude enum Arch, in its variant order, so a Target's
// arch compares by discriminant with Arch.X64 in Mk code.
type Arch uint8

const (
	ArchX64 Arch = iota
	ArchArm64
	ArchRiscv64
)

// String is the prelude variant name.
func (a Arch) String() string {
	switch a {
	case ArchX64:
		return "X64"
	case ArchArm64:
		return "Arm64"
	case ArchRiscv64:
		return "Riscv64"
	}
	return "arch(" + strconv.Itoa(int(a)) + ")"
}

// OS mirrors the prelude enum Os, in its variant order.
type OS uint8

const (
	OSLinux OS = iota
	OSWindows
	OSMacos
	OSFreestanding
	// OSNutshell is the Nutshell operating system: a hosted
	// target whose std.sys speaks Nutshell's system calls. Last, so the
	// earlier discriminants do not move.
	OSNutshell
)

// String is the prelude variant name.
func (o OS) String() string {
	switch o {
	case OSLinux:
		return "Linux"
	case OSWindows:
		return "Windows"
	case OSMacos:
		return "Macos"
	case OSFreestanding:
		return "Freestanding"
	case OSNutshell:
		return "Nutshell"
	}
	return "os(" + strconv.Itoa(int(o)) + ")"
}

// Target is one build target: the facts of the compile-time constant Target
// (TargetInfo: arch, os, pointer_bytes) plus the names the back
// end needs. Comparable.
type Target struct {
	// Name is mk's target name, "x86_64-windows".
	Name string
	Arch Arch
	OS   OS
	// PointerBytes is 8 on every supported target; int and uint are 64 bits.
	PointerBytes uint8
	// LLVMTriple is passed to clang --target.
	LLVMTriple string
	// Built is true for the targets mk build compiles and links end to end;
	// the hosted ones among them also run in the one-shot, while freestanding
	// and Nutshell executables are never run on the build host. The others
	// are layout- and ABI-tested only.
	Built bool
	// VectorBits is TargetInfo.vector_bits, fixed per target: 128 on x86_64
	// and aarch64. --cpu never changes it.
	VectorBits uint16
}

// The supported targets.
var (
	X86_64Windows = Target{Name: "x86_64-windows", Arch: ArchX64, OS: OSWindows, PointerBytes: 8,
		LLVMTriple: "x86_64-pc-windows-msvc", Built: true, VectorBits: 128}
	X86_64Linux = Target{Name: "x86_64-linux", Arch: ArchX64, OS: OSLinux, PointerBytes: 8,
		LLVMTriple: "x86_64-unknown-linux-gnu", Built: true, VectorBits: 128}
	Aarch64Linux = Target{Name: "aarch64-linux", Arch: ArchArm64, OS: OSLinux, PointerBytes: 8,
		LLVMTriple: "aarch64-unknown-linux-gnu", VectorBits: 128}
	Aarch64Macos = Target{Name: "aarch64-macos", Arch: ArchArm64, OS: OSMacos, PointerBytes: 8,
		LLVMTriple: "arm64-apple-macosx11.0.0", VectorBits: 128}
	// X86_64Freestanding is the freestanding target:
	// no OS, an ELF object for ld.lld, layers 0 and 1 of std only. It is in
	// AllTargets() and found by TargetByName, but not in Targets(), which
	// lists the hosted targets every hosted test iterates.
	X86_64Freestanding = Target{Name: "x86_64-freestanding", Arch: ArchX64, OS: OSFreestanding, PointerBytes: 8,
		LLVMTriple: "x86_64-unknown-none-elf", Built: true, VectorBits: 128}
	// X86_64Nutshell is the Nutshell target: a hosted target,
	// all of std, whose std.sys is the directory std/pkg.mk selects for
	// os "nutshell", linked by ld.lld into a static fixed-address ELF
	// entered at _start. Like freestanding it is in AllTargets() and found
	// by TargetByName, but not in Targets(): the hosted tests run their
	// programs, and nothing runs a Nutshell executable on the build host.
	X86_64Nutshell = Target{Name: "x86_64-nutshell", Arch: ArchX64, OS: OSNutshell, PointerBytes: 8,
		LLVMTriple: "x86_64-unknown-nutshell-elf", Built: true, VectorBits: 128}
	// Aarch64Nutshell is checked only: mk check type-checks for it, and
	// mk build refuses it until aarch64 code generation is built.
	Aarch64Nutshell = Target{Name: "aarch64-nutshell", Arch: ArchArm64, OS: OSNutshell, PointerBytes: 8,
		LLVMTriple: "aarch64-unknown-nutshell-elf", VectorBits: 128}
)

// Targets lists every supported hosted target, in name order.
func Targets() []Target {
	return []Target{Aarch64Linux, Aarch64Macos, X86_64Linux, X86_64Windows}
}

// AllTargets lists every supported target: the hosted ones, then
// freestanding, then the two Nutshell targets.
func AllTargets() []Target {
	return append(Targets(), X86_64Freestanding, X86_64Nutshell, Aarch64Nutshell)
}

// TargetByName finds a target of AllTargets() by mk's name.
func TargetByName(name string) (Target, bool) {
	for _, t := range AllTargets() {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

// TargetField names a field of TargetInfo, for read sets: an evaluation that
// read Target.os depends on the os and nothing else.
type TargetField uint8

const (
	TargetArch TargetField = iota
	TargetOS
	TargetPointerBytes
	// TargetVectorBits is TargetInfo.vector_bits.
	TargetVectorBits
)

// String is the Mk field name.
func (f TargetField) String() string {
	switch f {
	case TargetVectorBits:
		return "vector_bits"
	case TargetArch:
		return "arch"
	case TargetOS:
		return "os"
	case TargetPointerBytes:
		return "pointer_bytes"
	}
	return "field(" + strconv.Itoa(int(f)) + ")"
}

// Profile is a build profile: contracts are checked in Dev, Test and Checked,
// off in Release. Checked is optimized as Release is, with Dev's checks; it is
// appended so no profile is renumbered (every cache key hashes the number).
type Profile uint8

const (
	Dev Profile = iota
	TestProfile
	Release
	Checked
)

// String is dev, test, release or checked.
func (p Profile) String() string {
	switch p {
	case Dev:
		return "dev"
	case TestProfile:
		return "test"
	case Release:
		return "release"
	case Checked:
		return "checked"
	}
	return "profile(" + strconv.Itoa(int(p)) + ")"
}

// ContractsChecked reports whether contracts run in the profile.
func (p Profile) ContractsChecked() bool { return p != Release }

// Optimized reports whether the profile compiles and links as release
// does: Release and Checked.
func (p Profile) Optimized() bool { return p == Release || p == Checked }
