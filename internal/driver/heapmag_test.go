// Why: in a program with no task, Main's small allocations and frees must use
// carrier 0's magazines (std/rt/heap.mk, MagOf), not the locked shared cache.
// What: TestHeapMainUsesMagazine, the program heapMagProgram and the thread
// programs in testdata/heapmag (mt for Windows, mtl for Linux).
// How: builds whole programs, since test blocks run as tasks, and tells a free
// into a magazine from a free to the page by the word the page's free list writes.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/mono"
	"mk/internal/types"
)

// heapMagProgram prints "kept 4 of 4, bad 0" when Main's frees go into a
// magazine: Kept writes a pattern in a block's first word, frees the block
// and reads the word again, and a free to the block's page (the shared
// cache) writes the page's free-list link there, so with the shared cache
// it prints "kept 0 of 4". Each allocation popped or refilled its class's
// magazine, so the free that follows is a push, never a drain, which would
// write the link too. The freed block stays committed (only an idle
// carrier's trim decommits a free page, and the program has no carrier),
// so the read is of mapped memory. Churn then keeps 64 live blocks of
// every small class and some large ones for 200,000 steps, each tagged at
// both ends and checked before its free: bad counts the tags a second
// owner overwrote and the failed allocations.
const heapMagProgram = `import std.raw

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

const Pattern: u64 = 0xA5A5_A5A5_A5A5_A5A5

Get(a: &Alloc, n: int): *u8 {
    unsafe "a raw block of n bytes, freed by Put with the same n" {
        return a.Bytes(n, 8)
    }
}

Put(a: &Alloc, p: *u8, n: int) {
    unsafe "p is a block Get gave for n bytes, freed once" {
        a.FreeBytes(p, n, 8)
    }
}

Kept(a: &Alloc, size: int): bool {
    p := Get(a, size)
    if raw.IsNull(p) {
        return false
    }
    unsafe "p is a fresh block of size >= 8 bytes" {
        raw.Write(raw.PtrCast[u8, u64](p), Pattern)
    }
    Put(a, p, size)
    unsafe "p's block was freed and is still mapped; the read is the probe" {
        return raw.Read(raw.PtrCast[u8, u64](p)) == Pattern
    }
}

Size(x: u64): int {
    if (x >> 56) & 15 == 0 {
        return 8193 + Trunc[int]((x >> 8) % 90_000)
    }
    return 1 + Trunc[int]((x % 8192) >> ((x >> 60) % 8))
}

Mark(p: *u8, n: int, tag: u64) {
    unsafe "p holds n bytes" {
        raw.Write(p, Trunc[u8](tag))
        raw.Write(raw.Offset(p, n - 1), Trunc[u8](tag >> 8))
        if n >= 16 {
            raw.Write(raw.PtrCast[u8, u64](p), tag)
            raw.Write(raw.PtrCast[u8, u64](raw.Offset(p, n - 8)), ~tag)
        }
    }
}

Marked(p: *u8, n: int, tag: u64): bool {
    unsafe "p holds n bytes" {
        if n >= 16 {
            return raw.Read(raw.PtrCast[u8, u64](p)) == tag && raw.Read(raw.PtrCast[u8, u64](raw.Offset(p, n - 8))) == ~tag
        }
        return (n == 1 || raw.Read(p) == Trunc[u8](tag)) && raw.Read(raw.Offset(p, n - 1)) == Trunc[u8](tag >> 8)
    }
}

Churn(a: &Alloc, n: int): int {
    var live: [64]*u8 = [raw.Null[u8](); 64]
    var size: [64]int = [0; 64]
    var tag: [64]u64 = [0; 64]
    var x: u64 = 0x5EED
    var bad := 0
    for _ in 0..n {
        x = Mix(x)
        i := Trunc[int](x & 63)
        if !raw.IsNull(live[i]) {
            if !Marked(live[i], size[i], tag[i]) {
                bad += 1
            }
            Put(a, live[i], size[i])
            live[i] = raw.Null[u8]()
        }
        if x & 64 != 0 {
            s := Size(Mix(x))
            p := Get(a, s)
            if raw.IsNull(p) {
                bad += 1
            } else {
                Mark(p, s, x)
                live[i] = p
                size[i] = s
                tag[i] = x
            }
        }
    }
    for i in 0..64 {
        if !raw.IsNull(live[i]) {
            if !Marked(live[i], size[i], tag[i]) {
                bad += 1
            }
            Put(a, live[i], size[i])
        }
    }
    return bad
}

export Main(sys: &System): WriteError!void {
    a := sys.heap.Alloc()
    var kept := 0
    for s in [16, 64, 1024, 8192] {
        if Kept(a, s) {
            kept += 1
        }
    }
    bad := Churn(a, 200_000)
    try sys.Print("kept {k} of 4, bad {b}\n", (k: kept, b: bad))
}
`

const heapMagPkg = `const Package = pkg.Package(
    name: "heapmag",
    bins: [pkg.Bin(name: "heapmag", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["."],
    links: [],
)
`

