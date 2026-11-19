// Why: import completion once offered std's module classes as modules.
// What: TestLSPImportCompletionModulesOnly.
// How: asks completion after `import ` and checks that every std item is a bare
// module path, with no class in parentheses.

package lsp

import (
	"strings"
	"testing"
)

// TestLSPImportCompletionModulesOnly: after `import `, every std item
// is a module path (`std.` and no class in parentheses), though std holds
// modules that are not Stable (std.dl, std.tls), and no item is a line
// prefix of mk query modules.
func TestLSPImportCompletionModulesOnly(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	// The editor's text while `import ` is typed does not parse; the
	// completion answers from the last checked state.
	text := "import \n" + fixtureMain
	c.change(mainURI, 2, text)
	got := completeAt(t, c, mainURI, text, "import ", 7)
	std := 0
	for label, it := range got {
		if it.Kind != complModule {
			t.Errorf("import completion offers %q of kind %d", label, it.Kind)
			continue
		}
		if strings.ContainsAny(label, "():, ") {
			t.Errorf("import completion offers %q, no module path", label)
		}
		if it.Detail == "std" {
			std++
			if !strings.HasPrefix(label, "std.") {
				t.Errorf("std item %q is no std module", label)
			}
		} else if strings.HasPrefix(label, "std") || strings.HasPrefix(label, "prelude") {
			t.Errorf("package item %q (%q) is a line of mk query modules", label, it.Detail)
		}
	}
	if _, ok := got["std.fs"]; std == 0 || !ok {
		t.Errorf("import completion offers %d std modules, std.fs among them: %v", std, ok)
	}
	c.shutdown()
}
