// Why: every option is checked before a replay starts, with a usage message.
// What: Options, ParseArgs, the BadArgs error and the per-option parsers.
// How: hand-written parsing of the argument list; each value is range-checked
// by Count, Seed, Positive, NonNegative, AccountArg and BandArg.

import src.feed

const Usage: str = """
    usage: orderbook replay [options] FEED
           orderbook replay [options] --synth N [--seed S]
           orderbook synth --synth N [--seed S]
           orderbook --help

    Replays a market-data feed through a price-time priority matching
    engine and reports the book, the trades and statistics over them.

    commands:
      replay            run the engine over FEED, or over N synthetic commands
      synth             write N synthetic commands as a feed to standard output

    options:
      --synth N         generate N commands instead of reading a feed
      --seed S          the generator's seed (default 1)
      --depth N         price levels per side in the depth table (default 5)
      --bars N          trades per bar (default 1000)
      --band LO:HI      show the book and the traded volume from LO to HI
      --account A       show where account A ranks by volume
      --snapshot N      snapshot the top of the book every N commands
                        (default 10000; 0 never)
      --memory-limit B  give the engine at most B bytes of heap
      --json PATH       also write the report as JSON to PATH (- for stdout)
      --book PATH       start from the resting orders of the book file PATH
      --book-out PATH   write the book before the close as a book file to
                        PATH (- for stdout)
      --fills PATH      write every fill as CSV to PATH (- for stdout)
      --trace           print every fill as it happens
      --quiet           print only the checksum line
      --help            print this help

    feed format, CSV (RFC 4180), one command a record; a record whose first
    field starts with `#` is a comment, so it holds no `"` unless quoted:
      N,ts,acct/serial,B|S,qty,price          limit order
      M,ts,acct/serial,B|S,qty                market order
      S,ts,acct/serial,B|S,qty,stop           stop order
      T,ts,acct/serial,B|S,qty,stop,limit     stop-limit order
      C,ts,acct/serial                        cancel
      A,ts,acct/serial,qty,price              amend size and price
      H,ts,acct                               halt an account
      R,ts,acct                               resume an account
      X,ts                                    bust the last trade

    book file, CSV with this header, one resting order a row (kind is
    limit, stop or stop-limit; tag is free text, quoted when it holds a
    comma, a quote or a line break):
      id,side,kind,qty,price,stop,ts,tag
    the fills file has the header
      ts,taker,maker,side,price,qty,taker_tag,maker_tag

    exit status: 0 done, 1 the --account did not trade, 2 bad command line,
    3 bad feed or book file, 4 file error, 5 out of memory
    """

/// What the command line asks for.
struct Options {
    command: str
    path: ?str
    synth: ?int
    seed: u64
    depth: int
    bars: int
    band: ?(lo: i64, hi: i64)
    account: ?u32
    snapshot: int
    memory_limit: ?int
    json: ?str
    book: ?str
    book_out: ?str
    fills: ?str
    trace: bool
    quiet: bool
    help: bool
}

/// A command-line mistake, with the argument's position from 1.
error BadArgs(at: int)

/// Parses `args`, the arguments after the program name. On a mistake,
/// writes a message to `err` and fails.
ParseArgs(args: []str, err: mut Writer): BadArgs!Options from args {
    var o := Options(
        command: "",
        path: none,
        synth: none,
        seed: 1,
        depth: 5,
        bars: 1000,
        band: none,
        account: none,
        snapshot: 10_000,
        memory_limit: none,
        json: none,
        book: none,
        book_out: none,
        fills: none,
        trace: false,
        quiet: false,
        help: false,
    )
    var i := 0
    for i < args.Len() {
        arg := args[i]
        i += 1
        if arg == "--help" || arg == "-h" {
            o.help = true
            continue
        }
        if arg == "--trace" {
            o.trace = true
            continue
        }
        if arg == "--quiet" {
            o.quiet = true
            continue
        }
        if arg.StartsWith("--") {
            if i == args.Len() {
                Complain(err, arg, " needs a value")
                fail BadArgs(at: i)
            }
            value := args[i]
            i += 1
            good := match arg {
                "--synth" { Count(value, mut o.synth, 1) }
                "--seed" { Seed(value, mut o.seed) }
                "--depth" { Positive(value, mut o.depth) }
                "--bars" { Positive(value, mut o.bars) }
                "--snapshot" { NonNegative(value, mut o.snapshot) }
                "--memory-limit" { Count(value, mut o.memory_limit, 1) }
                "--account" { AccountArg(value, mut o.account) }
                "--band" { BandArg(value, mut o.band) }
                "--json" {
                    o.json = some(value)
                    true
                }
                "--book" {
                    o.book = some(value)
                    true
                }
                "--book-out" {
                    o.book_out = some(value)
                    true
                }
                "--fills" {
                    o.fills = some(value)
                    true
                }
                _ {
                    Complain(err, "unknown option ", arg)
                    fail BadArgs(at: i - 1)
                }
            }
            if !good {
                Complain(err, arg, " does not take that value")
                fail BadArgs(at: i)
            }
            continue
        }
        if o.command == "" {
            if arg != "replay" && arg != "synth" {
                Complain(err, "unknown command ", arg)
                fail BadArgs(at: i)
            }
            o.command = arg
            continue
        }
        if o.path.IsSome() {
            Complain(err, "unexpected argument ", arg)
            fail BadArgs(at: i)
        }
        o.path = some(arg)
    }
    if o.help {
        return o
    }
    if o.command == "" {
        Complain(err, "no command given", "")
        fail BadArgs(at: 0)
    }
    if o.command == "synth" && o.synth.IsNone() {
        Complain(err, "synth needs --synth N", "")
        fail BadArgs(at: 0)
    }
    if o.command == "replay" && o.path.IsNone() == o.synth.IsNone() {
        Complain(err, "replay needs a feed file or --synth N, not both", "")
        fail BadArgs(at: 0)
    }
    return o
}

/// Writes `error: <first><second>` and a pointer to --help. A failed write
/// to standard error leaves nothing else to report to.
Complain(err: mut Writer, first: str, second: str) {
    Print(err, "error: {first}{second}\nrun 'orderbook --help' for usage\n", (first, second)) else {}
}

Count(s: str, out: mut ?int, min: int): bool {
    n := s.ParseInt() else {
        return false
    }
    if n < i64(min) || n > 1_000_000_000_000 {
        return false
    }
    *out = some(Cast[int](n))
    return true
}

Seed(s: str, out: mut u64): bool {
    n := s.ParseInt() else {
        return false
    }
    if n < 0 {
        return false
    }
    *out = Cast[u64](n)
    return true
}

Positive(s: str, out: mut int): bool {
    var v: ?int = none
    if !Count(s, mut v, 1) {
        return false
    }
    *out = v.Must() // Count set it
    return true
}

NonNegative(s: str, out: mut int): bool {
    var v: ?int = none
    if !Count(s, mut v, 0) {
        return false
    }
    *out = v.Must() // Count set it
    return true
}

AccountArg(s: str, out: mut ?u32): bool {
    n := s.ParseInt() else {
        return false
    }
    if n < 0 || n > 0xffffffff {
        return false
    }
    *out = some(Cast[u32](n))
    return true
}

BandArg(s: str, out: mut ?(lo: i64, hi: i64)): bool {
    colon := s.Find(":") else {
        return false
    }
    lo := feed.Price(s[..colon], 0, 0) else {
        return false
    }
    hi := feed.Price(s[colon + 1..], 0, 0) else {
        return false
    }
    if lo > hi {
        return false
    }
    *out = some((lo: lo, hi: hi))
    return true
}
