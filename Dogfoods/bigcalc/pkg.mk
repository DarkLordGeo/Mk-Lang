// Why: bigcalc is built by mk like any user package.
//
// What: the package `bigcalc` with one console bin rooted at src/main.
//
// How: no dependencies, C sources or unsafe modules.

const Package = pkg.Package(
    name: "bigcalc",
    bins: [pkg.Bin(name: "bigcalc", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
