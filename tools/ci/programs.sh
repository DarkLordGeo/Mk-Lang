#!/usr/bin/env bash
# Why: the Go runner of tests/programs (internal/corpus) knows two targets,
# x86_64-windows and x86_64-linux; on a macOS or AArch64 host the programs
# must still be built and run natively, with their output compared.
#
# What: builds every program of tests/programs that has a case for
# x86_64-linux (the POSIX cases), dev and release, for the host target, runs
# each such case in a fresh directory with its arguments, files and
# standard input, and compares standard output, standard error (up to a
# backtrace) and the exit status with the case's files. Prints one line per
# case and exits 1 if any failed. SKIP_PROGRAMS (space-separated) names
# programs left out, such as the x86-specific ones on aarch64.
#
# How: reads the .cases format of internal/corpus/cases.go (case, profile,
# target, args with double quotes, files, stdin, stdout, stderr, exit,
# timeout). The mk binary is built once, into $RUNNER_TEMP or the
# temporary directory.
set -u

root=$(cd "$(dirname "$0")/../.." && pwd)
work=${RUNNER_TEMP:-${TMPDIR:-/tmp}}/mk-programs
rm -rf "$work"
mkdir -p "$work/bin"
mk="$work/bin/mk"
(cd "$root" && go build -o "$mk" ./cmd/mk) || exit 1
progs="$root/tests/programs"
skip=" ${SKIP_PROGRAMS:-} "
pass=0
fail=0

# drop_bt prints its file up to a line starting "backtrace:", as
# internal/corpus's DropBacktrace compares.
drop_bt() {
  awk '/^backtrace:/ { exit } { print }' "$1"
}

run_case() { # name profile args stdin stdout stderr exit timeout files
  local name=$1 prof=$2 args=$3 stdin=$4 out=$5 err=$6 code=$7 limit=$8 files=$9
  local exe="$work/bin/$name-$prof"
  local dir="$work/run/$name-$prof-$cname"
  mkdir -p "$dir"
  for f in $files; do cp -R "$progs/$f" "$dir/"; done
  cp "$exe" "$dir/prog"
  local input=/dev/null
  [ -n "$stdin" ] && input="$progs/$stdin"
  local got
  (cd "$dir" && eval "set -- $args" && perl -e 'alarm shift; exec @ARGV' "$((limit * 3))" ./prog "$@" < "$input" > out.txt 2> err.txt)
  got=$?
  local why=""
  [ "$got" = "$code" ] || why="exit $got, want $code"
  if [ -n "$out" ]; then
    cmp -s "$dir/out.txt" <(tr -d '\r' < "$progs/$out") || why="$why stdout differs"
  elif [ -s "$dir/out.txt" ]; then
    why="$why unexpected stdout"
  fi
  if [ -n "$err" ]; then
    cmp -s <(drop_bt "$dir/err.txt") <(tr -d '\r' < "$progs/$err" | drop_bt /dev/stdin) || why="$why stderr differs"
  fi
  if [ -z "$why" ]; then
    pass=$((pass + 1))
    echo "PASS $name $prof $cname"
  else
    fail=$((fail + 1))
    echo "FAIL $name $prof $cname: $why"
    head -c 2000 "$dir/err.txt"
  fi
}

for cases in "$progs"/*.cases; do
  name=$(basename "$cases" .cases)
  case "$skip" in *" $name "*) echo "SKIP $name"; continue ;; esac
  src="$progs/$name.mk"
  [ -e "$src" ] || src="$progs/$name"
  built=""
  # One pass per case: the fields reset at each "case" line.
  emit() {
    [ -n "$cname" ] || return 0
    case " $targets " in *" x86_64-linux "*) ;; *) return 0 ;; esac
    for prof in $profiles; do
      case " $built " in
        *" $prof "*) ;;
        *)
          flags=""
          [ "$prof" = release ] && flags=--release
          if ! "$mk" build $flags -o "$work/bin/$name-$prof" "$src" > "$work/build.log" 2>&1; then
            fail=$((fail + 1))
            echo "FAIL $name $prof: build"
            head -c 3000 "$work/build.log"
            built="$built $prof-failed"
            continue
          fi
          built="$built $prof"
          ;;
      esac
      case " $built " in *" $prof-failed "*) continue ;; esac
      run_case "$name" "$prof" "$args" "$stdin" "$stdout" "$stderr" "$code" "$limit" "$files"
    done
  }
  cname=""
  while IFS= read -r line || [ -n "$line" ]; do
    line=${line%$'\r'}
    t=${line#"${line%%[![:space:]]*}"}
    case "$t" in ''|'#'*) continue ;; esac
    key=${t%% *}
    val=""
    [ "$key" != "$t" ] && val=${t#* }
    if [ "$key" = case ]; then
      emit
      cname=$val profiles="dev release" targets="x86_64-windows x86_64-linux" args="" files="" stdin="" stdout="" stderr="" code=0 limit=30
      continue
    fi
    case "$key" in
      profile) profiles=$val ;;
      target) targets=$val ;;
      args) args=$val ;;
      files) files=$val ;;
      stdin) stdin=$val ;;
      stdout) stdout=$val ;;
      stderr) stderr=$val ;;
      exit) code=$val ;;
      timeout) limit=$val ;;
    esac
  done < "$cases"
  emit
done
echo "pass $pass fail $fail"
[ "$fail" = 0 ]
