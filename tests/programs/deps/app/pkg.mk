const Package = pkg.Package(
    name: "app",
    bins: [pkg.Bin(name: "app", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [pkg.Dep(name: "units", path: "../units", url: "", hash: "")],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
