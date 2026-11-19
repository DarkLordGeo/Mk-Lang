// Why: a Main that makes a Sim and frees it without ever making a task panicked
// (invalid memory access at std/task/wait.mk): Sim.Free wakes the Sim's park
// words before the task system's bucket table exists.
// What: TestStdDefects_SimFreeWithNoTask.
// How: builds and runs that program and wants a clean exit.

package std

import "testing"

// TestStdDefects_SimFreeWithNoTask: mk run of a Main that makes a Sim,
// prints twice and frees it exits 0 with both lines, dev and release.
func TestStdDefects_SimFreeWithNoTask(t *testing.T) {
	files := map[string]string{
		"main.mk": `import std.sim

export Main(sys: &System): WriteError!void {
    var s := sim.Sim.New(sys.arena, 1).Must()
    defer s.Free()
    try sys.Print("one\n")
    try sys.Print("two\n")
}
`,
	}
	for _, flags := range [][]string{{"run", "main.mk"}, {"run", "--release", "main.mk"}} {
		out, status := dfRun(t, files, flags...)
		if status != 0 || out != "one\ntwo\n" {
			t.Errorf("mk %v: exit %d, output\n%s\nwant exit 0 and \"one\\ntwo\\n\"", flags, status, out)
		}
	}
}
