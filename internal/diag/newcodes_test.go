// Why: package, tail-call, range, file-input and C-form diagnostics each
// need a known code, a stable name and the right report group.
// What: TestNewCodes, a table of those codes with name and phase.
// How: checks Name, DefaultPhase and that Groups holds that phase alone.

package diag

import "testing"

// TestNewCodes: each code in the table is known by its catalogue name
// and reported in the group its emitter runs in, and in that group alone.
func TestNewCodes(t *testing.T) {
	for _, tc := range []struct {
		c     Code
		name  string
		phase Phase
	}{
		{E0214, "internal-module", PhaseImports},
		{E0215, "grant-missing", PhaseImports},
		{E0216, "mk-too-old", PhaseImports},
		{E0217, "test-dep-outside-test", PhaseTypes},
		{E0312, "tail-call-signature", PhaseTypes},
		{E0313, "tail-call-argument", PhaseTypes},
		{E0314, "tail-call-target", PhaseTypes},
		{E0315, "empty-range", PhaseTypes},
		{E0910, "file-input", PhaseTypes},
		{E1109, "no-c-form", PhaseTypes},
	} {
		if !tc.c.IsKnown() || tc.c.Name() != tc.name {
			t.Errorf("%s: name %q, want %q", tc.c, tc.c.Name(), tc.name)
		}
		if got := DefaultPhase(tc.c); got != tc.phase {
			t.Errorf("%s: phase %v, want %v", tc.c, got, tc.phase)
		}
		if g := Groups(tc.c); len(g) != 1 || g[0] != tc.phase {
			t.Errorf("%s: groups %v, want only %v", tc.c, g, tc.phase)
		}
	}
}
