// Why: an unsafe impl Send or Share in an imported module decides task and
// bound verdicts elsewhere; per-function reuse must see each such change.
// What: TestFuncReuseUnsafeImplSend and TestFuncReuseUnsafeImplSendAdded,
// over sendPkg and boundPkg.
// How: adds, removes and changes the impls and checks which functions are
// rechecked, warm against cold.

package driver

import (
	"bytes"
	"slices"
	"testing"
)

// Tests that an unsafe impl Send or Share added, removed or changed in an
// imported module reaches every function whose verdict it decides: a task
// argument (check's sendable, `s.go F(x)`) and a Send bound (check's
// satisfies). The function prefix holds no impl guard that would miss
// every function of the reader on any unsafe impl change: check's sendable
// records shapeReads. The function that reads no type of b (Other) must
// come from the cache.

// sendPkg: b (an unsafe module) declares P and Q, each holding a raw pointer
// (not Send), Box[T] holding a *T, and asserts Send for P, Share for P and
// Send for Box[T] when T is Send. a spawns each and has a function that does
// neither. boundPkg adds u, which takes a P through a Send bound (u, because
// an E0701 there would hide a's later-phase E1001s by the phase rule).
func boundPkg() map[string]string {
	files := sendPkg()
	files["u/u.mk"] = "import b\n\nNeedSend[T: Send](x: &T) {\n}\n\nexport Bound(p: &b.P) {\n    NeedSend(p)\n}\n\n" +
		"export Other(): int {\n    return 1\n}\n"
	return files
}

func sendPkg() map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"unsafe_modules": `["b"]`}),
		"b/b.mk": "export struct P {\n    export p: ?*u8\n}\n\nexport struct Q {\n    export p: ?*u8\n}\n\n" +
			"export struct Box[T] {\n    export p: ?*T\n}\n\n" +
			"unsafe impl Send for P {}\nunsafe impl Share for P {}\nunsafe impl[T: Send] Send for Box[T] {}\n",
		"a/a.mk": "import b\n\nPokeP(p: b.P) {\n}\n\nPokeRef(p: &b.P) {\n}\n\nPokeBox(x: b.Box[b.P]) {\n}\n\n" +
			"export SpawnP(io: Io, p: b.P) {\n    scope s := io.Scope() {\n        s.go PokeP(p)\n    }\n}\n\n" +
			"export SpawnRef(io: Io, p: &b.P) {\n    scope s := io.Scope() {\n        s.go PokeRef(p)\n    }\n}\n\n" +
			"export SpawnBox(io: Io, x: b.Box[b.P]) {\n    scope s := io.Scope() {\n        s.go PokeBox(x)\n    }\n}\n\n" +
			"export Other(): int {\n    return 1\n}\n",
	}
}

// TestFuncReuseUnsafeImplSend: each edit of b's unsafe impls gives the warm
// check the bytes a cold check gives, and rechecks the functions whose
// verdict it changes. The cold check of the edited tree must report the
// verdict change (the case is then not vacuous).
func TestFuncReuseUnsafeImplSend(t *testing.T) {
	cases := []struct {
		name string
		edit func(string) string
		// want: codes of the cold check after the edit; rechecked: a's
		// functions that must not come from the cache.
		want      []string
		rechecked []string
		bound     bool // boundPkg, not sendPkg
	}{
		{"remove Send for P", replace("unsafe impl Send for P {}\n", ""),
			[]string{"E1001", "E1001"}, []string{"a.SpawnP", "a.SpawnBox"}, false},
		{"remove Send for P, bound", replace("unsafe impl Send for P {}\n", ""),
			[]string{"E0701"}, []string{"u.Bound"}, true},
		{"remove Share for P", replace("unsafe impl Share for P {}\n", ""),
			[]string{"E1002"}, []string{"a.SpawnRef"}, false},
		{"Box Send needs Share too", func(s string) string {
			s = replace("unsafe impl[T: Send] Send for Box[T] {}", "unsafe impl[T: Send + Share] Send for Box[T] {}")(s)
			return replace("unsafe impl Share for P {}\n", "")(s)
		}, []string{"E1002", "E1001"}, []string{"a.SpawnRef", "a.SpawnBox"}, false},
		{"remove Send for Box", replace("unsafe impl[T: Send] Send for Box[T] {}\n", ""),
			[]string{"E1001"}, []string{"a.SpawnBox"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := sendPkg()
			if c.bound {
				files = boundPkg()
			}
			warm, cold := warmAfter(t, files, "b/b.mk", c.edit)
			got := codes(cold)
			slices.Sort(got)
			want := slices.Clone(c.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("cold codes after the edit %q, want %q\n%s", got, want, cold.Report.Text(cold.Sources))
			}
			if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
				t.Errorf("warm --json %s\ncold %s", jsonOf(t, warm), jsonOf(t, cold))
			}
			checked := pkgItems(warm.Trace.ItemsChecked)
			for _, k := range c.rechecked {
				if !slices.Contains(checked, k) {
					t.Errorf("%s not rechecked (rechecked %q, from cache %q)", k, checked, pkgItems(warm.Trace.ItemsFromCache))
				}
			}
			other := "a.Other"
			if c.bound {
				other = "u.Other"
			}
			if fromCache := pkgItems(warm.Trace.ItemsFromCache); !slices.Contains(fromCache, other) {
				t.Errorf("%s not from the cache (rechecked %q, from cache %q)", other, checked, fromCache)
			}
		})
	}
}

// TestFuncReuseUnsafeImplSendAdded: the other direction. Q is not Send, so
// a spawn of a Q is E1001 cold; adding unsafe impl Send for Q makes it
// clean, warm as cold.
func TestFuncReuseUnsafeImplSendAdded(t *testing.T) {
	files := sendPkg()
	files["a/a.mk"] += "\nPokeQ(q: b.Q) {\n}\n\nexport SpawnQ(io: Io, q: b.Q) {\n    scope s := io.Scope() {\n        s.go PokeQ(q)\n    }\n}\n"
	first := frontCheck(t, Config{Root: writeTree(t, files), CacheDir: t.TempDir()})
	if got := codes(first); !slices.Equal(got, []string{"E1001"}) {
		t.Fatalf("SpawnQ before the impl: codes %q, want E1001\n%s", got, first.Report.Text(first.Sources))
	}
	warm, cold := warmAfter(t, files, "b/b.mk", func(s string) string { return s + "unsafe impl Send for Q {}\n" })
	clean(t, cold)
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("warm --json %s\ncold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
	if got := pkgItems(warm.Trace.ItemsChecked); !slices.Contains(got, "a.SpawnQ") {
		t.Errorf("a.SpawnQ not rechecked (%q)", got)
	}
}
