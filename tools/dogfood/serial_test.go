// Why: serial input written at once can be lost before the guest's UART
// takes it, and a killed process leaves a profile cover cannot read.
// What: tests of the serial line over a socket with flow control, and of
// cover skipping an unreadable profile, with fakeQemu.
// How: the test binary stands in for QEMU (fakeQemu, selected by "-kernel"
// under DFH_FAKE=1); no test here needs QEMU.

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeQemu is the test binary as qemu-system-x86_64 with COM1 on the
// harness's socket. It insists on the chardev the harness passes, prints
// "booting", then, by DFH_QEMU_MODE:
//
//   - "" (echo): reads its input as a UART does, at most 16 bytes at a
//     time with a pause between, and answers each line "> line"; "exit N"
//     prints "bye" and exits with isa-debug-exit's (N << 1) | 1. End of
//     input (the harness closed its side) prints "eof" and exits 3.
//   - "tail": reads one line and answers "ok", waits while the next line
//     arrives, writes 256 KiB and "end", and exits 23 at once with that
//     line unread, so its socket closes with bytes in its receive buffer.
//   - "lost": writes "end" to its logfile only and exits 23, as when the
//     close reset the socket before the harness read the last bytes.
//
// Like QEMU's chardev logfile, it records every byte it sends.
func fakeQemu(args []string) int {
	var chardev, serial string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-chardev":
			chardev = args[i+1]
		case "-serial":
			serial = args[i+1]
		}
	}
	if serial != "chardev:com1" || !strings.HasPrefix(chardev, "socket,id=com1,") {
		fmt.Fprintf(os.Stderr, "fake qemu: -serial %q -chardev %q\n", serial, chardev)
		return 99
	}
	opts := map[string]string{}
	for _, kv := range strings.Split(chardev, ",") {
		k, v, _ := strings.Cut(kv, "=")
		opts[k] = v
	}
	conn, err := net.Dial("tcp", net.JoinHostPort(opts["host"], opts["port"]))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake qemu:", err)
		return 98
	}
	log, err := os.Create(opts["logfile"])
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake qemu:", err)
		return 97
	}
	defer log.Close()
	send := func(s string) {
		conn.Write([]byte(s))
		log.WriteString(s)
	}
	send("booting\n")
	switch os.Getenv("DFH_QEMU_MODE") {
	case "lost":
		log.WriteString("end\n")
		return 23
	case "tail":
		r := bufio.NewReader(conn)
		r.ReadString('\n')
		send("ok\n")
		time.Sleep(500 * time.Millisecond) // the next line arrives, unread
		send(strings.Repeat(strings.Repeat("x", 63)+"\n", 4096))
		send("end\n")
		return 23
	}
	u := &uartReader{r: conn}
	var pend []byte
	buf := make([]byte, 16)
	for {
		i := bytes.IndexByte(pend, '\n')
		if i < 0 {
			n, err := u.Read(buf)
			pend = append(pend, buf[:n]...)
			if err != nil {
				send("eof\n")
				return 3
			}
			continue
		}
		line := string(pend[:i])
		pend = pend[i+1:]
		if n, ok := strings.CutPrefix(line, "exit "); ok {
			code, _ := strconv.Atoi(n)
			send("bye\n")
			return code<<1 | 1
		}
		if line == "count" {
			// The bytes the line has delivered, as the kernel's stat
			// counts them: a line typed before this answer would show.
			time.Sleep(50 * time.Millisecond)
			conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
			n, _ := u.Read(buf)
			pend = append(pend, buf[:n]...)
			conn.SetReadDeadline(time.Time{})
			send(fmt.Sprintf("received %d\n", u.n))
			continue
		}
		send("> " + line + "\n")
	}
}

// uartReader reads at most 16 bytes at a time, with a pause before each
// read: a guest that drains a 16-byte FIFO now and then. n counts the
// bytes it has read.
type uartReader struct {
	r io.Reader
	n int
}

func (u *uartReader) Read(b []byte) (int, error) {
	time.Sleep(time.Millisecond)
	n, err := u.r.Read(b[:min(len(b), 16)])
	u.n += n
	return n, err
}

func qemuArgv(t *testing.T) []string {
	return []string{selfExe(t), "-kernel", "k.elf", "-display", "none", "-monitor", "none", "-no-reboot", "-device", "isa-debug-exit,iobase=0xf4,iosize=0x04", "-m", "128M"}
}

// shortPause makes the harness type the next line 2 ms after the guest's
// answer, so a test of 200 lines takes no 20 s.
func shortPause(t *testing.T) {
	p := serialPause
	serialPause = 2 * time.Millisecond
	t.Cleanup(func() { serialPause = p })
}

