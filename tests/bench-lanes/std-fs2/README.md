# std-fs2 benchmark rows (idiomatic standard library)

Rows for the idiomatic table: std.fs's tree operations against each standard library's own. One program per language (`fs/`), three variants, each printing one checksum line. Only `tools/bench` publishes a number from these rows.

The tree: 10 directories `d<i>`, each with 10 directories `s<j>` of 10 one-byte files `f<k>`, 1,110 entries, made under the language's temporary directory as `mkbench-fs2-<language>` (Windows: `TMP`; Linux: `TMPDIR` or `/tmp`, the distribution's own file system, so a Linux cell never measures the drive's 9p mount). A run first removes a tree a killed run left.

| Row | Work | Mk | Baselines |
|---|---|---|---|
| `fs-walk` | make the tree, list it `reps` times, remove it; prints `reps dirs files names` (the root not counted; names are the entries' name bytes) | `fs.Walk`, `Walker.Next` until `none` | Go `filepath.WalkDir`; Rust recursion over `fs::read_dir` and `DirEntry::file_type` (std has no walker); Zig `Dir.walk`; C `FindFirstFileExW` (`FindExInfoBasic`, `FIND_FIRST_EX_LARGE_FETCH`) or `opendir`/`readdir` with `d_type` |
| `fs-mkrm` | `reps` times make the tree and remove it; prints `reps entries fails` | `fs.MakeDirAll`, `fs.WriteAll`, `fs.RemoveAll` | Go `os.MkdirAll`, `os.WriteFile`, `os.RemoveAll`; Rust `fs::create_dir_all`, `fs::write`, `fs::remove_dir_all`; Zig `Dir.createDirPath`, `Dir.writeFile`, `Dir.deleteTree`; C `CreateDirectoryW`/`CreateFileW` and recursion with `DeleteFileW`/`RemoveDirectoryW`, or `mkdir`/`open` and `unlinkat` relative to an open directory |
| `fs-copy` | copy the generated 32 MiB `words` file `reps` times onto one destination in the temporary directory; prints `reps bytes` | `fs.Copy` (CopyFileExW, copy_file_range) | Go `os.Create` and `io.Copy` (copy_file_range on Linux); Rust `fs::copy`; Zig `Dir.copyFile`; C `CopyFileExW` or a `copy_file_range` loop |

Caveats: `fs-walk`'s run also makes and removes the tree once, since `tools/bench` subtracts a baseline only in a GB/s row, so its time is the walks plus one make-and-remove; how that moves each ratio depends on the cells' make-and-remove costs, which `fs-mkrm` measures on its own. On Linux `fs-copy`'s source is the data file on the drive's mount, read over 9p by every cell alike.
