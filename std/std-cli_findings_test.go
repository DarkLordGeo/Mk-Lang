// Why: std.log and std.cli had bugs in format checks, timed output, help
// handling and write errors; these tests keep them fixed.
// What: a Logger format checked as Print's (E0706), timed lines on a
// simulated clock, help before required checks, a failing Writer.
// How: small programs over a copy of std, checked or built and run.

package std

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestSTDCLI_SpecHonoured: `{x:x}` and `{w:.N}` through a Logger
// write what Print writes for the same format and record (Print is the
// reference: each case logs a line into a Text and Prints "INFO " and the
// format to stdout), at dev and release, for every integer width and both
// float widths.
func TestSTDCLI_SpecHonoured(t *testing.T) {
	t.Parallel()
	cases := []struct{ f, rec, want string }{
		{"{x:x}", "(x: 255)", "ff"},
		{"{x:x} {x}", "(x: 4096)", "1000 4096"},
		{"{a:x} {b:x} {c:x} {d:x} {e:x}", "(a: Trunc[u8](200), b: Trunc[i8](-1), c: Trunc[i32](-16), d: Cast[u64](0), e: Trunc[u16](65535))", "c8 ff fffffff0 0 ffff"},
		{"{a:x} {b:x} {c:x} {d:x} {e:x}", "(a: -1, b: Cast[uint](10), c: Cast[i64](-2), d: Cast[u32](3735928559), e: Trunc[i16](-2))", "ffffffffffffffff a fffffffffffffffe deadbeef fffe"},
		{"{w:.2} {v:.0} {u:.3} {t}", "(w: 2.5, v: 2.5, u: 0.0005, t: 0.1)", "2.50 2 0.001 0.1"},
		{"{w:.17} {v:.20}", "(w: 0.1, v: 0.1)", "0.10000000000000001 0.10000000000000000555"},
		{"{w:.2} {w}", "(w: Cast[f32](0.1))", ""},
		{"{s} {b} {{x:x}} }}", "(s: \"s\", b: true)", "s true {x:x} }"},
	}
	var sb strings.Builder
	sb.WriteString("import std.log\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n" +
		"    var text := Text.New(sys.arena)\n    var tw := text.Writer()\n    var l := log.Logger.New(mut tw, log.Level.Info)\n")
	for _, c := range cases {
		fmt.Fprintf(&sb, "    try l.Info(\"%s\", %s)\n    try Print(mut out, \"INFO %s\\n\", %s)\n", c.f, c.rec, c.f, c.rec)
	}
	sb.WriteString("    try out.Str(\"--\\n\")\n    try out.Str(text.Str())\n}\n")
	for _, mode := range cliModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			r := runNative(t, cliBuild(t, sb.String(), mode.flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
			if len(got) != 2*len(cases)+1 || got[len(cases)] != "--" {
				t.Fatalf("%d lines for %d cases:\n%s", len(got), len(cases), r.stdout)
			}
			for k, c := range cases {
				printed, logged := got[k], got[len(cases)+1+k]
				if logged != printed {
					t.Errorf("%s with %s: the Logger wrote %q, Print %q", c.f, c.rec, logged, printed)
				}
				if c.want != "" && logged != "INFO "+c.want {
					t.Errorf("%s with %s: %q, want %q", c.f, c.rec, logged, "INFO "+c.want)
				}
			}
		})
	}
}

// cliR2Bad are calls whose format Print rejects (E0706).
var cliR2Bad = []string{
	`l.Info("{x:x} {nope}", (x: 255, unused: 1))`, // the finding's case
	`l.Info("{nope}", (k: 1))`,
	`l.Info("{x} }", (x: 1))`,
	`l.Info("{x:.2}", (x: 1))`,
	`l.Debug("{y}", (x: 1))`, // below the level: still checked
	`l.Log(log.Level.Error, "{x", (x: 1))`,
	`l.Warn("{s:x}", (s: "s"))`,
	`l.Error("a", (x: 1))`,
	`l.Info("{x} {w:.100}", (x: 1, w: 0.5))`,
}

