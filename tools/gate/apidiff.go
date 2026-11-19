// Why: a release may break std's public API only where its notes say so; the
// apidiff step catches every other break before it ships.
//
// What: apidiffStep, apidiffVerdict (reads mk apidiff's JSON and exit status)
// and namesItem (matches a breaking entry against apidiffBreaking in
// apidiff_list.go), plus extractStd. Full and soak tiers; -skip apidiff.
//
// How: the std of apidiffBase, the previous release's tag, is extracted with
// git archive into the work directory, and `go run ./cmd/mk apidiff --std
// --json <old std> std` compares the public modules. A breaking entry fails
// the step unless the breaking list names its item by qualified name. The
// base is a fixed tag rather than -since, because -since is HEAD by default
// and would compare std with itself. 0.1.0 is the first release: there is no
// earlier std, apidiffBase is empty and the step passes with a note. The next
// release sets apidiffBase to "v0.1.0" and lists its breaking changes.

package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// apidiffBase is the tag of the previous release, whose std the step
// compares with; "" while no earlier release exists.
const apidiffBase = ""

// apidiffChange is one entry of mk apidiff --json.
type apidiffChange struct {
	Severity string `json:"severity"`
	Key      string `json:"key"`
	What     string `json:"what"`
	Old      string `json:"old"`
	New      string `json:"new"`
}

func (g *gate) apidiffStep() {
	switch {
	case g.skip["apidiff"]:
		g.skipStep("apidiff", "-skip apidiff")
		return
	case g.tier == "quick":
		g.skipStep("apidiff", "full and soak tiers only")
		return
	}
	s := g.newStep("apidiff")
	if apidiffBase == "" {
		s.Notes = append(s.Notes, "no earlier release: nothing to compare std with")
		fmt.Println("   no earlier release: nothing to compare std with")
		return
	}
	old := filepath.Join(g.work, "apidiff-"+fileName(apidiffBase))
	if err := os.RemoveAll(old); err != nil {
		fail(s, err.Error())
		return
	}
	if err := g.extractStd(apidiffBase, old); err != nil {
		fail(s, fmt.Sprintf("extract %s's std: %v", apidiffBase, err))
		return
	}
	listed := apidiffBreaking
	var doc []byte
	code := g.exec(s, spec{argv: []string{g.goExe, "run", "./cmd/mk", "apidiff", "--std", "--json", filepath.Join(old, "std"), "std"}}, func(l string) {
		if strings.HasPrefix(strings.TrimSpace(l), `{"changes"`) {
			doc = []byte(l)
			return
		}
		fmt.Printf("   %s\n", l)
	})
	unlisted, notes, err := apidiffVerdict(doc, code, listed)
	s.Notes = append(s.Notes, notes...)
	switch {
	case err != nil:
		fail(s, err.Error())
	case len(unlisted) > 0:
		s.Failed = unlisted
		fail(s, fmt.Sprintf("%d breaking changes against %s that the breaking list (apidiffBreaking in apidiff_list.go) does not name", len(unlisted), apidiffBase))
	}
}

// apidiffVerdict reads mk apidiff's JSON document and exit status: the
// breaking entries whose item listed does not name, as "key what: old ->
// new", and a note per change. A status of 2, or no document, is an error;
// an incomplete comparison (the old std no longer checks) is an error of
// its own.
func apidiffVerdict(doc []byte, code int, listed []string) (unlisted, notes []string, err error) {
	if doc == nil || code > 1 || code < 0 {
		return nil, nil, fmt.Errorf("mk apidiff --std exit status %d without its JSON document", code)
	}
	var d struct {
		Changes    []apidiffChange `json:"changes"`
		Incomplete bool            `json:"incomplete"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, nil, fmt.Errorf("mk apidiff --std: %v", err)
	}
	if d.Incomplete {
		return nil, nil, errors.New("incomplete: " + apidiffBase + "'s std does not check far enough under this compiler to compare (its diagnostics are in the log)")
	}
	for _, c := range d.Changes {
		line := fmt.Sprintf("%s %s %s: %s -> %s", c.Severity, c.Key, c.What, c.Old, c.New)
		if c.Severity == "breaking" && !namesItem(listed, c.Key) {
			unlisted = append(unlisted, line)
			line += " (not in the breaking list)"
		}
		notes = append(notes, line)
	}
	return unlisted, notes, nil
}

// namesItem reports whether one of the listed names is key's item, by
// its qualified name: the whole key (module.Name or
// module.Type.Member), or, for a name written without a module (one that
// does not begin "std."), the prelude's item of that name, std.core's
// (`Method.Flows` lists std.core.Method.Flows). A trailing name alone
// lists nothing else: `Bytes` is no std.x.T.Bytes.
func namesItem(listed []string, key string) bool {
	for _, n := range listed {
		switch {
		case n == "":
		case key == n:
			return true
		case !strings.HasPrefix(n, "std.") && key == "std.core."+n:
			return true
		}
	}
	return false
}

// extractStd writes rev's std/ under dir (dir/std/...), from git archive.
func (g *gate) extractStd(rev, dir string) error {
	var o bytes.Buffer
	code, err := g.start([]string{"git", "-c", "core.autocrlf=false", "archive", "--format=tar", rev, "std"}, g.env(spec{}), &o)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git archive exit status %d: %s", code, strings.TrimSpace(o.String()))
	}
	tr := tar.NewReader(&o)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("archive entry %q leaves the directory", h.Name)
		}
		p := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return err
			}
		}
	}
}
