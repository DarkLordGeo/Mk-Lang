// Why: web's generators run only in a module that imports web, so their
// tests live in this example.
// What: tests of `gen web.Body`, `gen web.Validate` and the whole server's
// handler.
// How: builds requests by hand and checks the status and the text sent.

import std.http
import src.order

const NoHeaders: []http.Header = []

// Post is a POST of body to path.
Post(path: str, body: str): http.Request from path, body {
    return http.Request(method: "POST", path: path, query: "", headers: NoHeaders, params: NoHeaders, body: body.AsBytes())
}

// StatusOf is the code of a Status failure, else -1.
StatusOf(e: http.Error): int {
    match e {
        http.Status(code) {
            return code
        }
        _ {
            return -1
        }
    }
}

// Sent is what res sends, as text.
Sent(a: &Arena, io: Io, res: &http.Response): http.Error!str from a {
    var t := Text.New(a)
    var w := t.Writer()
    try res.Send(io, mut w)
    return try a.Dup(t.Str())
}

test "Body reads a JSON body into the struct" (sys: &System) {
    req := Post("/items", "{\"name\": \"bolt\", \"qty\": 12, \"extra\": true}")
    n := try NewItem.FromRequest(sys.arena, &req)
    Assert(n.name == "bolt")
    Assert(n.qty == 12)
}

test "Body fails 400 on text that is not JSON or not the struct" (sys: &System) {
    bad := ["{\"name\": ", "[1, 2]", "{\"qty\": 1}", "{\"name\": 3, \"qty\": 1}"]
    for b in bad {
        req := Post("/items", b)
        _ = NewItem.FromRequest(sys.arena, &req) else e {
            Assert(StatusOf(e) == 400)
            continue
        }
        Assert(false)
    }
}

test "Validate passes a struct within its rules" {
    n := NewItem(name: "bolt", qty: 1000)
    Assert(n.Problem().IsNone())
    try n.Validate()
}

test "Validate names the first rule broken, in the order written" {
    Assert(NewItem(name: "", qty: 5).Problem().Must() == "name: must not be empty")
    Assert(NewItem(name: "", qty: -1).Problem().Must() == "name: must not be empty")
    long := "0123456789012345678901234567890123456789012345678901234567890123456789"
    Assert(NewItem(name: long, qty: 5).Problem().Must() == "name: must be at most 64 bytes long")
    Assert(NewItem(name: "a", qty: -1).Problem().Must() == "qty: must be at least 0")
    Assert(NewItem(name: "a", qty: 1001).Problem().Must() == "qty: must be at most 1000")
}

test "Validate fails 422 when a rule is broken" {
    n := NewItem(name: "a", qty: 1001)
    n.Validate() else e {
        Assert(StatusOf(e) == 422)
        return
    }
    Assert(false)
}

test "the server answers a POST with the item, the middleware's field and 201" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    app := App(served_by: "mk-web")
    req := Post("/items", "{\"name\": \"nut\", \"qty\": 3}")
    var res := http.Response.New(&ar)
    try Handle(&app, &ar, sys.io, &req, mut res)
    Assert(try Sent(&ar, sys.io, &res) == "HTTP/1.1 201 Created\r\nX-Served-By: mk-web\r\nContent-Type: application/json\r\nContent-Length: 29\r\n\r\n{\"id\":1,\"name\":\"nut\",\"qty\":3}")
}

test "the server refuses an item that breaks a rule with 422" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    app := App(served_by: "mk-web")
    req := Post("/items", "{\"name\": \"\", \"qty\": 3}")
    var res := http.Response.New(&ar)
    Handle(&app, &ar, sys.io, &req, mut res) else e {
        Assert(StatusOf(e) == 422)
        return
    }
    Assert(false)
}

test "Body and Validate of an exported type are called from another module" (sys: &System) {
    req := Post("/orders", "{\"item\": \"nut\", \"count\": 0}")
    o := try order.Order.FromRequest(sys.arena, &req)
    Assert(o.Problem().IsSome())
    _ = o.Validate() else e {
        Assert(StatusOf(e) == 422)
        return
    }
    Assert(false)
}
