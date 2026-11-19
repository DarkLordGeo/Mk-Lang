// Why: codegen lowers runtime hooks and intrinsics by position, so std's
// declared signatures must match the ones codegen assumes.
// What: normSig reads a std declaration into intrinsicSigs' form;
// TestHookTableMatchesStd compares runtimeSigs (the hook table)
// with std's declarations; TestIntrinsicTypesChecked shows a std
// signature a positional lowering does not match is an ICE at emission.
// How: a small parser (closing, splitTop, topColon) normalizes each line.

package codegen

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestHookTableMatchesStd: every std function codegen calls by key
// (runtimeSigs: the panic hooks, the entry, the scope, spawn and
// system-stack entries, float %) is declared by std with exactly that
// signature. std.math.Rem32 and Rem64 are compared like the others; if std
// lacks one, its absence is logged.
func TestHookTableMatchesStd(t *testing.T) {
	pending := map[string]bool{"std.math.Rem64": true, "std.math.Rem32": true}
	keys := make([]string, 0, len(runtimeSigs))
	for k := range runtimeSigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		dot := strings.LastIndex(k, ".")
		mod, name := k[:dot], k[dot+1:]
		dir := filepath.Join("..", "..", "std", filepath.FromSlash(strings.ReplaceAll(strings.TrimPrefix(mod, "std."), ".", "/")))
		decl := regexp.MustCompile(`^(?:export )?` + name + `\(`)
		var sigs []string
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".mk") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(string(b), "\n") {
				if decl.MatchString(line) {
					sig, ok := normSig(line)
					if !ok {
						t.Errorf("%s: cannot read the signature of %q", path, line)
					}
					sigs = append(sigs, sig)
				}
			}
			return nil
		})
		switch {
		case len(sigs) == 0 && pending[k]:
			t.Logf("%s: not declared by std yet; codegen calls it as %s", k, runtimeSigs[k])
		case len(sigs) == 0:
			t.Errorf("%s: std declares no such function; codegen calls it as %s", k, runtimeSigs[k])
		}
		for _, sig := range sigs {
			if sig != runtimeSigs[k] {
				t.Errorf("%s: std declares %s, codegen calls %s", k, sig, runtimeSigs[k])
			}
		}
	}
}

// TestIntrinsicTypesChecked: a std declaration whose types differ
// from what a positional lowering passes is an ICE at emission, never IR
// that clang rejects or a wrong-width store: std.sys.Syscall with a0 of
// type *u8, and std.raw.Compare returning i32.
func TestIntrinsicTypesChecked(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"syscall-ptr", "intrinsic std.sys.Syscall argument 1 is *u8"},
		{"compare-i32", "intrinsic std.raw.Compare returns i32"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv()
			pu8 := e.tt.RawPtr(types.U8)
			f := e.body(e.fn("Use", []types.TypeID{pu8}, types.Void, 0))
			var it types.ItemID
			if c.name == "syscall-ptr" {
				u := types.Uint
				it = e.stdFn("std.sys", "", "Syscall", 0, []types.TypeID{u, pu8, u, u, u, u, u}, types.Int)
				z := mir.Imm(u, 0)
				f.call(it, nil, types.Int, z, mir.Copy(place(1)), z, z, z, z, z)
			} else {
				it = e.stdFn("std.raw", "", "Compare", 0, []types.TypeID{pu8, pu8, types.Int}, types.I32)
				f.call(it, nil, types.I32, mir.Copy(place(1)), mir.Copy(place(1)), mir.Imm(types.Int, 4))
			}
			f.ret(f.at(30, 1))
			u := unitOf(e, []*mir.Body{f.b})
			u.Funcs = append(u.Funcs, Func{Symbol: "-", Key: e.items.Get(it).Key.String(), Item: it, Pack: -1})
			var b strings.Builder
			_, err := EmitPack(u, 0, e, Config{Target: types.X86_64Linux, Profile: types.Dev}, &b)
			ie, ok := err.(*InternalError)
			if !ok || !strings.Contains(ie.IE.What, c.want) {
				t.Fatalf("EmitPack: %v; want an ICE containing %q", err, c.want)
			}
		})
	}
}

