// Why: front-end PGO counts MIR edges in an instrumented build and feeds the
// counts back as branch weights and hot or cold attributes.
// What: TestPGOGenCounters (counter arrays, increments and the support
// pack's ProfileTable, run natively), TestPGOUseBranchWeights (entry counts,
// weights in successor order, hot, inlinehint and cold from the whole
// profile), TestPGOUseWrongLength (stale records), TestPGOUseThinLTOLink.
// How: pgoUnit's bodies are emitted with PGOGen and with a profile built by
// pgoUseProfile; profileHarness dumps the counters of a native run.

package codegen

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/pgo"
	"mk/internal/types"
)

// pgoUnit is runBodies' unit plus the bodiless std.rt.ProfileTable, and the
// bodies by name.
func pgoUnit(e *testEnv) (*Unit, map[string]*mir.Body) {
	bodies, bodiless := runBodies(e)
	rec := e.structIn("std.rt", "ProfileRecord", types.Str,
		e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 16)}, types.U8), e.tt.Slice(types.U64))
	pt := e.stdFn("std.rt", "", "ProfileTable", 0, nil, e.tt.Slice(rec))
	u := unitOf(e, bodies, append(bodiless, pt)...)
	by := map[string]*mir.Body{}
	for _, b := range bodies {
		by[e.items.Get(b.Item).Key.String()] = b
	}
	return u, by
}

// pgoPlans are the counter counts of runBodies' functions by the walk of
// written out by hand: entry, plus each counted terminator's edges.
var pgoPlans = []struct {
	sym string
	n   int
}{
	{"mk.main.Check", 4},  // entry; the tag switch: 2 cases and the default
	{"mk.main.CodeOf", 3}, // entry; the error split
	{"mk.main.Get", 5},    // entry; two bool switches
	{"mk.main.GetX", 1},
	{"mk.main.PayOf", 5}, // entry; the error split; a 1-case switch with a default
	{"mk.main.Reborrow", 1},
	{"mk.main.Take", 1},
	{"mk.main.Twice", 1},
	{"mk.main.ValOf", 3}, // entry; the error split
}

// profileHarness calls ValOf(5) three times, ValOf(0) once and ValOf(500)
// twice, then prints each ProfileTable record: its key, its counter count
// and its counts, one per line.
func profileHarness(target types.Target) string {
	call := `  %s = call { ptr, i64 } @mk.std.rt.ProfileTable()
  %d = extractvalue { ptr, i64 } %s, 0
  %n = extractvalue { ptr, i64 } %s, 1
`
	decl := "declare { ptr, i64 } @mk.std.rt.ProfileTable()\n"
	if target.OS == types.OSWindows {
		call = `  %out = alloca { ptr, i64 }, align 8
  call void @mk.std.rt.ProfileTable(ptr %out)
  %d = load ptr, ptr %out, align 8
  %np = getelementptr i8, ptr %out, i64 8
  %n = load i64, ptr %np, align 8
`
		decl = "declare void @mk.std.rt.ProfileTable(ptr)\n"
	}
	var calls strings.Builder
	for _, a := range []int{5, 0, 5, 500, 5, 500} {
		fmt.Fprintf(&calls, "  call i64 @mk.main.ValOf(i64 %d)\n", a)
	}
	return decl + `declare i64 @mk.main.ValOf(i64)

define internal void @h_pn(i64 %v) nounwind {
  call void @h_num(i64 %v)
  call void @h_write(ptr @s.nl, i64 1)
  ret void
}

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
entry:
` + calls.String() + call + `  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i1, %next ]
  %end = icmp eq i64 %i, %n
  br i1 %end, label %done, label %body
body:
  %off = mul i64 %i, 48
  %row = getelementptr i8, ptr %d, i64 %off
  %kp = load ptr, ptr %row, align 8
  %klp = getelementptr i8, ptr %row, i64 8
  %kl = load i64, ptr %klp, align 8
  call void @h_write(ptr %kp, i64 %kl)
  call void @h_write(ptr @s.nl, i64 1)
  %cpp = getelementptr i8, ptr %row, i64 32
  %cp = load ptr, ptr %cpp, align 8
  %cnp = getelementptr i8, ptr %row, i64 40
  %cn = load i64, ptr %cnp, align 8
  call void @h_pn(i64 %cn)
  br label %cloop
cloop:
  %j = phi i64 [ 0, %body ], [ %j1, %cbody ]
  %cend = icmp eq i64 %j, %cn
  br i1 %cend, label %next, label %cbody
cbody:
  %coff = mul i64 %j, 8
  %c = getelementptr i8, ptr %cp, i64 %coff
  %cv = load i64, ptr %c, align 8
  call void @h_pn(i64 %cv)
  %j1 = add i64 %j, 1
  br label %cloop
next:
  %i1 = add i64 %i, 1
  br label %loop
done:
  call void @h_exit(i32 0)
  unreachable
}
`
}

