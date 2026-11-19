// Why: each error code has a conformance unit, and the exact JSON that mk
// check --json writes for it is kept as a snapshot, so any change to a
// code's message, facts or fixes shows in the diff.
//
// What: SnapshotDir, Snapshot, SnapshotName, SnapshotCheck, Snapshots (compare
// a run with the files) and WriteSnapshots.
//
// How: a snapshot is diag.Report.WriteJSON's one LF-terminated line with
// the unit's virtual root removed from every path, so it holds no repository
// path. Only a unit the judge passes is snapshotted: a pending unit's bytes
// come from a run that stopped early.

package corpus

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"mk/internal/driver"
)

// SnapshotDir is the repository-relative directory of the per-code units and
// their snapshots.
const SnapshotDir = conformanceDir + "/codes"

// Snapshot is the snapshot of unit u from its result r: r.Report as the JSON
// envelope, with the unit's virtual root made the package root. The session
// places a unit's files at "<u.Name>/<path>" and names its root module u.Name
// and a subdirectory d's module "<u.Name>/d", so "<u.Name>/" is removed from
// every string, which leaves file paths, location facts and subdirectory
// modules as mk check prints them, and the remaining u.Name, the root module's
// path, becomes main, the root module of a package. The bytes then hold no
// repository path, so they do not depend on where the unit sits.
func Snapshot(u *Unit, r driver.BlockResult) []byte {
	b := r.Report.WriteJSON(r.Sources)
	b = bytes.ReplaceAll(b, []byte(u.Name+"/"), nil)
	return bytes.ReplaceAll(b, []byte(u.Name), []byte("main"))
}

// SnapshotName is the snapshot file of a codes/ unit: "<code>.json" beside a
// file unit <code>.mk or a directory unit <code>/, and ""
// for any other unit (extra triggers <code>_<slug>.mk have no snapshot).
func SnapshotName(u *Unit) string {
	dir, base := path.Split(u.Key)
	if strings.TrimSuffix(dir, "/") != SnapshotDir || len(u.Codes) != 1 {
		return ""
	}
	if strings.TrimSuffix(base, ".mk") != u.Codes[0].String() {
		return ""
	}
	return u.Codes[0].String() + ".json"
}

// SnapshotCheck is the comparison of one unit's snapshot with its file.
type SnapshotCheck struct {
	Key  string // the unit's key
	File string // the snapshot's path under the repository root, with '/'
	Got  []byte // Snapshot of the unit's result
	Want []byte // the file's bytes; nil when it does not exist
}

// Matches reports whether the file exists and equals the snapshot.
func (c SnapshotCheck) Matches() bool { return c.Want != nil && bytes.Equal(c.Got, c.Want) }

// Snapshots compares the snapshot of every passing codes/ unit of a run made
// with Options.Conformance against its file under root, read as committed
// (readCommitted: WriteJSON escapes every CR, so a CR LF in the file is a
// checkout's translation). Units that do not
// pass, and units with no SnapshotName, are not compared. The order is
// the run's (path order). The error is an I/O error other than a missing
// file.
func Snapshots(root string, res *Result) ([]SnapshotCheck, error) {
	var out []SnapshotCheck
	for _, ur := range res.Units {
		name := SnapshotName(ur.Unit)
		if name == "" || ur.Status != StatusPass {
			continue
		}
		c := SnapshotCheck{Key: ur.Unit.Key, File: SnapshotDir + "/" + name, Got: ur.Snapshot}
		want, err := readCommitted(filepath.Join(root, filepath.FromSlash(c.File)))
		switch {
		case err == nil:
			c.Want = want
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// WriteSnapshots writes every check whose file is missing or differs, under
// dir (the repository root for go test -run TestSnapshots -update), and
// returns the files it wrote.
func WriteSnapshots(dir string, checks []SnapshotCheck) ([]string, error) {
	var wrote []string
	for _, c := range checks {
		if c.Matches() {
			continue
		}
		full := filepath.Join(dir, filepath.FromSlash(c.File))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return wrote, err
		}
		if err := os.WriteFile(full, c.Got, 0o644); err != nil {
			return wrote, fmt.Errorf("snapshot %s: %w", c.File, err)
		}
		wrote = append(wrote, c.File)
	}
	return wrote, nil
}
