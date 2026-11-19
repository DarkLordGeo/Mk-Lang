// Why: gen cli.Args(T) (std/cli/args.mk) writes a command-line parser from
// a record type, so its output must parse exactly the documented grammar.
// What: Parse over hand cases and a seeded random corpus against cliModel, a
// Go model of the grammar; Usage and the gen view as goldens; E0903 refusals.
// How: each program is generated, built and run over a copy of std.

package std

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

// cliTypes is the struct every Parse test runs: each field kind, a
// required option of each type, and a snake_case name.
const cliTypes = `import std.cli

struct Opts {
    /// Print each file as it is read.
    export verbose: bool
    /// How many times to run.
    /// At least one.
    export count: int
    export name: ?str
    /// Show what would change, and change nothing.
    export dry_run: bool
    /// Stop after this many.
    export limit: ?int
    export label: str
    /// The files to read.
    export files: []str
}

gen cli.Args(Opts)
`

// cliMain reads one argument vector per input line ("n|a|b|..."), parses
// it, and prints the outcome as cliModel does.
const cliMain = `
Show(out: mut Writer, o: &Opts): WriteError!void {
    try Print(mut *out, "ok v={v} c={c}", (v: o.verbose, c: o.count))
    if nm := o.name {
        try Print(mut *out, " n:{x}", (x: nm))
    } else {
        try out.Str(" n-")
    }
    try Print(mut *out, " d={d}", (d: o.dry_run))
    if l := o.limit {
        try Print(mut *out, " l:{x}", (x: l))
    } else {
        try out.Str(" l-")
    }
    try Print(mut *out, " lab={x} f=[", (x: o.label))
    var first := true
    for f in o.files {
        if !first {
            try out.Str(",")
        }
        try out.Str(f)
        first = false
    }
    try out.Str("]\n")
}

export Main(sys: &System): ReadError|WriteError|Utf8Error|ParseError|OutOfMemory!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 22)
    n := try inp.ReadAll(buf)
    text := try str.From(buf[0..n])
    var out := sys.Stdout()
    var lines := text.Lines()
    for line := lines.Next() {
        var parts := line.Split("|")
        head := parts.Next().Must()
        count := try head.ParseInt()
        var l := List[str].New(sys.arena)
        for _ in 0..Cast[int](count) {
            try l.Push(parts.Next().Must())
        }
        all := l.Done()
        args := all[..]
        match Opts.Parse(sys.arena, args) {
            ok(o) {
                try Show(mut out, &o)
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
}
`

// cliField is one field of the model's struct.
type cliField struct {
	name string // the option name, with hyphens
	kind string // flag, int, str, ?int, ?str, pos
}

var cliOptsFields = []cliField{
	{"verbose", "flag"}, {"count", "int"}, {"name", "?str"}, {"dry-run", "flag"},
	{"limit", "?int"}, {"label", "str"}, {"files", "pos"},
}

// cliModel parses args as std.cli's documented grammar says and prints the
// outcome in cliMain's form.
func cliModel(fields []cliField, args []string) string {
	vals := map[string]string{}
	set := map[string]bool{}
	var pos []string
	rest := false
	takesPos := false
	for _, f := range fields {
		takesPos = takesPos || f.kind == "pos"
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if rest || len(arg) < 2 || arg[0] != '-' {
			if !takesPos {
				return fmt.Sprintf("unknown %d", i)
			}
			pos = append(pos, arg)
			continue
		}
		if arg[1] != '-' {
			return fmt.Sprintf("unknown %d", i)
		}
		if len(arg) == 2 {
			rest = true
			continue
		}
		key, val, has := strings.Cut(arg[2:], "=")
		var f *cliField
		for k := range fields {
			if fields[k].kind != "pos" && fields[k].name == key {
				f = &fields[k]
			}
		}
		if f == nil {
			return fmt.Sprintf("unknown %d", i)
		}
		if f.kind == "flag" {
			switch {
			case !has || val == "true":
				vals[key] = "true"
			case val == "false":
				vals[key] = "false"
			default:
				return fmt.Sprintf("invalid %d", i)
			}
			continue
		}
		at := i
		if !has {
			if i+1 == len(args) {
				return fmt.Sprintf("invalid %d", i)
			}
			i++
			at = i
			val = args[i]
		}
		if f.kind == "int" || f.kind == "?int" {
			v, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return fmt.Sprintf("invalid %d", at)
			}
			val = strconv.FormatInt(v, 10)
		}
		vals[key] = val
		set[key] = true
	}
	for k, f := range fields {
		if (f.kind == "int" || f.kind == "str") && !set[f.name] {
			return fmt.Sprintf("missing %d", k)
		}
	}
	flag := func(n string) string {
		if vals[n] == "true" {
			return "true"
		}
		return "false"
	}
	opt := func(n string) string {
		if set[n] {
			return ":" + vals[n]
		}
		return "-"
	}
	return fmt.Sprintf("ok v=%s c=%s n%s d=%s l%s lab=%s f=[%s]",
		flag("verbose"), vals["count"], opt("name"), flag("dry-run"), opt("limit"), vals["label"], strings.Join(pos, ","))
}

