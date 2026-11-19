// Why: the build profile flags interact (--release, --profile, --symbols,
// --pgo), and a wrong combination must be refused, not guessed.
// What: TestProfileFlags and TestProfileChecked.
// How: a table of argument lists, each with the profile it must select or the
// refusal it must give.

package main

import (
	"io"
	"testing"

	"mk/internal/driver"
)

func TestProfileFlags(t *testing.T) {
	for _, c := range []struct {
		args                []string
		ok, rel, size, syms bool
	}{
		{nil, true, false, false, false},
		{[]string{"--profile", "size"}, true, true, true, false},
		{[]string{"--profile", "release"}, true, true, false, false},
		{[]string{"--release", "--symbols"}, true, true, false, true},
		{[]string{"--profile", "size", "--symbols"}, true, true, true, true},
		{[]string{"--symbols"}, false, false, false, false},
		{[]string{"--profile", "fast"}, false, false, false, false},
	} {
		var nf nativeFlags
		fs := newNativeFlagSet("build", io.Discard, &nf)
		if _, _, err := parseArgs(fs, c.args, false); err != nil {
			t.Fatal(err)
		}
		cfg, ok := nf.config("", io.Discard)
		if ok != c.ok {
			t.Errorf("%q: ok %v", c.args, ok)
			continue
		}
		if ok && ((cfg.Profile == driver.ProfileRelease) != c.rel || cfg.Size != c.size || cfg.Symbols != c.syms) {
			t.Errorf("%q: profile %v size %v symbols %v", c.args, cfg.Profile, cfg.Size, cfg.Symbols)
		}
	}
}

// TestProfileChecked: mk build and mk run take --profile checked,
// which sets the checked profile (optimized, not Release), keeps
// --symbols, refuses --release and --pgo, and leaves every other flag's
// profile as it was.
func TestProfileChecked(t *testing.T) {
	for _, name := range []string{"build", "run"} {
		for _, c := range []struct {
			args []string
			ok   bool
			syms bool
		}{
			{[]string{"--profile", "checked"}, true, false},
			{[]string{"--profile", "checked", "--symbols"}, true, true},
			{[]string{"--release", "--profile", "checked"}, false, false},
			{[]string{"--profile", "checked", "--pgo", "x.mkprof"}, false, false},
			{[]string{"--profile", "checked", "--pgo-gen"}, false, false},
		} {
			var nf nativeFlags
			fs := newNativeFlagSet(name, io.Discard, &nf)
			if _, _, err := parseArgs(fs, c.args, false); err != nil {
				t.Fatal(err)
			}
			cfg, ok := nf.config("", io.Discard)
			if ok != c.ok {
				t.Errorf("%s %q: ok %v, want %v", name, c.args, ok, c.ok)
				continue
			}
			if ok && (cfg.Profile != driver.ProfileChecked || cfg.Size || cfg.Symbols != c.syms) {
				t.Errorf("%s %q: profile %v size %v symbols %v", name, c.args, cfg.Profile, cfg.Size, cfg.Symbols)
			}
		}
	}
}
