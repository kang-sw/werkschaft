package wsnetguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// TestGuardPolicies runs every address through both policies, directly and
// through the Control hook with the address in host:port form.
func TestGuardPolicies(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		local   bool // refused by RefuseLocal
		private bool // refused by RefuseLocalAndPrivate
	}{
		{"v4 documentation", "203.0.113.7", false, false},
		{"v6 documentation", "2001:db8::7", false, false},
		{"v4-mapped documentation", "::ffff:203.0.113.7", false, false},
		{"v4 loopback", "127.0.0.1", true, true},
		{"v4 loopback range", "127.10.20.30", true, true},
		{"v6 loopback", "::1", true, true},
		{"v4-mapped loopback", "::ffff:127.0.0.1", true, true},
		{"v4 link-local", "169.254.169.254", true, true},
		{"v6 link-local", "fe80::1", true, true},
		{"v6 zoned link-local", "fe80::1%eth0", true, true},
		{"v4 link-local multicast", "224.0.0.251", true, true},
		{"v4 multicast", "239.1.2.3", true, true},
		{"v6 link-local multicast", "ff02::1", true, true},
		{"v6 interface-local multicast", "ff01::1", true, true},
		{"v6 global multicast", "ff0e::1", true, true},
		{"v4 unspecified", "0.0.0.0", true, true},
		{"v6 unspecified", "::", true, true},
		{"v4-mapped unspecified", "::ffff:0.0.0.0", true, true},
		{"v4 private 10", "10.1.2.3", false, true},
		{"v4 private 172", "172.16.5.4", false, true},
		{"v4 private 192", "192.168.1.20", false, true},
		{"v4-mapped private", "::ffff:192.168.1.20", false, true},
		{"v6 unique local", "fd12:3456::1", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tc.addr)
			if got := RefuseLocal(addr); got != tc.local {
				t.Errorf("RefuseLocal(%s) = %v, want %v", addr, got, tc.local)
			}
			if got := RefuseLocalAndPrivate(addr); got != tc.private {
				t.Errorf("RefuseLocalAndPrivate(%s) = %v, want %v", addr, got, tc.private)
			}
			hostPort := netip.AddrPortFrom(addr, 443).String()
			for _, policy := range []struct {
				name   string
				refuse func(netip.Addr) bool
				want   bool
			}{{"local", RefuseLocal, tc.local}, {"private", RefuseLocalAndPrivate, tc.private}} {
				err := Control(policy.refuse)("tcp", hostPort, nil)
				if (err != nil) != policy.want {
					t.Errorf("Control(%s)(%q) = %v, want refused=%v", policy.name, hostPort, err, policy.want)
				}
				if err == nil {
					continue
				}
				var refused *RefusedError
				if !errors.As(err, &refused) {
					t.Fatalf("Control error %T, want *RefusedError", err)
				}
				// The reported address is the one the policy judged: no zone,
				// unmapped.
				want := addr.WithZone("").Unmap()
				if refused.Addr != want || !strings.Contains(err.Error(), want.String()) {
					t.Errorf("refused %v (%q), want %v named", refused.Addr, err, want)
				}
			}
		})
	}
	if !RefuseLocal(netip.Addr{}) || !RefuseLocalAndPrivate(netip.Addr{}) {
		t.Error("the zero address must be refused")
	}
}

// TestGuardControlFailsClosed refuses a dialed address that is not an IP and
// port, whatever the policy says.
func TestGuardControlFailsClosed(t *testing.T) {
	allow := func(netip.Addr) bool { return false }
	for _, address := range []string{"", "example.invalid:443", "203.0.113.7", "[2001:db8::7]", "not an address"} {
		if err := Control(allow)("tcp", address, nil); err == nil {
			t.Errorf("Control(%q) allowed an address it could not parse", address)
		}
	}
	if err := Control(allow)("tcp", "127.0.0.1:443", nil); err != nil {
		t.Errorf("a policy that allows everything refused loopback: %v", err)
	}
	if err := Control(nil)("tcp", "127.0.0.1:443", nil); err == nil {
		t.Error("a nil policy must select RefuseLocal")
	}
}

// TestGuardDialer wires Control into a real net.Dialer: a loopback listener
// is refused under RefuseLocal and reached under a policy allowing it.
func TestGuardDialer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	refusing := &net.Dialer{Control: Control(RefuseLocal)}
	_, err = refusing.DialContext(ctx, "tcp", ln.Addr().String())
	var refused *RefusedError
	if !errors.As(err, &refused) || refused.Addr.String() != "127.0.0.1" {
		t.Fatalf("loopback dial error = %v, want a refusal naming 127.0.0.1", err)
	}

	allowing := &net.Dialer{Control: Control(func(a netip.Addr) bool { return !a.IsLoopback() && RefuseLocal(a) })}
	conn, err := allowing.DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("loopback dial under a policy allowing it: %v", err)
	}
	conn.Close()
}