// TestNormSig pins the normalizer the two signature tests rely on.
func TestNormSig(t *testing.T) {
	for in, want := range map[string]string{
		"export Atomic[T].Cas(&self, old: T, new: T): bool":                                           "[T](&self, T, T): bool",
		"export Io.ScopeOf[E](self): Scope[E]":                                                        "[E](self): Scope[E]",
		"export ReadUnaligned[T: Plain](p: *T): T":                                                    "[T: Plain](*T): T",
		"export Mul128(a: u64, b: u64): (hi: u64, lo: u64)":                                           "(u64, u64): (hi: u64, lo: u64)",
		"export Spawn(s: &Scope, thunk: fn(*Io, *u8, *u8): bool, block: *u8) {":                       "(&Scope, fn(*Io, *u8, *u8): bool, *u8)",
		"export Enter(s: *u8, io: Io, limit: int, stack: StackSize, err_size: int, site: &rt.Site) {": "(*u8, Io, int, StackSize, int, &Site)",
		"Fence()": "()",
	} {
		if got, ok := normSig(in); !ok || got != want {
			t.Errorf("normSig(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// cpuClauseTail is a declaration's trailing cpu clause (cpu "aes").
var cpuClauseTail = regexp.MustCompile(`(^|\s)cpu\s+"[^"]*"\s*$`)

// qualifier is a module qualifier before a type name (rt.Site).
var qualifier = regexp.MustCompile(`\b[a-z][a-z0-9_]*\.([A-Z])`)

// normSig is a std declaration line in intrinsicSigs' form: the generic
// lists of owner and function, the parameter types without names
// (receivers kept as written), and ": result" unless void; module
// qualifiers dropped. A trailing body brace is ignored.
func normSig(line string) (string, bool) {
	s := strings.TrimPrefix(strings.TrimSpace(line), "export ")
	var gens strings.Builder
	i := 0
	for i < len(s) && s[i] != '(' {
		if s[i] == '[' {
			j := closing(s, i)
			if j < 0 {
				return "", false
			}
			gens.WriteString(s[i : j+1])
			i = j + 1
			continue
		}
		i++
	}
	if i == len(s) {
		return "", false
	}
	j := closing(s, i)
	if j < 0 {
		return "", false
	}
	var params []string
	for _, p := range splitTop(s[i+1 : j]) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if c := topColon(p); c >= 0 {
			p = strings.TrimSpace(p[c+1:])
		}
		params = append(params, p)
	}
	rest := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s[j+1:]), "{"))
	rest = strings.TrimSpace(cpuClauseTail.ReplaceAllString(rest, "")) // the crypto intrinsics carry one
	out := gens.String() + "(" + strings.Join(params, ", ") + ")"
	if strings.HasPrefix(rest, ":") {
		out += ": " + strings.TrimSpace(rest[1:])
	} else if rest != "" {
		return "", false
	}
	return qualifier.ReplaceAllString(out, "$1"), true
}

// closing is the index of the bracket that closes s[i] ('(' or '['), or -1.
func closing(s string, i int) int {
	depth := 0
	for k := i; k < len(s); k++ {
		switch s[k] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return -1
}

// splitTop splits s at the commas outside brackets.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for k := 0; k < len(s); k++ {
		switch s[k] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:k])
				start = k + 1
			}
		}
	}
	return append(out, s[start:])
}

// topColon is the index of the first ':' of p outside brackets, or -1.
func topColon(p string) int {
	depth := 0
	for k := 0; k < len(p); k++ {
		switch p[k] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ':':
			if depth == 0 {
				return k
			}
		}
	}
	return -1
}
