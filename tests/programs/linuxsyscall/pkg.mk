// std.linux's raw system call, x86_64-linux only: an import of std.linux on another target
// is E0202 (tests/conformance/rules/D919_reject_linux_off_target.mk), and
// a call is unsafe only (E1101), so the program is a package whose app module is in
// unsafe_modules. linuxsyscall.cases beside this directory runs it on x86_64-linux only.
const Package = pkg.Package(
    name: "linuxsyscall",
    bins: [pkg.Bin(name: "linuxsyscall", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["app"],
    links: [],
)
