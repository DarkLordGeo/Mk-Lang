// Report generator: reads name,amount lines,
// totals them per name with a Map, sorts by total then name, and prints a title built
// with Text and Arena.Fmt, then one row per name with the total in decimal and hex and
// its share of the grand total to two places. Input: report.in; output: report.out.

import std.fs

error BadLine(line: i64)

struct Row {
    name: str
    total: i64
}

ByTotal(a: &Row, b: &Row): bool {
    if a.total != b.total {
        return a.total > b.total
    }
    return a.name < b.name
}

Sum(a: &Arena, text: str into a): BadLine|OutOfMemory!List[Row] from a, text {
    var totals := Map[str, i64].New(a)
    var lines := text.Lines()
    var n: i64 = 0
    for line := lines.Next() {
        n += 1
        if line.Len() == 0 {
            continue
        }
        var parts := line.Split(",")
        name := parts.Next() else {
            fail BadLine(line: n)
        }
        field := parts.Next() else {
            fail BadLine(line: n)
        }
        amount := field.Trim().ParseInt() else {
            fail BadLine(line: n)
        }
        t := try totals.Ensure(name.Trim(), 0)
        *t += amount
    }
    var rows := List[Row].New(a)
    for (key, value) in totals.Iter() {
        try rows.Push(Row(name: *key, total: *value))
    }
    rows.Sort(ByTotal)
    return rows
}

export Main(sys: &System): fs.Error|BadLine|OutOfMemory|WriteError!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    rows := try Sum(sys.arena, text)
    var grand: i64 = 0
    for r in rows {
        grand += r.total
    }
    var title := Text.New(sys.arena)
    try title.Push("Report: ")
    count := try sys.arena.Fmt("{n} names, total {grand}", (n: rows.Len(), grand))
    try title.Push(count)
    var out := sys.Stdout()
    try Print(mut out, "{t}\n", (t: title.Str()))
    for r in rows {
        share := Cast[f64](r.total) / Cast[f64](grand)
        try Print(mut out, "{name} {total} {hex:x} {share:.2}\n", (name: r.name, total: r.total, hex: r.total, share))
    }
}
