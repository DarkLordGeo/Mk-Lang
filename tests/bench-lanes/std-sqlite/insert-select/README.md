# sqlite-insert-select (SQLite as a C source)

The task, in pseudo-code, for every column:

```
open ":memory:"
exec "create table t(id integer primary key, v text not null)"
exec "begin"
ins = prepare "insert into t(id, v) values(?, ?)"
for i in 1..=n: bind ins(1, i), bind ins(2, "abcdefghijklmnop"), step ins, reset ins
exec "commit"
sel = prepare "select v, id from t where id = ?"
sum = 0
for k in 0..n: id = (k * 7919) mod n + 1; bind sel(1, id); step sel (a row); sum += len(text 0) + int 1; reset sel
print sum
```

7919 is prime and divides neither 2 nor 5, so the ids are a permutation of 1..n and
sum = n(n + 1) / 2 + 16n: 5,001,650,000 at n = 100,000 and 2,000,033,000,000 at n = 2,000,000.

The amalgamation (`sqlite3.c`, `sqlite3.h`) is never in the repository. It comes
from `$MK_EVIDENCE/vendor/sqlite`, through the row's `vendor` field, which tools/bench
reads (tools/bench/vendor.go).
