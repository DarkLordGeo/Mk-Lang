// The amalgamation is not in the repository: sqlite3.c and
// sqlite3.h are copied from $MK_EVIDENCE/vendor/sqlite into this package's
// build copy by tools/bench (the row's "vendor" field).
// -march=x86-64-v2 is the contract's
// one CPU target (tests/bench/README.md): the driver gives a C source
// no --cpu, and a CSource's flags come last on its clang line, so this
// sets it.
const Package = pkg.Package(
    name: "main",
    bins: [pkg.Bin(name: "main", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "sqlite3.c", flags: ["-DSQLITE_OS_OTHER=1", "-DSQLITE_THREADSAFE=0", "-DSQLITE_OMIT_LOCALTIME", "-DSQLITE_DEFAULT_MEMSTATUS=0", "-DSQLITE_TEMP_STORE=3", "-DSQLITE_WITHOUT_MSIZE", "-DSQLITE_DISABLE_INTRINSIC", "-DSQLITE_OMIT_SEH", "-DSQLITE_MALLOCSIZE=malloc_usable_size", "-march=x86-64-v2"])],
    c_imports: [pkg.CImport(name: "sqlite3", headers: ["sqlite3.h"], defines: [], macros: [], vendor: false)],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
