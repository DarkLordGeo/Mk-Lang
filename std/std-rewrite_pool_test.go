// Why: mem.Pool (std/mem/pool.mk) hands out generational handles, and a
// stale or foreign handle must never reach a value.
// What: a differential test against a Go model over seeded random sequences
// of Add, Get, GetMut, Free, Len and Handle.Eq on two pools.
// How: every handle ever returned stays in play, so stale and foreign ones
// are asked about as often as live ones; built natively, dev and release.

package std

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// poolProgram replays the sequence of args[1] ops from seed args[2].
const poolProgram = `import std.mem

struct H {
    h: mem.Handle
    q: bool // from pool q
}

Next(s: mut u64): u64 {
    *s = *s *% 6364136223846793005 +% 1442695040888963407
    return *s >> 33
}

export Main(sys: &System): WriteError|OutOfMemory|ParseError!void {
    var out := sys.Stdout()
    n := int(try sys.args[1].ParseInt())
    var s := Trunc[u64](try sys.args[2].ParseInt())
    var p := mem.Pool[int].New(sys.arena)
    var q := mem.Pool[int].New(sys.arena)
    var hs := List[H].New(sys.arena)
    var v := 0
    for _ in 0..n {
        op := Next(mut s) % 10
        if op <= 3 {
            v += 1
            if op == 3 {
                h := try q.Add(v)
                try hs.Push(H(h: h, q: true))
            } else {
                h := try p.Add(v)
                try hs.Push(H(h: h, q: false))
            }
            try Print(mut out, "add {v}\n", (v))
            continue
        }
        if hs.Len() == 0 {
            try out.Str("empty\n")
            continue
        }
        k := Trunc[int](Next(mut s) % Trunc[u64](hs.Len()))
        onq := Next(mut s) % 4 == 0
        h := hs[k].h
        if op <= 5 {
            var got := -1
            if onq {
                if x := q.Get(h) {
                    got = *x
                }
            } else if x := p.Get(h) {
                got = *x
            }
            try Print(mut out, "get {k} {got}\n", (k, got))
        } else if op == 6 {
            var got := -1
            if onq {
                if x := q.GetMut(h) {
                    *x += 1000
                    got = *x
                }
            } else if x := p.GetMut(h) {
                *x += 1000
                got = *x
            }
            try Print(mut out, "mut {k} {got}\n", (k, got))
        } else if op <= 8 {
            var ok := false
            if onq {
                ok = q.Free(h)
            } else {
                ok = p.Free(h)
            }
            try Print(mut out, "free {k} {ok}\n", (k, ok))
        } else {
            j := Trunc[int](Next(mut s) % Trunc[u64](hs.Len()))
            eq := h.Eq(&hs[j].h)
            lp := p.Len()
            lq := q.Len()
            try Print(mut out, "len {lp} {lq} eq {j} {eq}\n", (lp, lq, j, eq))
        }
    }
}
`

// poolModel is the reference: Go maps from handle number to value, per
// pool. A handle is live in its own pool until freed; it never names
// anything in the other pool, nor after its Free.
func poolModel(n int, seed uint64) []string {
	s := seed
	next := func() uint64 {
		s = s*6364136223846793005 + 1442695040888963407
		return s >> 33
	}
	type hd struct {
		q    bool
		live bool
		v    int
	}
	var hs []*hd
	live := [2]int{}
	v := 0
	var out []string
	for i := 0; i < n; i++ {
		op := next() % 10
		if op <= 3 {
			v++
			h := &hd{q: op == 3, live: true, v: v}
			hs = append(hs, h)
			if h.q {
				live[1]++
			} else {
				live[0]++
			}
			out = append(out, fmt.Sprintf("add %d", v))
			continue
		}
		if len(hs) == 0 {
			out = append(out, "empty")
			continue
		}
		k := int(next() % uint64(len(hs)))
		onq := next()%4 == 0
		h := hs[k]
		reach := h.live && h.q == onq
		switch {
		case op <= 5:
			got := -1
			if reach {
				got = h.v
			}
			out = append(out, fmt.Sprintf("get %d %d", k, got))
		case op == 6:
			got := -1
			if reach {
				h.v += 1000
				got = h.v
			}
			out = append(out, fmt.Sprintf("mut %d %d", k, got))
		case op <= 8:
			if reach {
				h.live = false
				if h.q {
					live[1]--
				} else {
					live[0]--
				}
			}
			out = append(out, fmt.Sprintf("free %d %v", k, reach))
		default:
			j := int(next() % uint64(len(hs)))
			out = append(out, fmt.Sprintf("len %d %d eq %d %v", live[0], live[1], j, j == k))
		}
	}
	return out
}

// TestSTDRW_PoolMatchesModel runs mem.Pool against the Go model.
//
// It checks pool.mk's invariants P1 (only a live handle of this pool
// reaches a value; never a panic, never another value) and P3 (Len); P2's
// retirement at 2^32 - 1 reuses is argued in pool.mk, not run.
func TestSTDRW_PoolMatchesModel(t *testing.T) {
	t.Parallel()
	for _, prof := range [][]string{nil, {"--release"}} {
		exe := buildNative(t, nil, poolProgram, prof...)
		for seed := uint64(1); seed <= 40; seed++ {
			n := 50 + int(seed)*37
			r := runNative(t, exe, "", strconv.Itoa(n), strconv.FormatUint(seed*104729, 10))
			if r.code != 0 {
				t.Fatalf("%v seed %d: exit %d\n%s", prof, seed, r.code, r.stderr)
			}
			got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
			want := poolModel(n, seed*104729)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				for i := range want {
					if i >= len(got) || got[i] != want[i] {
						t.Fatalf("%v seed %d: first difference at op %d: got %q, want %q", prof, seed, i, poolAt(got, i), want[i])
					}
				}
				t.Fatalf("%v seed %d: %d lines, want %d", prof, seed, len(got), len(want))
			}
		}
	}
}

func poolAt(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "<missing>"
}
