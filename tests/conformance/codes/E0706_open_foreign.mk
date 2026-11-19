// verdict: reject E0706
// note: `open` over an error set with a member of another package: error sets are structural, so only the package that declares every member writes the mark (reason: open-foreign)
import std.fs

export error Mine

export open type Mixed = fs.Error | Mine // <- E0706

export open type Theirs = fs.Error // <- E0706
