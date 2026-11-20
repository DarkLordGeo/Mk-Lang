// Why: web's generators export what they write for an exported type, so a
// module other than the type's calls them; the tests in src do.
// What: Order, an exported request body with its codec, Body and Validate.
// How: the gen lines only; tests_hello.mk calls the generated functions.

import std.json
import web

/// An order, as a client posts it.
export struct Order {
    export item: str
    export count: i64
}

gen json.Codec(Order)
gen web.Body(Order)
gen web.Validate(Order, "item: nonempty; count: min 1")
