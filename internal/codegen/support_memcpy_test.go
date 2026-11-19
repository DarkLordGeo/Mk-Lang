// Why: the support pack's memcpy copies 65 to 256 bytes as 64-byte groups
// from each end, which breaks on overlapping ranges; every length must be right.
// What: TestSupportMemcpyEveryLengthAndOffset and its IR harness memcpyHarness.
// How: copies every length 0 to 300 at every source and destination offset
// 0 to 15 into guard bytes, and memcpy(p, p, n) (an aggregate onto itself).

package codegen

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"mk/internal/types"
)

// memcpyHarness is the IR, compiled at -O0, that runs the copies and
// counts the bytes that differ from the model: for each source offset os,
// destination offset od (0 to 15) and length n (0 to 300), dst is set to
// 0xEE, memcpy(dst + od, src + os, n) runs, and every byte of dst is
// compared with src[os + i - od] inside the copy and 0xEE outside it; then
// memcpy(src + o, src + o, n) for each o and n, after which src must equal
// its first copy src0. It writes four i64: the bad bytes and the copies
// of each part.
const memcpyHarness = `
@src = internal global [432 x i8] c"%s", align 16
@src0 = internal constant [432 x i8] c"%s", align 16
@dst = internal global [432 x i8] zeroinitializer, align 16
@res = internal global [4 x i64] zeroinitializer, align 16

declare ptr @memcpy(ptr, ptr, i64)

define void @"mk.std.task.Start"(ptr %%sp) noreturn nounwind {
entry:
  %%os = alloca i64, align 8
  %%od = alloca i64, align 8
  %%n = alloca i64, align 8
  %%i = alloca i64, align 8
  store i64 0, ptr %%os, align 8
  br label %%los
los:
  %%osv = load i64, ptr %%os, align 8
  %%osd = icmp eq i64 %%osv, 16
  br i1 %%osd, label %%self, label %%los.body
los.body:
  store i64 0, ptr %%od, align 8
  br label %%lod
lod:
  %%odv = load i64, ptr %%od, align 8
  %%odd = icmp eq i64 %%odv, 16
  br i1 %%odd, label %%los.next, label %%lod.body
lod.body:
  store i64 0, ptr %%n, align 8
  br label %%ln
ln:
  %%nv = load i64, ptr %%n, align 8
  %%nd = icmp eq i64 %%nv, 301
  br i1 %%nd, label %%lod.next, label %%ln.body
ln.body:
  store i64 0, ptr %%i, align 8
  br label %%fill
fill:
  %%fv = load i64, ptr %%i, align 8
  %%fd = icmp eq i64 %%fv, 432
  br i1 %%fd, label %%copy, label %%fill.body
fill.body:
  %%fp = getelementptr inbounds i8, ptr @dst, i64 %%fv
  store i8 -18, ptr %%fp, align 1
  %%fv1 = add i64 %%fv, 1
  store i64 %%fv1, ptr %%i, align 8
  br label %%fill
copy:
  %%dp = getelementptr inbounds i8, ptr @dst, i64 %%odv
  %%sq = getelementptr inbounds i8, ptr @src, i64 %%osv
  call ptr @memcpy(ptr %%dp, ptr %%sq, i64 %%nv)
  store i64 0, ptr %%i, align 8
  br label %%chk
chk:
  %%cv = load i64, ptr %%i, align 8
  %%cd = icmp eq i64 %%cv, 432
  br i1 %%cd, label %%ln.next, label %%chk.body
chk.body:
  %%ge = icmp uge i64 %%cv, %%odv
  %%cend = add i64 %%odv, %%nv
  %%lt = icmp ult i64 %%cv, %%cend
  %%in = and i1 %%ge, %%lt
  %%rel = sub i64 %%cv, %%odv
  %%si = add i64 %%osv, %%rel
  %%sic = select i1 %%in, i64 %%si, i64 0
  %%sp1 = getelementptr inbounds i8, ptr @src, i64 %%sic
  %%sb = load i8, ptr %%sp1, align 1
  %%want = select i1 %%in, i8 %%sb, i8 -18
  %%dq = getelementptr inbounds i8, ptr @dst, i64 %%cv
  %%db = load i8, ptr %%dq, align 1
  %%ne = icmp ne i8 %%db, %%want
  %%ne64 = zext i1 %%ne to i64
  %%b0 = load i64, ptr @res, align 8
  %%b1 = add i64 %%b0, %%ne64
  store i64 %%b1, ptr @res, align 8
  %%cv1 = add i64 %%cv, 1
  store i64 %%cv1, ptr %%i, align 8
  br label %%chk
ln.next:
  %%c0p = getelementptr inbounds i8, ptr @res, i64 8
  %%c0 = load i64, ptr %%c0p, align 8
  %%c1 = add i64 %%c0, 1
  store i64 %%c1, ptr %%c0p, align 8
  %%nv1 = add i64 %%nv, 1
  store i64 %%nv1, ptr %%n, align 8
  br label %%ln
lod.next:
  %%odv1 = add i64 %%odv, 1
  store i64 %%odv1, ptr %%od, align 8
  br label %%lod
los.next:
  %%osv1 = add i64 %%osv, 1
  store i64 %%osv1, ptr %%os, align 8
  br label %%los
self:
  store i64 0, ptr %%os, align 8
  br label %%so
so:
  %%sov = load i64, ptr %%os, align 8
  %%sod = icmp eq i64 %%sov, 16
  br i1 %%sod, label %%cmp, label %%so.body
so.body:
  store i64 0, ptr %%n, align 8
  br label %%sn
sn:
  %%snv = load i64, ptr %%n, align 8
  %%snd = icmp eq i64 %%snv, 301
  br i1 %%snd, label %%so.next, label %%sn.body
sn.body:
  %%pp = getelementptr inbounds i8, ptr @src, i64 %%sov
  call ptr @memcpy(ptr %%pp, ptr %%pp, i64 %%snv)
  %%s0p = getelementptr inbounds i8, ptr @res, i64 24
  %%s0 = load i64, ptr %%s0p, align 8
  %%s1 = add i64 %%s0, 1
  store i64 %%s1, ptr %%s0p, align 8
  %%snv1 = add i64 %%snv, 1
  store i64 %%snv1, ptr %%n, align 8
  br label %%sn
so.next:
  %%sov1 = add i64 %%sov, 1
  store i64 %%sov1, ptr %%os, align 8
  br label %%so
cmp:
  store i64 0, ptr %%i, align 8
  br label %%k
k:
  %%kv = load i64, ptr %%i, align 8
  %%kd = icmp eq i64 %%kv, 432
  br i1 %%kd, label %%done, label %%k.body
k.body:
  %%ka = getelementptr inbounds i8, ptr @src, i64 %%kv
  %%kb = getelementptr inbounds i8, ptr @src0, i64 %%kv
  %%xa = load i8, ptr %%ka, align 1
  %%xb = load i8, ptr %%kb, align 1
  %%kne = icmp ne i8 %%xa, %%xb
  %%kne64 = zext i1 %%kne to i64
  %%k0p = getelementptr inbounds i8, ptr @res, i64 16
  %%k0 = load i64, ptr %%k0p, align 8
  %%k1 = add i64 %%k0, %%kne64
  store i64 %%k1, ptr %%k0p, align 8
  %%kv1 = add i64 %%kv, 1
  store i64 %%kv1, ptr %%i, align 8
  br label %%k
done:
  call void @h_write(ptr @res, i64 32)
  call void @h_exit(i32 0)
  unreachable
}
`