// TestSTDCLI_MismatchIsE0706: since std.log forwards its format to
// Print, a format Print rejects is E0706 at the Logger call that
// wrote it, at compile time, whatever the logger's level: one diagnostic
// per bad call, each at its own line (call k is at line 8 + 3k). It
// replaces the interim walk's run-time contract.
func TestSTDCLI_MismatchIsE0706(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	sb.WriteString("import std.log\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    var l := log.Logger.New(mut out, log.Level.Info)\n    k := sys.args[1]\n")
	for k, c := range cliR2Bad {
		fmt.Fprintf(&sb, "    if k == \"%d\" {\n        try %s\n    }\n", k, c)
	}
	sb.WriteString("    try out.Str(\"end\\n\")\n}\n")
	out, status := cliCheck(t, sb.String())
	if status == 0 {
		t.Fatalf("mk check accepted every bad format:\n%s", out)
	}
	if n := strings.Count(out, "E0706"); n < len(cliR2Bad) {
		t.Errorf("%d E0706 for %d bad calls:\n%s", n, len(cliR2Bad), out)
	}
	for k, c := range cliR2Bad {
		if at := "main.mk:" + strconv.Itoa(8+3*k) + ":"; !strings.Contains(out, at) {
			t.Errorf("%s: no diagnostic at %s\n%s", c, at, out)
		}
	}
}

// TestSTDCLI_E0706AtCall: a Logger call whose format does not match
// its record is E0706 at the format argument. It needs a forwarded const
// parameter to be accepted as Print's format: while a one-line forwarder is
// still E0705 the test skips, naming it. Once internal/check accepts it the
// test runs and fails until std/log/log.mk's Line forwards f to Print.
func TestSTDCLI_E0706AtCall(t *testing.T) {
	t.Parallel()
	probe := "F(out: mut Writer, const f: str): WriteError!void {\n    try Print(out, f, (x: 1))\n}\n\n" +
		"export Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    try F(mut out, \"{x}\\n\")\n}\n"
	out, status := cliCheck(t, probe)
	if status != 0 && strings.Contains(out, "E0705") {
		t.Skipf("a forwarded const parameter is still E0705, so std.log checks its formats at run time (Checked's contract, TestSTDCLI_CLIR2_MismatchIsContract):\n%s", out)
	}
	if status != 0 {
		t.Fatalf("the forwarder probe fails for another reason:\n%s", out)
	}
	src := "import std.log\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    var l := log.Logger.New(mut out, log.Level.Info)\n" +
		"    try l.Info(\"{x:x} {nope}\", (x: 255, unused: 1))\n}\n"
	out, status = cliCheck(t, src)
	if status == 0 || !strings.Contains(out, "main.mk:6:") || !strings.Contains(out, "E0706") {
		t.Fatalf("forwarded const parameters are checked, so the call must be E0706 at main.mk:6; std/log/log.mk's Line still walks f at run time (switch it to Print):\n%s", out)
	}
}

// TestSTDCLI_LogBlocks: mk test --std=std.log passes the blocks of
// std/log/tests_log.mk (Stamp's bytes) at dev and release. The format
// check is check's E0706 (TestSTDCLI_MismatchIsE0706),
// so the run-time Fits and its differential blocks are gone.
func TestSTDCLI_LogBlocks(t *testing.T) {
	t.Parallel()
	names := []string{"Stamp writes seconds and six digits of microseconds", "Stamp writes a negative difference as zero"}
	for _, mode := range cliModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			stdDir := copyStd(t, root, nil)
			out, _ := mkCmd(t, root, stdDir, append([]string{"test", "--std=std.log"}, mode.flags...)...)
			res := parseStdTest(out, "std.log")
			for _, n := range names {
				if r, ok := res[n]; !ok || !r.pass {
					t.Errorf("block %q: %+v\n%s", n, r, out)
				}
			}
		})
	}
}

