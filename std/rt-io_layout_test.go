// Why: aarch64-linux layouts are checked without a toolchain, so a struct
// size that differs from x86_64 must be caught by reading the source.
// What: TestEpollEventBuffer, the epoll event buffer's size check.
// How: reads sys/linux/poll_epoll.mk and checks that maxevents is derived
// from the buffer and that the buffer holds 64 or more events on both targets.

package std

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestEpollEventBuffer: EpollReap's maxevents is derived from the
// buffer and the target's struct epoll_event size (12 bytes packed on
// x86_64, 16 on aarch64), and the buffer holds that many events on both
// (64 x 16 bytes did not fit the old 768-byte buffer on aarch64).
func TestEpollEventBuffer(t *testing.T) {
	b, err := os.ReadFile("sys/linux/poll_epoll.mk")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	m := regexp.MustCompile(`const EventWords: int = (\d+)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("poll_epoll.mk: no EventWords constant")
	}
	words, _ := strconv.Atoi(m[1])
	if !strings.Contains(src, "var events: [EventWords]u64 = [0; EventWords]") {
		t.Error("EpollReap's buffer is not [EventWords]u64")
	}
	if !strings.Contains(src, "return EventWords * 8 / EvBytes()") {
		t.Error("EvMax is not derived from EventWords and EvBytes")
	}
	for _, call := range []string{"epoll_pwait2, (*pp).epfd, evp, max,", "epoll_pwait, (*pp).epfd, evp, max,"} {
		if !strings.Contains(src, call) {
			t.Errorf("maxevents of %q is not EvMax()", call)
		}
	}
	if regexp.MustCompile(`epoll_pwait2?, \(\*pp\)\.epfd, evp, \d+`).MatchString(src) {
		t.Error("an epoll wait passes a literal maxevents")
	}
	for arch, ev := range map[string]int{"x86_64": 12, "aarch64": 16} {
		max := words * 8 / ev
		if max*ev > words*8 || max < 64 {
			t.Errorf("%s: %d events of %d bytes in %d bytes", arch, max, ev, words*8)
		}
	}
}