// cliHandCases are argument vectors for the grammar's edges.
var cliHandCases = [][]string{
	{},
	{"--count", "3", "--label", "x"},
	{"--count=3", "--label=x", "a", "b"},
	{"a", "--count", "3", "b", "--label", "x", "c"}, // interleaved: the list
	{"a", "b", "--count", "1", "--label", "L"},      // a run before the options: a view
	{"--count", "1", "--label", "L", "--", "--verbose", "-x", "-"},
	{"--count", "-5", "--label", "-"}, // a value-taking option takes the next argument
	{"--count"},                       // no value at the end
	{"--label", "x", "--count"},
	{"--count", "x", "--label", "y"},
	{"--count=", "--label", "y"},
	{"--count", "9223372036854775807", "--label", "y"},
	{"--count", "9223372036854775808", "--label", "y"},
	{"--count", "+4", "--label", "y"},
	{"--count", "1"},         // Missing label, field 5
	{"--label", "1"},         // Missing count, field 1
	{"--zz", "--count", "1"}, // Unknown before Missing
	{"-v"},                   // no short options
	{"--dry_run"},            // the field's own spelling is not the option
	{"--dry-run", "--count=1", "--label=a"},
	{"--verbose=false", "--verbose", "--count=1", "--label=a"},
	{"--verbose=true", "--verbose=false", "--count=1", "--label=a"},
	{"--verbose=yes"},
	{"--verbose=", "--count=1"},
	{"--count=1", "--count=2", "--label=a", "--label=b"}, // the last wins
	{"--name=", "--count=1", "--label=a"},
	{"--name", "", "--count=1", "--label=a"},
	{"--name=a=b", "--count=1", "--label=a"},
	{"--=x"},
	{"---count=1"},
	{"--limit", "5", "--count", "0", "--label", ""},
	{"-", "--count=1", "--label=a"},
	{"", "--count=1", "--label=a", ""},
	{"--", "--", "--count=1"},
}

// cliRandomCases are n seeded vectors over the grammar's tokens, half with
// the required options inserted somewhere so that most of them parse.
func cliRandomCases(seed int64, n int) [][]string {
	r := rand.New(rand.NewSource(seed))
	toks := []string{
		"--verbose", "--verbose=true", "--verbose=false", "--verbose=x", "--count", "--count=5",
		"--count=-3", "--count=abc", "5", "-7", "--name", "--name=", "--name=q", "--dry-run",
		"--dry_run", "--limit", "--limit=12", "--label", "--label=L", "--", "-", "-x", "--zz",
		"f1", "f2", "f3", "", "--=x", "--count=99999999999999999999", "+4", "a=b",
	}
	var out [][]string
	for k := 0; k < n; k++ {
		var v []string
		for j := r.Intn(9); j > 0; j-- {
			v = append(v, toks[r.Intn(len(toks))])
		}
		if r.Intn(2) == 0 {
			for _, req := range [][]string{{"--count", strconv.Itoa(r.Intn(100) - 50)}, {"--label=" + strconv.Itoa(k)}} {
				at := r.Intn(len(v) + 1)
				v = append(v[:at], append(append([]string{}, req...), v[at:]...)...)
			}
		}
		out = append(out, v)
	}
	return out
}

