// Why: the constant-time tests read a --debug build (symbols kept, no leaf
// inlining), which is not the code a release build ships.
// What: TestSTDCRYPTO_ReadBodiesAreShipped, across all eleven builds.
// How: every read function's body, addresses masked, must appear as a run of
// consecutive instructions in the stripped shipped .text.

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// ctMaskRe matches what a relink can move: a number in hex (an address, a
// displacement, a rip-relative offset) and a symbol in angle brackets.
var ctMaskRe = regexp.MustCompile(`0x[0-9a-f]+|<[^>]*>|\b[0-9a-f]{6,}\b`)

// ctMasked is one instruction with objdump's trailing comment (a resolved
// address and, with symbols, its name) dropped and ctMaskRe's parts
// masked.
func ctMasked(op, args string) string {
	if i := strings.Index(args, "#"); i >= 0 {
		args = strings.TrimSpace(args[:i])
	}
	return op + " " + ctMaskRe.ReplaceAllString(args, "#")
}

// ctStream reads llvm-objdump -d output as one list of masked
// instructions, whatever function each belongs to.
func ctStream(dis string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(dis, "\r\n", "\n"), "\n") {
		if m := ctInsnRe.FindStringSubmatch(l); m != nil {
			out = append(out, ctMasked(m[2], strings.TrimSpace(m[3])))
		}
	}
	return out
}

// ctBodyNote is what ctShippedBodies found for one read function that is
// not in the shipped build instruction for instruction: same is true when
// the shipped copy (found by its longest common start, at least
// ctMinPrefix instructions, to the padding that ends it) has the same
// conditional jumps in the same order, and differs only in other
// instructions (a store, a register).
type ctBodyNote struct {
	fn     string
	same   bool
	detail string
}

// ctMinPrefix is how many leading instructions a shipped copy must share
// with the read body to be taken as its copy.
const ctMinPrefix = 16

// ctCondJumps is the conditional jumps of a masked body, in order.
func ctCondJumps(b []string) []string {
	var out []string
	for _, s := range b {
		if strings.HasPrefix(s, "j") && !strings.HasPrefix(s, "jmp") {
			out = append(out, s)
		}
	}
	return out
}

// ctPad reports whether a masked instruction is padding between bodies.
func ctPad(s string) bool {
	return strings.HasPrefix(s, "int3") || strings.HasPrefix(s, "nop")
}

// ctShippedBodies returns a note for each function read (by checked) in
// the --debug disassembly ins whose masked body is not a run of shipped.
func ctShippedBodies(ins []ctInsn, shipped []string, checked func(string) bool) []ctBodyNote {
	bodies := map[string][]string{}
	var order []string
	for _, x := range ins {
		if !checked(x.fn) {
			continue
		}
		if _, ok := bodies[x.fn]; !ok {
			order = append(order, x.fn)
		}
		bodies[x.fn] = append(bodies[x.fn], ctMasked(x.op, x.args))
	}
	// Index the shipped stream by instruction, so each body is tried only
	// where its first instruction is.
	at := map[string][]int{}
	for i, s := range shipped {
		at[s] = append(at[s], i)
	}
	var notes []ctBodyNote
	for _, fn := range order {
		b := bodies[fn]
		// The padding after a body (int3 or nop up to the next function's
		// alignment) depends on what follows it.
		for len(b) > 1 && ctPad(b[len(b)-1]) {
			b = b[:len(b)-1]
		}
		best, start := 0, -1
		for _, i := range at[b[0]] {
			k := 0
			for k < len(b) && i+k < len(shipped) && shipped[i+k] == b[k] {
				k++
			}
			if k > best {
				best, start = k, i
			}
		}
		if best == len(b) {
			continue
		}
		if best < ctMinPrefix {
			notes = append(notes, ctBodyNote{fn, false, fmt.Sprintf("no shipped copy: at most %d of its %d instructions match at any start", best, len(b))})
			continue
		}
		end := start + best
		for end < len(shipped) && !ctPad(shipped[end]) {
			end++
		}
		dj, sj := ctCondJumps(b), ctCondJumps(shipped[start:end])
		notes = append(notes, ctBodyNote{fn, slices.Equal(dj, sj), fmt.Sprintf("the shipped copy shares its first %d of %d instructions, then differs (%d instructions to its padding); conditional jumps %d read, %d shipped", best, len(b), end-start, len(dj), len(sj))})
	}
	return notes
}

