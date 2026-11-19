// Why: the kernel is a freestanding program for x86_64-freestanding, booted
// by QEMU's PVH loader, built by mk like any other package.
//
// What: two bins: the kernel (src) and crashtest (crash), each with its own
// link script.
//
// How: a link_script makes mk build the bins for x86_64-freestanding only;
// the boot, arch and mm modules may use unsafe code.

const Package = pkg.Package(
    name: "kernel",
    bins: [
        pkg.Bin(name: "kernel", root: "src", link_script: "link.ld", cpu: "", subsystem: "console"),
        pkg.Bin(name: "crashtest", root: "crash", link_script: "crash.ld", cpu: "", subsystem: "console"),
    ],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["src/boot", "src/arch", "src/mm"],
    links: [],
)
