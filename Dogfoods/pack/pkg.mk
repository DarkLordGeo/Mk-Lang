// Why: pack is built by mk like any user package, so its manifest is the
// whole build description.
//
// What: the package `pack` with one console bin rooted at src/main.
//
// How: no dependencies, no C sources and no unsafe modules: everything pack
// does comes from std.

const Package = pkg.Package(
    name: "pack",
    bins: [pkg.Bin(name: "pack", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
