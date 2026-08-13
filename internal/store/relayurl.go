package store

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateRelayURL refuses a relay address that would carry saves across an
// untrusted network in the clear.
//
// Sync payloads are gzipped and base64'd inside JSON and nothing else — there
// is no application-layer encryption anywhere in OpenSave, so ws:// means the
// save file itself is readable by anything on the path. wss:// is the only
// thing standing between a save and the network it crosses, which makes
// "did you type ws or wss" a security decision rather than a preference.
//
// ws:// is still allowed where the network is the trust boundary: the same
// machine, a home LAN, or a private overlay like Tailscale. That is what the
// relay installer means when it says an unencrypted relay is "fine on a
// network you trust", and refusing it outright would break the people who
// took that advice.
func ValidateRelayURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil // "no relay configured" is not an error here
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("that does not look like a relay address: %w", err)
	}

	switch u.Scheme {
	case "wss", "ws":
		// Checked further below. Both still need a host.
	case "":
		return fmt.Errorf("a relay address needs to start with wss:// (or ws:// on a network you trust), and %q has no scheme", raw)
	default:
		return fmt.Errorf("a relay address uses wss:// (or ws:// on a network you trust), not %s://", u.Scheme)
	}

	// Before the scheme question: an address with no host is not one. Checking
	// this for wss:// too, because otherwise "wss://" alone validates here and
	// fails much later at the dial, where the message is far less clear.
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("that relay address has no host in it: %q", raw)
	}

	if u.Scheme == "wss" || isTrustedNetworkHost(host) {
		return nil
	}
	return fmt.Errorf(
		"ws://%s would send your saves across the internet unencrypted, because OpenSave "+
			"relies on the relay connection for that and adds no encryption of its own.\n"+
			"Use wss://%s instead. If you run this relay, give it a domain name and a "+
			"certificate — the installer does that for you with --domain.", host, host)
}

// isTrustedNetworkHost reports whether an unencrypted connection to this host
// stays somewhere the user can reasonably be said to control.
func isTrustedNetworkHost(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))

	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	// mDNS and the conventional private-suffix names never leave the LAN.
	if strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") ||
		strings.HasSuffix(h, ".home.arpa") {
		return true
	}

	ip := net.ParseIP(h)
	if ip == nil {
		// A public-looking name. We deliberately do not resolve it: the answer
		// could differ by the time the dial happens, and a DNS lookup inside a
		// validator turns a settings save into a network operation.
		return false
	}
	// 0.0.0.0 and :: are the wildcard addresses. A server that bound to every
	// interface reports itself this way, and dialling one means this machine —
	// so it belongs with loopback rather than with the public internet.
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
		return true
	}
	// Carrier-grade NAT, 100.64.0.0/10 — where Tailscale and similar overlays
	// live. Traffic there is already inside an encrypted tunnel.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}
