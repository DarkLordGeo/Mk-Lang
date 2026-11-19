// Why: before changing the compiler to close a gap with C, one reads what
// each side generated (tests/bench/README.md "Reading a row's code"); this
// gathers all of it for one row in one command.
//
// What: `bench ir <row>` (irRow, irView): it builds one same-algorithm row's
// mk and c columns with the row's own command lines and puts side by side
// Mk's pack IR (mk build's hidden --emit-ll), Mk's post-LTO IR per ThinLTO
// module, the C twin's -O3 IR, the optimization remarks of both, ThinLTO's
// import report, and the disassembly of the named hot functions of both
// executables (readLLDMap, matchFuncs, disasm, sideBySide).
//
// How: hot functions are found by name in each lld map and disassembled by
// llvm-objdump over their RVA range. It times nothing, takes no machine lock
// and writes nothing under tests/bench, so it can run on a loaded machine;
// every output is a deterministic fact of the two builds, never a benchmark
// number.

package main

import (
	"bufio"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// irRemarks are the remark flags of both sides: the loop vectorizer's
// successes, misses and reasons, and the loop unroller's successes. clang
// 21 keeps only the last of each -R option (intsum.c: `-Rpass=loop-vectorize
// -Rpass=loop-unroll` prints only unroll remarks, the reverse order only
// vectorize ones, the alternation both), so one pattern per kind.
var irRemarks = []string{"-Rpass=loop-vectorize|loop-unroll", "-Rpass-missed=loop-vectorize", "-Rpass-analysis=loop-vectorize"}

// irImportFlags are the lld-link options that print ThinLTO's import
// decisions at the index step.
var irImportFlags = []string{"/mllvm:-print-imports", "/mllvm:-print-import-failures"}

type irOptions struct {
	Root, Work, Mk string
	Row            string
	MkFuncs        []string // hot functions to disassemble in the mk executable
	CFuncs         []string // in the C executable (default: MkFuncs)
	Log            func(string)
}

// irSide is one column's build in irView: its executable, its lld map and
// where its disassembly goes.
type irSide struct {
	ID, Exe, Map string
	Funcs        []string
}

// irRow is the one row bench ir reads. It needs an mk source; a row with no c
// source (most std rows) is read on Mk's side alone. Its vendor flags are
// checked as for every build, but its data file is not made: bench ir runs
// nothing, and prepareRows would write the file into tests/bench/data.
func irRow(root, name string) (*row, error) {
	rows, err := discover(root)
	if err == nil {
		rows, err = selectRows(rows, "", []string{name})
	}
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("bench ir takes one row, %q names %d", name, len(rows))
	}
	r := rows[0]
	if err := checkVendorFlags(r); err != nil {
		return nil, err
	}
	_, ok1 := columnByID("mk")
	_, ok2 := columnByID("c")
	_, has1 := r.Sources["mk"]
	if !ok1 || !ok2 || !has1 {
		return nil, fmt.Errorf("row %s needs an mk source", r.id())
	}
	return r, nil
}

