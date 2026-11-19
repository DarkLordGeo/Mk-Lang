// Why: orderbook is built as an ordinary Mk package, one executable.
// What: the package manifest: the orderbook bin rooted at src/main, no
// dependencies, no C sources.
// How: mk reads Package; every list but bins is empty.

const Package = pkg.Package(
    name: "orderbook",
    bins: [pkg.Bin(name: "orderbook", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
