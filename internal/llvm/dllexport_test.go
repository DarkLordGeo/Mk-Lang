// Why: a dynamic library built on Windows must mark its exports.
// What: TestExportDLLStorage, for FuncSig.DLLStorage.
// How: checks that the export prints dllexport after its visibility.

package llvm

import (
	"strings"
	"testing"
)

func TestExportDLLStorage(t *testing.T) {
	m := NewModule("lib", "x86_64-pc-windows-msvc", "")
	f := m.AddFunction("Twice", FuncSig{Linkage: External, DSOLocal: true, DLLStorage: DLLExport, Ret: m.I(32), Params: []Type{m.I(32)}})
	b := f.NewBlock("entry")
	v := f.Param(0)
	b.Ret(&v)
	m.Declare("Other", FuncSig{Linkage: External, DLLStorage: DLLExport, Ret: m.Void()})
	out := m.String()
	for _, want := range []string{"define dso_local dllexport i32 @Twice(i32 %", "declare dllexport void @Other()"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if (FuncSig{}).DLLStorage.String() != "" {
		t.Error("the zero DLLStorage prints")
	}
}
