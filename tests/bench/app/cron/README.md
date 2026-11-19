# cron (app, Tier A)

**An Mk-only row** (tests/bench/README.md, "Tier A"). The Mk side is the cron dogfood, `Dogfoods/cron`, unchanged, built as its bin `cron` (the package also has `jobkit`). The row has no twin, so it has no peer, no ratio to another language and no defect line: it is compared across mk binaries (`tools/bench compare --base <mk A> --head <mk B> -cols mk cron`).

**Task.** The dogfood's bench case: `cron sim {pkg}/cases/data/busy.tab [--days N]`. cron reads the crontab (the dogfood's own, named through `{pkg}`: four frequent jobs in Europe/Berlin, America/Sao_Paulo and Asia/Kathmandu) and runs its scheduler under std.sim's simulated clock for N days (365 by default, 3,660 at most), with its lock and its log on the simulation's disk. It prints the fires that a time-zone transition skipped or repeated, then the summary by job.

**Check.** The SHA-256 of the standard output (`digest`).

**Environment.** `MK_CARRIERS=1`.

**Sizes.** The quick arguments are the dogfood's bench case (365 days); the publish arguments run 3,660 days, sim's largest, and are provisional: `verify -publish-size` fixes them in the quiet window.
