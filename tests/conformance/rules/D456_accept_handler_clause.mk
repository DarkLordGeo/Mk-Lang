// verdict: ok
// rule: D-456
// note: std.http's handler type states *res into res; a handler that sets a header or a body writes the clause, one that only fails needs none, and both fit a Route
import std.http

struct App {
    greeting: str
}

Greet(c: &App, a: &Arena, io: Io, req: &http.Request, res: mut http.Response): http.Error!void *res into res {
    try res.Header("content-type", "text/plain")
    try res.Body(b"hello\n")
}

Gone(c: &App, a: &Arena, io: Io, req: &http.Request, res: mut http.Response): http.Error!void {
    fail http.Status(code: 410)
}

const Routes: []http.Route[App] = [
    http.Route[App](method: "GET", pattern: "/hello", handler: Greet),
    http.Route[App](method: "GET", pattern: "/old", handler: Gone),
]

Handle(c: &App, a: &Arena, io: Io, req: &http.Request, res: mut http.Response): http.Error!void *res into res {
    try http.Dispatch(a, io, Routes, c, req, res)
}

Run(sys: &System): http.Error!void {
    app := App(greeting: "hello")
    try http.Serve(sys, "127.0.0.1:0", 1_000, &app, Handle)
}

export Main(sys: &System): void {
    _ = Routes.Len()
}
