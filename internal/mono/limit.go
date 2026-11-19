// Why: a recursive generic can instantiate without end; the build must stop
// with one clear error instead of running out of memory.
//
// What: limit, which reports E0708 (instantiation limit) once per build
// root and stops that root's collection.
//
// How: the limits are depth 64 along one instantiation chain and 100,000
// instances per build root (fixed bounds, not tuned by measurement). The error
// is anchored at the outermost concrete call of the chain, and its chain fact
// lists every instance from there down to the one refused.

package mono

import (
	"fmt"

	"mk/internal/diag"
	"mk/internal/types"
)

// limit reports the one E0708 of caller's build root (one per build
// root) and stops that root's collection after the current
// level; other roots go on. The offending request r of caller would make an
// instance past the limit of the given kind.
//
// The anchor is the outermost concrete call of the chain: walking up from
// the caller, the first instance of depth 0 (a non-generic function, since
// only generic instances deepen a chain) is the concrete function the
// runaway chain starts in, and its call into the chain is the anchor. The
// chain fact lists that function, then every instance down to the one
// refused, outermost first.
func (c *collector) limit(caller int, r request, kind string, lim int) {
	c.nodes[c.nodes[caller].root].stopped = true
	var path []int // caller up to the concrete function, innermost first
	for i := caller; i >= 0; i = c.nodes[i].parent {
		path = append(path, i)
		if c.nodes[i].inst.Depth == 0 {
			break
		}
	}
	// The anchor is the call in top that requested the next instance of the
	// path, or r itself when the caller is top.
	siteBody, siteSpan := c.nodes[caller].inst.Body, r.span
	if len(path) > 1 {
		child := c.nodes[path[len(path)-2]]
		siteBody, siteSpan = child.siteBody, child.siteSpan
	}
	chain := make([]string, 0, len(path)+1)
	for j := len(path) - 1; j >= 0; j-- {
		chain = append(chain, c.nodes[path[j]].inst.Name)
	}
	refused := nameOf(c.env, r.item, r.args)
	chain = append(chain, refused)
	var msg string
	if kind == "depth" {
		msg = fmt.Sprintf("instantiating %s goes past depth %d along one chain", c.shortName(r.item), lim)
	} else {
		msg = fmt.Sprintf("this build root needs more than %d instances", lim)
	}
	d := diag.New(diag.E0708, siteBody.Span(siteSpan), msg).
		Int("limit", int64(lim)).
		Text("kind", kind).
		Fact("chain", diag.TextList(chain...))
	c.diags = append(c.diags, d)
}

// shortName is an item's key, for messages.
func (c *collector) shortName(item types.ItemID) string {
	return c.env.Items().Get(item).Key.String()
}
