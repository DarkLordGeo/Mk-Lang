const Package = pkg.Package(
    name: "kernel",
    bins: [pkg.Bin(name: "kernel", root: "src", link_script: "link.ld", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "c/sum.c", flags: [])],
    c_imports: [],
    asm_sources: [pkg.AsmSource(path: "boot.s", target: "x86_64-freestanding")],
    targets: [],
    unsafe_modules: ["src"],
    links: [],
)
