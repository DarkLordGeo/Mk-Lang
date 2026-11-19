// Why: std.log's level filter and line shape are what every program's logs
// look like, and its message is Print's format.
// What: the level filter of every method and of Log, the line shape of New
// and NewTimed, and the message against logModel, a Go model.
// How: seeded random format strings written into the program as literals
// (a const f is known at compile time), at dev and release.

package std

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// logRecord is the record every walk test passes, as Mk source, and
// logValue formats its field name with the spec code as Line's Hole does:
// 0 none, 1 `:x`, 2 `:.N` (prec N), 3 any other spec.
const logRecord = "(x: 255, n: -2, y: \"s\", zz: true, w: 0.1)"

func logValue(name string, spec, prec int) (string, bool) {
	switch name {
	case "x":
		if spec == 1 {
			return "ff", true
		}
		return "255", true
	case "n":
		if spec == 1 {
			return strconv.FormatUint(1<<64-2, 16), true
		}
		return "-2", true
	case "y":
		return "s", true
	case "zz":
		return "true", true
	case "w":
		if spec == 2 {
			return strconv.FormatFloat(0.1, 'f', prec, 64), true
		}
		return "0.1", true
	}
	return "", false
}

// logClass is check's fieldClass of a logRecord field: 1 an integer, 2 a
// float, 0 another type.
var logClass = map[string]int{"x": 1, "n": 1, "y": 0, "zz": 0, "w": 2}

// logSpec is Line's SpecCode of a hole's spec text (after the `:`), with
// the precision of a `.N`.
func logSpec(spec string, has bool) (code, prec int) {
	switch {
	case !has:
		return 0, 0
	case spec == "x":
		return 1, 0
	case len(spec) >= 2 && len(spec) <= 3 && spec[0] == '.' && strings.Trim(spec[1:], "0123456789") == "":
		p, _ := strconv.Atoi(spec[1:])
		return 2, p
	}
	return 3, 0
}

// logModel is the text Print writes for f with logRecord, for an f Print
// takes (logFits): `{{` and `}}` are braces, a hole naming a field is its
// value with its spec, and anything else is text (the interim run-time
// walk's model, kept for the formats that compile).
func logModel(f string) string {
	var sb strings.Builder
	lit := 0
	i := 0
	n := len(f)
	for i < n {
		c := f[i]
		if c == '}' && i+1 < n && f[i+1] == '}' {
			sb.WriteString(f[lit : i+1])
			i += 2
			lit = i
			continue
		}
		if c != '{' {
			i++
			continue
		}
		if i+1 < n && f[i+1] == '{' {
			sb.WriteString(f[lit : i+1])
			i += 2
			lit = i
			continue
		}
		j := i + 1
		for j < n && f[j] != '}' && f[j] != '{' {
			j++
		}
		if j == n || f[j] == '{' {
			i++
			continue
		}
		name, spec, has := strings.Cut(f[i+1:j], ":")
		code, prec := logSpec(spec, has)
		v, ok := logValue(name, code, prec)
		if ok {
			sb.WriteString(f[lit:i])
			sb.WriteString(v)
		}
		i = j + 1
		if ok {
			lit = i
		}
	}
	sb.WriteString(f[lit:])
	return sb.String()
}

// logFits is check's scanFormat and matchFormat (internal/check/format.go)
// over logRecord: whether Print takes f, so whether a Logger call with it
// checks (the others are E0706, TestSTDCLI_MismatchIsE0706).
func logFits(f string) bool {
	used := map[string]bool{}
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case '{':
			if i+1 < len(f) && f[i+1] == '{' {
				i++
				continue
			}
			j := i + 1
			for j < len(f) && f[j] != '}' && f[j] != '{' {
				j++
			}
			if j == len(f) || f[j] == '{' {
				return false
			}
			name, spec, has := strings.Cut(f[i+1:j], ":")
			if !logIdent(name) {
				return false
			}
			code, _ := logSpec(spec, has)
			k, ok := logClass[name]
			if code == 3 || !ok || code == 1 && k != 1 || code == 2 && k != 2 {
				return false
			}
			used[name] = true
			i = j
		case '}':
			if i+1 < len(f) && f[i+1] == '}' {
				i++
				continue
			}
			return false
		}
	}
	return len(used) == len(logClass)
}

func logIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// logToks are the tokens of the random formats: every hole of logRecord
// with and without specs, holes Print rejects, lone and doubled braces.
var logToks = []string{"{x}", "{y}", "{zz}", "{n}", "{w}", "{nope}", "{x:x}", "{n:x}", "{y:x}", "{w:x}", "{w:.0}", "{w:.2}",
	"{w:.17}", "{w:.99}", "{w:.100}", "{x:.2}", "{y:.2}", "{x:X}", "{w:.}", "{x:}", "{", "}", "{{", "}}", "a", " ", ":",
	"{x", "x}", "{}", "{{x}}", "b c", "{1x}", "{ x}", "{x{y}"}

// logFormats are n seeded format strings: fixed edge cases, then half
// free sequences of logToks and half every field's hole, shuffled, with
// tokens between, so that a fair share of them fit.
func logFormats(seed int64, n int) []string {
	r := rand.New(rand.NewSource(seed))
	out := []string{"", "plain", "{x}", "{", "}", "{{", "}}", "{x}{y}{zz}", "{zz} {nope} {x", "{x:x}{n:x}{y}{zz}{w:.2}"}
	for len(out) < n {
		var sb strings.Builder
		if r.Intn(2) == 0 {
			for j := r.Intn(7); j > 0; j-- {
				sb.WriteString(logToks[r.Intn(len(logToks))])
			}
		} else {
			holes := []string{"{x}", "{n}", "{y}", "{zz}", "{w}"}
			alt := map[string][]string{"{x}": {"{x:x}"}, "{n}": {"{n:x}"}, "{w}": {"{w:.0}", "{w:.2}", "{w:.17}", "{w:.99}"}}
			r.Shuffle(len(holes), func(a, b int) { holes[a], holes[b] = holes[b], holes[a] })
			for _, h := range holes {
				if a := alt[h]; a != nil && r.Intn(2) == 0 {
					h = a[r.Intn(len(a))]
				}
				sb.WriteString(h)
				if r.Intn(3) == 0 {
					sb.WriteString([]string{" ", "a", "{{", "}}", ":", "b c", "{x}", "}"}[r.Intn(8)])
				}
			}
		}
		out = append(out, sb.String())
	}
	return out
}

// logProgram calls Info once per format, each with logRecord.
func logProgram(fs []string) string {
	var sb strings.Builder
	sb.WriteString("import std.log\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    var l := log.Logger.New(mut out, log.Level.Info)\n")
	for _, f := range fs {
		fmt.Fprintf(&sb, "    try l.Info(\"%s\", %s)\n", f, logRecord)
	}
	sb.WriteString("}\n")
	return sb.String()
}

// TestSTDCLI_LogWalkMatchesModel: at dev and release, every format logFits
// takes logs "INFO " and logModel's text (the others do not compile: E0706,
// TestSTDCLI_SpecHonoured). A planted change to the model fails it.
func TestSTDCLI_LogWalkMatchesModel(t *testing.T) {
	t.Parallel()
	all := logFormats(7, 400)
	var fit []string
	for _, f := range all {
		if logFits(f) {
			fit = append(fit, f)
		}
	}
	if len(fit) < 60 || len(fit) > len(all)-100 {
		t.Fatalf("%d of %d formats fit: the generator lost its mix", len(fit), len(all))
	}
	for _, mode := range cliModes {
		fs := fit
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			r := runNative(t, cliBuild(t, logProgram(fs), mode.flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s", r.code, r.stderr)
			}
			got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
			if len(got) != len(fs) {
				t.Fatalf("%d lines for %d formats:\n%s", len(got), len(fs), r.stdout)
			}
			for k, f := range fs {
				if want := "INFO " + logModel(f); got[k] != want {
					t.Errorf("format %q:\n got %q\nwant %q", f, got[k], want)
				}
			}
		})
	}
}

