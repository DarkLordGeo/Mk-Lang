// Why: Calling C is unsafe, so it is confined to one module.
//
// What: Register, which adds the SQL function `normtext` from the package's own
// normtext.c, and SqliteVersion.
//
// How: The only module listed in unsafe_modules; everything else reaches sqlite
// through std.db.sqlite.

import c.normtext
import c.sqlite3

/// Registers the SQL function `normtext` on every sqlite connection opened
/// after this call, and returns its name as the C side reports it. Fails with
/// `none` when sqlite refuses the registration. O(1); allocates nothing.
export Register(): ?str {
    var rc: i32 = 0
    var name := ""
    unsafe "notes_register_normtext only calls sqlite3_auto_extension, and notes_normtext_name returns a static NUL-terminated string" {
        rc = normtext.notes_register_normtext()
        name = str.FromC(normtext.notes_normtext_name())
    }
    if rc != 0 {
        return none
    }
    return some(name)
}

/// The linked sqlite library's version, such as `3.54.1`. O(n); allocates nothing.
export SqliteVersion(): str {
    unsafe "sqlite3_libversion returns a static NUL-terminated string" {
        return str.FromC(sqlite3.sqlite3_libversion())
    }
}
