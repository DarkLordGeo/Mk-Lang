const Package = pkg.Package(
    name: "mtl",
    bins: [pkg.Bin(name: "mtl", root: ".", link_script: "", cpu: "", subsystem: "console", kind: "", abi: "")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["."],
    links: [pkg.Link(name: "libc.so.6", kind: "dynamic", path: "lib/libc.def", target: "x86_64-linux")],
)
