// Why: the support pack defines the 128-bit division routines LLVM may
// call, and a user's own definition of them must win without a duplicate
// symbol.
// What: tests of the precedence rule over C sources, archive members, a
// DLL .def and system-libc shared object lists, with the C programs they
// build (tiCDiv and the others).
// How: tiRunEach builds each root for both targets and profiles and runs
// what it can, comparing the printed quotient (a user routine marks it).

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"mk/internal/types"
)

// The precedence rule: the support pack's __udivti3, __umodti3, __divti3 and
// __modti3 are weak aliases, so a C source's or a pulled archive member's
// own definition takes the name with no duplicate symbol, on lld-link and
// ld.lld, in dev and release; C that divides __int128 with no routine of its
// own gets the support pack's; and system-libc mode does not refuse a shared
// object's list that names one of them (libgcc_s's, which such C may still
// name).

// tiCDiv divides __int128 signed (__divti3) and takes an unsigned
// remainder (__umodti3), from values the compiler cannot fold: for
// (10^12, 7) it is 142857571428571428 + 4.
const tiCDiv = "long long CDiv(long long a, long long b) {\n" +
	"    volatile __int128 x = (__int128)a * 1000003;\n    volatile __int128 y = b;\n" +
	"    __int128 q = x / y;\n    unsigned __int128 uq = (unsigned __int128)x % (unsigned __int128)y;\n" +
	"    return (long long)(q + (__int128)uq);\n}\n"

// tiCDivRight is what every build of tiCDiv prints with correct routines.
const tiCDivRight = "142857571428571432\n"

// tiCUserUdiv is a C source with its own __udivti3, which marks its
// quotient (+1000), and a CDiv that divides unsigned through it.
const tiCUserUdiv = "typedef unsigned __int128 tu;\n" +
	"tu __udivti3(tu a, tu b) {\n    tu q = 0, r = 0;\n" +
	"    for (int i = 127; i >= 0; i--) {\n        r = (r << 1) | ((a >> i) & 1);\n" +
	"        if (r >= b) { r -= b; q |= (tu)1 << i; }\n    }\n    return q + 1000;\n}\n" +
	"long long CDiv(long long a, long long b) {\n    volatile tu x = (tu)a * 1000003;\n    volatile tu y = (tu)b;\n" +
	"    return (long long)(x / y);\n}\n"

// tiArchiveMember is an archive member with its own __udivti3, __divti3
// (which marks its quotient, +1000) and __umodti3, and a Helper the C
// source calls, so the link pulls the member for Helper.
const tiArchiveMember = "typedef unsigned __int128 tu; typedef __int128 ti;\n" +
	"tu __udivti3(tu a, tu b) { tu q = 0, r = 0; for (int i = 127; i >= 0; i--) { r = (r << 1) | ((a >> i) & 1); if (r >= b) { r -= b; q |= (tu)1 << i; } } return q; }\n" +
	"ti __divti3(ti a, ti b) { int s = (a < 0) ^ (b < 0); tu ua = a < 0 ? -(tu)a : (tu)a, ub = b < 0 ? -(tu)b : (tu)b; tu q = __udivti3(ua, ub); return (s ? -(ti)q : (ti)q) + 1000; }\n" +
	"tu __umodti3(tu a, tu b) { tu r = 0; for (int i = 127; i >= 0; i--) { r = (r << 1) | ((a >> i) & 1); if (r >= b) r -= b; } return r; }\n" +
	"long long Helper(long long x) { return x; }\n"

// tiCDivHelper is tiCDiv returning through the archive's Helper.
const tiCDivHelper = "long long Helper(long long);\n" +
	"long long CDiv(long long a, long long b) {\n" +
	"    volatile __int128 x = (__int128)a * 1000003;\n    volatile __int128 y = b;\n" +
	"    __int128 q = x / y;\n    unsigned __int128 uq = (unsigned __int128)x % (unsigned __int128)y;\n" +
	"    return Helper((long long)(q + (__int128)uq));\n}\n"

