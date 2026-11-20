# Runners

## What runs where

| Workflow | Runs on | When |
|---|---|---|
| `ci.yml` | GitHub-hosted `windows-2025` and `ubuntu-24.04` | every push to `main`, every pull request (forks included), by hand |
| `release.yml` | GitHub-hosted `windows-2025` and `ubuntu-24.04` | a `v*` tag pushed to this repository |
| `nightly.yml` | the self-hosted runners below, labelled `mk` | once a night on `main`, and by hand on `main` |

The hosted images are pinned by version; moving to a newer one is a change of its own.

`ci.yml` runs with a read-only token and no secrets, so it runs a fork's pull request as it is. It installs Go and LLVM 21 on each job, runs a checks job (build, vet, gofmt, the documentation tests), and splits the Go tests across parallel jobs: `internal/driver` in shards, `internal/corpus`, `internal/lsp`, and the other packages. `tools/ci/test.sh` runs each part. `release.yml` calls `ci.yml` on both hosts and builds a bundle on each. Only its signing job gets the release key, from the `release` environment, and only its publishing job gets a token that can write ([Releases](../../CONTRIBUTING.md#releases)).

The nightly gate runs on the self-hosted runners because it reads the evidence directory, which is not in the repository, and runs std's own test suite, which takes hours:

| Runner | Where | Started by |
|---|---|---|
| `mk-windows` | the maintainer's Windows machine, as a Windows service under its own account | `windows/service.ps1` |
| `mk-linux-docker` | an Ubuntu 24.04 container in Docker on the same machine | `linux/start.ps1` |

## Setting up the self-hosted runners

The Windows runner, from an elevated PowerShell in which `gh` is logged in with admin rights on the repository:

```powershell
pwsh -NoProfile -File .github/runners/windows/service.ps1 -Evidence <evidence directory>
```

It installs the runner in `-Dir` (by default `$env:SystemDrive\actions-runner\mk-windows`, where `windows/start.ps1` put it) as a service under the virtual account `NT SERVICE\<service name>`. Run it again for every new runner version: the runner is registered with `--disableupdate` and does not update itself, and GitHub stops sending jobs to a runner that falls too far behind. Each run changes this machine as follows, and prints each change:

- It stops at once if `-Dir`, `-Work`, `-Cache`, `-Temp` or `-Evidence` holds a symbolic link or junction, since the steps below change permissions and delete files throughout those trees.
- It stops and deletes the old service, stops a runner that `start.ps1` started and removes its Startup shortcut, deletes the old registration through the API, extracts the runner from its checked zip over `-Dir` again, and registers it. The registration token comes from `gh` and reaches `config.cmd` in an environment variable, never on a command line or in a file.
- It creates the service with `sc.exe`, stopped, and starts it only after every step below, so no job runs before them. The service has a restricted service SID and only the privileges `SeChangeNotifyPrivilege`, `SeCreateGlobalPrivilege` and `SeIncreaseWorkingSetPrivilege`. Without `SeImpersonatePrivilege`, the public tools that turn a service account into SYSTEM through that privilege do not work. The restricted SID lets the service write only where the account itself, or Everyone, may write, not where a group such as Authenticated Users may. If a job fails on access to a file it should be able to write, `sc.exe sidtype <service> unrestricted` and a restart of the service lift that restriction alone.
- `-Dir` stops inheriting from its drive: Administrators and SYSTEM have full control, the account can read and run it, and no other user can read it (the runner's credentials are in it). The account may modify only `_work`, `_diag`, `cache`, `tmp` and `home` (or `-Work`, `-Cache` and `-Temp` where given), and read `-Evidence`.
- On every fixed drive but the system drive, the drive's root gets an entry, inherited by everything on the drive, that denies the account reading, writing and deleting. An explicit grant below it, such as the one on `-Evidence`, still wins. Windows applies the entry to every file of the drive, which takes a while on a large one. The script then reads the ACLs back: each root must hold the entry, and no file or directory at the top of such a drive may grant the account, Everyone, Authenticated Users, Users or the service groups write access in an entry of its own. It stops without starting the service if one does. It cannot log on as the account, so this is a reading of the ACLs, not a test of every path.
- It writes the job-started hook ([Security](#security)) to `-Dir\hooks` and names it in the runner's `.env`, with the job's `PATH`, `HOME`, Go and mk caches, temp directory and `MK_EVIDENCE`.
- It sets below-normal CPU priority for every process named `RunnerService.exe`, `Runner.Listener.exe` or `Runner.Worker.exe` on the machine (Image File Execution Options), so the runner and every build and test it starts run below normal.

Go 1.27, LLVM 21 and Git for Windows must be installed for all users, in `C:\Program Files\Go`, `C:\Program Files\LLVM` and `C:\Program Files\Git`; the service account cannot read the maintainer's profile. Run the script again after a change to `-Dir` or `-Evidence`. The account cannot write `-Dir`'s root, so the runner cannot save a settings or credential refresh that GitHub sends; it logs "Failed to update runner" in `_diag` and keeps running with its registered settings. That message means: run the script again. To see a job's privileges, add `whoami /priv` to a step of `nightly.yml`.

The Linux runner:

```powershell
pwsh -NoProfile -File .github/runners/linux/start.ps1
```

The Linux image installs its own Go and LLVM, and the runner's job-started hook.

## Security

The repository is public, and a workflow runs whatever code its commit contains.

- A pull request runs the workflow files of its own commit, so a pull request, from a fork or from a branch of this repository, can change a workflow, or add one, that asks for the self-hosted runners. Repository runners cannot be put in a runner group that limits which workflows they run, so each runner has a job-started hook (`ACTIONS_RUNNER_HOOK_JOB_STARTED`) that fails every job, before any of its steps runs, unless `GITHUB_REPOSITORY` is `zerodayokay/Mk-Lang`, `GITHUB_EVENT_NAME` is `schedule` or `workflow_dispatch`, and `GITHUB_WORKFLOW_REF` is `zerodayokay/Mk-Lang/.github/workflows/nightly.yml@refs/heads/main`. The hook lives where the runner's account cannot change it: `-Dir\hooks\job-started.ps1` on Windows, written by `service.ps1`; `/usr/local/lib/mk-runner/job-started.sh` in the Linux image, owned by root, from `linux/job-started.sh`.
- Keep **Settings > Actions > General > Approval for running fork pull request workflows from contributors** at *Require approval for all external contributors*, the strictest option. Before approving a fork's run, check that the pull request changes nothing under `.github/`: then it runs this repository's workflows, and of those only `ci.yml` runs for a pull request, on hosted runners. Never approve a run of one that does change `.github/`; read the change and push it to a branch here instead.
- `nightly.yml` runs only on `main`, and `release.yml` only on `v*` tags pushed to this repository.
- `windows/start.ps1` runs the runner as the logged-in user, with no hook: use it only to try the runner, and remove its registration afterwards.
- The Linux container runs as an unprivileged user, with capped CPU and memory and no Docker socket.

## Windows and WSL

WSL distributions belong to a user, so the service account has none, and the nightly gate skips its Linux-through-WSL step. Docker Desktop runs the Linux container in its WSL 2 VM; the Linux tests that the maintainer drives through `wsl.exe` (`MK_LINUX=1`, under `tools/wsl-lock.ps1`) share that VM, so do not run them while the nightly Linux job runs.