// irView builds the row into <work>/ir/<row> (removed first) and writes
// index.txt there, which it also returns.
func irView(o irOptions) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("bench ir reads lld-link maps and the Windows ThinLTO path (thinLink); run it on Windows")
	}
	r, err := irRow(o.Root, o.Row)
	if err != nil {
		return "", err
	}
	mkCol, _ := columnByID("mk")
	cCol, _ := columnByID("c")
	t, _ := findToolchain(o.Work, o.Mk)
	if t.Clang == "" {
		return "", errors.New("clang not found")
	}
	llvm := filepath.Dir(t.Clang)
	objdump := filepath.Join(llvm, "llvm-objdump.exe")
	if _, err := os.Stat(objdump); err != nil {
		return "", fmt.Errorf("llvm-objdump beside clang: %w", err)
	}
	dir := filepath.Join(o.Work, "ir", strings.ReplaceAll(r.id(), "/", "_"))
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	sub := func(name string) string { return filepath.Join(dir, name) }
	// A row with no c source is read on Mk's side alone: no c-ll, C
	// remarks, c.exe, c.map, c-disasm.txt or side-by-side.txt.
	_, hasC := r.Sources["c"]
	dirs := []string{"shim", "mk-stderr", "mk-lto-ll"}
	if hasC {
		dirs = append(dirs, "c-ll")
	}
	for _, d := range dirs {
		if err := os.MkdirAll(sub(d), 0o755); err != nil {
			return "", err
		}
	}
	var idx strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&idx, f, a...) }
	if hasC {
		p("bench ir %s: Mk's IR and machine code beside the C twin's (README \"Reading a row's code\"). No timing.\n\n", r.id())
	} else {
		p("bench ir %s: Mk's IR and machine code; the row has no C twin (README \"Reading a row's code\"). No timing.\n\n", r.id())
	}

	// Mk: the row's mk build line, through the shim (shim.go), with the
	// hidden --emit-ll. The line has --no-cache, so every pack is compiled
	// and linked through the shim, and the remarks and the import report
	// come from the release link the table measures.
	mkSide := irSide{ID: "mk", Exe: sub("mk.exe"), Map: sub("mk.map"), Funcs: o.MkFuncs}
	backend := append(slices.Clone(irRemarks), "-mllvm", "-print-imports") // what each backend imported
	cfg := shimConfig{RealDir: llvm, Log: sub("mk-commands.txt"), BackendArgs: backend, IndexArgs: irImportFlags,
		FinalArgs: []string{"/lldmap:" + mkSide.Map}, BackendIRDir: sub("mk-lto-ll"), StderrDir: sub("mk-stderr")}
	if err := writeShimDir(sub("shim"), cfg); err != nil {
		return "", err
	}
	mcmd, err := t.command(r, mkCol, mkSide.Exe)
	if err != nil {
		return "", err
	}
	mcmd.Args = slices.Insert(mcmd.Args, 2, "--llvm", sub("shim"), "--emit-ll", sub("mk-ll"))
	o.Log("mk: " + mcmd.String())
	if out, err := runCmd(mcmd, nil); err != nil {
		return "", fmt.Errorf("mk build: %v\n%s", err, firstLines(out, 30))
	}
	p("mk build (the row's mk column, plus --llvm <shim> --emit-ll <dir>):\n  %s\n", mcmd.String())

	// C: the row's c line with an lld map, then the same compile as -O3 IR
	// with the remarks (single TU, no LTO: these are its final IR's).
	cSide := irSide{ID: "c", Exe: sub("c.exe"), Map: sub("c.map"), Funcs: o.CFuncs}
	if len(cSide.Funcs) == 0 {
		cSide.Funcs = o.MkFuncs
	}
	buildC := func() error {
		ccmd, err := t.command(r, cCol, cSide.Exe)
		if err != nil {
			return err
		}
		ccmd.Args = append(ccmd.Args, "-Wl,/lldmap:"+cSide.Map)
		o.Log("c: " + ccmd.String())
		if out, err := runCmd(ccmd, nil); err != nil {
			return fmt.Errorf("c build: %v\n%s", err, firstLines(out, 30))
		}
		icmd := ccmd
		icmd.Dir = sub("c-ll")
		icmd.Args = nil
		for i := 0; i < len(ccmd.Args); i++ {
			a := ccmd.Args[i]
			switch {
			case a == "-o":
				i++
				continue
			case a == "-fuse-ld=lld", strings.HasPrefix(a, "-Wl,"):
				continue
			}
			icmd.Args = append(icmd.Args, a)
		}
		icmd.Args = append(append(icmd.Args, "-S", "-emit-llvm"), irRemarks...)
		o.Log("c ir: " + icmd.String())
		rem, err := os.Create(sub("c-remarks.txt"))
		if err != nil {
			return err
		}
		_, err = runCmd(icmd, rem)
		rem.Close()
		if err != nil {
			return fmt.Errorf("c -S -emit-llvm: %v", err)
		}
		p("c build (the row's c column, plus -Wl,/lldmap):\n  %s\n", ccmd.String())
		p("c IR (the same compile, -S -emit-llvm and the remarks, in c-ll):\n  %s\n\n", icmd.String())
		return nil
	}
	sides := []irSide{mkSide}
	if hasC {
		if err := buildC(); err != nil {
			return "", err
		}
		sides = append(sides, cSide)
	} else {
		p("c: the row has no c source, so only Mk's side is read\n\n")
	}

	// The Mk remarks and the import report, from the shim's stderr files.
	if err := gatherStderr(sub("mk-stderr"), "backend-", sub("mk-remarks.txt")); err != nil {
		return "", err
	}
	if err := gatherStderr(sub("mk-stderr"), "index-", sub("mk-imports.txt")); err != nil {
		return "", err
	}

	p("Files (in %s):\n", dir)
	p("  mk-ll/<bin>/p*.ll, support.ll  Mk's pack IR as codegen wrote it (mk build --emit-ll)\n")
	p("  mk-lto-ll/m*.o.ll              Mk's IR after ThinLTO import and the backend's -O3, as it reaches code generation;\n")
	p("                                 m<i>.o is the link's i-th bitcode module, pack p<i>\n")
	p("  mk-remarks.txt                 the ThinLTO backends' remarks and imports (%s)\n", strings.Join(backend, " "))
	p("  mk-imports.txt                 the index step's missed imports (%s)\n", strings.Join(irImportFlags, " "))
	p("  mk-commands.txt                every LLVM command of the mk build\n")
	if hasC {
		p("  c-ll/*.ll                      the C twin's -O3 IR\n")
		p("  c-remarks.txt                  the C compile's remarks\n")
		p("  mk.exe, mk.map, c.exe, c.map   the executables and their lld maps\n")
		p("  mk-disasm.txt, c-disasm.txt    the named functions' disassembly; side-by-side.txt pairs the first of each\n\n")
	} else {
		p("  mk.exe, mk.map                 the executable and its lld map\n")
		p("  mk-disasm.txt                  the named functions' disassembly\n\n")
	}
	p("Remarks (lines):\n")
	for _, f := range []string{"mk-remarks.txt", "c-remarks.txt"} {
		if f == "c-remarks.txt" && !hasC {
			continue
		}
		p("  %-15s %s\n", f, remarkCounts(sub(f)))
	}
	if n, err := countImportFailures(sub("mk-imports.txt")); err == nil {
		p("  mk-imports.txt  %d lines naming a function not imported\n", n)
	}
	p("\n")

	var firsts [2][]string
	for i, s := range sides {
		fns, err := readLLDMap(s.Map)
		if err != nil {
			return "", fmt.Errorf("%s map: %w", s.ID, err)
		}
		base, err := imageBase(s.Exe)
		if err != nil {
			return "", err
		}
		if len(s.Funcs) == 0 {
			p("%s: no -func given; the largest functions (bytes):\n", s.ID)
			bySize := slices.Clone(fns)
			slices.SortStableFunc(bySize, func(a, b mapFunc) int { return int(b.End-b.Start) - int(a.End-a.Start) })
			for _, f := range bySize[:min(15, len(bySize))] {
				p("  %8d  %s\n", f.End-f.Start, f.Name)
			}
			p("\n")
			continue
		}
		var out strings.Builder
		for _, want := range s.Funcs {
			ms := matchFuncs(fns, want)
			if len(ms) == 0 {
				p("%s: no function matches %q\n", s.ID, want)
				continue
			}
			for k, f := range ms[:min(8, len(ms))] {
				lines, err := disasm(objdump, s.Exe, base, f)
				if err != nil {
					return "", err
				}
				p("%s: %s  %d bytes, %d instructions\n", s.ID, f.Name, f.End-f.Start, len(lines))
				fmt.Fprintf(&out, "== %s  rva 0x%x..0x%x  %d instructions\n%s\n\n", f.Name, f.Start, f.End, len(lines), strings.Join(lines, "\n"))
				if k == 0 && firsts[i] == nil {
					firsts[i] = append([]string{"== " + s.ID + ": " + f.Name}, lines...)
				}
			}
			if len(ms) > 8 {
				p("%s: %d more functions match %q (name one exactly)\n", s.ID, len(ms)-8, want)
			}
		}
		if err := os.WriteFile(sub(s.ID+"-disasm.txt"), []byte(out.String()), 0o644); err != nil {
			return "", err
		}
	}
	if hasC && (firsts[0] != nil || firsts[1] != nil) {
		if err := os.WriteFile(sub("side-by-side.txt"), []byte(sideBySide(firsts[0], firsts[1], 70)), 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(sub("index.txt"), []byte(idx.String()), 0o644); err != nil {
		return "", err
	}
	return idx.String(), nil
}

// runCmd runs one build command, its stderr also to errTo when given; the
// combined output is returned.
func runCmd(b buildCmd, errTo io.Writer) (string, error) {
	cmd := exec.Command(b.Args[0], b.Args[1:]...)
	cmd.Dir = b.Dir
	cmd.Env = append(os.Environ(), b.Env...)
	var all strings.Builder
	cmd.Stdout = &all
	cmd.Stderr = &all
	if errTo != nil {
		cmd.Stderr = io.MultiWriter(&all, errTo)
	}
	err := cmd.Run()
	return all.String(), err
}

// gatherStderr concatenates the shim's stderr files whose names start with
// prefix, in name order, into out.
func gatherStderr(dir, prefix, out string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		b.Write(body)
	}
	return os.WriteFile(out, []byte(b.String()), 0o644)
}

