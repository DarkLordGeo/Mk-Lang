const Package = pkg.Package(
    name: "rules",
    bins: [],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [
        pkg.Link(name: "winmm.dll", kind: "shared", path: "", target: "x86_64-windows"),               // <- E0202
        pkg.Link(name: "winmm.dll", kind: "dynamic", path: "", target: ""),                            // <- E0202
        pkg.Link(name: "winmm.dll", kind: "dynamic", path: "", target: "x86_64-freestanding"),         // <- E0202
        pkg.Link(name: "", kind: "dynamic", path: "", target: "x86_64-windows"),                       // <- E0202
        pkg.Link(name: "lib/winmm.dll", kind: "dynamic", path: "", target: "x86_64-windows"),          // <- E0202
        pkg.Link(name: "winmm.dll", kind: "dynamic", path: "lib/winmm.lib", target: "x86_64-windows"), // <- E0202
        pkg.Link(name: "zlib", kind: "static", path: "", target: "x86_64-windows"),                    // <- E0202
        pkg.Link(name: "zlib", kind: "static", path: "lib/z.a", target: "x86_64-windows"),             // <- E0202
        pkg.Link(name: "zlib", kind: "static", path: "../z.lib", target: "x86_64-windows"),            // <- E0202
    ],
)
