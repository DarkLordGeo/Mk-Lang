// Why: static data aligned over a page needs the image's section alignment
// raised, and the link key must change only for such images.
// What: TestLinkKeyHoldsSectionAlign.
// How: an x86_64 Windows image with static data aligned over a page links
// with that section alignment (codegen.StaticAlign), and the executable's
// key holds it, while every other link's key is unchanged.

package driver

import (
	"strings"
	"testing"

	"mk/internal/codegen"
	"mk/internal/source"
	"mk/internal/toolchain"
	"mk/internal/types"
)

func TestLinkKeyHoldsSectionAlign(t *testing.T) {
	tools := &toolchain.Tools{}
	cfg := codegen.Config{Target: types.X86_64Windows}
	job := toolchain.LinkJob{Format: toolchain.COFF, StackReserve: stackReserve}
	plain := nativeOptions(tools, source.Hash{}, cfg, types.Dev, job)
	if strings.Contains(plain, "align=") {
		t.Errorf("a link with no SectionAlign names one: %s", plain)
	}
	job.SectionAlign = 65536
	aligned := nativeOptions(tools, source.Hash{}, cfg, types.Dev, job)
	if aligned != plain+" align=65536" {
		t.Errorf("options %q, want %q", aligned, plain+" align=65536")
	}
}
