// The cookbook's "Call a function in a
// Windows DLL" as a package, winmm.dll named by links.
const Package = pkg.Package(
    name: "winmm",
    bins: [pkg.Bin(name: "winmm", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["app"],
    links: [pkg.Link(name: "winmm.dll", kind: "dynamic", path: "", target: "x86_64-windows")],
)
