// libm through links and std.dl: a Linux bin in system-libc mode
// that calls libm.so.6 through its links and through std.dl. The .defs pin
// cos and hypot at glibc's base version (GLIBC_2.2.5 on x86_64, GLIBC_2.17
// on aarch64), which glibc 2.34 has (2.39's default hypot is GLIBC_2.35,
// above the glibc 2.34 floor).
const Package = pkg.Package(
    name: "libm",
    bins: [pkg.Bin(name: "libm", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["app"],
    links: [
        pkg.Link(name: "libm.so.6", kind: "dynamic", path: "lib/libm.def", target: "x86_64-linux"),
        pkg.Link(name: "libm.so.6", kind: "dynamic", path: "lib/libm.aarch64.def", target: "aarch64-linux"),
    ],
)