// tiCDivMain prints CDiv(10^12, 7).
var tiCDivMain = ffiMain("extern CDiv(a: i64, b: i64): i64", "CDiv(1000000000000, 7)")

// tiCSource is the c_sources entry of csrc/d.c.
const tiCSource = `pkg.CSource(path: "csrc/d.c", flags: [])`

// tiRunEach builds root for each target and profile and, where it can run
// the executable (Windows natively, Linux under MK_LINUX=1), wants want.
func tiRunEach(t *testing.T, name string, roots map[types.Target]string, want string) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(name+"/"+target.Name+"/"+prof.String(), func(t *testing.T) {
				exe := mustBuild(t, roots[target], target, prof)
				switch {
				case target.OS == types.OSWindows:
					if out := runOut(t, exe); out != want {
						t.Errorf("stdout %q, want %q", out, want)
					}
				case os.Getenv("MK_LINUX") == "1":
					if out, code := runLinuxTmp(t, exe); code != 0 || out != want {
						t.Errorf("exit %d, stdout %q, want %q", code, out, want)
					}
				default:
					t.Logf("built %s; not run (MK_LINUX=1 not set)", filepath.Base(exe))
				}
			})
		}
	}
}

// TestCDividesInt128: C that divides __int128 and defines no routine
// links the support pack's __divti3 and __umodti3 and prints the right
// value.
func TestCDividesInt128(t *testing.T) {
	root := writeTree(t, map[string]string{"pkg.mk": ffiManifest(nil, tiCSource), "csrc/d.c": tiCDiv, "app/main.mk": tiCDivMain})
	tiRunEach(t, "cdiv", map[types.Target]string{types.X86_64Windows: root, types.X86_64Linux: root}, tiCDivRight)
}

// TestCSourceDefinesUdivti3: a C source's own strong __udivti3
// replaces the support pack's weak one: no duplicate symbol, and its mark
// (+1000) is in the result.
func TestCSourceDefinesUdivti3(t *testing.T) {
	root := writeTree(t, map[string]string{"pkg.mk": ffiManifest(nil, tiCSource), "csrc/d.c": tiCUserUdiv, "app/main.mk": tiCDivMain})
	tiRunEach(t, "cdef", map[types.Target]string{types.X86_64Windows: root, types.X86_64Linux: root}, "142857571428572428\n")
}

// TestArchiveMemberDefinesTi: an archive member pulled for Helper
// also defines __udivti3, __divti3 and __umodti3; they replace the support
// pack's (the member's __divti3 marks its quotient, +1000).
func TestArchiveMemberDefinesTi(t *testing.T) {
	tools := needTools(t)
	roots := map[types.Target]string{}
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		file := "h.lib"
		if target.OS == types.OSLinux {
			file = "libh.a"
		}
		root := writeTree(t, map[string]string{
			"pkg.mk":   ffiManifest([]string{linkLit("h", "static", "lib/"+file, target.Name)}, tiCSource),
			"csrc/d.c": tiCDivHelper, "app/main.mk": tiCDivMain,
		})
		cArchive(t, tools, filepath.Join(root, "lib", file), target, []string{tiArchiveMember})
		roots[target] = root
	}
	tiRunEach(t, "ar", roots, "142857571428572432\n")
}

