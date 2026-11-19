// Why: hello world is at most 65 KiB on both targets; a size regression in
// the runtime or the link shows here first.
// What: the hello-world size budget test.
// How: a release build of tests/programs/hello.mk is at most 66,560 bytes as
// the driver writes it, on x86_64-windows and x86_64-linux. Only the link
// runs, so the Linux leaf needs no WSL; whether hello runs is TestPrograms'
// leaf. The build has cmd/mk's default settings for its target (frame
// pointers off on cliFramePointersOff, on elsewhere), so the budget sees
// what `mk build --release` gives; cmd/mk's TestHelloBudgetCLIDefault builds
// it through the CLI itself.

package driver

import (
	"os"
	"path/filepath"
	"testing"

	"mk/internal/types"
)

const helloBudget = 66560

// cliFramePointersOff is the target for which cmd/mk's framePointersDefault
// is off; every other target's is on. TestCLIDefaultsInDriverTests in cmd/mk
// ties the two.
const cliFramePointersOff = "x86_64-windows"

func TestHelloWorldWithinBudget(t *testing.T) {
	tools := needTools(t)
	root, err := filepath.Abs(filepath.Join("..", "..", "tests", "programs", "hello.mk"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target types.Target
		out    string
	}{{types.X86_64Windows, "hello.exe"}, {types.X86_64Linux, "hello"}} {
		t.Run(tc.target.Name, func(t *testing.T) {
			s, err := NewSession(Config{Root: root, Target: tc.target, Profile: types.Release, Tools: tools, CacheDir: t.TempDir(),
				FramePointers: tc.target.Name != cliFramePointersOff})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(t.TempDir(), tc.out))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("release build for %s:\n%s", tc.target.Name, r.Report.Text(r.Sources))
			}
			fi, err := os.Stat(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			if fi.Size() > helloBudget {
				t.Errorf("release hello for %s is %d bytes, over the %d-byte budget", tc.target.Name, fi.Size(), helloBudget)
			}
			t.Logf("release hello for %s: %d bytes", tc.target.Name, fi.Size())
		})
	}
}
