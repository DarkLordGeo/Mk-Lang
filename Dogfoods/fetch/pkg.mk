// Why: fetch's package manifest, read by mk build, mk run and mk test.
// What: one console bin, `fetch`, rooted at src/main, with no dependencies.
// How: plain std only; TLS, HTTP, WebSocket and compression all come from
// std.

const Package = pkg.Package(
    name: "fetch",
    bins: [pkg.Bin(name: "fetch", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