// TestSupportMemcpyEveryLengthAndOffset runs memcpyHarness over the support
// pack as the driver compiles it (-O3; the --profile size pack at -Oz) with
// cmd/mk's frame pointers, which dev and release share, and without them.
func TestSupportMemcpyEveryLengthAndOffset(t *testing.T) {
	src := make([]byte, 432)
	rand.New(rand.NewSource(13)).Read(src)
	for i := range src {
		if src[i] == 0xEE { // a guard byte copied would hide a missed store
			src[i] = 0x11
		}
	}
	h := fmt.Sprintf(memcpyHarness, llBytes(src), llBytes(src))
	for _, target := range runTargets {
		for _, c := range []struct {
			name string
			cfg  Config
			opt  string
		}{
			{"frame-pointers", Config{Target: target, FramePointers: true}, "-O3"},
			{"no-frame-pointers", Config{Target: target}, "-O3"},
			{"size", Config{Target: target, FramePointers: true, Size: true}, "-Oz"},
		} {
			t.Run(target.Name+"/"+c.name, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				if c.cfg.Size && target.OS != types.OSWindows {
					// The pack's 128-bit division helpers call compiler-rt's
					// __ashlti3 at -Oz on x86_64-linux, which this CRT-free
					// harness link does not have (the driver's link does).
					t.Skip("the harness links no compiler-rt for the -Oz pack on " + target.Name)
				}
				var b bytes.Buffer
				if err := EmitSupport(&Unit{}, nil, c.cfg, &b); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(fnText(b.String(), symMemcpy), "le256") {
					t.Fatal("memcpy has no 129-to-256-byte class: the test no longer covers what it was written for")
				}
				dir := t.TempDir()
				osIR, kernel := harnessOS(target)
				sup := clangC(t, target, dir, "support", b.String(), c.opt)
				obj := clangC(t, target, dir, "harness", osIR+h, "-O0")
				exe := linkExe(t, target, dir, []string{sup, obj}, linkOpts{kernel: kernel})
				out, code := runExe(t, target, exe, "")
				if code != 0 || len(out) != 32 {
					t.Fatalf("exit status %d, %d bytes of output, want 0 and 32", code, len(out))
				}
				r := []byte(out)
				bad, copies := binary.LittleEndian.Uint64(r), binary.LittleEndian.Uint64(r[8:])
				selfBad, selfCopies := binary.LittleEndian.Uint64(r[16:]), binary.LittleEndian.Uint64(r[24:])
				if copies != 16*16*301 || selfCopies != 16*301 {
					t.Fatalf("%d copies and %d onto themselves, want %d and %d", copies, selfCopies, 16*16*301, 16*301)
				}
				if bad != 0 || selfBad != 0 {
					t.Errorf("%d bytes differ from the model over %d copies; %d after the copies onto themselves", bad, copies, selfBad)
				}
			})
		}
	}
}