// TestSTDCLI_TimedUnderSim: NewTimed's stamp is the time since the
// logger was made, to the microsecond, on a simulated clock, so the bytes
// are exact: the deterministic half of the timed logger's tests. The clock moves by Advance
// and by 1 us at every read (std/sim/table.mk SimNow), so the n-th line
// after NewTimed's read is n us later than the Advance calls alone.
func TestSTDCLI_TimedUnderSim(t *testing.T) {
	t.Parallel()
	src := `import std.log
import std.sim
import std.time

Stamped(sys: &System, out: mut Writer, d: time.Duration): OutOfMemory|WriteError!void {
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    var text := Text.New(sys.arena)
    var tw := text.Writer()
    var l := log.Logger.NewTimed(s.Io(), mut tw, log.Level.Warn)
    s.Advance(d)
    try l.Warn("x", ())
    try l.Info("dropped", ())
    s.Advance(time.Millis(1))
    try l.Error("e {k}", (k: 1))
    try out.Str(text.Str())
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    try Stamped(sys, mut out, time.Millis(1234))
    try Stamped(sys, mut out, time.Seconds(3))
    try Stamped(sys, mut out, time.Millis(0))
    try Stamped(sys, mut out, time.Seconds(90061))
}
`
	want := "1.234001 WARN x\n1.235002 ERROR e 1\n3.000001 WARN x\n3.001002 ERROR e 1\n0.000001 WARN x\n0.001002 ERROR e 1\n" +
		"90061.000001 WARN x\n90061.001002 ERROR e 1\n"
	for _, mode := range cliModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			r := runNative(t, cliBuild(t, src, mode.flags...), "")
			r.stdout = strings.ReplaceAll(r.stdout, "\r\n", "\n")
			wantRun(t, r, 0, want)
		})
	}
}

// cliHelpTypes has a help flag beside two required options.
const cliHelpTypes = `import std.cli

struct H {
    /// Print this help.
    export help: bool
    /// How many.
    export count: int
    export name: ?str
    export label: str
    export files: []str
}

gen cli.Args(H)

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    match H.Parse(sys.arena, sys.args[1..]) {
        ok(h) {
            try Print(mut out, "ok help={h} count={c} label=[{l}] files={f}\n", (h: h.help, c: h.count, l: h.label, f: h.files.Len()))
            if h.help {
                try H.Usage(mut out)
            }
        }
        err(cli.Unknown(arg)) {
            try Print(mut out, "unknown {a}\n", (a: arg))
        }
        err(cli.Missing(field)) {
            try Print(mut out, "missing {a}\n", (a: field))
        }
        err(cli.Invalid(arg)) {
            try Print(mut out, "invalid {a}\n", (a: arg))
        }
        err(_) {
            try out.Str("oom\n")
        }
    }
}
`

// TestSTDCLI_HelpSkipsMissing: with a `help: bool` field, `--help`
// alone parses (help true, 0 and "" in the required options not given) so
// the program reaches its Usage call; the Missing checks still run without
// it, and Unknown and Invalid still stop Parse with it. The grepc case of
// TestSTDCLI_ExamplePrograms is `grepc --help` alone.
func TestSTDCLI_HelpSkipsMissing(t *testing.T) {
	t.Parallel()
	usage := "Options:\n  --help         Print this help.\n  --count <int>  How many. (required)\n  --name <str>\n  --label <str>  (required)\nArguments:\n  <files>...\n"
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "ok help=true count=0 label=[] files=0\n" + usage},
		{[]string{"--help=true"}, "ok help=true count=0 label=[] files=0\n" + usage},
		{[]string{"--help", "--count", "3"}, "ok help=true count=3 label=[] files=0\n" + usage},
		{[]string{"a", "--help", "b", "--label=x"}, "ok help=true count=0 label=[x] files=2\n" + usage},
		{[]string{"--count", "3", "--label", "x"}, "ok help=false count=3 label=[x] files=0\n"},
		{[]string{}, "missing 1\n"},
		{[]string{"--count", "3"}, "missing 3\n"},
		{[]string{"--help=false"}, "missing 1\n"},
		{[]string{"--help", "--help=false"}, "missing 1\n"}, // the last wins
		{[]string{"--", "--help"}, "missing 1\n"},           // a positional
		{[]string{"--help", "--nope"}, "unknown 1\n"},
		{[]string{"--nope", "--help"}, "unknown 0\n"},
		{[]string{"--help", "--count", "x"}, "invalid 2\n"},
		{[]string{"--help=yes"}, "invalid 0\n"},
		{[]string{"--count", "--help"}, "invalid 1\n"}, // --count takes "--help" as its value
	}
	for _, mode := range cliModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			exe := cliBuild(t, cliHelpTypes, mode.flags...)
			for _, c := range cases {
				r := runNative(t, exe, "", c.args...)
				if got := strings.ReplaceAll(r.stdout, "\r\n", "\n"); r.code != 0 || got != c.want {
					t.Errorf("%q: exit %d\n%s\nwant:\n%s%s", c.args, r.code, got, c.want, r.stderr)
				}
			}
		})
	}
}