// TestPGOGenCounters: with PGOGen each member defines S.pgo, a hidden (ELF)
// or dso_local (COFF) [n x i64] zeroinitializer with n from the walk of
// the support pack's ProfileTable lists every member sorted by
// symbol, with its CFG checksum and its counter array; and natively (dev at
// -O0, release at -O2) the counts after six calls are the entry and edge
// counts of the program: Get [6, 1, 5, 2, 3] (x == 0 once, x > 100 twice),
// ValOf [6, 3, 3], every other function zeros. Without PGOGen there is no
// counter.
func TestPGOGenCounters(t *testing.T) {
	for _, target := range runTargets {
		for _, mode := range []string{"dev", "release", "thinlto"} {
			prof := types.Dev
			if mode != "dev" {
				prof = types.Release
			}
			t.Run(target.Name+"/"+mode, func(t *testing.T) {
				e := newTestEnv()
				u, by := pgoUnit(e)
				cfg := Config{Target: target, Profile: prof, PGOGen: true, RootModule: "main"}
				var b strings.Builder
				if _, err := EmitPack(u, 0, e, cfg, &b); err != nil {
					t.Fatal(err)
				}
				ll := b.String()
				vis := "hidden"
				if target.OS == types.OSWindows {
					vis = "dso_local"
				}
				for _, p := range pgoPlans {
					want := fmt.Sprintf("@%s.pgo = %s global [%d x i64] zeroinitializer, align 8", p.sym, vis, p.n)
					if !strings.Contains(ll, want) {
						t.Errorf("want %q", want)
					}
					if got := planCounters(by[strings.TrimPrefix(p.sym, "mk.")]).n; got != p.n {
						t.Errorf("%s: the walk gives %d counters, want %d", p.sym, got, p.n)
					}
				}
				var plain strings.Builder
				if _, err := EmitPack(u, 0, e, Config{Target: target, Profile: prof}, &plain); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(plain.String(), ".pgo") {
					t.Error("counters without PGOGen")
				}
				var sb strings.Builder
				if err := EmitSupport(u, e, cfg, &sb); err != nil {
					t.Fatal(err)
				}
				sl := sb.String()
				last := -1
				for _, p := range pgoPlans {
					decl := fmt.Sprintf("@%s.pgo = external %s global i64, align 8", p.sym, vis)
					if !strings.Contains(sl, decl) {
						t.Errorf("the support pack does not declare %s.pgo", p.sym)
					}
					sum := pgoChecksum(by[strings.TrimPrefix(p.sym, "mk.")])
					rec := fmt.Sprintf("[16 x i8] c\"%s\", ptr @%s.pgo, i64 %d }", llBytes(sum[:]), p.sym, p.n)
					at := strings.Index(sl, rec)
					if at < 0 {
						t.Errorf("no ProfileTable record %s", rec)
					} else if at < last {
						t.Errorf("record %s out of key order", p.sym)
					} else {
						last = at
					}
				}
				dir := t.TempDir()
				opt := map[string]string{"dev": "-O0", "release": "-O2 -ffunction-sections -fdata-sections",
					"thinlto": "-O2 -flto=thin -ffunction-sections -fdata-sections"}[mode]
				pack := clangC(t, target, dir, "pack", ll, opt)
				support := clangC(t, target, dir, "support", sl, "-O2")
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				osIR, kernel := harnessOS(target)
				objs := []string{support, pack, clangC(t, target, dir, "harness", osIR+harnessRT+profileHarness(target), "-O0"),
					clangC(t, target, dir, "clib", cTag(0), "-O2")}
				out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel, release: prof == types.Release, lto: mode == "thinlto"}), "")
				counts := map[string][]int{"mk.main.Get": {6, 1, 5, 2, 3}, "mk.main.ValOf": {6, 3, 3}}
				var want strings.Builder
				for _, p := range pgoPlans {
					fmt.Fprintf(&want, "%s\n%d\n", p.sym, p.n)
					c := counts[p.sym]
					for k := 0; k < p.n; k++ {
						v := 0
						if c != nil {
							v = c[k]
						}
						fmt.Fprintf(&want, "%d\n", v)
					}
				}
				if code != 0 || out != want.String() {
					t.Fatalf("exit %d, output\n%s\nwant\n%s", code, out, want.String())
				}
			})
		}
	}
}

