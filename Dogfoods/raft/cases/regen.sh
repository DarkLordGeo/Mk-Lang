#!/usr/bin/env bash
# Why: raft's cases hold expected output that must be regenerated whenever
# the program changes, and paths in that output differ between machines.
#
# What: writes Dogfoods/raft/cases: the fixture recordings and histories in
# data/, the .args (and .env) of each case, then the .out, .err and .code of
# each from the Windows dev build.
#
# How: each case runs in its own temporary directory with a 60 s timeout;
# the case directory and the work directory in its output are written back
# as $CASES and $WORK, and an empty .out or .err, or a zero .code, is not
# kept. The directories come from the script's own place, so it runs from
# any checkout (in Git for Windows' bash, which has cygpath).
set -u
C=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
D=$(dirname "$C")
EXE=$D/raft.exe
CW=$(cygpath -w "$C")
mkdir -p "$C/data"

args() { # name, then one argument per parameter
  local n=$1; shift
  : > "$C/$n.args"
  for a in "$@"; do printf '%s\n' "$a" >> "$C/$n.args"; done
}

# fixtures
W=$(mktemp -d)
(cd "$W" && "$EXE" sim --seed 7 --partitions --loss 5 --record "$C/data/seed7.rec" --history "$C/data/seed7.txt" >/dev/null)
(cd "$W" && "$EXE" sim --seed 10 --crash --record "$C/data/crash10.rec" --history "$C/data/crash10.txt" >/dev/null)
(cd "$W" && "$EXE" sim --seed 5 --partitions --loss 5 --stale --history "$C/data/stale.txt" >/dev/null)
cat > "$C/data/malformed.txt" <<'EOF'
# a history with a typo on line 4
0 1000 2000 put k0 5
1 1500 2500 get k0 -> 5
2 3000 4000 pu k0 6
EOF
# a recording whose header promises another run than its log holds
(cd "$W" && "$EXE" sim --seed 7 --partitions --loss 5 --duration 200 --record "$W/short.rec" >/dev/null)
sed "1s/loss=5/loss=6/" "$W/short.rec" > "$C/data/diverge.rec"
# a recording whose header names a seed out of range
sed "1s/seed=7 /seed=-1 /" "$C/data/seed7.rec" | head -c 3000 > "$C/data/badheader.rec"
# a recording cut short in its log
head -c 4000 "$C/data/seed7.rec" > "$C/data/torn.rec"

args help --help
args no-command
args bad-option sim --bogus
args bad-value sim --seed x
args bad-seeds sim --seeds 9..2
args bad-nodes sim --nodes 9
args too-many-ops sim --clients 8 --ops 12
args unknown-command frob
args seed-history sim --seed 7 --partitions --loss 5 --verbose
args partitions-1000 sim --seeds 1..1000 --partitions --loss 5
args faults sim --seeds 1..200 --partitions --loss 10 --delay 5 --duplicate 5 --corrupt 3 --faults
args inline-20 sim --seeds 1..20 --partitions --loss 5
args tasks-1-carrier sim --seeds 1..20 --partitions --loss 5 --tasks
printf 'MK_CARRIERS=1\n' > "$C/tasks-1-carrier.env"
args tasks-16-carriers sim --seeds 1..20 --partitions --loss 5 --tasks
printf 'MK_CARRIERS=16\n' > "$C/tasks-16-carriers.env"
args tasks-verbose sim --seed 9 --partitions --loss 10 --delay 5 --duplicate 5 --corrupt 3 --faults --tasks --verbose
args stale-reads sim --seeds 5..5 --partitions --loss 5 --stale
args memory-budget sim --seed 2 --memory 4096
args memory-enough sim --seed 2 --memory 1000000
args check-good check '$CASES/data/seed7.txt'
args check-stale check '$CASES/data/stale.txt'
args check-malformed check '$CASES/data/malformed.txt'
args check-missing check '$CASES/data/nothere.txt'
args check-directory check '$CASES/data'
args check-extra check '$CASES/data/seed7.txt' '$CASES/data/stale.txt'
args replay replay '$CASES/data/seed7.rec'
args replay-not-recording replay '$CASES/data/seed7.txt'
args replay-diverges replay '$CASES/data/diverge.rec'
args replay-torn replay '$CASES/data/torn.rec'
args replay-bad-header replay '$CASES/data/badheader.rec'
args bad-memory sim --memory -1
args replay-missing replay '$CASES/data/nothere.rec'
args history-unwritable sim --seed 1 --history '$WORK/no/such/dir/h.txt'
args peers-resolved sim --seed 7 --partitions --loss 5 --peers localhost:7001,localhost:7002,localhost:7003,localhost:7004,localhost:7005
args peers-ephemeral sim --seed 7 --partitions --loss 5 --peers 127.0.0.1:0,127.0.0.1:0,127.0.0.1:0,127.0.0.1:0,127.0.0.1:0
args peers-bad sim --peers 127.0.0.1:7001,127.0.0.1:7002,127.0.0.1:7003,127.0.0.1:7004,nowhere.invalid:7005
args peers-count sim --peers 127.0.0.1:7001,127.0.0.1:7002
args three-nodes sim --seeds 1..100 --nodes 3 --clients 2 --ops 20 --partitions --loss 5
args one-node sim --seeds 1..20 --nodes 1 --partitions
args small-inbox sim --seeds 1..3 --partitions --duplicate 20 --inbox 2 --verbose
args crash-seeds sim --seeds 1..300 --crash --partitions --loss 5 --faults
args crash-verbose sim --seed 10 --crash --faults --verbose
args crash-tasks sim --crash --tasks
args replay-crash replay '$CASES/data/crash10.rec'
args bench sim --seeds 1..300 --partitions --loss 5

# expected output from the Windows dev build
for f in "$C"/*.args; do
  n=$(basename "$f" .args)
  W=$(mktemp -d)
  mapfile -t A < "$f"
  for i in "${!A[@]}"; do
    A[$i]=${A[$i]//\$CASES/$CW}
    A[$i]=${A[$i]//\$WORK/$(cygpath -w "$W")}
  done
  envs=()
  if [ -f "$C/$n.env" ]; then mapfile -t envs < "$C/$n.env"; fi
  (cd "$W" && env "${envs[@]}" timeout 60 "$EXE" "${A[@]}" > "$W/.out" 2> "$W/.err"; echo $? > "$W/.code")
  WW=$(cygpath -w "$W")
  for k in out err; do
    sed -e "s|$(printf '%s' "$CW" | sed 's/[[\\.]/\\&/g')|\$CASES|g" -e "s|$(printf '%s' "$WW" | sed 's/[[\\.]/\\&/g')|\$WORK|g" "$W/.$k" > "$C/$n.$k"
    [ -s "$C/$n.$k" ] || rm -f "$C/$n.$k"
  done
  code=$(cat "$W/.code")
  if [ "$code" != 0 ]; then echo "$code" > "$C/$n.code"; else rm -f "$C/$n.code"; fi
  rm -rf "$W"
  echo "$n: exit $code"
done
