// Why: two runs of one crontab must print the same lines, so the log can be
// compared.
//
// What: Journal with Info, Warn, FileOnly and Over, and Trouble.
//
// How: every record goes to the log file, and with `--echo` to standard
// error too; the standard-error copy carries no time and no process id, the
// file gets the process ids.

import std.fs
import std.log

/// Where records go. Plain, `Send` and `Share`: tasks log through `&`.
export struct Journal {
    export file: log.Sink
    export echo: bool
}

/// Makes a journal over a log file the caller owns and closes after its
/// tasks have joined.
export Journal.Over(f: &Mutex[fs.File], echo: bool): Journal from f {
    return Journal(file: log.ToFile(f, log.Level.Info), echo: echo)
}

/// Logs an `INFO` record. A record that cannot be written is reported on
/// standard error, and the job goes on: a full disk stops the log, not
/// the jobs.
export Journal.Info[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A) {
    self.file.Info(io, f, args) else e {
        Trouble(io, e)
    }
    if self.echo {
        log.Stderr(log.Level.Info).Info(io, f, args) else {}
    }
}

/// Logs a `WARN` record, as `Info` does.
export Journal.Warn[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A) {
    self.file.Warn(io, f, args) else e {
        Trouble(io, e)
    }
    if self.echo {
        log.Stderr(log.Level.Info).Warn(io, f, args) else {}
    }
}

/// Logs to the file only: for facts that differ from run to run.
export Journal.FileOnly[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A) {
    self.file.Info(io, f, args) else e {
        Trouble(io, e)
    }
}

/// Reports a record the log file refused.
Trouble(io: Io, e: WriteError) {
    log.Stderr(log.Level.Error).Error(io, "cron: cannot write the job log: {e}", (e)) else {}
}
