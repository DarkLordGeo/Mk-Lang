// Why: mgrep's package manifest, read by mk build, mk run and mk test.
// What: one console bin, `mgrep`, rooted at src, with no dependencies.
// How: plain std only; no unsafe modules and no C.

const Package = pkg.Package(
    name: "mgrep",
    bins: [pkg.Bin(name: "mgrep", root: "src", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
