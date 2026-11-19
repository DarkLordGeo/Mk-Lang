// Why: textkit is a set of Unicode text tools; ucdcases builds its cases.
// What: the package manifest: the textkit and ucdcases bins.
// How: mk reads Package; no dependencies or C sources.

const Package = pkg.Package(
    name: "textkit",
    bins: [
        pkg.Bin(name: "textkit", root: "src/main", link_script: "", cpu: "", subsystem: "console"),
        pkg.Bin(name: "ucdcases", root: "src/ucdcases", link_script: "", cpu: "", subsystem: "console"),
    ],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
