// Why: a corpus run is read by people in a terminal and by tools as JSON;
// both need every unit's outcome and every pending unit's reason, in the
// same order on every run.
//
// What: WriteText (two summary lines, a std line when std limits judging,
// one line per unit, the totals, then std's own report) and WriteJSON (one
// object per unit, then the summary).
//
// How: a FAIL line shows the expected and the emitted diagnostics; a PENDING
// line carries its reason. When std is judged only partway, the names it
// lacks are printed in full. Output never depends on map order.

package corpus

import (
	"fmt"
	"io"
	"strings"

	"mk/internal/diag"
)

// WriteText writes the text report: the two summary lines, a std line
// when std limits judging, one line per unit
// ("doc/reference.md:17 ok PENDING std: ..."), then the totals, and
// std's own report when it has diagnostics.
func WriteText(w io.Writer, r *Result) error {
	var b strings.Builder
	s := r.Summary
	fmt.Fprintf(&b, "%d blocks: %d ok, %d parse, %d reject\n", s.Blocks, s.OK, s.Parse, s.Reject)
	fmt.Fprintf(&b, "parsed: %d of %d\n", s.Parsed, s.Blocks)
	if line := stdLine(r.Std); line != "" {
		b.WriteString(line + "\n")
	}
	for _, u := range r.Units {
		b.WriteString(u.Unit.Key + " " + verdictText(u.Unit) + " " + u.Status.String())
		if u.Reason != "" {
			b.WriteString(" " + u.Reason)
		}
		if u.Explained != "" {
			b.WriteString(" (pending.txt: " + u.Explained + ")")
		}
		b.WriteByte('\n')
	}
	if s.Units > 0 {
		fmt.Fprintf(&b, "%d conformance units\n", s.Units)
	}
	fmt.Fprintf(&b, "pass %d, fail %d, pending %d (listed %d)\n", s.Pass, s.Fail, s.Pending, s.Explained)
	if s.Skip > 0 {
		fmt.Fprintf(&b, "skipped %d (their // host: names another target)\n", s.Skip)
	}
	if r.Build {
		fmt.Fprintf(&b, "build: %d units built, %d Mains run, %d built but not run\n", s.Built, s.Ran, s.RunSkipped)
	}
	if r.Std.Text != "" {
		b.WriteString("std report:\n" + r.Std.Text)
		if !strings.HasSuffix(r.Std.Text, "\n") {
			b.WriteByte('\n')
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// stdLine says how far std lets units be judged, with every name and
// module std lacks, or "" when std is complete and clean.
func stdLine(st StdInfo) string {
	n := len(st.State.Missing)
	d := len(st.State.Report.Diags)
	if n == 0 && d == 0 {
		return ""
	}
	line := fmt.Sprintf("std: units judged through %s; %d diagnostics; %d names and modules undeclared", st.Through, d, n)
	if n > 0 {
		// The report prints the prelude names std lacks, every one of them,
		// with no truncation.
		line += " (" + strings.Join(st.State.Missing, ", ") + ")"
	}
	return line
}

// verdictText is the verdict as written: "ok", "parse", "reject E0501 E0502".
func verdictText(u *Unit) string {
	if u.Verdict != VerdictReject {
		return u.Verdict.String()
	}
	parts := []string{"reject"}
	for _, c := range u.Codes {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, " ")
}

// WriteJSON writes JSON Lines: one object per unit, keys always present and
// in this order: unit, verdict, codes, status, reason, listed, expected,
// emitted; then one summary object with the keys summary and std, and
// build (built, ran, run_skipped) after them under --build.
func WriteJSON(w io.Writer, r *Result) error {
	var b []byte
	for _, u := range r.Units {
		b = append(b, unitJSON(u)...)
		b = append(b, '\n')
	}
	b = append(b, summaryJSON(r)...)
	b = append(b, '\n')
	_, err := w.Write(b)
	return err
}

func unitJSON(u UnitResult) []byte {
	var w diag.JSONWriter
	w.BeginObject()
	w.Key("unit")
	w.String(u.Unit.Key)
	w.Key("verdict")
	w.String(u.Unit.Verdict.String())
	w.Key("codes")
	w.BeginArray()
	for _, c := range u.Unit.Codes {
		w.String(c.String())
	}
	w.EndArray()
	w.Key("status")
	w.String(strings.ToLower(u.Status.String()))
	w.Key("reason")
	w.String(u.Reason)
	w.Key("listed")
	w.String(u.Explained)
	w.Key("expected")
	w.BeginArray()
	for _, m := range u.Expected {
		w.BeginObject()
		w.Key("file")
		w.String(m.File)
		w.Key("line")
		w.Int(int64(m.Line))
		w.Key("code")
		w.String(m.Code.String())
		w.EndObject()
	}
	w.EndArray()
	w.Key("emitted")
	w.BeginArray()
	for _, g := range u.Emitted {
		w.BeginObject()
		w.Key("file")
		w.String(g.File)
		w.Key("line")
		w.Int(int64(g.Line))
		w.Key("code")
		w.String(g.Code.String())
		w.Key("phase")
		w.String(g.Phase.String())
		w.Key("message")
		w.String(g.Message)
		w.EndObject()
	}
	w.EndArray()
	w.EndObject()
	return w.Finish()
}

func summaryJSON(r *Result) []byte {
	s := r.Summary
	var w diag.JSONWriter
	w.BeginObject()
	w.Key("summary")
	w.BeginObject()
	for _, kv := range []struct {
		k string
		v int
	}{{"blocks", s.Blocks}, {"ok", s.OK}, {"parse", s.Parse}, {"reject", s.Reject}, {"parsed", s.Parsed},
		{"units", s.Units}, {"pass", s.Pass}, {"fail", s.Fail}, {"pending", s.Pending}, {"listed", s.Explained}, {"skip", s.Skip}} {
		w.Key(kv.k)
		w.Int(int64(kv.v))
	}
	w.EndObject()
	w.Key("std")
	w.BeginObject()
	w.Key("through")
	w.String(r.Std.Through.String())
	w.Key("diagnostics")
	w.Int(int64(len(r.Std.State.Report.Diags)))
	w.Key("missing")
	w.BeginArray()
	for _, m := range r.Std.State.Missing {
		w.String(m)
	}
	w.EndArray()
	w.EndObject()
	if r.Build {
		w.Key("build")
		w.BeginObject()
		for _, kv := range []struct {
			k string
			v int
		}{{"built", s.Built}, {"ran", s.Ran}, {"run_skipped", s.RunSkipped}} {
			w.Key(kv.k)
			w.Int(int64(kv.v))
		}
		w.EndObject()
	}
	w.EndObject()
	return w.Finish()
}
