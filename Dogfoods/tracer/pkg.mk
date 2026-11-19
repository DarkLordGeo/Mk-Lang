// Why: tracer is built as one package with one console program.
//
// What: The package description: the bin `tracer`, rooted at src/main, with no
// dependencies, C sources or unsafe modules.
//
// How: A plain pkg.Package value; the build reads it, nothing runs it.

const Package = pkg.Package(
    name: "tracer",
    bins: [pkg.Bin(name: "tracer", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
