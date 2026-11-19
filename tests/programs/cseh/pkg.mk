// A package's C source that calls the support pack's memcpy
// and memset inside __try and handles their faults in its own __except
// (such C code keeps working beside Mk's fault handling). The cases are
// in cseh.cases beside this directory.
const Package = pkg.Package(
    name: "cseh",
    bins: [pkg.Bin(name: "cseh", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "lib/seh.c", flags: [])],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["ext"],
    links: [],
)
