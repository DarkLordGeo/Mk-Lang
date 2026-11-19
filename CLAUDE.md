# Working in the Mk repository

Read [CONTRIBUTING.md](CONTRIBUTING.md) first: it has the repository layout, how to build and test, and the rules every change follows. [doc/README.md](doc/README.md) lists the documentation; `doc/reference.md` and `doc/memory.md` are the language's rules, and [doc/compiler.md](doc/compiler.md) describes the compiler.

Beyond CONTRIBUTING: run `go test -p 2` so a test run leaves the machine usable, and follow [Linux tests and WSL](CONTRIBUTING.md#linux-tests-and-wsl) and [timing tests](CONTRIBUTING.md#timing-tests-and-benchmarks) to the letter: an agent breaks them most often.
