// Why: std/pkg.mk's targets must map the same module names on every target,
// so std.linux needs a module on targets other than Linux.
//
// What: an empty placeholder for std.linux on every target but os "linux".
//
// How: nothing can reach it: an import of std.linux off x86_64-linux is
// E0202 reason target-selected at the import (internal/driver's
// stdTargetOnly). The real module is std/linux/linux.