// TestSTDCLI_WriteErrors: every Logger method and Usage return the
// Writer's WriteError, and nothing after the failed piece reaches the
// file. The Writer is a simulated file's with no buffer, so each piece is
// one write and Sim.FailNext fails exactly the next one. A call below the
// level returns ok without a write: the armed fault is still there for
// the next call. Fault-free calls around them land whole.
func TestSTDCLI_WriteErrors(t *testing.T) {
	t.Parallel()
	src := `import std.cli
import std.fs
import std.log
import std.sim

struct Opts {
    /// Print more.
    export verbose: bool
}

gen cli.Args(Opts)

Show(out: mut Writer, what: str, failed: bool): WriteError!void {
    if failed {
        try Print(mut *out, "{w}: WriteError\n", (w: what))
    } else {
        try Print(mut *out, "{w}: ok\n", (w: what))
    }
}

Calls(s: mut sim.Sim, io: Io, f: mut fs.File, out: mut Writer): WriteError!void {
    var nb: [1]u8 = [0]
    var w := f.Writer(io, mut nb[0..0])
    var l := log.Logger.New(mut w, log.Level.Info)
    var failed := false
    l.Info("first {k}", (k: 0)) else {
        failed = true
    }
    try Show(out, "info", failed)
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    failed = false
    l.Info("a {k} b", (k: 1)) else {
        failed = true
    }
    try Show(out, "info armed", failed)
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    failed = false
    l.Debug("d {k}", (k: 2)) else {
        failed = true
    }
    try Show(out, "debug filtered, armed", failed)
    failed = false
    l.Warn("w {k}", (k: 3)) else {
        failed = true
    }
    try Show(out, "warn after it", failed)
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    failed = false
    l.Error("e {k}", (k: 4)) else {
        failed = true
    }
    try Show(out, "error armed", failed)
    s.FailNext(sim.Op.Write, sim.Fault.Denied)
    failed = false
    l.Log(log.Level.Warn, "l {k}", (k: 5)) else {
        failed = true
    }
    try Show(out, "log armed", failed)
    failed = false
    l.Info("second {k}", (k: 6)) else {
        failed = true
    }
    try Show(out, "info", failed)
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    failed = false
    Opts.Usage(mut w) else {
        failed = true
    }
    try Show(out, "usage armed", failed)
    failed = false
    Opts.Usage(mut w) else {
        failed = true
    }
    try Show(out, "usage", failed)
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    io := s.Io()
    var f := try fs.Create(io, "log")
    onerr f.Abandon()
    try Calls(mut s, io, mut f, mut out)
    try f.Close(io)
    text := try fs.ReadText(sys.arena, io, "log")
    try Print(mut out, "file:\n{t}", (t: text))
}
`
	want := "info: ok\ninfo armed: WriteError\ndebug filtered, armed: ok\nwarn after it: WriteError\nerror armed: WriteError\n" +
		"log armed: WriteError\ninfo: ok\nusage armed: WriteError\nusage: ok\n" +
		"file:\nINFO first 0\nINFO second 6\nOptions:\n  --verbose  Print more.\n"
	for _, mode := range cliModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			r := runNative(t, cliBuild(t, src, mode.flags...), "")
			r.stdout = strings.ReplaceAll(r.stdout, "\r\n", "\n")
			wantRun(t, r, 0, want)
		})
	}
}
