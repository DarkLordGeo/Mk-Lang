// Why: profile-guided builds are faster on their own workload, so their
// numbers must stay in a labelled table of their own (tests/bench/README.md
// "PGO table") and never feed another table's ratio.
//
// What: `tools/bench publish -pgo`: the mk-pgo and mk-pgo-gen columns,
// buildPGO, trainPGO, pgoFlag and renderPGO.
//
// How: each same-algorithm row's Mk program is built two ways by one mk
// executable, as in every other table (`mk`, --pgo=off) and optimised with a
// profile of the row's own workload (`mk-pgo`), and the table is written to
// results/<date>/pgo. The profile comes from a training run per row: an
// instrumented build (--pgo-gen) run once at the row's quick arguments with
// MK_PROFILE_DIR in the work directory, merged by `mk profile merge` into
// <work>/pgo/<row>/<row>.mkprof, never into the tree (a default.mkprof under
// the benchmark trees is still refused). The mk-pgo column is in no other
// table: it is not in columns, so allRowColumns, buildAll and every other
// subcommand never see it; only buildPGO builds it. The table compares Mk
// with itself and never feeds another table's ratio.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// pgoColumn is the PGO table's second column: the row's mk source built
// with --pgo=<its trained profile>. Its facts are the mk column's.
var pgoColumn = column{ID: "mk-pgo", Lang: "mk", Bounds: "checked", Overflow: "trap", Panic: "abort (exit 101)", LTO: "ThinLTO", Linker: "lld-link", CRT: "none (Mk runtime, kernel32 only)", StdV2: "yes (built with the program)", PGO: true}

// pgoGenColumn is the instrumented build of a training run (--pgo-gen); it
// is built into the work directory and never timed.
var pgoGenColumn = column{ID: "mk-pgo-gen", Lang: "mk", Bounds: "checked", Overflow: "trap", PGO: true}

// pgoColumnIDs is the columns of the PGO table, in table order.
var pgoColumnIDs = []string{"mk", "mk-pgo"}

// pgoProfile is where the training run of row r leaves its merged
// profile: under the work directory, one directory per row.
func pgoProfile(work string, r *row) string {
	name := strings.ReplaceAll(r.id(), "/", "_")
	return filepath.Join(work, "pgo", name, name+".mkprof")
}

// pgoRows is the rows the PGO table measures: the same-algorithm rows with
// an Mk source.
func pgoRows(rows []*row) []*row {
	return slices.DeleteFunc(slices.Clone(rows), func(r *row) bool { return r.Table != "same" || r.Sources["mk"] == "" })
}

// buildPGO builds the PGO table's two columns of every row: mk as every
// table builds it, then the training run and mk-pgo. A row whose training
// fails has its mk-pgo build marked failed with the reason.
func buildPGO(t *toolchain, tag string, rows []*row, log func(string)) []*built {
	var out []*built
	for _, r := range rows {
		out = append(out, buildAll(t, tag, []*row{r}, func(c column) bool { return c.ID == "mk" }, log)...)
		b := &built{Row: r, Col: pgoColumn, Exe: exeName(t.Work, tag, r, pgoColumn)}
		if err := t.trainPGO(tag, r, log); err != nil {
			b.Err = fmt.Errorf("training run: %w", err)
		} else if b.Cmd, b.Err = t.command(r, pgoColumn, b.Exe); b.Err == nil {
			b.Output, b.Err = buildOne(b, log)
		}
		if b.Err != nil {
			log(fmt.Sprintf("build %s %s: %v", r.id(), pgoColumn.ID, b.Err))
		}
		out = append(out, b)
	}
	return out
}

