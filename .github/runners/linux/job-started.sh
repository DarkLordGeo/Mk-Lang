#!/bin/sh
# Why: a pull request can carry a workflow that asks for the self-hosted
# runners, and this runner should run nothing but the nightly gate.
#
# What: the runner's job-started hook (ACTIONS_RUNNER_HOOK_JOB_STARTED). It
# fails every job but the nightly workflow of MK_RUNNER_REPO on main,
# started by its schedule or by hand; a failing hook fails the job before
# any of its steps runs.
#
# How: the Dockerfile copies it to a root-owned directory, which the
# runner's user cannot change, and sets both variables in the image.
repo=${MK_RUNNER_REPO:-}
workflow="$repo/.github/workflows/nightly.yml@refs/heads/main"
case "${GITHUB_EVENT_NAME:-}" in
schedule | workflow_dispatch) event=ok ;;
*) event= ;;
esac
if [ -z "$repo" ] || [ "${GITHUB_REPOSITORY:-}" != "$repo" ] || [ -z "$event" ] || [ "${GITHUB_WORKFLOW_REF:-}" != "$workflow" ]; then
	echo "This runner runs only $workflow, on a schedule or by hand; this job is ${GITHUB_WORKFLOW_REF:-} (${GITHUB_EVENT_NAME:-}) of ${GITHUB_REPOSITORY:-}." >&2
	exit 1
fi
exit 0
