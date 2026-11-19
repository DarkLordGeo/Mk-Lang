// Why: std is built like any other package, from a manifest.
// What: Package, std's manifest: the per-target modules and the modules
// allowed unsafe code.
// How: each target OS selects its own sys and linux modules (sys/windows,
// sys/linux, free/sys, sys/nutshell); unsafe_modules lists the std modules
// that may hold unsafe blocks.

const Package = pkg.Package(
    name: "std",
    bins: [],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [
        pkg.Select(os: "windows", modules: [pkg.Module(name: "sys", dir: "sys/windows"), pkg.Module(name: "linux", dir: "linux/other")]),
        pkg.Select(os: "linux", modules: [pkg.Module(name: "sys", dir: "sys/linux"), pkg.Module(name: "linux", dir: "linux/linux")]),
        pkg.Select(os: "freestanding", modules: [pkg.Module(name: "sys", dir: "free/sys"), pkg.Module(name: "linux", dir: "linux/other")]),
        pkg.Select(os: "nutshell", modules: [pkg.Module(name: "sys", dir: "sys/nutshell"), pkg.Module(name: "linux", dir: "linux/other")]),
    ],
    unsafe_modules: ["math", "core", "rt", "sync", "sys/windows", "sys/linux", "task", "coll", "mem", "sim", "dyn", "libc", "db/sqlite", "http", "simd", "simd/kernel", "free", "free/sys", "sys/nutshell", "x509", "runtime", "vm"],
    links: [],
)