// ctDis builds src (main.mk in root) for x86_64-linux in release with
// flags and returns llvm-objdump's disassembly.
func ctDis(t *testing.T, objdump, root, stdDir, name string, flags ...string) string {
	t.Helper()
	exe := filepath.Join(root, name)
	args := append([]string{"build", "--release", "-target", "x86_64-linux"}, flags...)
	out, st := mkCmd(t, root, stdDir, append(args, "-o", exe, "main.mk")...)
	if st != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk %s: exit %d\n%s", strings.Join(args, " "), st, out)
	}
	dis, err := exec.Command(objdump, "-d", "--no-show-raw-insn", exe).Output()
	if err != nil {
		t.Fatalf("%s -d: %v", objdump, err)
	}
	return string(dis)
}

// TestSTDCRYPTO_ReadBodiesAreShipped: the constant-time tests read a
// --debug build, since a release ELF link without --debug passes
// --strip-all and the disassembly is read by symbol; and --debug turns off
// the leaf inliner, so a --debug build is not the shipped code. This test
// checks that every function the four tests read has, addresses masked,
// the same instructions in the shipped build (no --debug, symbols
// stripped), in all eleven builds, PGO included. The shipped .text is one
// unnamed stream, so each read body is looked for in it as a run of
// consecutive instructions.
func TestSTDCRYPTO_ReadBodiesAreShipped(t *testing.T) {
	objdump := cryptoObjdump()
	if objdump == "" {
		t.Skip("needs llvm-objdump (LLVM 21)")
	}
	probes := []struct {
		name    string
		src     string
		forces  []sdForce
		pgo     bool
		checked func(string) bool
	}{
		{"crypto", cryptoCTProbe, nil, false, ctChecked},
		{"aes-kernel", cr2CtProbe, nil, false, cr2CtChecked},
		{"aes-kernel-pgo", cr2CtProbe, nil, true, cr2CtChecked},
		{"aes-masked", cr2CtProbe, s8AesOff, false, cr2CtChecked},
		{"aes-masked-pgo", cr2CtProbe, s8AesOff, true, cr2CtChecked},
		{"ec-release", cr2EcCtProbe, nil, false, cr2EcCtChecked},
		{"ec-release-pgo", cr2EcCtProbe, nil, true, cr2EcCtChecked},
		{"hash2-kernel", h2CtProbe, nil, false, h2CtChecked},
		{"hash2-kernel-pgo", h2CtProbe, nil, true, h2CtChecked},
		{"hash2-masked", h2CtProbe, s8ShaOff, false, h2CtChecked},
		{"hash2-masked-pgo", h2CtProbe, s8ShaOff, true, h2CtChecked},
	}
	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			stdDir := cryptoStdDir(t, root, sdFixtures(t, p.forces))
			if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(p.src), 0o644); err != nil {
				t.Fatal(err)
			}
			pgo := "--pgo=off"
			if p.pgo {
				pgo = "--pgo=" + h2CtProfile(t, root, stdDir)
			}
			ins := ctParse(ctDis(t, objdump, root, stdDir, "debug.elf", "--debug", pgo))
			shipped := ctStream(ctDis(t, objdump, root, stdDir, "shipped.elf", pgo))
			fns := map[string]bool{}
			for _, x := range ins {
				if p.checked(x.fn) {
					fns[x.fn] = true
				}
			}
			read := len(fns)
			if read == 0 {
				t.Fatal("the --debug build holds no function the test reads")
			}
			for _, n := range ctShippedBodies(ins, shipped, p.checked) {
				if n.same {
					t.Logf("%s: %s, so its reading holds for the shipped code", n.fn, n.detail)
					continue
				}
				t.Errorf("%s: %s, so the constant-time reading of the --debug body does not cover the shipped code: read the shipped copy", n.fn, n.detail)
			}
			t.Logf("%d read functions, %d shipped instructions", read, len(shipped))
		})
	}
}
