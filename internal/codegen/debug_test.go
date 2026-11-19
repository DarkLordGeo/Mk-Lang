// Why: panics, profilers and debuggers map addresses to source lines through
// the line tables every build carries.
// What: TestDebugLineTables checks every golden case's pack (one compile
// unit, package-relative files, a subprogram per definition, a location on
// every instruction, the module flags per object format);
// TestDebugLineTablesDWARF; TestPackBytesDeterministic.
// How: the IR text is checked, and a compiled x86_64-linux object's DWARF
// line table is read with debug/dwarf.

package codegen

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"mk/internal/types"
)

func TestDebugLineTables(t *testing.T) {
	for _, c := range emitCases {
		for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
			for _, prof := range []types.Profile{types.Dev, types.Release} {
				t.Run(fmt.Sprintf("%s/%s/%d", c.name, target.Name, prof), func(t *testing.T) {
					e := newTestEnv()
					bodies, bodiless := c.build(e)
					ll := emit(t, e, unitOf(e, bodies, bodiless...), target, prof)
					checkDebugText(t, ll, target, prof)
				})
			}
		}
	}
}

func checkDebugText(t *testing.T, ll string, target types.Target, prof types.Profile) {
	t.Helper()
	if n := strings.Count(ll, "DICompileUnit("); n != 1 {
		t.Errorf("%d compile units, want 1", n)
	}
	opt := fmt.Sprintf("isOptimized: %v", prof == types.Release)
	if !strings.Contains(ll, opt) {
		t.Errorf("compile unit without %q", opt)
	}
	if target.OS == types.OSWindows {
		if !strings.Contains(ll, `!"CodeView", i32 1`) || strings.Contains(ll, "Dwarf Version") {
			t.Error("COFF module flags are not CodeView 1")
		}
	} else if !strings.Contains(ll, `!"Dwarf Version", i32 4`) || strings.Contains(ll, "CodeView") {
		t.Error("ELF module flags are not Dwarf Version 4")
	}
	if !strings.Contains(ll, `!"Debug Info Version", i32 3`) {
		t.Error("no Debug Info Version 3")
	}
	for _, m := range regexp.MustCompile(`!DIFile\(filename: "([^"]*)", directory: "([^"]*)"\)`).FindAllStringSubmatch(ll, -1) {
		if m[2] != "." || strings.Contains(m[1], ":") || strings.HasPrefix(m[1], "/") || strings.Contains(m[1], `\`) {
			t.Errorf("DIFile %q in %q: not package-relative with directory \".\"", m[1], m[2])
		}
	}
	if sf := regexp.MustCompile(`source_filename = "([^"]*)"`).FindStringSubmatch(ll); sf == nil || strings.ContainsAny(sf[1], `:\/`) {
		t.Errorf("source_filename %v", sf)
	}
	in := false
	for i, line := range strings.Split(ll, "\n") {
		switch {
		case strings.HasPrefix(line, "define "):
			in = true
			if !regexp.MustCompile(`!dbg !\d+ \{$`).MatchString(line) {
				t.Errorf("line %d: definition without a subprogram: %s", i+1, line)
			}
		case line == "}":
			in = false
		case in && strings.HasPrefix(line, "    "):
			// a switch case; the closing "]" line carries the location
		case in && strings.HasPrefix(line, "  ") && strings.HasSuffix(line, "["):
			// a switch head
		case in && strings.HasPrefix(line, "  ") && !strings.Contains(line, "!dbg !"):
			t.Errorf("line %d: instruction without a location: %s", i+1, line)
		}
	}
}

// TestDebugLineTablesDWARF compiles the calls case for x86_64-linux and
// reads its line table: main/t.mk rows at the statements' lines and
// columns (Use's call at 2:8 and its add at 3:10).
func TestDebugLineTablesDWARF(t *testing.T) {
	var calls emitCase
	for _, c := range emitCases {
		if c.name == "calls" {
			calls = c
		}
	}
	ll := caseIR(t, calls, types.X86_64Linux)
	obj := clangC(t, types.X86_64Linux, t.TempDir(), "calls", ll, "-O0")
	f, err := elf.Open(obj)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	r := d.Reader()
	rows := map[string]bool{}
	for {
		ent, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if ent == nil {
			break
		}
		lr, err := d.LineReader(ent)
		if err != nil || lr == nil {
			continue
		}
		var le dwarf.LineEntry
		for lr.Next(&le) == nil {
			if le.File != nil {
				rows[fmt.Sprintf("%s:%d:%d", le.File.Name, le.Line, le.Column)] = true
			}
		}
	}
	for _, want := range []string{"main/t.mk:2:8", "main/t.mk:3:10", "main/t.mk:5:10", "main/t.mk:7:3"} {
		found := false
		for k := range rows {
			if strings.HasSuffix(k, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no line-table row %s in %d rows", want, len(rows))
		}
	}
}

// TestPackBytesDeterministic: every golden case's pack, emitted
// once cold, then by 8 goroutines at once over the same unit and env under
// GOMAXPROCS 1 and 8, gives the same bytes every time.
func TestPackBytesDeterministic(t *testing.T) {
	for _, c := range emitCases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv()
			bodies, bodiless := c.build(e)
			u := unitOf(e, bodies, bodiless...)
			cfg := Config{Target: types.X86_64Windows, Profile: types.Release, CPU: "x86-64-v2"}
			var cold bytes.Buffer
			if _, err := EmitPack(u, 0, e, cfg, &cold); err != nil {
				t.Fatal(err)
			}
			for _, procs := range []int{1, 8} {
				old := runtime.GOMAXPROCS(procs)
				outs := make([]bytes.Buffer, 8)
				errs := make([]error, 8)
				var wg sync.WaitGroup
				for i := range outs {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, errs[i] = EmitPack(u, 0, e, cfg, &outs[i])
					}()
				}
				wg.Wait()
				runtime.GOMAXPROCS(old)
				for i := range outs {
					if errs[i] != nil {
						t.Fatal(errs[i])
					}
					if !bytes.Equal(outs[i].Bytes(), cold.Bytes()) {
						t.Fatalf("GOMAXPROCS %d, emission %d differs from the cold one", procs, i)
					}
				}
			}
		})
	}
}
