// Why: --cpu names reach clang, and a bad name must be one clear error, not
// a failed compile later.
// What: TestTargetCPUParse and TestResolveCPU, the --cpu option and ResolveCPU.
// How: reads -target-cpu from clang -### output (targetCPU) and resolves
// "native", known and unknown names with the host clang.

package toolchain

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestTargetCPUParse(t *testing.T) {
	out := " \"C:\\\\LLVM\\\\bin\\\\clang.exe\" \"-cc1\" \"-triple\" \"x86_64-pc-windows-msvc19.33.0\" \"-target-cpu\" \"znver5\" \"-target-feature\" \"+avx\"\n"
	if got, ok := targetCPU(out); !ok || got != "znver5" {
		t.Errorf("targetCPU = %q, %v; want znver5", got, ok)
	}
	for _, bad := range []string{"", `"-target-cpu" ""`, `"-target-cpu"`} {
		if got, ok := targetCPU(bad); ok {
			t.Errorf("targetCPU(%q) = %q, want no answer", bad, got)
		}
	}
	if got, want := nativeArgs("x86_64-unknown-linux-gnu", "/w/e.ll", "/w/e.o"),
		[]string{"--target=x86_64-unknown-linux-gnu", "-march=native", "-###", "-x", "ir", "-c", "/w/e.ll", "-o", "/w/e.o"}; !reflect.DeepEqual(got, want) {
		t.Errorf("nativeArgs x86_64 = %q", got)
	}
	if got := nativeArgs("aarch64-unknown-linux-gnu", "/w/e.ll", "/w/e.o"); got[1] != "-mcpu=native" {
		t.Errorf("nativeArgs aarch64 = %q, want -mcpu=native", got)
	}
}

// TestResolveCPU: "native" becomes a concrete name, a known name is
// returned as is, and an unknown one is a started *ToolError carrying
// clang's message, so the driver reports one E0001.
func TestResolveCPU(t *testing.T) {
	tl := realTools(t)
	ctx := context.Background()
	const triple = "x86_64-pc-windows-msvc"
	got, err := tl.ResolveCPU(ctx, triple, CPUNative)
	if err != nil || got == "" || got == CPUNative {
		t.Errorf("native = %q, %v; want a CPU name", got, err)
	}
	if got, err := tl.ResolveCPU(ctx, triple, "x86-64-v3"); err != nil || got != "x86-64-v3" {
		t.Errorf("x86-64-v3 = %q, %v", got, err)
	}
	if got, err := tl.ResolveCPU(ctx, triple, ""); err != nil || got != "" {
		t.Errorf(`"" = %q, %v`, got, err)
	}
	_, err = tl.ResolveCPU(ctx, triple, "bogus")
	var te *ToolError
	if !errors.As(err, &te) || !te.Started() || !strings.Contains(te.Stderr, "bogus") {
		t.Errorf("bogus: %v, want a started ToolError naming the CPU", err)
	} else if !strings.Contains(te.Command, "<work>") || strings.Contains(te.Command, "mk-cpu-") {
		t.Errorf("bogus: command %q names the temporary directory", te.Command)
	}
}
