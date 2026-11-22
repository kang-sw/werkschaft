// Package wsnetguard refuses outbound connections to address ranges a caller
// names, at dial time.
//
// Control plugs into net.Dialer.Control, which runs after name resolution and
// once for every address the dialer tries. A host name that resolves to a
// refused address, including a DNS answer that changes between two lookups,
// is therefore refused at the connection itself rather than at a check made
// before the dial.
//
// The refused-range policy is a parameter rather than part of the guard.
// RefuseLocal suits a fetch that may reach a host on the user's own network;
// RefuseLocalAndPrivate suits a stricter caller that must stay on the public
// internet. A stricter caller reuses Control with its own policy instead of
// keeping a second copy of the dial hook.
package wsnetguard

import (
	"fmt"
	"net/netip"
	"syscall"
)

// RefusedError reports a dial whose address the policy refused.
type RefusedError struct {
	Addr netip.Addr // the dialed address, without zone and unmapped
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("refused to connect to %s: the address is in a blocked range", e.Addr)
}

// Control returns a net.Dialer.Control hook that refuses a dial when refuse
// reports true for the dialed address. The address loses any zone and is
// unmapped from its IPv4-mapped IPv6 form before refuse sees it, so one
// policy covers both forms. An address that does not parse as an IP and
// port is refused, so the hook fails closed. A nil refuse selects
// RefuseLocal.
func Control(refuse func(netip.Addr) bool) func(network, address string, c syscall.RawConn) error {
	if refuse == nil {
		refuse = RefuseLocal
	}
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("refused to connect to %q: the address could not be checked", address)
		}
		addr := ap.Addr().WithZone("").Unmap()
		if refuse(addr) {
			return &RefusedError{Addr: addr}
		}
		return nil
	}
}

// RefuseLocal refuses loopback, link-local unicast and multicast,
// unspecified, and every multicast address, judged after unmapping. Private
// ranges stay allowed, so a host on the user's own network is reachable. An
// invalid address is refused.
func RefuseLocal(a netip.Addr) bool {
	a = a.WithZone("").Unmap()
	return !a.IsValid() ||
		a.IsLoopback() ||
		a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() ||
		a.IsUnspecified() ||
		a.IsMulticast()
}

// RefuseLocalAndPrivate refuses everything RefuseLocal refuses and the
// private ranges (10/8, 172.16/12, 192.168/16, fc00::/7), for a caller that
// must reach only public addresses.
func RefuseLocalAndPrivate(a netip.Addr) bool {
	return RefuseLocal(a) || a.WithZone("").Unmap().IsPrivate()
}
