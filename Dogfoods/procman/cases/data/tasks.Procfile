web: $PROCMAN demo echo web
task hello: $PROCMAN demo echo hello
task fail: $PROCMAN demo exit 3 "migration 42 failed"
# a runaway task: it writes until procman kills it at the limit. It writes
# forever, not a fixed amount, so it is killed while writing and never while
# exiting: DEFECT DF-procman-06
task flood [limit=1k]: $PROCMAN demo flood 0
task ghost: procman-no-such-program
task binary: $PROCMAN demo binary
task crash: $PROCMAN demo panic
