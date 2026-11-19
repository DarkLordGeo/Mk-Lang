// Why: raft is built as an ordinary Mk package, one executable.
// What: the package manifest: the raft bin rooted at src/main.
// How: mk reads Package; every list but bins is empty.

const Package = pkg.Package(
    name: "raft",
    bins: [pkg.Bin(name: "raft", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
