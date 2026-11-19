// The run test of become: main.mk reads the stack pointer with asm, so
// the package's one module is unsafe.
const Package = pkg.Package(
    name: "become",
    bins: [pkg.Bin(name: "become", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["."],
    links: [],
)
