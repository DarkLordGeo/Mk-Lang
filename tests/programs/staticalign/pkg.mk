// Consts and vars aligned over a page sit at their
// alignment on x86_64 Windows, in dev and release (staticalign.cases).
// app is in unsafe_modules for std.raw's address reads.
const Package = pkg.Package(
    name: "staticalign",
    bins: [pkg.Bin(name: "staticalign", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["app"],
    links: [],
)
