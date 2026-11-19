// Why: the example programs tests/programs/grepc.mk and logdemo.mk show
// std.cli and std.log in use, so they must build and behave as shown.
// What: grepc.mk (a line filter whose options come from gen cli.Args) and
// logdemo.mk (std.log's levels), built and run here.
// How: each runs over the cases its .cases file holds.

package std

import (
	"strings"
	"testing"
)

// cliGrepc is tests/programs/grepc.mk.
const cliGrepc = `// A line filter: prints the lines of standard input that hold --pattern
// (with --invert, those that do not), then their count. The options and
// the help come from gen cli.Args over Options and its /// comments.
import std.cli
import std.log
import std.proc

struct Options {
    /// The text a line must hold.
    export pattern: str
    /// Keep the lines that do not hold it.
    export invert: bool
    /// Stop after this many lines.
    export max: ?int
    /// Log each kept line's number to stderr.
    export verbose: bool
    /// Print this help.
    export help: bool
    /// Words printed before the count.
    export labels: []str
}

gen cli.Args(Options)

// Usage reports a bad command line on stderr, with the help, and exits 2.
Usage(sys: &System, what: str, at: int): WriteError!void {
    var e := sys.Stderr()
    try Print(mut e, "grepc: {what} {at}\n", (what: what, at: at))
    try Options.Usage(mut e)
    try e.Flush()
    proc.Exit(2)
}

Parse(sys: &System): WriteError!Options from sys {
    match Options.Parse(sys.arena, sys.args[1..]) {
        ok(o) {
            return o
        }
        err(cli.Unknown(arg)) {
            try Usage(sys, "unknown argument", arg)
        }
        err(cli.Missing(field)) {
            try Usage(sys, "missing the option of field", field)
        }
        err(cli.Invalid(arg)) {
            try Usage(sys, "bad value at argument", arg)
        }
        err(_) {
            try Usage(sys, "out of memory at argument", 0)
        }
    }
    proc.Exit(2)
}

export Main(sys: &System): ReadError|WriteError|Utf8Error|OutOfMemory!void {
    o := try Parse(sys)
    var out := sys.Stdout()
    if o.help {
        try Options.Usage(mut out)
        return
    }
    var level := log.Level.Warn
    if o.verbose {
        level = log.Level.Info
    }
    var errw := sys.Stderr()
    var lg := log.Logger.New(mut errw, level)
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 20)
    n := try inp.ReadAll(buf)
    text := try str.From(buf[0..n])
    var lines := text.Lines()
    var kept := 0
    var no := 0
    for line := lines.Next() {
        no += 1
        if line.Has(o.pattern) == o.invert {
            continue
        }
        if m := o.max {
            if kept == m {
                break
            }
        }
        try out.Str(line)
        try out.Str("\n")
        kept += 1
        try lg.Info("kept line {no}", (no: no))
    }
    for l in o.labels {
        try out.Str(l)
        try out.Str(" ")
    }
    try Print(mut out, "{kept} lines\n", (kept: kept))
}
`

// cliLogdemo is tests/programs/logdemo.mk.
const cliLogdemo = `// std.log's levels: one line per call, the level first; --quiet keeps
// warnings and errors only. The flags come from gen cli.Args.
import std.cli
import std.log

struct Flags {
    /// Log warnings and errors only.
    export quiet: bool
    /// How many events to log (3 when not given).
    export events: ?int
}

gen cli.Args(Flags)

export Main(sys: &System): cli.Error|WriteError!void {
    f := try Flags.Parse(sys.arena, sys.args[1..])
    var out := sys.Stdout()
    var level := log.Level.Debug
    if f.quiet {
        level = log.Level.Warn
    }
    var l := log.Logger.New(mut out, level)
    n := f.events else { 3 }
    for k in 0..n {
        try l.Debug("event {k} starts", (k: k))
        try l.Info("event {k} has {size} bytes", (k: k, size: k * 100))
        if k % 2 == 1 {
            try l.Warn("event {k} is odd", (k: k))
        }
    }
    try l.Error("{n} events, {{done}}", (n: n))
}
`

