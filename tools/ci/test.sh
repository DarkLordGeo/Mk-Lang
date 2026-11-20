#!/usr/bin/env bash
# Why: one test package, internal/driver, takes most of a CI run (over an
# hour of its 934 tests run one after another), so a run lasts as long as
# that package. Splitting its tests across processes, and keeping the
# memory-heavy tests apart, makes the job several times shorter.
#
# What: runs `go test` over every package but std, the way the CI jobs and
# the release job do, in phases, and prints each log and a summary.
#
# How: phase 1 runs ALONE_TESTS of internal/driver by themselves (tests that
# need most of the machine's memory). Phase 2 runs the rest of
# internal/driver as DRIVER_SHARDS processes, each with every n-th test name,
# beside one `go test -p OTHER_P` over the other packages except LAST_PKGS.
# Phase 3 runs LAST_PKGS one at a time. Each phase sets GOMEMLIMIT from
# ALONE_MEM, SHARD_MEM or LAST_MEM when given. The driver runs are verbose,
# so the summary can list the slowest tests. DRY_RUN=1 prints the commands.

set -u
timeout=${TEST_TIMEOUT:-3h}
shards=${DRIVER_SHARDS:-1}
otherp=${OTHER_P:-4}
alone=${ALONE_TESTS:-}
last=${LAST_PKGS:-}
logs="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/test-logs"
mkdir -p "$logs"
driver=mk/internal/driver
status=0
started=$(date +%s)

# run NAME MEM CMD...: runs CMD with GOMEMLIMIT=MEM (if MEM is set), output to
# NAME's log, and records a failure.
run() {
    local name=$1 mem=$2
    shift 2
    if [ "${DRY_RUN:-}" = 1 ]; then
        echo "[$name] GOMEMLIMIT=${mem:-unset} $*" | cut -c1-200
        return 0
    fi
    if [ -n "$mem" ]; then
        GOMEMLIMIT=$mem "$@" >"$logs/$name.log" 2>&1
    else
        "$@" >"$logs/$name.log" 2>&1
    fi
    local rc=$?
    echo "$rc" >"$logs/$name.rc"
    echo "$(date +%H:%M:%S) $name finished: exit $rc"
}

phase() { echo "== $1 ($(( $(date +%s) - started ))s in)"; }

all=$(go list ./... | grep -v '^mk/std$')
others=$(echo "$all" | grep -v -x "$driver")
for p in $last; do others=$(echo "$others" | grep -v -x "$p"); done

if [ -n "$alone" ]; then
    phase "phase 1: $driver tests that run alone"
    run driver-alone "${ALONE_MEM:-}" go test -v -p 1 -timeout "$timeout" -run "^(${alone})$" ./internal/driver
fi

phase "phase 2: $driver in $shards shards, other packages with -p $otherp"
tests=$(go test -list . ./internal/driver | grep '^Test')
if [ -n "$alone" ]; then tests=$(echo "$tests" | grep -v -x -E "$alone"); fi
for ((i = 0; i < shards; i++)); do
    group=$(echo "$tests" | awk -v n="$shards" -v i="$i" 'NR % n == i' | paste -sd '|' -)
    [ -z "$group" ] && continue
    run "driver-$i" "${SHARD_MEM:-}" go test -v -p 1 -timeout "$timeout" -run "^(${group})$" ./internal/driver &
done
run others "${SHARD_MEM:-}" go test -p "$otherp" -timeout "$timeout" $others &
wait

if [ -n "$last" ]; then
    phase "phase 3: $last, one at a time"
    run last "${LAST_MEM:-}" go test -p 1 -timeout "$timeout" $last
fi

[ "${DRY_RUN:-}" = 1 ] && exit 0
phase "done"
for f in "$logs"/*.log; do
    name=$(basename "$f" .log)
    echo "::group::$name"
    cat "$f"
    echo "::endgroup::"
done
echo "== summary"
for f in "$logs"/*.rc; do
    name=$(basename "$f" .rc)
    rc=$(cat "$f")
    echo "$name: exit $rc"
    [ "$rc" != 0 ] && status=1
done
grep -h -E '^(--- FAIL|FAIL|panic:)' "$logs"/*.log | head -100
echo "== the 40 slowest top-level tests of internal/driver"
grep -h -E '^--- (PASS|FAIL|SKIP): Test[^ /]+ \([0-9.]+s\)' "$logs"/driver-*.log |
    sed -E 's/^--- [A-Z]+: (Test[^ ]+) \(([0-9.]+)s\)/\2 \1/' | sort -rn | head -40
exit $status
