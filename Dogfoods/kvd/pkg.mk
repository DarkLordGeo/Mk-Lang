// Why: kvd's package manifest, read by mk build, mk run and mk test.
// What: one console bin, `kvd`, rooted at src/main, with no dependencies.
// How: src/snap is the one unsafe module, since it memory-maps snapshot
// files.

const Package = pkg.Package(
    name: "kvd",
    bins: [pkg.Bin(name: "kvd", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["src/snap"],
    links: [],
)
