// Why: std/pkg.mk's targets must map the same module names on every target,
// so std.sys.lc, macOS's libSystem names, needs a module elsewhere too.
//
// What: an empty placeholder for std.sys.lc on every target but os "macos".
//
// How: no system layer but macOS's imports it; the real module is
// std/lc/macos.
