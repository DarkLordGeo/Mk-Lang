// verdict: reject E0910
// note: a build.File path that climbs out of the package root (reason path, which the source text alone decides)
import std.build

const Data: []u8 = build.File("../secret.txt") // <- E0910
