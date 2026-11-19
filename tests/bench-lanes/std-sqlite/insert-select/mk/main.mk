// sqlite-insert-select, Mk: std.db.sqlite on an in-memory database. n rows
// inserted in one transaction through one prepared statement (BindInt,
// BindText, Step, Reset), then n point selects by primary key in the
// order id = (k * 7919) mod n + 1, a permutation of 1..n. Usage:
// main <n>. Prints the checksum: the sum of each selected id and its
// text's length (README.md).

import std.db.sqlite

Insert(db: &sqlite.Db, sys: &System, n: i64): sqlite.Error!void {
    var ins := try db.Prepare(sys.arena, sys.io, "insert into t(id, v) values(?, ?)")
    defer ins.Close()
    for i in 1..n + 1 {
        try ins.BindInt(1, i)
        try ins.BindText(2, "abcdefghijklmnop")
        _ = try ins.Step(sys.io)
        ins.Reset()
    }
}

Select(db: &sqlite.Db, sys: &System, n: i64): sqlite.Error!i64 {
    var sel := try db.Prepare(sys.arena, sys.io, "select v, id from t where id = ?")
    defer sel.Close()
    var sum: i64 = 0
    for k in 0..n {
        try sel.BindInt(1, (k * 7919) % n + 1)
        if !try sel.Step(sys.io) {
            fail sqlite.Misuse
        }
        sum += Cast[i64](sel.Str(0).Len()) + sel.Int(1)
        sel.Reset()
    }
    return sum
}

Run(sys: &System, n: i64): sqlite.Error!i64 {
    db := try sqlite.Open(sys.arena, sys.io, ":memory:")
    defer db.Close()
    try db.Exec(sys.io, "create table t(id integer primary key, v text not null)")
    try db.Exec(sys.io, "begin")
    try Insert(&db, sys, n)
    try db.Exec(sys.io, "commit")
    return try Select(&db, sys, n)
}

export Main(sys: &System): sqlite.Error|ParseError|WriteError!void {
    n := try sys.args[1].ParseInt()
    sum := try Run(sys, n)
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum: sum))
}