// TestSerialInputArrivesWhole: every byte of .in reaches a
// guest that reads slowly, a line at a time (a guest's count of the bytes
// the line delivered, taken mid-input, sees no line typed ahead of its
// answer), its output comes back byte for byte as the case's stdout, and
// the exit status is QEMU's.
func TestSerialInputArrivesWhole(t *testing.T) {
	fakeEnv(t)
	shortPause(t)
	dir := t.TempDir()
	var in, want strings.Builder
	want.WriteString("booting\n")
	for i := range 200 {
		l := fmt.Sprintf("line %03d %s", i, strings.Repeat("abcdefgh", 4))
		in.WriteString(l + "\n")
		want.WriteString("> " + l + "\n")
		if i == 99 {
			in.WriteString("count\n")
			fmt.Fprintf(&want, "received %d\n", in.Len())
		}
	}
	in.WriteString("exit 4\n")
	want.WriteString("bye\n")
	write(t, dir, map[string]string{"k.in": in.String()})
	r := runSerial(qemuArgv(t), dir, os.Environ(), filepath.Join(dir, "k.in"), 30*time.Second)
	if r.err != "" || r.timedOut || r.code != 9 {
		t.Fatalf("err %q, timed out %v, code %d (want 9); stderr %s", r.err, r.timedOut, r.code, r.stderr)
	}
	if string(r.stdout) != want.String() {
		t.Errorf("stdout: %d bytes, want %d\n%s", len(r.stdout), want.Len(), ends(r.stdout))
	}
	// No input: the line stays open, nothing is written, and the guest
	// sees no end of input until QEMU is stopped.
	r = runSerial(qemuArgv(t), dir, os.Environ(), "", 2*time.Second)
	if !r.timedOut || string(r.stdout) != "booting\n" {
		t.Errorf("no input: timed out %v, stdout %q, want a timeout after %q", r.timedOut, r.stdout, "booting\n")
	}
}

// TestSerialTailAtExit: a guest that exits with input it
// never read still has every byte it wrote compared.
func TestSerialTailAtExit(t *testing.T) {
	fakeEnv(t, "DFH_QEMU_MODE", "tail")
	shortPause(t)
	dir := t.TempDir()
	write(t, dir, map[string]string{"k.in": "first\nsecond, never read\nthird, never sent\n"})
	r := runSerial(qemuArgv(t), dir, os.Environ(), filepath.Join(dir, "k.in"), 30*time.Second)
	want := "booting\nok\n" + strings.Repeat(strings.Repeat("x", 63)+"\n", 4096) + "end\n"
	if r.err != "" || r.timedOut || r.code != 23 || string(r.stdout) != want {
		t.Errorf("err %q, timed out %v, code %d (want 23), stdout %d bytes (want %d)", r.err, r.timedOut, r.code, len(r.stdout), len(want))
	}
	// Bytes the socket lost at the close are taken from QEMU's logfile.
	t.Setenv("DFH_QEMU_MODE", "lost")
	r = runSerial(qemuArgv(t), dir, os.Environ(), filepath.Join(dir, "k.in"), 30*time.Second)
	if r.code != 23 || string(r.stdout) != "booting\nend\n" {
		t.Errorf("lost tail: code %d, stdout %q, want 23 and %q", r.code, r.stdout, "booting\nend\n")
	}
}

// TestRunBootsWithSerialSocket: run boots a freestanding
// dogfood's case through the socket and judges .out and .code; without
// QEMU the ELF check still stands in.
func TestRunBootsWithSerialSocket(t *testing.T) {
	tmp := t.TempDir()
	dogs := filepath.Join(tmp, "Dogfoods")
	files := dogfoodFiles("k")
	files["pkg.mk"] += "// x86_64-freestanding\n"
	files["cases/a.in"] = "hello\nexit 2\n"
	files["cases/a.out"] = "booting\n> hello\nbye\n"
	files["cases/a.code"] = "5\n"
	files["cases/b.in"] = "exit 0\n"
	files["cases/b.out"] = "booting\nbye\n"
	files["cases/b.code"] = "1\n"
	write(t, filepath.Join(dogs, "k"), files)
	fakeEnv(t, "MK_DOGFOOD_QEMU", selfExe(t))
	report := filepath.Join(tmp, "last.json")
	var out bytes.Buffer
	code := run([]string{"run", "-dir", dogs, "-work", filepath.Join(tmp, "work"), "-mk", selfExe(t), "-target", targetFree, "-profile", "dev", "-json", report}, &out, &out)
	if code != 0 {
		t.Fatalf("run: exit %d\n%s", code, out.String())
	}
	var rep Report
	b, err := os.ReadFile(report)
	if err == nil {
		err = json.Unmarshal(b, &rep)
	}
	if err != nil {
		t.Fatal(err)
	}
	cases := rep.Dogfoods[0].Runs[0].Cases
	if len(cases) != 2 || cases[0].Status != "pass" || !strings.Contains(cases[0].Command, "-serial chardev:com1") || strings.Contains(cases[0].Command, "stdio") {
		t.Errorf("cases %+v", cases[0])
	}
	// Without QEMU, a file that is not an ELF fails the check. The fake
	// build writes text: the test binary itself is a static x86-64 ELF with
	// a PT_NOTE on Linux, and passes.
	t.Setenv("MK_DOGFOOD_QEMU", "none")
	t.Setenv("DFH_BUILD_TEXT", "not an ELF\n")
	out.Reset()
	if code := run([]string{"run", "-dir", dogs, "-work", filepath.Join(tmp, "work"), "-mk", selfExe(t), "-target", targetFree, "-profile", "dev"}, &out, &out); code != 1 || !strings.Contains(out.String(), "QEMU not found, and the ELF check failed") {
		t.Errorf("no QEMU: exit %d, want 1 and the ELF check\n%s", code, out.String())
	}
}