// pgoUseProfile is a profile of runBodies' program as the counter run above
// recorded it, plus: Take never entered (cold), Twice under another
// checksum (stale), Check with 3 counts where the walk gives 4 (stale), and
// PayOf's error path taken four times, TooBig three of them.
func pgoUseProfile(by map[string]*mir.Body) *pgo.Profile {
	sum := func(k string) [16]byte { return pgoChecksum(by[k]) }
	p := &pgo.Profile{Records: []pgo.Record{
		{Key: "mk.main.Check", Checksum: sum("main.Check"), Counts: []uint64{9, 1, 1}},
		{Key: "mk.main.Get", Checksum: sum("main.Get"), Counts: []uint64{6, 1, 5, 2, 3}},
		{Key: "mk.main.PayOf", Checksum: sum("main.PayOf"), Counts: []uint64{5, 1, 4, 3, 1}},
		{Key: "mk.main.Take", Checksum: sum("main.Take"), Counts: []uint64{0}},
		{Key: "mk.main.Twice", Checksum: [16]byte{1}, Counts: []uint64{100}},
		{Key: "mk.main.ValOf", Checksum: sum("main.ValOf"), Counts: []uint64{6, 3, 3}},
	}}
	if err := p.Check(); err != nil {
		panic(err)
	}
	return p
}

// mdID is the number of the metadata node printed as text, "" if none.
func mdID(ll, text string) string {
	m := regexp.MustCompile(`(?m)^(![0-9]+) = ` + regexp.QuoteMeta(text) + `$`).FindStringSubmatch(ll)
	if m == nil {
		return ""
	}
	return m[1]
}

