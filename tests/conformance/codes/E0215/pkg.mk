const Package = pkg.Package(
    name: "codes",
    deps: [pkg.Dep(name: "ini", url: "https://example.com/ini.tar.gz")], // <- E0215
)
