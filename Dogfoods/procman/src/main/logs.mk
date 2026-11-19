// Why: procman's own messages must go to the console and, under `start`, to
// a timed log file.
//
// What: Logs with Debug, Info, Warn, Error and At.
//
// How: one logger to stderr, and under `start` a second one with times into
// the run directory's procman.log.

import std.log

struct Logs {
    console: log.Logger
    file: ?log.Logger
}

Logs.Debug[A: Fields[Format] + Plain](mut self, const f: str, args: A) {
    self.console.Debug(f, args) else {}
    if l := mut self.file {
        l.Debug(f, args) else {}
    }
}

Logs.Info[A: Fields[Format] + Plain](mut self, const f: str, args: A) {
    self.console.Info(f, args) else {}
    if l := mut self.file {
        l.Info(f, args) else {}
    }
}

Logs.Warn[A: Fields[Format] + Plain](mut self, const f: str, args: A) {
    self.console.Warn(f, args) else {}
    if l := mut self.file {
        l.Warn(f, args) else {}
    }
}

Logs.Error[A: Fields[Format] + Plain](mut self, const f: str, args: A) {
    self.console.Error(f, args) else {}
    if l := mut self.file {
        l.Error(f, args) else {}
    }
}

/// Logs at a level chosen at run time, as an event's severity is.
Logs.At[A: Fields[Format] + Plain](mut self, level: log.Level, const f: str, args: A) {
    self.console.Log(level, f, args) else {}
    if l := mut self.file {
        l.Log(level, f, args) else {}
    }
}
