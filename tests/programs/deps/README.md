# deps: a package with a path dependency

A package with a path dependency builds and runs. `app/` is a
package whose `pkg.mk` names `units` with `path: "../units"`; it imports the dependency's root
module (`import units`) and its module in `conv/` (`import units.conv`). `mk run` in `app/`
prints `app.out` and exits 0.

`TestPrograms` builds single files only (a `.cases` file names `<name>.mk`, and a file root never
reads a `pkg.mk`), so this program is run by hand until the corpus runner builds package
directories.
