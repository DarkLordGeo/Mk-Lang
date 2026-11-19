// verdict: reject E0604
// rule: D-456
// note: Response.Header and Body store a loan of self into self, so a handler that calls them states *res into res; without it each call is E0604 (the accept twin: D456_accept_handler_clause)
import std.http

struct App {
    name: str
}

Reply(c: &App, a: &Arena, io: Io, req: &http.Request, res: mut http.Response): http.Error!void {
    try res.Header("content-type", "text/plain") // <- E0604
    try res.Body(b"ok\n")                        // <- E0604
}

export Main(sys: &System): void {
}
