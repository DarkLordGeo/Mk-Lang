// Why: kits/web needs a real server to show its use and to test its
// generators, which run only in a module that imports web.
//
// What: the manifest of the hello example, one console binary built from
// src.
//
// How: it depends on the framework by path ("../.."), so it always builds
// against the kit next to it.

const Package = pkg.Package(
    name: "hello",
    bins: [pkg.Bin(name: "hello", root: "src", subsystem: "console")],
    deps: [pkg.Dep(name: "web", path: "../..")],
)
