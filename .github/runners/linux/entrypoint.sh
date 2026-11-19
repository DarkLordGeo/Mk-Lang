#!/bin/bash
# Why: the container must come back as the same registered runner after a
# restart, without a new registration token.
#
# What: starts the GitHub Actions runner, refusing to start an unregistered
# one.
#
# How: start.ps1 registers the runner once in a one-off container; the
# registration lives in the mk-runner-linux volume mounted here, so this
# script only checks for it and runs the listener.
set -euo pipefail
cd /home/runner/actions-runner
if [ ! -f .runner ]; then
  echo "runner is not registered: run .github/runners/linux/start.ps1" >&2
  exit 1
fi
exec ./run.sh
