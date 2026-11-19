// A differential program for scoped alias facts: main.mk builds
// its views from raw pointers, so the package's one module is unsafe.
const Package = pkg.Package(
    name: "aliasdiff",
    bins: [pkg.Bin(name: "aliasdiff", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["."],
    links: [],
)
