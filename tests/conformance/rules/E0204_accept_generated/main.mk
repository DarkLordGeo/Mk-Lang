// verdict: ok
// note: std's generators export the methods they write when the type is exported, so another module calls them; for a private type they stay private, and its module calls them (an exported method naming the private type would be E0412)
import std.cli
import std.csv
import std.json
import lib

Calls(a: &Arena, args: []str, w: mut Writer): json.Error|cli.Error|WriteError!bool {
    v := try json.Parse(a, "{\"x\":1,\"y\":2}")
    p := try lib.Pt.FromJson(a, &v)
    o := csv.DefaultOptions
    try lib.Pt.WriteCsvHeader(w, &o)
    try p.WriteCsv(w, &o)
    opts := try lib.Opts.Parse(a, args)
    try lib.Opts.Usage(w)
    m := lib.Hex.Find("0x1f")
    return opts.loud && lib.Hex.IsMatch("ff") && m.IsSome() && lib.Hidden(a)
}