// TestCoverSkipsUnreadableProfile: a case whose only
// profile is empty, and one with a profile cut in half beside a whole one,
// are noted (case, file, why) and cover goes on: the whole profile and a
// later case in name order still count.
func TestCoverSkipsUnreadableProfile(t *testing.T) {
	tmp := t.TempDir()
	dogs := filepath.Join(tmp, "Dogfoods")
	files := dogfoodFiles("p")
	files["src/main.mk"] = "import std.hash\n\nexport Main(sys: &System) {\n    _ = \"x\".Trim()\n    _ = hash.Crc32([])\n}\n"
	for _, c := range []string{"a_empty", "b_cut", "c_late"} {
		files["cases/"+c+".out"] = "ok\n"
	}
	write(t, filepath.Join(dogs, "p"), files)
	uses := `{"query":"uses","uses":[{"item":"std.core.str:Trim","sites":["src/main.mk:4:13"]},{"item":"std.hash.Crc32","sites":["src/main.mk:5:14"]}]}`
	fakeEnv(t, "DFH_USES", uses,
		"DFH_PROFMODE_a_empty", "empty", "DFH_CASEKEYS_a_empty", "mk.std.math.Max",
		"DFH_PROFMODE_b_cut", "cut", "DFH_CASEKEYS_b_cut", "mk.std.core.str:Trim",
		"DFH_CASEKEYS_c_late", "mk.std.hash.Crc32")
	cov := filepath.Join(tmp, "COVERAGE.md")
	report := filepath.Join(tmp, "cover.json")
	var out bytes.Buffer
	code := run([]string{"cover", "-dir", dogs, "-work", filepath.Join(tmp, "work"), "-mk", selfExe(t), "-target", hostTarget(), "-coverage", cov, "-json", report}, &out, &out)
	if code != 1 {
		t.Fatalf("cover: exit %d, want 1 (gaps)\n%s", code, out.String())
	}
	b, err := os.ReadFile(cov)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for line, want := range map[string]string{
		"str.Trim(self): str":         "covered (executed)",
		"hash.Crc32(data: []u8): u32": "covered (executed)",
	} {
		if got := coverRow(t, text, line); got != want {
			t.Errorf("%s: %q, want %q\n%s", line, got, want, out.String())
		}
	}
	var rep Report
	if b, err = os.ReadFile(report); err == nil {
		err = json.Unmarshal(b, &rep)
	}
	if err != nil {
		t.Fatal(err)
	}
	skipped := rep.Coverage.SkippedProfiles
	if len(skipped) != 2 {
		t.Fatalf("skipped profiles %q, want 2\n%s", skipped, out.String())
	}
	for i, want := range []string{"case a_empty: profile ", "case b_cut: profile "} {
		s := skipped[i]
		if !strings.Contains(s, want) || !strings.Contains(s, ".mkprof skipped: ") {
			t.Errorf("note %q does not name %q and its file", s, want)
		}
	}
	if !strings.Contains(skipped[0], "empty (0 bytes)") || !strings.Contains(skipped[1], "truncated") {
		t.Errorf("notes do not say why: %q", skipped)
	}
	if !strings.Contains(text, "- Profile skipped") || !strings.Contains(out.String(), "case b_cut: profile") {
		t.Errorf("COVERAGE.md or the log does not show the skipped profiles\n%s", out.String())
	}
}

// ends is the first and last bytes of a long output.
func ends(b []byte) string {
	if len(b) <= 200 {
		return string(b)
	}
	return string(b[:100]) + " ... " + string(b[len(b)-100:])
}
