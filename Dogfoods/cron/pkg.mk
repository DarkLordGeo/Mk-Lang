// Why: cron is built by mk like any user package.
//
// What: the package `cron` with two console bins: cron itself (src/main) and
// jobkit (src/jobkit), the helper its crontabs and cases run as jobs.
//
// How: no dependencies, C sources or unsafe modules.

const Package = pkg.Package(
    name: "cron",
    bins: [
        pkg.Bin(name: "cron", root: "src/main", link_script: "", cpu: "", subsystem: "console"),
        pkg.Bin(name: "jobkit", root: "src/jobkit", link_script: "", cpu: "", subsystem: "console"),
    ],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
