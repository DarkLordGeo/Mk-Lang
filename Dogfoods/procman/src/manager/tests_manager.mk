// Why: the restart policy and the back-off decide when a process runs again.
//
// What: tests of Backoff's doubling and ceiling, and of WantsRestart.
//
// How: plain calls with chosen codes and restart counts.

import src.procfile

test "the back-off doubles from the first wait and stops at its ceiling" {
    Assert(Backoff(100, 0) == 100)
    Assert(Backoff(100, 1) == 200)
    Assert(Backoff(100, 3) == 800)
    Assert(Backoff(100, 20) == MaxBackoffMs)
    Assert(Backoff(0, 5) == 0)
}

test "on-failure restarts a non-zero exit or a failed run, and nothing else" {
    Assert(WantsRestart(procfile.Restart.OnFailure, 1, false))
    Assert(WantsRestart(procfile.Restart.OnFailure, 0, true))
    Assert(!WantsRestart(procfile.Restart.OnFailure, 0, false))
    Assert(WantsRestart(procfile.Restart.Always, 0, false))
    Assert(!WantsRestart(procfile.Restart.Never, 7, false))
}
