// Why: procman is built by mk like any user package.
//
// What: the package `procman` with one console bin rooted at src/main.
//
// How: no dependencies, C sources or unsafe modules.

const Package = pkg.Package(
    name: "procman",
    bins: [pkg.Bin(name: "procman", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