// trainPGO makes row r's profile, unless the one in the work directory was
// made by the same instrumented executable and arguments: it builds the
// instrumented program (stamped like any build), runs it once at the row's
// quick arguments in the row's environment with MK_PROFILE_DIR set to a
// fresh directory beside the profile (runEnv clears the inherited one),
// requires the row's quick checksum, and merges what the run wrote
// with `mk profile merge`.
func (t *toolchain) trainPGO(tag string, r *row, log func(string)) error {
	prof := pgoProfile(t.Work, r)
	dir := filepath.Dir(prof)
	gen := &built{Row: r, Col: pgoGenColumn, Exe: filepath.Join(dir, "gen", "mk-pgo-gen.exe")}
	var err error
	if gen.Cmd, err = t.command(r, pgoGenColumn, gen.Exe); err != nil {
		return err
	}
	if _, err := buildOne(gen, log); err != nil {
		return err
	}
	exe, err := os.ReadFile(gen.Exe)
	if err != nil {
		return err
	}
	h := sha256.New()
	h.Write(exe)
	fmt.Fprintf(h, "%q\n", r.runQuick)
	key := hex.EncodeToString(h.Sum(nil))
	stampFile := prof + ".stamp"
	if old, err := os.ReadFile(stampFile); err == nil && string(old) == key {
		if _, err := os.Stat(prof); err == nil {
			return nil
		}
	}
	os.Remove(stampFile)
	raw := filepath.Join(dir, "raw")
	if err := os.RemoveAll(raw); err != nil {
		return err
	}
	if err := os.MkdirAll(raw, 0o755); err != nil {
		return err
	}
	env := append(rowEnv(r), "MK_PROFILE_DIR="+raw)
	out, _, err := runOnceEnv(gen.Exe, r.runQuick, env)
	if err != nil {
		return err
	}
	if r.QuickSum != "" && out != r.QuickSum {
		return fmt.Errorf("the instrumented program printed %q, the row's quick checksum is %q", out, r.QuickSum)
	}
	ins, err := filepath.Glob(filepath.Join(raw, "*.mkprof"))
	if err != nil {
		return err
	}
	if len(ins) == 0 {
		return errors.New("the instrumented program wrote no .mkprof into MK_PROFILE_DIR")
	}
	slices.Sort(ins)
	if t.Mk == "" {
		return errors.New("mk not found")
	}
	cmd := exec.Command(t.Mk, append([]string{"profile", "merge", "-o", prof}, ins...)...)
	cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(t.Work, "mkcache"))
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mk profile merge: %v: %s", err, firstLines(string(b), 10))
	}
	log(fmt.Sprintf("trained %s: %d profile(s) merged into %s", r.id(), len(ins), prof))
	return os.WriteFile(stampFile, []byte(key), 0o644)
}

// pgoFlag is the --pgo flag of an Mk column's build: off for every column
// of every table but the PGO table's two builds.
func (t *toolchain) pgoFlag(r *row, c column) string {
	switch c.ID {
	case pgoColumn.ID:
		return "--pgo=" + pgoProfile(t.Work, r)
	case pgoGenColumn.ID:
		return "--pgo-gen"
	}
	return "--pgo=off"
}

// renderPGO is the PGO table's section of RESULTS.md: each row's two cells
// and the ratio of medians, mk-pgo over mk, with its interval, and the
// median over the rows.
func renderPGO(res *results, date string) string {
	var w strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&w, format, a...) }
	p("# PGO\n\n")
	p("From `tests/bench/results/%s/pgo/results.json`. A labelled table: Mk against itself on the same-algorithm rows, each row's Mk program built by one mk executable as in every other table (`mk`: `--pgo=off`) and with a profile of the row's own workload (`mk-pgo`: `--pgo=<profile>`). Each profile comes from one training run: the program built with `--pgo-gen`, run once at the row's quick arguments (`bench.json` `quick`) with `MK_PROFILE_DIR` in the work directory, its profiles merged by `mk profile merge`; the profile stays in the work directory, never in the tree. So the profile is trained on the row's own input at a smaller size, the favourable case for PGO: a profile from another workload may give less. The ratio is mk-pgo over mk, a ratio of medians with its 95%% bootstrap interval: under 1 is faster with the profile. Each program still prints the row's checksum, which tools/bench checks before timing it. PGO is off in every other table, and this table never feeds a cross-language ratio, the median mk / c or a budget.\n\n", date)
	for i := range res.Runs {
		renderFacts(&w, res, i)
	}
	ps := budgetPrices(res, "mk-pgo", "mk")
	p("| Row | mk | mk-pgo | mk-pgo / mk [95%%] |\n|---|---|---|---|\n")
	for _, r := range res.Rows {
		cs := rowCells(res, r.ID)
		p("| %s |", r.ID)
		for _, col := range pgoColumnIDs {
			c := findCell(cs, col)
			switch {
			case c == nil:
				p(" - |")
			case c.Excluded != "":
				p(" excluded (%s) |", cellText(c.Excluded))
			default:
				noisy := ""
				if c.Noisy {
					noisy = ", noisy"
				}
				p(" %s (min %s, MAD %s, RSD %.1f%%%s) |", fmtSec(c.Summary.Median), fmtSec(c.Summary.Min), fmtSec(c.Summary.MAD), c.Summary.RSD*100, noisy)
			}
		}
		ratio := "-"
		for _, x := range ps {
			if x.Row == r.ID {
				ratio = fmtRatio(x.Price)
			}
		}
		p(" %s |\n", ratio)
	}
	m, ci := medianPrice(ps), medianPriceCI(ps)
	p("\n- **Median over %d rows:** mk-pgo / mk %.2f [%.2f, %.2f] (a 95%% bootstrap interval with the rows fixed). No bar: the table is labelled and informs, it gates nothing.\n", len(ps), m, ci.Lo, ci.Hi)
	p("\nArguments and checksums are the same-algorithm rows' (`bench.json`); every timed build's command is in `results.json` (`builds`), mk-pgo's naming its profile.\n\n")
	return w.String()
}
