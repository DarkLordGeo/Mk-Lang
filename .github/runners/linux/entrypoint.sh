#!/bin/bash
# Why: the container must come back as the same registered runner after a
# restart, without a new registration token, and never without its
# job-started hook.
#
# What: starts the GitHub Actions runner, refusing to start an unregistered
# one or one whose hook is missing or overridden.
#
# How: start.ps1 registers the runner once in a one-off container; the
# registration lives in the mk-runner-linux volume mounted here, so this
# script only checks for it and runs the listener. The hook's variable comes
# from the image; the runner would let a line in the volume's .env replace
# it, so such a line stops the start.
set -euo pipefail
cd /home/runner/actions-runner
if [ ! -f .runner ]; then
  echo "runner is not registered: run .github/runners/linux/start.ps1" >&2
  exit 1
fi
if [ -z "${ACTIONS_RUNNER_HOOK_JOB_STARTED:-}" ] || [ ! -f "$ACTIONS_RUNNER_HOOK_JOB_STARTED" ]; then
  echo "the image sets no job-started hook: rebuild it with start.ps1" >&2
  exit 1
fi
if [ -f .env ] && grep -q '^ACTIONS_RUNNER_HOOK_' .env; then
  echo "$PWD/.env sets a runner hook, which would replace the image's: remove the line" >&2
  exit 1
fi
exec ./run.sh