// heapMagNoTasks fails t unless the package at dir, built for target, has
// no task (mono's UsesTasks is false). cache is the program's cache.
func heapMagNoTasks(t *testing.T, dir string, target types.Target, cache string) {
	t.Helper()
	s, err := NewSession(Config{Root: dir, Target: target, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Check(); len(r.Report.Diags) > 0 {
		t.Fatalf("check: %s", r.Report.Text(r.Sources))
	}
	insts, ds := mono.Collect(s.w.sem5.monoEnv(s.w), s.w.buildRoots("main", s.pkgB.mods, false, false), s.w.monoOptions())
	if len(ds) > 0 {
		t.Fatalf("collect: %s", ds[0].Message)
	}
	if insts.UsesTasks() {
		t.Fatalf("%s: UsesTasks true: the program must have no task", dir)
	}
}

// heapMagCopy copies testdata/heapmag/<name> to a fresh directory and returns
// it, so a build writes nothing beside the test's sources.
func heapMagCopy(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", "heapmag", name)
	dst := filepath.Join(t.TempDir(), name)
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// heapMagBuild builds the program at dir for target and profile into a
// fresh directory with the program's cache, and returns the executable.
// The cache is shared by the program's builds and its check, which may
// run at once (the store is content-addressed and safe for concurrent
// sessions); keys differ by target and profile, so each build still
// compiles and links its own executable.
func heapMagBuild(t *testing.T, dir string, target types.Target, prof types.Profile, cache string) string {
	t.Helper()
	s, err := NewSession(Config{Root: dir, Target: target, Profile: prof, Tools: needTools(t), CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), filepath.Base(dir)+exeSuffix(target)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		skipKnownBlockers(t, r)
		t.Fatalf("build %s %s %v: %d paths\n%s", dir, target.Name, prof, len(paths), r.Report.Text(r.Sources))
	}
	return paths[0]
}

// heapMagThreads is the line testdata/heapmag/mt and mtl print when Main
// takes magazine 0, the C thread the shared cache, and no block ever has
// two owners.
const heapMagThreads = "bad 0 of 20 rounds; Main kept 4 of 4, the %s 0 of 4"

// TestHeapMainUsesMagazine: the programs have no task (UsesTasks is
// false), and built for each target in dev and release, heapMagProgram
// prints that every probed free of Main went into a magazine and no block
// had two owners, and the threads leaf (testdata/heapmag: mt, a thread
// kernel32's CreateThread starts, on x86_64-windows; mtl, a pthread, on
// x86_64-linux) that Main's frees go into a magazine and the C thread's to
// the pages, over 20 rounds of a churn whose blocks cross between the two.
// The x86_64-linux leaves build here and run only with MK_LINUX=1. Each
// program has one cache for its check and builds, and the leaves run in
// parallel (each with its own output directory; a program's leaves share
// its cache), except the x86_64-linux
// leaves under MK_LINUX=1: one wsl.exe client at a time.
func TestHeapMainUsesMagazine(t *testing.T) {
	needTools(t)
	dir := filepath.Join(t.TempDir(), "heapmag")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"main.mk": heapMagProgram, "pkg.mk": heapMagPkg} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cache := t.TempDir()
	heapMagNoTasks(t, dir, types.X86_64Windows, cache)
	threads := map[string]string{types.X86_64Windows.Name: heapMagCopy(t, "mt"), types.X86_64Linux.Name: heapMagCopy(t, "mtl")}
	who := map[string]string{types.X86_64Windows.Name: "C thread", types.X86_64Linux.Name: "pthread"}
	threadCache := map[string]string{types.X86_64Windows.Name: t.TempDir(), types.X86_64Linux.Name: t.TempDir()}
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		heapMagNoTasks(t, threads[target.Name], target, threadCache[target.Name])
		parallel := func(t *testing.T) {
			if target.Name != types.X86_64Linux.Name || !linuxRun() {
				t.Parallel()
			}
		}
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				parallel(t)
				exe := heapMagBuild(t, dir, target, prof, cache)
				out, stderr, code := runExe(t, target, exe, "")
				if code != 0 || strings.TrimSpace(out) != "kept 4 of 4, bad 0" {
					t.Errorf("exit %d, stdout %q, want \"kept 4 of 4, bad 0\"\nstderr:\n%s", code, out, stderr)
				}
			})
			t.Run(target.Name+"/"+prof.String()+"/threads", func(t *testing.T) {
				parallel(t)
				exe := heapMagBuild(t, threads[target.Name], target, prof, threadCache[target.Name])
				out, stderr, code := runExe(t, target, exe, "")
				want := fmt.Sprintf(heapMagThreads, who[target.Name])
				if code != 0 || strings.TrimSpace(out) != want {
					t.Errorf("exit %d, stdout %q, want %q\nstderr:\n%s", code, out, want, stderr)
				}
			})
		}
	}
}
