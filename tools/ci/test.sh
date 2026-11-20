#!/usr/bin/env bash
# Why: one test package, internal/driver, takes most of a CI run (over an
# hour of its 934 tests run one after another), so a run lasts as long as
# that package. Splitting its tests across processes or jobs, and keeping
# the memory-heavy tests apart, makes the run several times shorter.
#
# What: runs `go test` over every package but std, the way the CI and
# release jobs do, and prints each log and a summary. CI_MODE picks how:
#
#   phased (the default) runs everything on one machine, in phases.
#   driver runs shard SHARD_INDEX (0 to SHARD_COUNT-1) of internal/driver:
#     every SHARD_COUNT-th top-level test, with -parallel DRIVER_PARALLEL
#     (default 4) and no GOMEMLIMIT: a soft memory cap slowed the shards'
#     tests down, and -parallel bounds their heap instead.
#   others runs PKGS (a list of import paths), or when PKGS is empty every
#     package but std, internal/driver and EXCLUDE_PKGS, with -p OTHER_P.
#     The packages of SHORT_PKGS among them run in a second `go test`, with
#     -short: tools/bench's full tests build cells with whatever C, Rust and
#     MSVC toolchains the machine has, which a hosted image does not match.
#
# How, in phased mode: phase 1 runs ALONE_TESTS of internal/driver by
# themselves (tests that need most of the machine's memory). Phase 2 runs
# the rest of internal/driver as DRIVER_SHARDS processes, each with every
# n-th test name, beside one `go test -p OTHER_P` over the other packages
# except LAST_PKGS. Phase 3 runs LAST_PKGS one at a time. Each phase sets
# GOMEMLIMIT from ALONE_MEM, SHARD_MEM or LAST_MEM when given. The driver
# runs are verbose, so the summary can list the slowest tests. TEST_TIMEOUT
# is each `go test -timeout` (default 3h). DRY_RUN=1 prints the commands.

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

# failures LOG...: the failure and panic lines of the logs.
failures() { grep -h -E '^(--- FAIL|FAIL|panic:)' "$@" | head -100; }

# slowest LOG...: the 40 slowest top-level tests in verbose driver logs.
slowest() {
    echo "== the 40 slowest top-level tests of internal/driver"
    grep -h -E '^--- (PASS|FAIL|SKIP): Test[^ /]+ \([0-9.]+s\)' "$@" |
        sed -E 's/^--- [A-Z]+: (Test[^ ]+) \(([0-9.]+)s\)/\2 \1/' | sort -rn | head -40
}

all=$(go list ./... | grep -v '^mk/std$') || exit 1
others=$(echo "$all" | grep -v -x "$driver")

case ${CI_MODE:-phased} in
phased) ;;
driver)
    count=${SHARD_COUNT:-}
    index=${SHARD_INDEX:-}
    if ! [[ $count =~ ^[1-9][0-9]*$ && $index =~ ^[0-9]+$ ]] || [ "$index" -ge "$count" ]; then
        echo "CI_MODE=driver needs SHARD_COUNT >= 1 and 0 <= SHARD_INDEX < SHARD_COUNT (got '$count', '$index')"
        exit 2
    fi
    # go test -list compiles the test binary: a build failure fails the shard.
    list=$(go test -list . ./internal/driver) || { echo "$list"; exit 1; }
    group=$(echo "$list" | grep '^Test' | awk -v n="$count" -v i="$index" 'NR % n == i' | paste -sd '|' -)
    if [ -z "$group" ]; then
        echo "shard $index of $count has no tests"
        exit 0
    fi
    echo "== $driver shard $index of $count: $(echo "$group" | tr '|' '\n' | wc -l) tests"
    cmd=(go test -v -p 1 -parallel "${DRIVER_PARALLEL:-4}" -timeout "$timeout" -run "^(${group})$" ./internal/driver)
    if [ "${DRY_RUN:-}" = 1 ]; then
        echo "${cmd[*]}" | cut -c1-200
        echo "$group" | tr '|' '\n'
        exit 0
    fi
    env -u GOMEMLIMIT "${cmd[@]}" 2>&1 | tee "$logs/driver-$index.log"
    rc=${PIPESTATUS[0]}
    echo "== summary: exit $rc"
    failures "$logs/driver-$index.log"
    slowest "$logs/driver-$index.log"
    exit "$rc"
    ;;
others)
    if [ -n "${PKGS:-}" ]; then
        pkgs=$PKGS
    else
        pkgs=$others
        for p in ${EXCLUDE_PKGS:-}; do pkgs=$(echo "$pkgs" | grep -v -x "$p"); done
    fi
    short=
    for p in ${SHORT_PKGS:-}; do
        if echo "$pkgs" | grep -q -x "$p"; then
            pkgs=$(echo "$pkgs" | grep -v -x "$p")
            short="$short $p"
        fi
    done
    # $pkgs and $short unquoted: one argument per package.
    cmd=(go test -p "$otherp" -timeout "$timeout" $pkgs)
    scmd=(go test -short -p "$otherp" -timeout "$timeout" $short)
    if [ "${DRY_RUN:-}" = 1 ]; then
        [ -n "$pkgs" ] && echo "${cmd[*]}"
        [ -n "$short" ] && echo "${scmd[*]}"
        exit 0
    fi
    rc=0
    : >"$logs/others.log"
    if [ -n "$pkgs" ]; then
        "${cmd[@]}" 2>&1 | tee -a "$logs/others.log"
        rc=${PIPESTATUS[0]}
    fi
    if [ -n "$short" ]; then
        "${scmd[@]}" 2>&1 | tee -a "$logs/others.log"
        src=${PIPESTATUS[0]}
        [ "$rc" = 0 ] && rc=$src
    fi
    echo "== summary: exit $rc"
    failures "$logs/others.log"
    exit "$rc"
    ;;
*)
    echo "CI_MODE is '$CI_MODE'; it must be phased, driver or others"
    exit 2
    ;;
esac

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
failures "$logs"/*.log
slowest "$logs"/driver-*.log
exit $status
