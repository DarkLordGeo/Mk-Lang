const Package = pkg.Package(
    name: "rules",
    bins: [],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["."],
    links: [pkg.Link(name: "zstd.dll", kind: "dynamic", path: "lib/zstd.def", target: "x86_64-windows")],
)
