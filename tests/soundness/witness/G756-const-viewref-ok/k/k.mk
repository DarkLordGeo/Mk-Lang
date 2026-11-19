import std.url

export struct L {
    export d: []u8
    export p: ?&url.Url
}

export const D: L = L(d: [], p: none)
