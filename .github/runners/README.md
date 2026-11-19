# Self-hosted runners

Mk's CI runs on two self-hosted GitHub Actions runners, both labelled `mk`:

| Runner | Where | Started by |
|---|---|---|
| `mk-windows` | the maintainer's Windows machine, as that user | `windows/start.ps1` |
| `mk-linux-docker` | an Ubuntu 24.04 container in Docker on the same machine | `linux/start.ps1` |

Both scripts register the runner with a token from `gh api`, so run them where `gh` is logged in to an account with admin rights on the repository.

The Windows runner installs into `-Dir`, by default `$env:SystemDrive\actions-runner\mk-windows`. Pass `-Dir <dir>` to keep an existing runner where it is: started on a directory with no runner, the script installs one there and registers it under the same name, which replaces the old install's registration.

```powershell
pwsh -NoProfile -File .github/runners/windows/start.ps1 -Autostart
pwsh -NoProfile -File .github/runners/linux/start.ps1
```

The Windows runner needs Go 1.27, LLVM 21 (`clang`, `lld`) in `C:\Program Files\LLVM`, Git for Windows (its `bash` runs the workflow steps) and Node.js (to package the VS Code extension for releases). The Linux image installs its own Go and LLVM.

## Security

The repository is public, and a workflow runs whatever code its commit contains. So:

- `ci.yml` runs on pushes to `main`, on pull requests whose branch is in this repository, and by hand. Its job skips pull requests from forks, but a pull request runs the workflow files of its own commit, so a fork can remove that guard or add a workflow that asks for these runners. What holds such a run back is the approval setting below: a fork's run waits for a maintainer, and approving it runs the fork's workflow files on this machine. Never approve one; read the change and push it to a branch here instead.
- `nightly.yml` and `release.yml` run only on `main` and on tags pushed to this repository.
- Keep **Settings > Actions > General > Fork pull request workflows from outside collaborators** at *Require approval for all outside collaborators*, the strictest option.
- The Windows runner runs as the maintainer, with that account's files and its `gh` login, which has admin rights on the repository. Any job can read them, including one from a branch pushed by a collaborator, so write access to the repository amounts to admin access. Give write access only to accounts trusted with admin rights, or run the runner as a dedicated local account (`config.cmd --runasservice --windowslogonaccount <account>`) without the maintainer's login or files.
- The Linux container runs as an unprivileged user, with capped CPU and memory and no Docker socket.

## Windows and WSL

Docker Desktop runs the Linux container in its WSL 2 VM. The Linux tests that Windows drives through `wsl.exe` (`MK_LINUX=1`, under `tools/wsl-lock.ps1`) share that VM, so do not run them while a long Linux CI job runs.
