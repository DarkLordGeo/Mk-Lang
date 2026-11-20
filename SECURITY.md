# Security

## Reporting a vulnerability

Report a vulnerability privately, through GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability** ([direct link](https://github.com/zerodayokay/Mk-Lang/security/advisories/new)). Do not open a public issue for it.

Include the output of `mk version`, the operating system, a minimal program or input that shows the problem, and what an attacker gains. The discussion happens in the advisory thread.

There is no bug bounty.

## Scope

In scope:

- The standard library and the runtime in `std/`, in particular code that reads untrusted input: `std.net`, `std.http`, `std.tls`, `std.x509`, the crypto primitives, and the parsers (JSON, CSV, regex, URLs, compression).
- The compiler accepting a program without `unsafe` that breaks memory safety when it runs: a use after free, an out-of-bounds access, a data race.
- `mk upgrade` and the package tools: installing a release whose signature or hash does not check, or a dependency that does not match `pkg.sum`.
- `kits/web` and the VS Code extension in `editors/vscode`.

Out of scope:

- Bugs in LLVM, clang, lld or the operating system. Report those to their projects.
- The benchmark baselines in `tests/bench` and `tests/bench-lanes`, which are test programs.
- Code inside a user's `unsafe` blocks, and C code linked through `c_sources` or `import c`.
- A program that runs out of memory or time on input its author did not bound, unless the standard library promises a bound.
