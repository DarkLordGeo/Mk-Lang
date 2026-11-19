// Why: a handler reads its route's `{name}` segments, and a missing or
// malformed one is the client's fault, not the server's.
//
// What: Param (a required text) and ParamInt (a required decimal i64).
//
// How: both read the segments http.Dispatch matched and fail with
// Status(400), which http.Serve answers, when the parameter is missing or
// malformed.

import std.http

/// The path parameter name of the route's pattern. Fails with Status(400)
/// when the route has no such parameter. O(parameters); allocates nothing.
export Param(req: &http.Request, name: str): http.Error!str from req {
    v := req.Param(name) else {
        fail http.Status(code: 400)
    }
    return v
}

/// The path parameter name as a decimal i64. Fails with Status(400) when it
/// is missing or not a number. O(parameters plus the value's length).
export ParamInt(req: &http.Request, name: str): http.Error!i64 {
    v := req.ParamInt(name) else {
        fail http.Status(code: 400)
    }
    return v
}
