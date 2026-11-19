// The cookbook's "Load a library at run
// time" as a package with a Main. std.raw's cast is the caller's unsafe,
// so app is listed in unsafe_modules; no link is named: std.dl loads it.
const Package = pkg.Package(
    name: "dl",
    bins: [pkg.Bin(name: "dl", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["app"],
    links: [],
)
