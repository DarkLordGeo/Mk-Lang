// libc from tasks: a Linux bin in
// system-libc mode whose tasks call libc on every carrier. Its
// libc.so.6 link adds four names to std's list (the lists merge).
const Package = pkg.Package(
    name: "libc",
    bins: [pkg.Bin(name: "libc", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["app"],
    links: [pkg.Link(name: "libc.so.6", kind: "dynamic", path: "lib/libc.def", target: "x86_64-linux")],
)