// cliProgramCase is one case of a program's .cases file.
type cliProgramCase struct {
	name, stdin     string
	args            []string
	stdout, stderr  string
	exit            int
	stderrHasPrefix bool // stderr is checked by prefix (the Usage text follows)
}

const cliGrepcIn = "alpha one\nbeta two\nalpha three\ngamma\nalpha four\n"

var cliGrepcCases = []cliProgramCase{
	{name: "ok", stdin: cliGrepcIn, args: []string{"--pattern", "alpha"}, stdout: "alpha one\nalpha three\nalpha four\n3 lines\n"},
	{name: "invert-max-labels", stdin: cliGrepcIn, args: []string{"x", "--invert", "--pattern=alpha", "--max", "1", "y"}, stdout: "beta two\nx y 1 lines\n"},
	{name: "verbose", stdin: cliGrepcIn, args: []string{"--verbose", "--pattern", "three"}, stdout: "alpha three\n1 lines\n", stderr: "INFO kept line 3\n"},
	{name: "help", args: []string{"--help"}, stdout: "Options:\n" +
		"  --pattern <str>  The text a line must hold. (required)\n" +
		"  --invert         Keep the lines that do not hold it.\n" +
		"  --max <int>      Stop after this many lines.\n" +
		"  --verbose        Log each kept line's number to stderr.\n" +
		"  --help           Print this help.\n" +
		"Arguments:\n" +
		"  <labels>...      Words printed before the count.\n"},
	{name: "unknown", args: []string{"--pattern", "p", "--colour"}, stderr: "grepc: unknown argument 2\nOptions:\n", exit: 2, stderrHasPrefix: true},
	{name: "missing", args: []string{"--invert"}, stderr: "grepc: missing the option of field 0\nOptions:\n", exit: 2, stderrHasPrefix: true},
	{name: "bad-max", args: []string{"--pattern", "p", "--max", "many"}, stderr: "grepc: bad value at argument 3\nOptions:\n", exit: 2, stderrHasPrefix: true},
}

var cliLogdemoCases = []cliProgramCase{
	{name: "ok", stdout: "DEBUG event 0 starts\nINFO event 0 has 0 bytes\nDEBUG event 1 starts\nINFO event 1 has 100 bytes\nWARN event 1 is odd\n" +
		"DEBUG event 2 starts\nINFO event 2 has 200 bytes\nERROR 3 events, {done}\n"},
	{name: "quiet", args: []string{"--quiet", "--events=4"}, stdout: "WARN event 1 is odd\nWARN event 3 is odd\nERROR 4 events, {done}\n"},
	{name: "bad-flag", args: []string{"--events", "x"}, stderr: "error: cli.Invalid(arg: 1)\n", exit: 1},
}

// TestSTDCLI_ExamplePrograms builds each program at dev and release and
// runs its cases.
func TestSTDCLI_ExamplePrograms(t *testing.T) {
	t.Parallel()
	progs := []struct {
		name, src string
		cases     []cliProgramCase
	}{{"grepc", cliGrepc, cliGrepcCases}, {"logdemo", cliLogdemo, cliLogdemoCases}}
	for _, p := range progs {
		for _, mode := range cliModes {
			t.Run(p.name+"/"+mode.name, func(t *testing.T) {
				t.Parallel()
				exe := cliBuild(t, p.src, mode.flags...)
				for _, c := range p.cases {
					if c.name == "bad-flag" && mode.name == "dev" {
						continue // dev adds the error-return trace after the first line
					}
					r := runNative(t, exe, c.stdin, c.args...)
					stdout := strings.ReplaceAll(r.stdout, "\r\n", "\n")
					stderr := strings.ReplaceAll(r.stderr, "\r\n", "\n")
					okErr := stderr == c.stderr || c.stderrHasPrefix && strings.HasPrefix(stderr, c.stderr)
					if r.code != c.exit || stdout != c.stdout || !okErr {
						t.Errorf("case %s: exit %d, want %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s\nwant:\n%s", c.name, r.code, c.exit, stdout, c.stdout, stderr, c.stderr)
					}
				}
			})
		}
	}
}
