// Why: imgproc is an image pipeline over PNM files with SIMD kernels.
// What: the package manifest: the imgproc bin rooted at src/main.
// How: src/dispatch is the one unsafe module, as it names the kernels
// compiled for CPU features as function values.

const Package = pkg.Package(
    name: "imgproc",
    bins: [pkg.Bin(name: "imgproc", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["src/dispatch"],
    links: [],
)