// TestPGOUseBranchWeights: with a profile, a matched function carries
// function_entry_count, and each counted terminator !prof branch_weights in
// LLVM's successor order: Get's bool switches (br: true, then false) {1, 5}
// and {2, 3}; ValOf's and PayOf's error splits (ok, then error) {3, 3} and
// {1, 4}; PayOf's switch (default first, then the case) {1, 3}. Over the
// whole profile (total 126, the stale Twice's 100 and Check's 9 included)
// the hot set is 100, 9, 6, 6, so the threshold is 6: Get and ValOf (6, at
// most 64 operations) are hot and inlinehint, PayOf (5) is neither, and Take
// (entry 0) is cold. A second profile makes only Get hot. The packs compile at -O2, and two emissions are equal.
func TestPGOUseBranchWeights(t *testing.T) {
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			u, by := pgoUnit(e)
			p := pgoUseProfile(by)
			cfg := Config{Target: target, Profile: types.Release, PGO: p}
			var b, b2 strings.Builder
			rep, err := EmitPack(u, 0, e, cfg, &b)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := EmitPack(u, 0, e, cfg, &b2); err != nil || b.String() != b2.String() {
				t.Fatalf("two emissions differ (%v)", err)
			}
			ll := b.String()
			if got := strings.Join(rep.Stale, ","); got != "mk.main.Check,mk.main.Twice" {
				t.Errorf("Stale = %q, want mk.main.Check,mk.main.Twice", got)
			}
			head := func(sym string) string {
				f := fnText(ll, sym)
				return f[:strings.Index(f, "\n")]
			}
			for sym, n := range map[string]int{"mk.main.Get": 6, "mk.main.ValOf": 6, "mk.main.PayOf": 5, "mk.main.Take": 0} {
				id := mdID(ll, fmt.Sprintf(`!{!"function_entry_count", i64 %d}`, n))
				if id == "" || !strings.Contains(head(sym), "!prof "+id+" ") {
					t.Errorf("%s: no function_entry_count %d on %q", sym, n, head(sym))
				}
			}
			for _, sym := range []string{"mk.main.Check", "mk.main.Twice", "mk.main.CodeOf"} {
				if strings.Contains(head(sym), "!prof") {
					t.Errorf("%s has an entry count: %q", sym, head(sym))
				}
			}
			if h := head("mk.main.Take"); !strings.Contains(h, " cold") || strings.Contains(h, " hot") {
				t.Errorf("Take is not cold: %q", h)
			}
			for _, sym := range []string{"mk.main.Get", "mk.main.ValOf"} {
				if h := head(sym); !strings.Contains(h, " hot inlinehint") {
					t.Errorf("%s: not hot and inlinehint at threshold 6: %q", sym, h)
				}
			}
			for _, sym := range []string{"mk.main.PayOf", "mk.main.Check"} {
				if h := head(sym); strings.Contains(h, " hot") || strings.Contains(h, " cold") {
					t.Errorf("%s: hot or cold at threshold 6: %q", sym, h)
				}
			}
			weights := func(sym, inst string, ws ...int) {
				t.Helper()
				text := `!{!"branch_weights"`
				for _, w := range ws {
					text += fmt.Sprintf(", i32 %d", w)
				}
				id := mdID(ll, text+"}")
				// a switch prints its cases on lines of their own, then "], !prof"
				re := `(?m)^  ` + inst + ` .*, !prof ` + id + `(,|$)`
				if strings.HasPrefix(inst, "switch") {
					re = `(?m)^  ` + inst + ` [^\]]*\], !prof ` + id + `(,|$)`
				}
				if id == "" || !regexp.MustCompile(re).MatchString(fnText(ll, sym)) {
					t.Errorf("%s: no %s with weights %v in\n%s", sym, inst, ws, fnText(ll, sym))
				}
			}
			weights("mk.main.Get", "br i1", 1, 5)
			weights("mk.main.Get", "br i1", 2, 3)
			weights("mk.main.ValOf", "br i1", 3, 3)
			weights("mk.main.PayOf", "br i1", 1, 4)
			weights("mk.main.PayOf", "switch i64", 1, 3)
			if n := strings.Count(fnText(ll, "mk.main.Check"), `!prof`); n != 0 {
				t.Errorf("stale Check has %d !prof", n)
			}
			var plain strings.Builder
			if _, err := EmitPack(u, 0, e, Config{Target: target, Profile: types.Release}, &plain); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(plain.String(), "function_entry_count") || mdID(plain.String(), `!{!"branch_weights", i32 1, i32 5}`) != "" {
				t.Error("profile facts without a profile")
			}
			// LLVM's ProfileSummary of the whole profile: 154 counts in total
			// over 6 records, the largest 100 (Twice's entry), the largest
			// internal count 5 (Get's), in every pack alike.
			for _, s := range []string{`!{i32 1, !"ProfileSummary", `, `!{!"ProfileFormat", !"InstrProf"}`, `!{!"TotalCount", i64 154}`,
				`!{!"MaxCount", i64 100}`, `!{!"MaxInternalCount", i64 5}`, `!{!"MaxFunctionCount", i64 100}`,
				`!{!"NumCounts", i64 18}`, `!{!"NumFunctions", i64 6}`, `!{!"DetailedSummary", `, `!{i32 990000, i64 `} {
				if !strings.Contains(ll, s) {
					t.Errorf("no %s in the ProfileSummary", s)
				}
			}
			if strings.Contains(plain.String(), "ProfileSummary") {
				t.Error("a ProfileSummary without a profile")
			}
			dir := t.TempDir()
			clangC(t, target, dir, "pack", ll, "-O2")
			if target.OS == types.OSLinux {
				wslClang(t, ll, "-O2")
			}

			hot := &pgo.Profile{Records: []pgo.Record{{Key: "mk.main.Get", Checksum: pgoChecksum(by["main.Get"]), Counts: []uint64{1000, 1, 999, 2, 997}},
				{Key: "mk.main.Take", Checksum: pgoChecksum(by["main.Take"]), Counts: []uint64{1}}}}
			var hb strings.Builder
			if _, err := EmitPack(u, 0, e, Config{Target: target, Profile: types.Release, PGO: hot}, &hb); err != nil {
				t.Fatal(err)
			}
			hl := hb.String()
			if h := fnText(hl, "mk.main.Get"); !strings.Contains(h[:strings.Index(h, "\n")], " hot inlinehint") {
				t.Errorf("Get is not hot and inlinehint: %q", h[:strings.Index(h, "\n")])
			}
			if h := fnText(hl, "mk.main.Take"); strings.Contains(h[:strings.Index(h, "\n")], " hot") {
				t.Errorf("Take (1 of 1001) is hot")
			}
			clangC(t, target, dir, "hot", hl, "-O2")
		})
	}
}

