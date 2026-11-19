// Why: a warm build kept an old size when only an error's payload struct
// changed (204 for a 304-byte union).
// What: TestIncrSizeOfErrorUnionReads.
// How: a SizeOf of an error union taken as a narrow integer const argument
// (sizeNow) must read the layout of each error's payload; the test checks
// the body records those shapes.

package check

import (
	"strings"
	"testing"

	"mk/internal/types"
)

func TestIncrSizeOfErrorUnionReads(t *testing.T) {
	t.Parallel()
	const lib = "export struct Big {\n\ta: [200]u8\n}\n\nexport error Bad(b: Big)\n"
	const src = "import lib\n\nG8[const N: u8](): u8 {\n\treturn N\n}\n\nF(): u8 {\n\treturn G8[SizeOf[lib.Bad!u8]()]()\n}\n"
	reads := bodyReads(t, lib, src)
	var all []string
	shape, sig := false, false
	for _, r := range reads {
		all = append(all, r.Kind.String()+" "+r.Key.String()+" "+r.Name)
		shape = shape || r.Kind == types.ReadShape && r.Key.String() == "lib.Big"
		sig = sig || r.Kind == types.ReadSignature && r.Key.String() == "lib.Bad"
	}
	if !shape || !sig {
		t.Errorf("want ReadShape lib.Big and ReadSignature lib.Bad in\n%s", strings.Join(all, "\n"))
	}
}