var remarkTag = regexp.MustCompile(`\[(-R[a-z-]+=[a-z-]+)\]`)

// remarkCounts is the number of remark lines of each -R kind in file.
func remarkCounts(file string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	counts := map[string]int{}
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.Contains(l, "remark:") {
			continue
		}
		tag := "other"
		if m := remarkTag.FindStringSubmatch(l); m != nil {
			tag = m[1]
		}
		counts[tag]++
	}
	if len(counts) == 0 {
		return "none"
	}
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}

// countImportFailures counts the import report's lines about a function
// that was not imported (LLVM prints its reason, such as TooLarge).
func countImportFailures(file string) (int, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "Reason =") || strings.Contains(l, "not imported") {
			n++
		}
	}
	return n, nil
}

// mapFunc is one function of an lld map: its name and its RVA range.
type mapFunc struct {
	Name       string
	Start, End uint64
}

// readLLDMap reads lld-link's /lldmap: the symbols of the .text output
// section, each ending at the next symbol's address or at the end of its
// input section, whichever is first. Columns are found from the header
// ("Address Size Align Out In Symbol").
func readLLDMap(path string) ([]mapFunc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	if !sc.Scan() {
		return nil, errors.New("empty map")
	}
	head := sc.Text()
	cOut, cIn, cSym := strings.Index(head, "Out"), strings.Index(head, "In "), strings.Index(head, "Symbol")
	if cOut < 0 || cIn < 0 || cSym < 0 {
		return nil, fmt.Errorf("not an lld map header: %q", head)
	}
	type sym struct {
		name     string
		addr, ce uint64
	}
	var syms []sym
	section, contribEnd := "", uint64(0)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		fs := strings.Fields(l)
		if len(fs) < 4 {
			continue
		}
		if len(l) <= cSym {
			l += strings.Repeat(" ", cSym+1-len(l)) // a section line ends at its name
		}
		addr, err1 := strconv.ParseUint(fs[0], 16, 64)
		size, err2 := strconv.ParseUint(fs[1], 16, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		switch {
		case strings.TrimSpace(l[cOut:cIn]) != "":
			section = strings.TrimSpace(l[cOut:])
		case len(l) > cSym && strings.TrimSpace(l[cIn:cSym]) == "":
			// A $-name is an MSVC object's local label ($LN12), inside a
			// function: it would cut the function's range short.
			if name := strings.TrimSpace(l[cSym:]); section == ".text" && !strings.HasPrefix(name, "$") {
				syms = append(syms, sym{name, addr, contribEnd})
			}
		default:
			contribEnd = addr + size
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	slices.SortStableFunc(syms, func(a, b sym) int {
		switch {
		case a.addr < b.addr:
			return -1
		case a.addr > b.addr:
			return 1
		}
		return 0
	})
	var out []mapFunc
	seen := map[string]bool{}
	for i, s := range syms {
		end := s.ce
		for _, n := range syms[i+1:] {
			if n.addr > s.addr {
				end = min(end, n.addr)
				break
			}
		}
		key := fmt.Sprintf("%s@%x", s.name, s.addr)
		if end <= s.addr || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, mapFunc{s.name, s.addr, end})
	}
	return out, nil
}

// matchFuncs is the functions named want: an exact name, or a name ending
// in "." + want (Mk's mk.<module>.<name>), else every name containing want
// regardless of case.
func matchFuncs(fns []mapFunc, want string) []mapFunc {
	var exact, sub []mapFunc
	lw := strings.ToLower(want)
	for _, f := range fns {
		switch {
		case f.Name == want || strings.HasSuffix(f.Name, "."+want):
			exact = append(exact, f)
		case strings.Contains(strings.ToLower(f.Name), lw):
			sub = append(sub, f)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return sub
}

// imageBase is the PE image base of exe (RVA + base is llvm-objdump's
// address).
func imageBase(exe string) (uint64, error) {
	f, err := pe.Open(exe)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return h.ImageBase, nil
	case *pe.OptionalHeader32:
		return uint64(h.ImageBase), nil
	}
	return 0, fmt.Errorf("%s: no optional header", exe)
}

var insnLine = regexp.MustCompile(`^\s*[0-9a-f]+:\s`)

// disasm is llvm-objdump's disassembly of f in exe, its instruction lines
// only.
func disasm(objdump, exe string, base uint64, f mapFunc) ([]string, error) {
	out, err := exec.Command(objdump, "-d", "--no-show-raw-insn",
		fmt.Sprintf("--start-address=0x%x", base+f.Start), fmt.Sprintf("--stop-address=0x%x", base+f.End), exe).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("llvm-objdump %s: %v\n%s", f.Name, err, firstLines(string(out), 10))
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimRight(l, "\r")
		if insnLine.MatchString(l) {
			lines = append(lines, strings.ReplaceAll(strings.TrimSpace(l), "\t", " "))
		}
	}
	return trimPadding(lines), nil
}

// trimPadding drops the alignment padding (nop and int3) after a
// function's last instruction: a map range ends at the next symbol.
func trimPadding(lines []string) []string {
	for len(lines) > 0 {
		f := strings.Fields(lines[len(lines)-1])
		if len(f) < 2 || !(strings.HasPrefix(f[1], "nop") || f[1] == "int3") {
			break
		}
		lines = lines[:len(lines)-1]
	}
	return lines
}

// sideBySide is a and b in two columns of width w.
func sideBySide(a, b []string, w int) string {
	var s strings.Builder
	for i := range max(len(a), len(b)) {
		l, r := "", ""
		if i < len(a) {
			l = a[i]
		}
		if i < len(b) {
			r = b[i]
		}
		if len(l) > w-1 {
			l = l[:w-2] + "~"
		}
		fmt.Fprintf(&s, "%-*s| %s\n", w, l, r)
	}
	return s.String()
}
