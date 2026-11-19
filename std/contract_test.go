// Why: the runtime fills std's process state by layout, and the evaluator
// refuses addresses, so these std-internal contracts must not drift.
// What: TestProcessStateMatchesRuntimeContract and
// TestArenaMakesNoAddresses, with structFields and processStateContract.
// How: text checks: struct fields compared with the layout the runtime
// writes, and a scan of the arena and allocator bodies for address reads.

package std

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// structFields returns the fields of `struct name { ... }` in src, in order,
// each as "export name: type" or "name: type", comments dropped. A one-line
// body separates fields by two spaces, as processStateContract writes EnvVar.
func structFields(src, name string) ([]string, bool) {
	m := regexp.MustCompile(`(?s)\bstruct ` + regexp.QuoteMeta(name) + ` \{(.*?)\}`).FindStringSubmatch(src)
	if m == nil {
		return nil, false
	}
	var out []string
	for _, line := range strings.Split(m[1], "\n") {
		line, _, _ = strings.Cut(line, "//")
		for _, f := range strings.Split(line, "  ") {
			if f = strings.TrimSpace(f); f != "" {
				out = append(out, f)
			}
		}
	}
	return out, true
}

// processStateContract is the layout the runtime fills at start-up: the
// fields of core/io_system.mk's BufState, Process, EnvVar and System, in
// order, with their export marks. Change it only together with the runtime
// code that writes Process.
const processStateContract = `export struct BufState {
    export lock: raw.Atomic[u32]
    export h: uint
    export buf: *u8
    export len: int
    export cap: int
    export start: int
}
export struct Process {
    export stdout: BufState
    export stderr: BufState
    export stdin: BufState
    export env: []EnvVar
}
export struct EnvVar { export name: str  export value: str }
export struct System {
    export args: []str
    export io: Io
    export arena: &Arena
    export heap: &Heap
    proc: *Process
}
`

// TestProcessStateMatchesRuntimeContract: BufState, Process, EnvVar
// and System in core/io_system.mk have exactly processStateContract's
// fields, names, types, order and export marks: the runtime fills Process
// at start-up against that layout. BufState's len was once renamed used,
// which E0206 never required (BufState has no methods).
func TestProcessStateMatchesRuntimeContract(t *testing.T) {
	spec := processStateContract
	src := readText(t, "core/io_system.mk")
	for _, name := range []string{"BufState", "Process", "EnvVar", "System"} {
		want, ok := structFields(spec, name)
		if !ok {
			t.Fatalf("processStateContract declares no %s", name)
		}
		got, ok := structFields(src, name)
		if !ok {
			t.Errorf("core/io_system.mk declares no %s", name)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s fields = %q\nprocessStateContract = %q", name, got, want)
		}
	}
}

// TestArenaMakesNoAddresses: no arena or allocator body reads or makes
// an address, which the evaluator refuses (raw.Addr is pointer-bytes;
// raw.FromAddr's pointer cannot be dereferenced), so Arena runs at
// compile time over evaluation memory. The allocator context is a pointer
// (`ctx: *u8`), not a uint, and the arena header keeps positions as a
// pointer plus byte counts.
func TestArenaMakesNoAddresses(t *testing.T) {
	for _, f := range []string{"core/mem_arena.mk", "core/mem_alloc.mk", "core/coll_list.mk", "core/text_text.mk"} {
		src := readText(t, f)
		for _, bad := range []string{"raw.Addr(", "raw.Addr[", "raw.FromAddr(", "raw.FromAddr["} {
			if strings.Contains(src, bad) {
				t.Errorf("std/%s calls %s", f, bad)
			}
		}
	}
	alloc := readText(t, "core/mem_alloc.mk")
	table, ok := structFields(alloc, "AllocTable")
	if !ok {
		t.Fatal("core/mem_alloc.mk declares no AllocTable")
	}
	want := []string{"export alloc: fn(*u8, int, int): *u8", "export resize: fn(*u8, *u8, int, int, int): bool", "export free: fn(*u8, *u8, int, int)"}
	if !slices.Equal(table, want) {
		t.Errorf("AllocTable = %q, want %q", table, want)
	}
	if f, _ := structFields(alloc, "Alloc"); !slices.Contains(f, "ctx: *u8") {
		t.Errorf("Alloc = %q: ctx is not a *u8", f)
	}
	hdr, ok := structFields(readText(t, "core/mem_arena.mk"), "ArenaHdr")
	if !ok {
		t.Fatal("core/mem_arena.mk declares no ArenaHdr")
	}
	for _, f := range hdr {
		if strings.HasSuffix(f, ": uint") {
			t.Errorf("ArenaHdr field %q holds an address as an integer", f)
		}
	}
	for _, sig := range []string{"MakeHeap(ctx: *u8, t: *AllocTable): Heap", "HeapCtx(h: &Heap): *u8"} {
		if !strings.Contains(readText(t, "core/mem_internal.mk"), "export "+sig+" {") {
			t.Errorf("core/mem_internal.mk does not declare %s", sig)
		}
	}
}
