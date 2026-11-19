// Why: the std.libc and std.db.sqlite tests build whole packages with C
// sources, which needs scratch packages, the amalgamation and a shared cache.
// What: the harness: sqliteFlags, sqlitePkg, newSqliteRoot, mk and run.
// How: a test package is a temp directory with pkg.mk, main.mk and the
// amalgamation copied from $MK_EVIDENCE/vendor/sqlite (it is never kept in the
// repository). One mk cache is shared, as compiling the amalgamation is slow.

package std

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// sqliteFlags are the CSource flags the amalgamation is built with: the four
// that fit std.libc (no OS layer, no threads, no localtime, no memory
// statistics), a memory temp store (the VFS makes no temporary files), the
// three that keep the amalgamation off malloc.h and Windows.h under the msvc
// triple, and SQLITE_MALLOCSIZE, which has sqlite ask std.libc's
// malloc_usable_size for a block's size instead of keeping its own 8-byte
// prefix. The C headers come from the driver, generated from std.libc.
var sqliteFlags = []string{
	"-DSQLITE_OS_OTHER=1", "-DSQLITE_THREADSAFE=0", "-DSQLITE_OMIT_LOCALTIME",
	"-DSQLITE_DEFAULT_MEMSTATUS=0", "-DSQLITE_TEMP_STORE=3", "-DSQLITE_WITHOUT_MSIZE",
	"-DSQLITE_DISABLE_INTRINSIC", "-DSQLITE_OMIT_SEH", "-DSQLITE_MALLOCSIZE=malloc_usable_size",
}

// sqlitePkg is a manifest; withC adds the sqlite3 CImport and CSource.
func sqlitePkg(withC bool, unsafeMods string) string {
	cs, ci := "[]", "[]"
	if withC {
		cs = `[pkg.CSource(path: "sqlite3.c", flags: ["` + strings.Join(sqliteFlags, `", "`) + `"])]`
		ci = `[pkg.CImport(name: "sqlite3", headers: ["sqlite3.h"], defines: [], macros: [], vendor: false)]`
	}
	return "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n" +
		"    c_sources: " + cs + ",\n    c_imports: " + ci + ",\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: " + unsafeMods + ",\n    links: [],\n)\n"
}

// sqliteAmalgamation finds sqlite3.c and sqlite3.h, or skips with the
// reason.
func sqliteAmalgamation(t *testing.T) string {
	t.Helper()
	ev := os.Getenv("MK_EVIDENCE")
	if ev == "" {
		ev = filepath.Join("..", "..", "Mk-evidence")
	}
	dir := filepath.Join(ev, "vendor", "sqlite")
	for _, f := range []string{"sqlite3.c", "sqlite3.h"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Skipf("the sqlite amalgamation is not at %s (it is kept outside the repository): %v", dir, err)
		}
	}
	return dir
}

// sqliteRoot is one scratch package: a std copy, a cache, the package.
type sqliteRoot struct {
	pkg, std, cache string
}

var sqliteShared struct {
	once  sync.Once
	cache string
}

// sharedSqliteCache is one mk cache for every sqlite test of this run.
func sharedSqliteCache(t *testing.T) string {
	sqliteShared.once.Do(func() {
		d, err := packageTempDir("mksqlitecache")
		if err == nil {
			sqliteShared.cache = d
		}
	})
	if sqliteShared.cache == "" {
		return filepath.Join(t.TempDir(), "cache")
	}
	return sqliteShared.cache
}

// newSqliteRoot writes main.mk as src and the manifest; withC copies the
// amalgamation.
func newSqliteRoot(t *testing.T, src string, withC bool, unsafeMods string) *sqliteRoot {
	t.Helper()
	root := t.TempDir()
	r := &sqliteRoot{pkg: filepath.Join(root, "p"), std: copyStd(t, root, nil), cache: filepath.Join(root, "cache")}
	files := map[string]string{"main.mk": src, "pkg.mk": sqlitePkg(withC, unsafeMods)}
	if withC {
		dir := sqliteAmalgamation(t)
		for _, f := range []string{"sqlite3.c", "sqlite3.h"} {
			b, err := os.ReadFile(filepath.Join(dir, f))
			if err != nil {
				t.Fatal(err)
			}
			files[f] = string(b)
		}
		r.cache = sharedSqliteCache(t)
	}
	if err := os.MkdirAll(r.pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(r.pkg, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// mk runs mk with args in the package directory and returns its combined
// output and exit status.
func (r *sqliteRoot) mk(t *testing.T, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), mkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, mkExe(t), args...)
	cmd.Dir = r.pkg
	cmd.Env = append(os.Environ(), "MK_STD="+r.std, "MK_CACHE="+r.cache)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("mk %s: killed after %s\n%s", strings.Join(args, " "), mkTimeout, out.String())
	}
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(out.String(), "\r\n", "\n"), status
}

// run builds and runs the package on Windows (dev, or release with
// --release) and returns the program's output; a build failure fails the
// test.
func (r *sqliteRoot) run(t *testing.T, flags ...string) string {
	t.Helper()
	args := append([]string{"build", "--target=x86_64-windows"}, flags...)
	if out, st := r.mk(t, append(args, ".")...); st != 0 {
		t.Fatalf("mk build: exit %d\n%s", st, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), mkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(r.pkg, "p.exe"))
	cmd.Dir = r.pkg
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("p.exe: %v\n%s", err, out)
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n")
}
