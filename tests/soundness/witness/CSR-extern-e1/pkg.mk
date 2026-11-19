const Package = pkg.Package(
    name: "csr-extern-e1",
    bins: [pkg.Bin(name: "csr-extern-e1", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "f.c", flags: [])],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["."],
    links: [],
)
