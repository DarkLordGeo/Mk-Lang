// Why: the crontab reader is the daemon's first line of defence.
//
// What: tests of entries with names, steps and options, weekday 7, pipelines
// and problem reports.
//
// How: parses small crontabs against a fixed Host and checks the entries and
// the problems with their lines.

const Host0 = Host(dir: "", cron_dir: "/opt/cron", lock: "cron.lock")

test "a five-field entry with names, steps and options reads whole" (sys: &System) {
    text := "CRON_TZ=UTC\n*/20 9-17 * jan,Dec mon-fri  report  timeout=2m  --  run \"two words\" $CRON_DIR/x\n"
    tab := try Parse(sys.arena, sys.io, "t", text, Host0)
    Assert(tab.problems.Len() == 0)
    e := &tab.entries[0]
    Assert(e.name == "report")
    match e.schedule {
        .Cron(spec) {
            Assert(spec.minutes == (u64(1) | (u64(1) << 20) | (u64(1) << 40)))
            Assert(spec.months == ((u64(1) << 1) | (u64(1) << 12)))
            Assert(spec.weekdays == 0x3e)
            Assert(!spec.any_weekday && spec.any_day)
        }
        _ { Assert(false) }
    }
    Assert(e.argv.Len() == 3)
    Assert(e.argv[1] == "two words")
    Assert(e.argv[2] == "/opt/cron/x")
}

test "weekday 7 is Sunday" (sys: &System) {
    tab := try Parse(sys.arena, sys.io, "t", "CRON_TZ=UTC\n0 0 * * 7 sunday -- x\n", Host0)
    match tab.entries[0].schedule {
        .Cron(spec) { Assert(spec.weekdays == 1) }
        _ { Assert(false) }
    }
}

test "a pipeline splits at one bar" (sys: &System) {
    tab := try Parse(sys.arena, sys.io, "t", "CRON_TZ=UTC\n@hourly p -- a b | c\n", Host0)
    e := &tab.entries[0]
    Assert(e.argv.Len() == 2 && e.then.Len() == 1 && e.then[0] == "c")
}

test "every problem is reported with its line" (sys: &System) {
    tab := try Parse(sys.arena, sys.io, "t", "CRON_TZ=UTC\n61 * * * * a -- x\n\n@daily -- x\n", Host0)
    Assert(tab.problems.Len() == 2)
    Assert(tab.problems[0].line == 2)
    Assert(tab.problems[1].line == 4)
}
