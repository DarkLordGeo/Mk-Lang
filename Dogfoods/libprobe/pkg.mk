// Why: libprobe's package manifest, read by mk build, mk run and mk test.
// What: one console bin, `libprobe`, a per-target `plat` module, two unsafe
// modules, and version.dll in `links`.
// How: on Windows the `version` command reads file versions through
// version.dll, which mk does not search by itself, so `links` names it;
// `src/plat` is chosen per target, so the Linux build names no Windows
// function.
const Package = pkg.Package(
    name: "libprobe",
    bins: [pkg.Bin(name: "libprobe", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [
        pkg.Select(os: "windows", modules: [pkg.Module(name: "plat", dir: "src/plat/windows")]),
        pkg.Select(os: "linux", modules: [pkg.Module(name: "plat", dir: "src/plat/linux")]),
    ],
    // a module that `targets` selects is listed by its directory, not its name
    unsafe_modules: ["src/probe", "src/plat/windows"],
    links: [pkg.Link(name: "version.dll", kind: "dynamic", path: "", target: "x86_64-windows")],
)
