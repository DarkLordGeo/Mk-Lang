// libm through links and std.dl: a Linux bin in system-libc mode
// that calls libm.so.6 through its links and through std.dl. The .def pins
// cos and hypot at GLIBC_2.2.5, the versions glibc 2.34 has (2.39's
// default hypot is GLIBC_2.35, above the glibc 2.34 floor).
const Package = pkg.Package(
    name: "libm",
    bins: [pkg.Bin(name: "libm", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["app"],
    links: [pkg.Link(name: "libm.so.6", kind: "dynamic", path: "lib/libm.def", target: "x86_64-linux")],
)
