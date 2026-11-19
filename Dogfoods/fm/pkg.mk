// Why: fm's package manifest, read by mk build, mk run and mk test.
// What: one console bin, `fm`, rooted at src/main, with no dependencies.
// How: plain std only; no unsafe modules and no C.

const Package = pkg.Package(
    name: "fm",
    bins: [pkg.Bin(name: "fm", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
