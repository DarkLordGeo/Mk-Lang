// Why: an mk built with a compiler experiment measures that experiment, not
// Mk, so it must never reach a published table.
// What: TestIdentityKeepsExperiment, TestPublishRefusesExperiment
// and TestPublishRefusesUnknownIdentity.
// How: the identity keeps the "experiment:" line, every publish path refuses
// such a binary (or one it cannot identify), and compare, ir and the tables
// without -publish take it.

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

const (
	defaultVersion    = "mk 1.2.0 commit 0123456789ab\nmk build 0123456789abcdef (go1.27.3 windows/amd64)\nLLVM 21.1.8 from C:\\LLVM\\bin (default location)"
	experimentLine    = "experiment: unroll=100 (never a default build)"
	experimentVersion = "mk 1.2.0 commit 0123456789ab\nmk build 0123456789abcdef (go1.27.3 windows/amd64)\n" + experimentLine + "\nLLVM 21.1.8 from C:\\LLVM\\bin (default location)"
	badVersion        = "mk 1.2.0 commit 0123456789ab\nmk build 0123456789abcdef (go1.27.3 windows/amd64)\nexperiment: experiment \"irce\": unknown name \"irce\"\nLLVM 21.1.8 from C:\\LLVM\\bin (default location)"
)

// TestIdentityKeepsExperiment: mkIdentity keeps the LLVM line and,
// after it, the experiment line (a bad baked value's error line too); a
// default binary's identity has neither the line nor anything new.
func TestIdentityKeepsExperiment(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "mk.exe") // no build information: the fixed "no VCS revision" text
	base := mkIdentity(fake, "")
	if got, want := mkIdentity(fake, defaultVersion), base+", LLVM 21.1.8 from C:\\LLVM\\bin (default location)"; got != want {
		t.Errorf("default binary: mkIdentity = %q, want %q", got, want)
	}
	if e := mkExperiment(mkIdentity(fake, defaultVersion)); e != "" {
		t.Errorf("default binary: mkExperiment of its identity = %q, want none", e)
	}
	id := mkIdentity(fake, experimentVersion)
	if want := base + ", LLVM 21.1.8 from C:\\LLVM\\bin (default location), " + experimentLine; id != want {
		t.Errorf("experiment binary: mkIdentity = %q, want %q", id, want)
	}
	if e := mkExperiment(id); e != experimentLine {
		t.Errorf("mkExperiment of the identity = %q, want %q (publish -mk-version reads it back)", e, experimentLine)
	}
	if e := mkExperiment(badVersion); !strings.HasPrefix(e, "experiment: experiment \"irce\"") {
		t.Errorf("a bad baked value: mkExperiment = %q, want its error line", e)
	}
}

