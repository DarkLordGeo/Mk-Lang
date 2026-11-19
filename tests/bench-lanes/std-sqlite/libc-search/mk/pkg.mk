// libc-search, Mk column: the kernel in kernel.c over std.libc's strlen and
// memchr. The driver compiles a CSource -ffreestanding -fno-builtin at -O3
// for --release; -march=x86-64-v2 is the contract's one CPU target, which
// the driver does not pass to C jobs.
const Package = pkg.Package(
    name: "main",
    bins: [pkg.Bin(name: "main", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "kernel.c", flags: ["-march=x86-64-v2"])],
    c_imports: [pkg.CImport(name: "kernel", headers: ["kernel.h"], defines: [], macros: [], vendor: false)],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["."],
    links: [],
)
