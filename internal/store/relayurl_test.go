package store

import (
	"strings"
	"testing"
)

// The whole point of the guard: a cleartext relay on the public internet
// carries save files readable by anything on the path, because nothing else
// in OpenSave encrypts them.
func TestValidateRelayURL_RefusesCleartextAcrossTheInternet(t *testing.T) {
	for _, raw := range []string{
		"ws://relay.example.com",
		"ws://relay.example.com:8386",
		"ws://opensave-relay.onrender.com",
		"ws://153.75.249.131:8386", // a real public IP
		"ws://8.8.8.8",
		"ws://[2606:4700:4700::1111]:8386", // public IPv6
	} {
		if err := ValidateRelayURL(raw); err == nil {
			t.Errorf("%s was accepted; it sends saves in the clear over the internet", raw)
		}
	}
}

// Refusing these outright would break people who followed the installer's own
// advice that an unencrypted relay is fine on a network you trust.
func TestValidateRelayURL_AllowsCleartextOnATrustedNetwork(t *testing.T) {
	for _, raw := range []string{
		"ws://localhost:8386",
		"ws://127.0.0.1:8386",
		"ws://[::1]:8386",
		"ws://192.168.1.50:8386", // home LAN
		"ws://10.0.0.5:8386",
		"ws://172.16.4.9:8386", // RFC1918, the range easiest to get wrong
		"ws://100.101.102.103", // CGNAT: Tailscale and friends
		"ws://169.254.10.10",   // link-local
		// The wildcard addresses. A relay bound to every interface reports
		// itself as these, which is how the e2e suite addresses its own local
		// relay — and dialling one means this machine.
		"ws://[::]:8386",
		"ws://0.0.0.0:8386",
		"ws://nas.local:8386", // mDNS
		"ws://relay.internal", //
		"ws://box.home.arpa",  //
	} {
		if err := ValidateRelayURL(raw); err != nil {
			t.Errorf("%s was refused, but that network is the trust boundary: %v", raw, err)
		}
	}
}

// 172.16/12 is the range people mis-implement: 172.15 and 172.32 are public.
func TestValidateRelayURL_PrivateRangeEdges(t *testing.T) {
	public := []string{"ws://172.15.0.1", "ws://172.32.0.1", "ws://100.63.0.1", "ws://100.128.0.1"}
	private := []string{"ws://172.16.0.1", "ws://172.31.255.254", "ws://100.64.0.1", "ws://100.127.255.254"}
	for _, raw := range public {
		if err := ValidateRelayURL(raw); err == nil {
			t.Errorf("%s is a PUBLIC address but was treated as trusted", raw)
		}
	}
	for _, raw := range private {
		if err := ValidateRelayURL(raw); err != nil {
			t.Errorf("%s is inside a private range but was refused: %v", raw, err)
		}
	}
}

func TestValidateRelayURL_AcceptsEncrypted(t *testing.T) {
	for _, raw := range []string{
		"wss://relay.example.com",
		"wss://open-save-backup-relay.onrender.com",
		"wss://relay.example.com:443/",
		"  wss://relay.example.com  ", // whitespace is trimmed, not a rejection
	} {
		if err := ValidateRelayURL(raw); err != nil {
			t.Errorf("%s is encrypted and should be accepted: %v", raw, err)
		}
	}
}

func TestValidateRelayURL_RejectsNonsense(t *testing.T) {
	for _, raw := range []string{
		"relay.example.com",         // no scheme: easy to type, silently wrong
		"https://relay.example.com", // right idea, wrong protocol
		"http://relay.example.com",
		"wss://", // scheme but no host
	} {
		if err := ValidateRelayURL(raw); err == nil {
			t.Errorf("%q was accepted as a relay address", raw)
		}
	}
}

// Empty means "not configured", which the rest of the code already handles by
// simply not connecting. Turning that into a validation error would make the
// settings screen unsaveable for anyone not using internet sync.
func TestValidateRelayURL_EmptyIsNotAnError(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		if err := ValidateRelayURL(raw); err != nil {
			t.Errorf("empty relay URL should be allowed, got %v", err)
		}
	}
}

// The refusal has to say what to do about it, or it is just a wall.
func TestValidateRelayURL_MessageSaysHowToFixIt(t *testing.T) {
	err := ValidateRelayURL("ws://relay.example.com")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"wss://", "--domain"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal never mentions %q; it reads:\n%s", want, err)
		}
	}
}
