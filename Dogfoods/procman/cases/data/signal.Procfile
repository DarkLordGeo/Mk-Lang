# Linux only: sh sends SIGINT to its parent, which is procman, then waits to be stopped
sleeper: $PROCMAN demo sleep 20000
stubborn: $PROCMAN demo ignore-signals 20000
killer: /bin/sh -c "sleep 0.5; kill -INT $$PPID; exec sleep 20"
