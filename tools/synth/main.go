// Why: the compiler's edit loop (cold build, no-op rebuild, one-line body
// edit, public signature edit) must be measured at the scale of a large
// program, and the repository holds no Mk program that large; this tool
// writes one.
//
// What: the synth command: run (flags, generate, write), applyEdit (the
// body, sig, reset and noop edits), the synth.json marker and write, which
// writes only the files whose bytes change.
//
// How: plan.go chooses the workspace's shape from the flags alone and
// emit.go writes its Mk text, so the same flags give byte-identical trees.
// `tools/bench editloop` times builds of it; this tool times nothing.

// tools/synth writes a deterministic synthetic workspace for measuring the
// edit loop, and applies the loop's edits to it.
//
//	go run ./tools/synth -out DIR [-lines 1000000] [-packages 8] [-seed 1] [-workspace]
//	go run ./tools/synth -out DIR -edit body|sig|reset|noop [-rev N]
//
// The same flags give byte-identical trees. The layout is a root package
// (lux, its program in app/; see pkgNames in plan.go) whose pkg.mk names
// every other package as a path dependency, each package in a directory of
// the root. -workspace writes the 1.3 workspace form instead (the root
// manifest lists the packages as members).
//
// Edits (the four measurements are a cold build, a no-op rebuild, and the
// two below):
//
//	body   one line in a function body of base's first module, a leaf of the
//	       import graph; each run changes the line again (-rev N sets it)
//	sig    toggles a parameter on base.prim.SigProbe, a public function of a
//	       module every package imports, and its callers (one per module)
//	reset  back to the baseline
//	noop   writes nothing (the no-op rebuild)
//
// The flags and the current edit are kept in DIR/synth.json, so an edit needs
// only -out. Only files whose bytes change are written.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const markerName = "synth.json"

// marker is synth.json: the flags that made the tree and the edit applied.
type marker struct {
	Lines     int    `json:"lines"`
	Packages  int    `json:"packages"`
	Seed      uint64 `json:"seed"`
	Workspace bool   `json:"workspace"`
	Body      int    `json:"body"`
	Sig       bool   `json:"sig"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "synth:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fl := flag.NewFlagSet("synth", flag.ContinueOnError)
	dir := fl.String("out", "", "the workspace directory (required)")
	lines := fl.Int("lines", 1000000, "total lines of Mk to write")
	pkgs := fl.Int("packages", 8, "packages, the root included (at least 2)")
	seed := fl.Uint64("seed", 1, "the seed of every choice")
	ws := fl.Bool("workspace", false, "write the 1.3 workspace form (a root members list)")
	edit := fl.String("edit", "", "apply an E2 edit to an existing tree: body, sig, reset or noop")
	rev := fl.Int("rev", 0, "with -edit body: the body revision to write (default: the next one)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *dir == "" || fl.NArg() != 0 {
		return errors.New("usage: synth -out DIR [-lines N] [-packages P] [-seed S] [-workspace] | -out DIR -edit body|sig|reset|noop [-rev N]")
	}
	if *edit != "" {
		set := map[string]bool{}
		fl.Visit(func(f *flag.Flag) { set[f.Name] = true })
		for _, n := range []string{"lines", "packages", "seed", "workspace"} {
			if set[n] {
				return fmt.Errorf("-%s is read from %s with -edit", n, markerName)
			}
		}
		res, err := applyEdit(*dir, *edit, *rev)
		if err != nil {
			return err
		}
		fmt.Println(res)
		return nil
	}
	cfg := config{Lines: *lines, Packages: *pkgs, Seed: *seed, Workspace: *ws}
	if err := cfg.valid(); err != nil {
		return err
	}
	n, total, err := write(*dir, cfg, state{}, true)
	if err != nil {
		return err
	}
	fmt.Printf("synth: %d lines in %d packages; %d files written to %s\n", total, cfg.Packages, n, *dir)
	return nil
}

func (c config) valid() error {
	if c.Packages < 2 || c.Packages > 64 {
		return fmt.Errorf("-packages %d: between 2 and 64", c.Packages)
	}
	if c.Lines < 1000 || c.Lines > 50000000 {
		return fmt.Errorf("-lines %d: between 1,000 and 50,000,000", c.Lines)
	}
	return nil
}

// applyEdit reads the marker, moves the edit state and rewrites the files
// that change. It returns a line for the user.
func applyEdit(dir, kind string, rev int) (string, error) {
	mk, err := readMarker(dir)
	if err != nil {
		return "", err
	}
	cfg := config{Lines: mk.Lines, Packages: mk.Packages, Seed: mk.Seed, Workspace: mk.Workspace}
	st := state{Body: mk.Body, Sig: mk.Sig}
	switch kind {
	case "body":
		if rev > 0 {
			st.Body = rev
		} else {
			st.Body++
		}
	case "sig":
		st.Sig = !st.Sig
	case "reset":
		st = state{}
	case "noop":
		return "synth: noop: nothing written", nil
	default:
		return "", fmt.Errorf("-edit %q: body, sig, reset or noop", kind)
	}
	n, _, err := write(dir, cfg, st, false)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("synth: edit %s (body revision %d, signature edit %v): %d files written", kind, st.Body, st.Sig, n), nil
}

func readMarker(dir string) (marker, error) {
	var mk marker
	data, err := os.ReadFile(filepath.Join(dir, markerName))
	if err != nil {
		return mk, fmt.Errorf("%s: no synth workspace here (%v)", dir, err)
	}
	if err := json.Unmarshal(data, &mk); err != nil {
		return mk, fmt.Errorf("%s: %v", markerName, err)
	}
	return mk, nil
}

func markerText(cfg config, st state) []byte {
	data, _ := json.MarshalIndent(marker{cfg.Lines, cfg.Packages, cfg.Seed, cfg.Workspace, st.Body, st.Sig}, "", "  ")
	return append(data, '\n')
}

// write generates the tree for cfg and st into dir, writing only files whose
// bytes differ. A fresh generation needs an empty or missing dir, or one this
// tool made with the same flags. It returns the .mk files written and the lines.
func write(dir string, cfg config, st state, fresh bool) (int, int, error) {
	if fresh {
		if mk, err := readMarker(dir); err == nil {
			if mk.Lines != cfg.Lines || mk.Packages != cfg.Packages || mk.Seed != cfg.Seed || mk.Workspace != cfg.Workspace {
				return 0, 0, fmt.Errorf("%s holds a workspace made with other flags; use an empty directory", dir)
			}
		} else if ents, err := os.ReadDir(dir); err == nil && len(ents) > 0 {
			return 0, 0, fmt.Errorf("%s is not empty and holds no %s", dir, markerName)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, 0, err
		}
	}
	o := generate(cfg, st)
	n := 0
	put := func(rel string, data []byte) error {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, data) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if rel != markerName {
			n++
		}
		return os.WriteFile(p, data, 0o644)
	}
	for _, rel := range o.paths() {
		if err := put(rel, []byte(o.files[rel])); err != nil {
			return n, 0, err
		}
	}
	if err := put(markerName, markerText(cfg, st)); err != nil {
		return n, 0, err
	}
	return n, o.lines, nil
}
