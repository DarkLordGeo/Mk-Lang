// Why: web applications want routing, middleware and request helpers above
// std.http, without std itself growing a framework.
//
// What: the manifest of kits/web, the first-party web framework, package
// "web".
//
// How: a package of its own, safe Mk on std alone: it needs no grant, so
// `mk install web` adds it with nothing to approve.

const Package = pkg.Package(name: "web", mk: "0.1")
