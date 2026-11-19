// Why: frame pointers let profilers and debuggers walk the stack cheaply;
// some users trade them for a register, so the setting must be a switch.
//
// What: framePointersDefault, framePointersValue, framePointersEnv and
// framePointersFlag: --frame-pointers=on|off on mk build and mk run, and
// MK_FRAME_POINTERS (on or off) for every command that builds, mk test
// included.
//
// How: the flag wins over the variable, the variable over the default. The
// default depends on the build's target (framePointersDefault): on, except
// on x86_64-windows, where Win64 walks frames through .pdata and the panic
// backtrace uses RtlVirtualUnwind, so a frame pointer buys nothing there and
// costs a register in every function.

package main

import (
	"fmt"
	"io"
	"os"

	"mk/internal/driver"
)

// framePointersOffTarget is the one target whose default is off.
const framePointersOffTarget = "x86_64-windows"

// framePointersDefault is the setting with neither the flag nor the
// variable for the target named target ("" is the host's, as
// driver.Config's zero Target is): a cross-build gets its target's default,
// not the host's.
func framePointersDefault(target string) bool {
	if target == "" {
		target = driver.HostTarget().Name
	}
	return target != framePointersOffTarget
}

// framePointersValue reads "on" or "off"; ok is false for anything else.
func framePointersValue(v string) (on, ok bool) {
	switch v {
	case "on":
		return true, true
	case "off":
		return false, true
	}
	return false, false
}

// framePointersEnv sets cfg.FramePointers from MK_FRAME_POINTERS, or the
// default for cfg.Target (which the caller sets first); a value other than
// on or off is an error.
func framePointersEnv(cfg *driver.Config, stderr io.Writer) bool {
	cfg.FramePointers = framePointersDefault(cfg.Target.Name)
	v := os.Getenv("MK_FRAME_POINTERS")
	if v == "" {
		return true
	}
	on, ok := framePointersValue(v)
	if !ok {
		fmt.Fprintf(stderr, "mk: MK_FRAME_POINTERS is %q (on or off)\n", v)
		return false
	}
	cfg.FramePointers = on
	return true
}

// framePointersFlag applies --frame-pointers when given ("" leaves the
// variable's setting).
func framePointersFlag(cfg *driver.Config, v string, stderr io.Writer) bool {
	if v == "" {
		return true
	}
	on, ok := framePointersValue(v)
	if !ok {
		fmt.Fprintf(stderr, "mk: unknown --frame-pointers %q (on or off)\n", v)
		return false
	}
	cfg.FramePointers = on
	return true
}