// TestPublishRefusesExperiment: each publish path refuses an
// experiment binary before it reads the tree or takes the machine lock
// (the root is an empty directory, so a path that went on would fail for
// another reason); publish also refuses an identity handed over by
// publish-linux. The same binary is not refused by codegen, compile or
// http without -publish, nor is a default binary.
func TestPublishRefusesExperiment(t *testing.T) {
	exp := helperExe(t, experimentVersion)
	bad := helperExe(t, badVersion)
	def := helperExe(t, defaultVersion)
	root := t.TempDir()
	lock := filepath.Join(t.TempDir(), "bench.lock")
	logf := func(string) {}
	say := func(string) {}
	expID := mkIdentity(exp, experimentVersion)
	for _, c := range []struct {
		name    string
		run     func() error
		refused bool
	}{
		{"publish", func() error {
			return publish(publishOptions{Root: root, Work: t.TempDir(), Mk: exp, LockPath: lock, Log: logf})
		}, true},
		{"publish -budgets", func() error {
			return publish(publishOptions{Root: root, Work: t.TempDir(), Mk: exp, LockPath: lock, Log: logf, Budgets: true})
		}, true},
		{"publish, bad baked value", func() error {
			return publish(publishOptions{Root: root, Work: t.TempDir(), Mk: bad, LockPath: lock, Log: logf})
		}, true},
		{"publish inside WSL (-mk-version)", func() error {
			return publish(publishOptions{Root: root, Work: t.TempDir(), Prebuilt: t.TempDir(), MkVersion: expID, NoLock: true, Platform: "linux", Log: logf})
		}, true},
		{"publish-linux", func() error {
			return publishLinux(linuxOptions{Root: root, Work: t.TempDir(), Mk: exp, LockPath: lock, LinuxWork: "/tmp/w", Log: logf})
		}, true},
		{"codegen -publish", func() error {
			return codegen(codegenOptions{Root: root, Work: t.TempDir(), Mk: exp, Publish: true, Procs: 1, LockPath: lock, Mask: 1, Log: logf}, say)
		}, true},
		{"compile -publish", func() error {
			return compileTable(compileOptions{Root: root, Work: t.TempDir(), Mk: exp, Langs: []string{"mk"}, Publish: true, LockPath: lock, Log: logf}, say)
		}, true},
		{"http -publish", func() error {
			return httpTable(httpOptions{Root: root, Work: t.TempDir(), Mk: exp, Publish: true, LockPath: lock, Log: logf}, say)
		}, true},
		{"codegen", func() error {
			return codegen(codegenOptions{Root: root, Work: t.TempDir(), Mk: exp, Procs: 1, LockPath: lock, Mask: 1, Log: logf}, say)
		}, false},
		{"compile", func() error {
			return compileTable(compileOptions{Root: root, Work: t.TempDir(), Mk: exp, Langs: []string{"no-such-language"}, LockPath: lock, Log: logf}, say)
		}, false},
		{"http", func() error {
			return httpTable(httpOptions{Root: root, Work: t.TempDir(), Mk: exp, LockPath: lock, Log: logf}, say)
		}, false},
		{"codegen -publish, default binary", func() error {
			return codegen(codegenOptions{Root: root, Work: t.TempDir(), Mk: def, Publish: true, Procs: 1, LockPath: lock, Mask: 1, Log: logf}, say)
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			refused := err != nil && strings.Contains(err.Error(), "refusing to publish") && strings.Contains(err.Error(), "tuning experiment")
			if refused != c.refused {
				t.Errorf("refused %v, want %v (error: %v)", refused, c.refused, err)
			}
		})
	}
	if err := refuseExperiment(def, ""); err != nil {
		t.Errorf("a default binary is refused: %v", err)
	}
}

// TestPublishRefusesUnknownIdentity: the
// check fails closed. A binary whose `mk version` cannot be read (no such
// file, empty output, or text with no "mk build" line) is refused, as is a
// check with neither a binary nor a handed-over identity; publish
// -prebuilt, which has no mk to ask, needs -mk-version.
func TestPublishRefusesUnknownIdentity(t *testing.T) {
	root := t.TempDir()
	lock := filepath.Join(t.TempDir(), "bench.lock")
	logf := func(string) {}
	for _, c := range []struct {
		name, mk, want string
	}{
		{"no such binary", filepath.Join(t.TempDir(), "no-such-mk.exe"), "cannot read"},
		{"empty version", helperExe(t, ""), "cannot read"},
		{"no mk build line", helperExe(t, "LLVM 21.1.8 from C:\\LLVM\\bin (default location)"), "cannot read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := publish(publishOptions{Root: root, Work: t.TempDir(), Mk: c.mk, LockPath: lock, Log: logf})
			if err == nil || !strings.Contains(err.Error(), "refusing to publish") || !strings.Contains(err.Error(), c.want) {
				t.Errorf("publish: error %v, want a refusal that says %q", err, c.want)
			}
		})
	}
	t.Run("nothing to check", func(t *testing.T) {
		if err := refuseExperiment("", ""); err == nil || !strings.Contains(err.Error(), "refusing to publish") {
			t.Errorf("refuseExperiment with no binary and no identity: %v, want a refusal", err)
		}
	})
	t.Run("prebuilt without -mk-version", func(t *testing.T) {
		err := publish(publishOptions{Root: root, Work: t.TempDir(), Prebuilt: t.TempDir(), NoLock: true, Platform: "linux", Log: logf})
		if err == nil || !strings.Contains(err.Error(), "-prebuilt needs -mk-version") {
			t.Errorf("publish -prebuilt with no -mk-version: error %v, want one that says -prebuilt needs -mk-version", err)
		}
	})
	t.Run("default binary", func(t *testing.T) {
		if err := refuseExperiment(helperExe(t, defaultVersion), ""); err != nil {
			t.Errorf("a default binary is refused: %v", err)
		}
	})
}
