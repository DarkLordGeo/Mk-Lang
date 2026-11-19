// Why: std.sys.LibcBuild() (std/sys/linux/syscall.mk) is a constant of
// the build, the driver's system-libc mode; folding it before collection
// keeps the branch the mode cannot take out of the binary, so a static bin
// holds no PthreadStart and a libc-mode bin no clone path.
//
// What: libcInit and constFoldOf, the fold of every LibcBuild call to
// Options.LibcMode.
//
// How: the call folds as UsesTasks does (resolve.go), with the value known
// before collection, so nothing the untaken branch alone calls is
// collected. The fold equals the process's own answer, because the driver
// links the libc-mode entry, which fills std's Libc table before mk_start,
// exactly when it sets LibcMode (internal/driver native.go checks the link
// plan against it).

package mono

import (
	"mk/internal/mir"
	"mk/internal/types"
)

var keyLibcBuild = types.ItemKey("std.sys", "LibcBuild")

// libcInit sets the fold of LibcBuild when std declares it (Linux only).
func (c *collector) libcInit() {
	if id, ok := c.env.Items().ByKey(keyLibcBuild); ok {
		c.opt.libc = taskFold{item: id, value: c.opt.LibcMode}
	}
}

// constFoldOf is the build-constant fold that call t is of: UsesTasks
// or LibcBuild.
func (bl *builder) constFoldOf(t *mir.Terminator) (taskFold, bool) {
	if bl.opt.tasks.calls(t) {
		return bl.opt.tasks, true
	}
	if bl.opt.libc.calls(t) {
		return bl.opt.libc, true
	}
	return taskFold{}, false
}
