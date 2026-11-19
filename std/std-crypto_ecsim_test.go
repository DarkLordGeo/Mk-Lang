// Why: EcSecret.Generate is the one curve operation a cancel can interrupt,
// and a repeated call after a cancel must deliver what one run delivers.
// What: the Generate cancel tests under std.sim and on the native Io, and a
// P-384 Generate under a Sim.
// How: for 6 seeds, Generate in a cancelled scope is Canceled, then Generate
// on the same Sim gives the scalar a fresh Sim of that seed gives.
// Its tests are cancel-safety tests; tools/gate/safety.go lists each.

package std

import (
	"strings"
	"testing"
)

const cr2EcSimProbe = "import std.sim\nimport std.atomic\n" + cryptoPrelude + `

// Draw is the secret's scalar as a word sum, or 0 on any error.
Draw(io: Io): u64 {
    s := crypto.EcSecret.Generate(io, crypto.Curve.P256) else {
        return 0
    }
    d := crypto.FxEcScalar(&s)
    var x: u64 = 0
    for i in 0..32 {
        x = (x << 8) | (x >> 56) ^ u64(d[i])
    }
    return x
}

// GenTask runs in a Sim task: Generate in a cancelled scope, then again.
// out gets the second scalar's word over 4, or -1 when the first was not
// Canceled or the second failed.
GenTask(io: Io, out: &atomic.AtomicInt) {
    var cut := false
    scope c := io.Scope() {
        c.Cancel()
        _ = crypto.EcSecret.Generate(io, crypto.Curve.P256) else e {
            cut = match e {
                Canceled { true }
                _ { false }
            }
        }
    }
    x := Draw(io)
    out.Store(if cut && x != 0 { Cast[i64](x >> 2) } else { -1 })
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var first := "none"
    scope sc := sys.io.Scope() {
        sc.Cancel()
        _ = crypto.EcSecret.Generate(sys.io, crypto.Curve.P256) else e {
            first = match e {
                Canceled { "canceled" }
                _ { "other" }
            }
        }
    }
    var sim_ok := 0
    var seeds: [6]u64 = [1, 2, 3, 42, 99, 0xC2_4]
    for k in 0..6 {
        var s := try sim.Sim.New(&ar, seeds[k])
        io := s.Io()
        out := atomic.AtomicInt.New(0)
        scope r := io.Scope() {
            r.go GenTask(io, &out)
        }
        s.Free()
        var f := try sim.Sim.New(&ar, seeds[k])
        b := Draw(f.Io())
        f.Free()
        if b != 0 && out.Load() == Cast[i64](b >> 2) {
            sim_ok += 1
        }
    }
    var s1 := try sim.Sim.New(&ar, 42)
    defer s1.Free()
    io1 := s1.Io()
    var p384 := "none"
    _ = crypto.EcSecret.Generate(io1, crypto.Curve.P384) else e {
        p384 = match e {
            crypto.BadKey { "bad-key" }
            _ { "other" }
        }
    }
    a := Draw(io1)
    var s2 := try sim.Sim.New(&ar, 42)
    defer s2.Free()
    b := Draw(s2.Io())
    same := a == b && a != 0
    try sys.Print("{first} {sim_ok} {p384} {same}\n", (first: first, sim_ok: sim_ok, p384: p384, same: same))
}
`

// TestSTDCRYPTO_EcGenerateRepeatable: Generate is repeatable after a
// cancel, under std.sim (each await point interrupted and the call made
// again delivers what an uninterrupted run delivers) and on the native Io.
// Generate's await points are its Random calls, one per draw; a second
// draw happens with probability about 2^-32, and Random under a Sim makes
// no scheduling choice, so interrupting the first draw covers them all.
func TestSTDCRYPTO_EcGenerateRepeatable(t *testing.T) {
	t.Parallel()
	for _, v := range cr2EcVariants {
		dir, err := packageTempDir("mkcrypto2sim")
		if err != nil {
			t.Fatal(err)
		}
		exe := cr2BuildIn(t, dir, v, cr2EcSimProbe, map[string]string{"crypto/tests_crypto2ec.mk": cr2EcFixtureAll}, nil)
		r := runNative(t, exe, "")
		if r.code != 0 {
			t.Fatalf("%s: exit %d\n%s", v.name, r.code, r.stderr)
		}
		if got := strings.TrimSpace(r.stdout); got != "canceled 6 bad-key true" {
			t.Errorf("%s: %q, want \"canceled 6 bad-key true\"", v.name, got)
		}
	}
}