// TestSTDCLI_LogLevels: a logger at each level writes exactly the calls at
// or above it, through each method and through Log, one line per call.
func TestSTDCLI_LogLevels(t *testing.T) {
	t.Parallel()
	src := `import std.log

Each(out: mut Writer, level: log.Level): WriteError!void {
    var l := log.Logger.New(out, level)
    try l.Debug("d {n}", (n: 1))
    try l.Info("i {n}", (n: 2))
    try l.Warn("w {n}", (n: 3))
    try l.Error("e {n}", (n: 4))
    try l.Log(log.Level.Debug, "ld", ())
    try l.Log(log.Level.Info, "li", ())
    try l.Log(log.Level.Warn, "lw", ())
    try l.Log(log.Level.Error, "le", ())
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Each(mut out, log.Level.Debug)
    try out.Str("--\n")
    try Each(mut out, log.Level.Info)
    try out.Str("--\n")
    try Each(mut out, log.Level.Warn)
    try out.Str("--\n")
    try Each(mut out, log.Level.Error)
    var text := Text.New(sys.arena)
    var tw := text.Writer()
    var tl := log.Logger.New(mut tw, log.Level.Info)
    try tl.Info("into {what}\nsecond line", (what: "text"))
    try Print(mut out, "--\n{n} bytes\n", (n: text.Str().Len()))
}
`
	all := "DEBUG d 1\nINFO i 2\nWARN w 3\nERROR e 4\nDEBUG ld\nINFO li\nWARN lw\nERROR le\n"
	info := "INFO i 2\nWARN w 3\nERROR e 4\nINFO li\nWARN lw\nERROR le\n"
	warn := "WARN w 3\nERROR e 4\nWARN lw\nERROR le\n"
	errs := "ERROR e 4\nERROR le\n"
	want := all + "--\n" + info + "--\n" + warn + "--\n" + errs + "--\n" + fmt.Sprint(len("INFO into text\nsecond line\n")) + " bytes\n"
	for _, mode := range cliModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			r := runNative(t, cliBuild(t, src, mode.flags...), "")
			r.stdout = strings.ReplaceAll(r.stdout, "\r\n", "\n")
			wantRun(t, r, 0, want)
		})
	}
}

// TestSTDCLI_LogTimed: NewTimed's lines on the real clock start with the
// seconds and six digits of microseconds since the logger was made, and
// never go back. No bound on the time itself: a default run asserts no
// wall-clock figure; TestSTDCLI_TimedUnderSim and
// the Stamp blocks of std/log/tests_log.mk check the exact bytes.
func TestSTDCLI_LogTimed(t *testing.T) {
	t.Parallel()
	src := `import std.log

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var l := log.Logger.NewTimed(sys.io, mut out, log.Level.Warn)
    try l.Info("dropped", ())
    for k in 0..5 {
        try l.Warn("tick {k}", (k: k))
    }
    try l.Log(log.Level.Error, "done", ())
}
`
	r := runNative(t, cliBuild(t, src), "")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
	re := regexp.MustCompile(`^([0-9]+)\.([0-9]{6}) (WARN tick [0-4]|ERROR done)$`)
	if len(lines) != 6 {
		t.Fatalf("%d lines:\n%s", len(lines), r.stdout)
	}
	last := ""
	for k, l := range lines {
		m := re.FindStringSubmatch(l)
		if m == nil {
			t.Fatalf("line %d %q does not match %s", k, l, re)
		}
		stamp := fmt.Sprintf("%020s.%s", m[1], m[2])
		if stamp < last {
			t.Errorf("line %d went back in time: %q", k, l)
		}
		last = stamp
	}
}