// cliInput encodes vectors as cliMain reads them.
func cliInput(cases [][]string) string {
	var sb strings.Builder
	for _, c := range cases {
		sb.WriteString(strconv.Itoa(len(c)))
		for _, a := range c {
			sb.WriteString("|" + a)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestSTDCLI_ParseMatchesModel: the emitted Parse agrees with cliModel on
// every hand case and on 3,000 seeded random vectors, at dev and release.
func TestSTDCLI_ParseMatchesModel(t *testing.T) {
	t.Parallel()
	cases := append(append([][]string{}, cliHandCases...), cliRandomCases(1, 3000)...)
	in := cliInput(cases)
	for _, mode := range cliModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			exe := cliBuild(t, cliTypes+cliMain, mode.flags...)
			r := runNative(t, exe, in)
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
			if len(got) != len(cases) {
				t.Fatalf("%d lines for %d cases:\n%s", len(got), len(cases), r.stdout)
			}
			bad := 0
			for k, c := range cases {
				if want := cliModel(cliOptsFields, c); got[k] != want {
					bad++
					if bad <= 20 {
						t.Errorf("case %d %q:\n got %s\nwant %s", k, c, got[k], want)
					}
				}
			}
		})
	}
}

// TestSTDCLI_ProcessArgs: Parse over sys.args[1..] of a real process, and
// Usage printed on an error (the shape of the example program).
func TestSTDCLI_ProcessArgs(t *testing.T) {
	t.Parallel()
	src := cliTypes + `
export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    match Opts.Parse(sys.arena, sys.args[1..]) {
        ok(o) {
            try Print(mut out, "count={c} files={n}\n", (c: o.count, n: o.files.Len()))
        }
        err(_) {
            try Opts.Usage(mut out)
        }
    }
}
`
	exe := cliBuild(t, src)
	wantRun(t, runNative(t, exe, "", "x", "--count", "2", "y", "--label=z"), 0, "count=2 files=2\n")
	r := runNative(t, exe, "", "--nope")
	usage := strings.ReplaceAll(r.stdout, "\r\n", "\n")
	if r.code != 0 || !strings.HasPrefix(usage, "Options:\n  --verbose ") {
		t.Fatalf("exit %d\n%s", r.code, r.stdout)
	}
	cliGolden(t, "cli/testdata/usage.golden", usage)
}

// TestSTDCLI_GenView pins the emitted code of cliTypes.
func TestSTDCLI_GenView(t *testing.T) {
	t.Parallel()
	cliGolden(t, "cli/testdata/args.golden", cliView(t, cliTypes+"\nexport Main(sys: &System) {\n}\n"))
}

// TestSTDCLI_Shapes: a struct with no fields (only an empty argv parses;
// Usage writes nothing), one with only positionals (no Options section;
// every option is Unknown), one with only options (a positional is
// Unknown), and field names that are the emitted code's own names.
func TestSTDCLI_Shapes(t *testing.T) {
	t.Parallel()
	src := `import std.cli

struct Empty {}

struct Pos {
    /// Inputs.
    export inputs: []str
}

struct Only {
    export a: int
    export args: ?str
    export i: bool
}

gen cli.Args(Empty)
gen cli.Args(Pos)
gen cli.Args(Only)

Code(e: cli.Error): str {
    match e {
        cli.Unknown(arg: _) {
            return "unknown"
        }
        cli.Missing(field: _) {
            return "missing"
        }
        cli.Invalid(arg: _) {
            return "invalid"
        }
        _ {
            return "oom"
        }
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    match Empty.Parse(sys.arena, sys.args[1..1]) {
        ok(_) {
            try out.Str("none ok\n")
        }
        err(e) {
            try Print(mut out, "none {c}\n", (c: Code(e)))
        }
    }
    match Empty.Parse(sys.arena, sys.args[0..1]) {
        ok(_) {
            try out.Str("none ok\n")
        }
        err(e) {
            try Print(mut out, "none {c}\n", (c: Code(e)))
        }
    }
    try out.Str("[")
    try Empty.Usage(mut out)
    try out.Str("]\n")
    match Pos.Parse(sys.arena, sys.args[1..]) {
        ok(p) {
            try Print(mut out, "pos {n}\n", (n: p.inputs.Len()))
        }
        err(e) {
            try Print(mut out, "pos {c}\n", (c: Code(e)))
        }
    }
    try Pos.Usage(mut out)
    match Only.Parse(sys.arena, sys.args[1..]) {
        ok(o) {
            try Print(mut out, "only {a} {i}\n", (a: o.a, i: o.i))
        }
        err(e) {
            try Print(mut out, "only {c}\n", (c: Code(e)))
        }
    }
    try Only.Usage(mut out)
}
`
	exe := cliBuild(t, src)
	r := runNative(t, exe, "", "x", "y")
	wantRun(t, r, 0, "none ok\nnone unknown\n[]\npos 2\nArguments:\n  <inputs>...  Inputs.\nonly unknown\n"+
		"Options:\n  --a <int>     (required)\n  --args <str>\n  --i\n")
	r = runNative(t, exe, "", "--a", "7", "--i")
	wantRun(t, r, 0, "none ok\nnone unknown\n[]\npos unknown\nArguments:\n  <inputs>...  Inputs.\nonly 7 true\n"+
		"Options:\n  --a <int>     (required)\n  --args <str>\n  --i\n")
}

