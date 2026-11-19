# httpapi (app, concurrency)

**Task.** A JSON API over HTTP/1.1 keep-alive, built on each language's std HTTP server. It is shaped like Dogfoods/notes, but its store is kept in memory, because Go's cell cannot link SQLite with `CGO_ENABLED=0`. `httpapi <port>` listens on `127.0.0.1:<port>` and serves until it is killed. The spec is `httpapi.json`, not `bench.json`: the row has its own measurement, `tools/bench http -row httpapi` (`tools/bench/serverbench.go` and `apibench.go`), as the idiomatic `http` row has. The Mk side is a new program, `mk/httpapi.mk`. It was written from the documents only (doc/reference.md's module map and library surface, with Dogfoods/notes as user code), as an outside user would write it, and it is frozen before its first publish.

**The API.** An item is `{"id": i64, "name": str, "tag": str, "qty": i64}`. Its rules:

- A name is 1 to 64 bytes.
- A tag is 1 to 32 bytes of `a` to `z`, `0` to `9` and `-`.
- `qty` is 0 to 1,000,000,000.
- Ids start at 1 and are never reused.

| Request | Response |
|---|---|
| `POST /items` with `{"name","tag","qty"}` | 201, `Location: /items/<id>`, the item under the next id |
| `GET /items/{id}` | 200 and the item, or 404 |
| `PUT /items/{id}` with `{"name","tag","qty"}` | 200 and the item with its fields replaced, or 404 |
| `DELETE /items/{id}` | 200 and `{"deleted":true}`, or 404 |
| `GET /items?tag=T&limit=N` | 200 and `{"items":[...],"total":M}`: the tag's items by id, at most N of them (1 to 100, 20 when absent), and M, how many the tag has |

Errors are `{"message": ...}`, with these exact texts:

- 400: `the body is not UTF-8`, `the body is not JSON`, `an item id is a number`, `tag is 1 to 32 bytes of a-z, 0-9 and -`, `limit is 1 to 100`.
- 422 when the JSON is not an item: `an item is an object with a string name and tag and an integer qty`. Unknown members are ignored, and a missing one is an error.
- 422 when an item breaks the rules above: `an item needs a name of 1 to 64 bytes, a tag of 1 to 32 bytes of a-z, 0-9 and -, and a qty of 0 to 1000000000`.
- 404: `no such item`.

An id and a limit are an optional `+` or `-` and then decimal digits, as Mk's `str.ParseInt` and Go's `strconv` read them, so `+1` and `01` name item 1 in every column. A JSON member given twice with values of the same JSON type keeps its last value in every column; a repeat with another type is outside the load's alphabet (below).

**Outside the load's alphabet.** The rules above are the row's contract for what the load sends. Two differences outside it are known and stated, not removed. Go's `json.Marshal` writes `<`, `>` and `&` in a string as `\u003c`, `\u003e` and `\u0026`, while Mk and Zig write them raw. The load's names hold none of them. And a member given twice with values of two JSON types is answered three ways, since Mk's codec types every occurrence, Zig's std.json keeps only the last one (`duplicate_field_behavior = .use_last` on a `Value` parse, which cannot type the earlier ones), and Go's decoder refuses a wrongly typed earlier member but accepts `null` before a value: `{"qty":"5","qty":1}` and `{"name":7,"name":"ok"}` are 422 in Mk and Go and 201 in Zig, and `{"qty":null,"qty":3}` is 422 in Mk and 201 in Go and Zig (a probe, `api2-diff.txt`). The load never repeats a member. The routers' own 405 and 404 answers also differ ("What differs by language").

Query values are taken raw, not decoded, since the API's tags and limits need no escapes. Every body is compact JSON with its members in the order above. A body of 256 bytes or more (uncompressed) is gzip-compressed, with `Content-Encoding: gzip`, when the request's `Accept-Encoding` names gzip, at each std's default level; a shorter one never is. Every response has `Content-Type: application/json` and a `Content-Length`.

**The store.** Each column keeps its items in a hash map from id to item, behind a reader-writer lock, with the next id beside them:

- a POST, PUT or DELETE takes the write lock;
- a GET or a list takes the read lock;
- a list scans the whole map for the tag and sorts the matches by id.

Each connection keeps at most 12 items, so the map holds at most 3,072 and a list's scan stays the same size through a run.

**Load.** tools/bench's route mix (`apibench.go`), a raw HTTP/1.1 client on Go's `net.Conn`, one goroutine per connection. It runs 256 keep-alive connections with one request outstanding on each. Connection `i` keeps its own items under its own two tags, `c<i>-a` and `c<i>-b`. It draws its requests from splitmix(seed + i), with seed 3838:

| Share | Request |
|---|---|
| 25% | `POST`, a new item. When the connection already has 12 items, the oldest is deleted instead |
| 30% | `GET` by id |
| 20% | a list of one of its tags with a limit of 1 to 10 |
| 13% | `PUT`, new fields and either tag |
| 10% | `DELETE` |
| 2% | a `POST` whose `qty` is -1 (422) |

- A `GET`, `PUT` or `DELETE` names a live item, except one time in (live items + 1), when it names the connection's last deleted id, for a 404.
- Names are 4 to 48 bytes of lower-case letters, digits and inner spaces, so nothing needs a JSON escape.
- Half the requests accept gzip.

Every response is checked against the connection's model of its items:

- the status;
- `Content-Type`;
- the `Location` of a created item;
- that the body is gzip-compressed exactly when it should be;
- the body itself, byte for byte, after Go's `compress/gzip` has decoded it.

The id of a created item is the one value the model cannot predict: the generator reads it from the response, and it must be larger than every id the connection was given before.

**Measurement.** As in `http`:

- 5 s of warm-up, then 30 s measured;
- req/s, p50 and p99;
- three sweeps in rotated column order, each with a fresh server.

`tools/bench http -row httpapi -publish` holds the machine lock and pins the server to `0xff` and the generator to `0xff00`. GOMAXPROCS is 8, and `MK_CARRIERS=8`. Connections close with a reset (linger 0), and 256 connections hold 512 sockets at once, below the 10,000-socket cap.

**Ordering.** The Mk cell of `idiomatic/http` (hello world) is published first, so that the server core is measured apart from the JSON and gzip work this row adds. That order does not block the row.

**Cells.**

| Column | Kind | Server | Model |
|---|---|---|---|
| mk | std | `mk/httpapi.mk`: `std.http.Serve` and `Dispatch` with a `const` route table, one arena per request; `json.Parse`, then `FromJson` of a `gen json.Codec` type; `FormatJson`; `compress.Gzip`; a `HeapMap[i64, Stored]` of `HeapText` behind `sync.RwLock`. A list copies the matches' text into a sub-arena, since the stored text is the lock's | tasks, `MK_CARRIERS=8` |
| go | std | `testdata/httpapi.go`: `net/http` with a `ServeMux` of Go 1.22 method and wildcard patterns, `json.Valid`, then `json.Unmarshal` into a struct of pointers (so a missing member is told from a zero one); `json.Marshal`; `compress/gzip`, its writers reused through a `sync.Pool`; a map behind `sync.RWMutex` | goroutines, GOMAXPROCS 8 |
| zig-fast, zig-safe | std | `httpapi.zig`: `std.http.Server` over `std.Io.net` with `Io.Group.concurrent` as the http row, an `ArenaAllocator` reset per request, `std.json` into a `Value` and then into the item (unknown members ignored), `std.json.Stringify`, `std.compress.flate.Compress` with the gzip container, an `AutoHashMapUnmanaged` behind `std.Io.RwLock` | OS threads |
| c | | none: no std HTTP (C's std has no sockets) | |
| rust | | none: no std HTTP (`std::net` is TCP only). An HTTP server is a crate (hyper, or axum over tokio), and none is in the local cargo registry, which holds `serde_json` and `flate2` (checked 2026-11-16), so an `eco` cell needs its crates fetched and pinned first | |

**What differs by language.**

- **The 405 answers.** The routers answer a method a path does not have with 405 and an `Allow` header in their own words, and an unknown path with 404. The load sends neither.
- **No-delay.** Go's `net` sets no-delay on every TCP connection. Mk's `std.http.Serve` does not: it listens through `net.Listen`, whose listener has `no_delay` false (std/net/net.mk), so the Mk cell keeps Nagle's algorithm, and a program on `Serve` cannot change that. Zig's twin keeps it too: `std.Io.net` has no such option (zig 0.17.0), though Zig's std could set it through `std.os.windows.ws2_32.setsockopt` with `TCP.NODELAY`, which the twin does not do, as in `app/kvserve`. Mk's `Response.Send` writes the head and the body into one 4 KiB writer and flushes once, and Zig's `respond` goes through a 4 KiB writer too (both read from the source, not seen on the wire), so each response is one send with nothing else unacknowledged on the connection, and Nagle's algorithm should not hold it back.
- **The compressor for each gzip response.** Each cell sets up its compressor differently, and the setup is part of its work:
  - Mk's `compress.Gzip` takes 448 KiB of scratch from the request's arena on each call, which the arena zero-fills (std/compress/deflate.mk, std/mem/mem_arena.mk);
  - Go takes a `gzip.Writer` from a `sync.Pool` and `Reset`s it, as Go servers reuse their compressors: a new `gzip.NewWriter` would allocate about 1 MiB per response (measured with go1.27.3's allocation count);
  - Zig keeps one 64 KiB deflate window per connection, and `flate.Compress.init` sets up its hash heads on each response.
- **The routers' own answers.** Go's `ServeMux` writes a text body with its 405 and 404. Mk and Zig send none. In a probe, the Zig twin closed the connection after a 405, so its 404 for an unknown path that followed on the same connection was not seen. The load sends neither request.
- **Where the JSON is written.** Go copies the matching items out under the read lock and encodes them after releasing it. Mk and Zig write a list's and a GET's JSON while they hold the lock.

**Checks.** `tools/bench http -row httpapi -check` builds every column and runs 300 requests on each of the 256 connections, every response checked, and times nothing. With an mk built from the tree it passed in every column (2026-11-16, unpinned), and again after a later fix, with the Go twin's pooled compressors and the Zig twin's typed members (2026-11-16 01:55). In each column the 76,800 responses held 15,366 lists, 3,315 404s and 1,798 422s. The gzip bodies numbered 3,094 in three columns and 3,088 in zig-fast at the first run, and 3,093 (mk), 3,090 (zig-fast) and 3,094 (zig-safe, go) at the second: whether a list reaches 256 bytes depends on how many digits its ids have, and ids are given out across all the connections in the order the server sees them. `TestAPIGoTwinAndMutants` builds the Go twin, checks it the same way, and requires lists, gzip bodies, 404s and 422s among its responses. Then it puts a proxy between the generator and a fresh twin and makes one change at a time; each change fails the check:

- gzip dropped;
- a byte of a body changed;
- the `Location` removed;
- a 404 sent as 200.

No number of this row has been measured.
