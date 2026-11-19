// Why: every hash domain must be unique, and pgo declares its own domain
// apart from iface's table.
// What: TestPGODomainRegistered.
// How: compares pgo.HashDomain with iface.DomPGO; iface's
// TestDomainsUnique checks the table itself.

package driver

import (
	"testing"

	"mk/internal/iface"
	"mk/internal/pgo"
)

// TestPGODomainRegistered: the profile hash's domain, which pgo
// declares itself, is the one iface's domain table registers (iface's
// TestDomainsUnique checks the table).
func TestPGODomainRegistered(t *testing.T) {
	if pgo.HashDomain != iface.DomPGO {
		t.Fatalf("pgo.HashDomain = %q, iface.DomPGO = %q", pgo.HashDomain, iface.DomPGO)
	}
}
