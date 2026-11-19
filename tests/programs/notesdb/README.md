# notesdb: the notes service, sqlite half

The notes service of `doc/examples.md` with its store in sqlite. `main.mk` is
`../notes.mk` (routes, client task, transcript) with its `Mutex[Store]` replaced by one
`std.db.sqlite` `Db` on `:memory:`, shared by every connection task through `&Db`. Ids come from
`insert ... returning id`, and a delete of a missing note is a `delete ... returning id` that steps
no row. So the output `notesdb.out` is byte for byte `../notes.out`.

The sqlite amalgamation is not part of the repository, so `pkg.mk` names
`sqlite3.c` and `sqlite3.h` that are not here (`.gitignore` keeps them out). To run it, copy the
package to a scratch directory, copy the two files from `$MK_EVIDENCE/vendor/sqlite`
(or any copy of the sqlite amalgamation), then:

    mk build --target=x86_64-windows .            # or --release; prints nothing, exit 0
    ./notesdb.exe > got.out && diff got.out notesdb.out

and for Linux, under `tools/wsl-lock.ps1`, `mk build --target=x86_64-linux .` and
`wsl.exe --cd <dir> -- ./notesdb`. The C flags in `pkg.mk` are std-sqlite's `sqliteFlags`
(std/std-sqlite_harness_test.go). The port flake of `../notes.cases` applies here too.

`TestPrograms` builds single files only, so this package is run by hand until the corpus runner
builds package directories and copies the amalgamation in.
