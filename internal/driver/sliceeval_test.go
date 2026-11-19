// Why: compile-time evaluation runs mono's bodies, where a Slice[T]
// namespace function's instance takes self as []T or mut []T and every
// caller's receiver is made that slice; eval and the native code must agree.
// The instance-limit walk must also stop at a const whose evaluation failed.
// What: sliceThree, TestSliceCallerAtCompileTime,
// TestSliceCallerAgreesNative and TestWalkLeavesFailedConstReaders.
// How: checks consts that call Slice[T] functions, builds the same program
// natively and compares, and checks a program that reads a failing const.

package driver

import (
	"os"
	"path/filepath"
	"testing"

	"mk/internal/types"
)

// sliceThree is a user implementer of Slice[int], so a call of a Slice[T]
// function on it gets mono's call of its ItemsMut, which eval must run.
const sliceThree = `struct Three {
    a: [3]int
}

impl Slice[int] for Three {
    Items(&self): []int {
        return self.a[..]
    }

    ItemsMut(mut self): mut []int {
        return mut self.a[..]
    }
}

ArrayFirst(): int {
    var a: [3]int = [0; 3]
    a[0] = 1
    a[2] = 3
    a.Reverse()
    return a[0]
}

ThreeFirst(): int {
    var t := Three(a: [0; 3])
    t.a[0] = 1
    t.a[2] = 3
    t.Reverse()
    return t.a[0]
}
`

// TestSliceCallerAtCompileTime: a non-generic body that calls a Slice[T]
// function reaches eval as mono's body of it (program.Body), with its
// receiver made the slice the callee's instance takes. Both of mono's
// receiver forms: an array (a reslice) and an implementer (a call of its
// ItemsMut). Each const indexes a one-element array by value - 3, so a
// value other than the reversed first element, 3, is a compile-time bounds
// error. Before the fix, ArrayFirst evaluated to 1 (the array, unreversed)
// with no diagnostic, while the executable returned 3.
func TestSliceCallerAtCompileTime(t *testing.T) {
	src := sliceThree + `
const A: int = [0; 1][ArrayFirst() - 3]
const B: int = [0; 1][ThreeFirst() - 3]

export Main(sys: &System): int {
    return A + B
}
`
	clean(t, check(t, Config{Root: writeTree(t, map[string]string{"m.mk": src})}))
}

// TestSliceCallerAgreesNative: the executable computes what the const
// holds. Main returns 42 when the compile-time and run-time values agree,
// else 7, so the test fails on any disagreement, not only on a wrong 3.
func TestSliceCallerAgreesNative(t *testing.T) {
	src := sliceThree + `
const A: int = ArrayFirst()
const B: int = ThreeFirst()

export Main(sys: &System): int {
    if A == ArrayFirst() && B == ThreeFirst() && A == 3 && B == 3 {
        return 42
    }
    return 7
}
`
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "agree.mk")
			if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			exe := buildProgram(t, file, target, types.Dev, dir)
			out, errOut, code := runExe(t, target, exe, "")
			if code != 42 {
				t.Errorf("exit %d, stdout %q, stderr %q; want 42 (const and run time agree)", code, out, errOut)
			}
		})
	}
}

// TestWalkLeavesFailedConstReaders: Main reads a const whose evaluation
// fails. The walk treats Main as a leaf: exactly the E0901, exit 1, and no
// E0000 (never "iface: instance reads const main.H with no value", exit 3).
// A runaway chain elsewhere in the module is still E0708, so the walk is not
// skipped.
func TestWalkLeavesFailedConstReaders(t *testing.T) {
	src := nest + `
Boom(): int {
    Assert(false)
    return 1
}

const H: int = Boom()

Start(): int {
    return Nest(1, 3)
}

export Main(sys: &System): int {
    return H
}
`
	r := check(t, Config{Root: writeTree(t, map[string]string{"m.mk": src})})
	got := codes(r)
	if len(got) != 2 || got[0] != "E0708" && got[1] != "E0708" || got[0] != "E0901" && got[1] != "E0901" || ExitStatus(r.Report) != 1 {
		t.Errorf("codes %v exit %d, want E0708 and E0901, exit 1\n%s", got, ExitStatus(r.Report), r.Report.Text(r.Sources))
	}
}