// TestSTDCLI_GenErrors: every refused T and field is E0903 at its line,
// with no E0905 beside it.
func TestSTDCLI_GenErrors(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, decls, at, msg string }{
		{"scalar", "gen cli.Args(i32)\n", "gen cli.Args(i32)", "only a struct"},
		{"enum", "enum E {\n    A\n}\n\ngen cli.Args(E)\n", "enum E {", "only a struct"},
		{"unexported", "struct H {\n    export a: int\n    b: int\n}\n\ngen cli.Args(H)\n", "    b: int", "every field must be exported"},
		{"u8", "struct U {\n    export a: u8\n}\n\ngen cli.Args(U)\n", "    export a: u8", "a field must be bool"},
		{"optional bool", "struct B {\n    export a: ?bool\n}\n\ngen cli.Args(B)\n", "    export a: ?bool", "a field must be bool"},
		{"slice of int", "struct S {\n    export a: []int\n}\n\ngen cli.Args(S)\n", "    export a: []int", "a field must be bool"},
		{"two positionals", "struct P {\n    export a: []str\n    export b: []str\n}\n\ngen cli.Args(P)\n", "    export b: []str", "only one []str"},
		{"list", "struct L {\n    export a: List[str]\n}\n\ngen cli.Args(L)\n", "    export a: List[str]", "a field must be bool"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := "import std.cli\n\n" + c.decls + "\nexport Main(sys: &System) {\n}\n"
			out, status := cliCheck(t, src)
			if status == 0 {
				t.Fatalf("mk check passed, want E0903")
			}
			first := strings.SplitN(out, "\n", 2)[0]
			if !strings.Contains(first, "E0903") || !strings.Contains(out, c.msg) || strings.Contains(out, "E0905") {
				t.Fatalf("want E0903 with %q alone, got:\n%s", c.msg, out)
			}
			line := 1 + strings.Count(src[:strings.Index(src, c.at)], "\n")
			if !strings.HasPrefix(first, fmt.Sprintf("main.mk:%d:", line)) {
				t.Errorf("want the error at main.mk:%d, got:\n%s", line, out)
			}
		})
	}
}

// TestSTDCLI_LiveStdChecks: the tree's own std, with no fixture, checks a
// program that imports std.cli and std.log and runs a gen line, so these
// modules never break other builds (std is checked whole). Every
// other test also runs over the tree's std unchanged, so the Usage golden
// checks the help column that Field.Doc gives.
func TestSTDCLI_LiveStdChecks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	r := &cliRoot{pkg: root + "/p", std: copyStd(t, root, nil), cache: root + "/cache"}
	src := "import std.cli\nimport std.log\n\nstruct A {\n    /// Help.\n    export v: bool\n}\n\ngen cli.Args(A)\n\n" +
		"export Main(sys: &System): cli.Error|WriteError!void {\n    a := try A.Parse(sys.arena, sys.args[1..])\n    var out := sys.Stdout()\n" +
		"    var l := log.Logger.New(mut out, log.Level.Info)\n    try l.Info(\"v {v}\", (v: a.v))\n    try A.Usage(mut out)\n}\n"
	for name, text := range map[string]string{"main.mk": src, "pkg.mk": cliPkg} {
		if err := os.MkdirAll(r.pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(r.pkg+"/"+name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, status := r.mk(t, "check", "--target=x86_64-windows", "."); status != 0 {
		t.Fatalf("mk check over the tree's std: exit %d\n%s", status, out)
	}
}