// TestPGOUseWrongLength: a record whose counter count differs from the walk
// (shorter or longer), with the right checksum, gives no data: the function
// is in Report.Stale, carries no !prof and no hot or cold, and nothing is
// indexed past its counts (no panic).
func TestPGOUseWrongLength(t *testing.T) {
	e := newTestEnv()
	u, by := pgoUnit(e)
	for _, counts := range [][]uint64{{9}, {9, 1, 1}, {9, 1, 1, 1, 1, 1, 1, 1}} {
		p := &pgo.Profile{Records: []pgo.Record{{Key: "mk.main.Check", Checksum: pgoChecksum(by["main.Check"]), Counts: counts}}}
		var b strings.Builder
		rep, err := EmitPack(u, 0, e, Config{Target: types.X86_64Linux, Profile: types.Release, PGO: p}, &b)
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Stale) != 1 || rep.Stale[0] != "mk.main.Check" {
			t.Errorf("%d counts: Stale = %v", len(counts), rep.Stale)
		}
		f := fnText(b.String(), "mk.main.Check")
		if strings.Contains(f, "!prof") || strings.Contains(f[:strings.Index(f, "\n")], "cold") {
			t.Errorf("%d counts: profile data applied:\n%s", len(counts), f)
		}
	}
}

// TestPGOUseThinLTOLink: a --pgo= release build as the driver links it:
// runBodies' unit cut into two packs (so two modules carry the
// ProfileSummary flag, which ThinLTO merges with behaviour Error), compiled
// to ThinLTO bitcode and linked by lld at -O3 with the native support pack;
// natively ValOf(5), ValOf(0), ValOf(500) still give 10, -1, -1.
func TestPGOUseThinLTOLink(t *testing.T) {
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			if _, why := hostRunner(target); why != "" {
				skipRunner(t, why)
			}
			e := newTestEnv()
			u, by := pgoUnit(e)
			var members []int
			for i, f := range u.Funcs {
				if f.Body != nil {
					members = append(members, i)
				}
			}
			half := len(members) / 2
			u.Packs = [][]int{members[:half], members[half:]}
			for p, ms := range u.Packs {
				for _, fi := range ms {
					u.Funcs[fi].Pack = p
				}
			}
			cfg := Config{Target: target, Profile: types.Release, PGO: pgoUseProfile(by), RootModule: "main"}
			dir := t.TempDir()
			flags := "-O2 -flto=thin -ffunction-sections -fdata-sections"
			var objs []string
			for p := range u.Packs {
				var b strings.Builder
				if _, err := EmitPack(u, p, e, cfg, &b); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(b.String(), `!"ProfileSummary"`) {
					t.Fatalf("pack %d has no ProfileSummary", p)
				}
				objs = append(objs, clangC(t, target, dir, fmt.Sprint("pack", p), b.String(), flags))
			}
			var sb strings.Builder
			if err := EmitSupport(u, e, cfg, &sb); err != nil {
				t.Fatal(err)
			}
			osIR, kernel := harnessOS(target)
			h := osIR + harnessRT + `declare i64 @mk.main.ValOf(i64)

define internal void @h_pn(i64 %v) nounwind {
  call void @h_num(i64 %v)
  call void @h_write(ptr @s.nl, i64 1)
  ret void
}

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
  %a = call i64 @mk.main.ValOf(i64 5)
  call void @h_pn(i64 %a)
  %b = call i64 @mk.main.ValOf(i64 0)
  call void @h_pn(i64 %b)
  %c = call i64 @mk.main.ValOf(i64 500)
  call void @h_pn(i64 %c)
  call void @h_exit(i32 0)
  unreachable
}
`
			objs = append(objs, clangC(t, target, dir, "support", sb.String(), "-O2 -ffunction-sections -fdata-sections"),
				clangC(t, target, dir, "harness", h, "-O0"), clangC(t, target, dir, "clib", cTag(0), "-O2"))
			out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel, release: true, lto: true}), "")
			if want := "10\n-1\n-1\n"; code != 0 || out != want {
				t.Fatalf("exit %d, output %q, want %q", code, out, want)
			}
		})
	}
}