// TestArchiveOnlyTi: an archive whose only member defines a marked
// __udivti3 (+1000) and nothing else the bin needs is not pulled: the
// support pack's weak routine already defines the name, as any object's
// definition would, on lld-link and ld.lld. The program links and prints
// the unmarked quotient (the support pack's routine ran). This is the
// precedence rule's one case where a user's definition does not win.
func TestArchiveOnlyTi(t *testing.T) {
	tools := needTools(t)
	roots := map[types.Target]string{}
	const cdivU = "long long CDiv(long long a, long long b) {\n    volatile unsigned __int128 x = (unsigned __int128)a * 1000003;\n" +
		"    volatile unsigned __int128 y = (unsigned __int128)b;\n    return (long long)(x / y);\n}\n"
	const onlyUdiv = "typedef unsigned __int128 tu;\n" +
		"tu __udivti3(tu a, tu b) { tu q = 0, r = 0; for (int i = 127; i >= 0; i--) { r = (r << 1) | ((a >> i) & 1); if (r >= b) { r -= b; q |= (tu)1 << i; } } return q + 1000; }\n"
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		file := "u.lib"
		if target.OS == types.OSLinux {
			file = "libu.a"
		}
		root := writeTree(t, map[string]string{
			"pkg.mk":   ffiManifest([]string{linkLit("u", "static", "lib/"+file, target.Name)}, tiCSource),
			"csrc/d.c": cdivU, "app/main.mk": tiCDivMain,
		})
		cArchive(t, tools, filepath.Join(root, "lib", file), target, []string{onlyUdiv})
		roots[target] = root
	}
	tiRunEach(t, "ar-only", roots, "142857571428571428\n")
}

// TestDllDefNamesTi: a Windows dynamic link whose .def exports
// __udivti3, __divti3 and __umodti3 (a DLL that could supply them) builds
// in dev and release with no diagnostic; the support pack's weak routines
// take the names, so the executable imports nothing from that DLL (a link
// nothing uses) and runs without it.
func TestDllDefNamesTi(t *testing.T) {
	needWindows(t)
	root := writeTree(t, map[string]string{
		"pkg.mk":        ffiManifest([]string{linkLit("rt128.dll", "dynamic", "lib/rt128.def", "x86_64-windows")}, tiCSource),
		"lib/rt128.def": "__udivti3\n__divti3\n__umodti3\n",
		"csrc/d.c":      tiCDiv, "app/main.mk": tiCDivMain,
	})
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		exe := mustBuild(t, root, types.X86_64Windows, prof)
		if got := importsFrom(t, exe, "rt128.dll"); len(got) != 0 {
			t.Errorf("%v: rt128.dll imports %q, want none", prof, got)
		}
		if out := runOut(t, exe); out != tiCDivRight {
			t.Errorf("%v: stdout %q, want %q", prof, out, tiCDivRight)
		}
	}
}

// TestSoListNamesTi: in system-libc mode a list that names
// libgcc_s.so.1's __udivti3, __divti3 and __umodti3 is not refused, in dev
// and release, with C dividing __int128 (main builds it); a C source that
// defines __udivti3 itself beside such a list still is.
func TestSoListNamesTi(t *testing.T) {
	files := func(c string) map[string]string {
		return map[string]string{
			"pkg.mk": ffiManifest([]string{linkLit("libc.so.6", "dynamic", "lib/libc.def", "x86_64-linux"),
				linkLit("libgcc_s.so.1", "dynamic", "lib/gcc.def", "x86_64-linux")}, tiCSource),
			"lib/libc.def": "close@GLIBC_2.2.5\n",
			"lib/gcc.def":  "__udivti3@GCC_3.0\n__divti3@GCC_3.0\n__umodti3@GCC_3.0\n",
			"csrc/d.c":     c,
			"app/main.mk":  tiCDivMain,
		}
	}
	root := writeTree(t, files(tiCDiv))
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		exe := mustBuild(t, root, types.X86_64Linux, prof)
		if os.Getenv("MK_LINUX") == "1" {
			if out, code := runLinuxEnv(t, exe, nil); code != 0 || out != tiCDivRight {
				t.Errorf("%v: exit %d, stdout %q, want %q", prof, code, out, tiCDivRight)
			}
		}
	}
	r, exe := ffiBuild(t, writeTree(t, files(tiCUserUdiv)), types.X86_64Linux, types.Dev, TestHooks{})
	if got := libcFaults(t, r, exe); !slices.Equal(got, []string{"__udivti3 libgcc_s.so.1"}) {
		t.Errorf("a C source's own __udivti3 beside the list: E0001 for %q, want [__udivti3 libgcc_s.so.1]\n%s", got, r.Report.Text(r.Sources))
	}
}
