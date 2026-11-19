// Why: tools/dogfood is Go with no way to make a pseudo-console, so the
// terminal cases need a small Mk program that can.
// What: the manifest of ptyrun, one console bin rooted at src.
// How: no dependencies and no C; the harness builds it with the same mk as
// the dogfood under test.

const Package = pkg.Package(
    name: "ptyrun",
    bins: [pkg.Bin(name: "ptyrun", root: "src", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
