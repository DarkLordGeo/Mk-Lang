// Why: Windows answers version queries through a compatibility shim unless
// the executable declares a current OS version.
// What: TestOsVersion10, the PE header of an x86_64-windows
// executable.
// How: builds a program and reads its PE optional header: OS version 10.0,
// subsystem version 6.0 (an executable declaring 10.0 there does not load).

package driver

import (
	"debug/pe"
	"path/filepath"
	"testing"

	"mk/internal/types"
)

func TestOsVersion10(t *testing.T) {
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			exe := buildProgram(t, filepath.Join(programs, "panics", "overflow.mk"), types.X86_64Windows, prof, t.TempDir())
			f, err := pe.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			h, ok := f.OptionalHeader.(*pe.OptionalHeader64)
			if !ok {
				t.Fatalf("optional header %T, want PE32+", f.OptionalHeader)
			}
			if h.MajorOperatingSystemVersion != 10 || h.MinorOperatingSystemVersion != 0 {
				t.Errorf("OS version %d.%d, want 10.0", h.MajorOperatingSystemVersion, h.MinorOperatingSystemVersion)
			}
			if h.MajorSubsystemVersion != 6 {
				t.Errorf("subsystem version %d.%d, want 6.x", h.MajorSubsystemVersion, h.MinorSubsystemVersion)
			}
		})
	}
}
