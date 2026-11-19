const Package = pkg.Package(
    name: "notesdb",
    bins: [pkg.Bin(name: "notesdb", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "sqlite3.c", flags: ["-DSQLITE_OS_OTHER=1", "-DSQLITE_THREADSAFE=0", "-DSQLITE_OMIT_LOCALTIME", "-DSQLITE_DEFAULT_MEMSTATUS=0", "-DSQLITE_TEMP_STORE=3", "-DSQLITE_WITHOUT_MSIZE", "-DSQLITE_DISABLE_INTRINSIC", "-DSQLITE_OMIT_SEH", "-DSQLITE_MALLOCSIZE=malloc_usable_size"])],
    c_imports: [pkg.CImport(name: "sqlite3", headers: ["sqlite3.h"], defines: [], macros: [], vendor: false)],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
