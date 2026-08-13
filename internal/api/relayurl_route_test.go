package api

import (
	"net/http"
	"strings"
	"testing"
)

// A cleartext relay pointed at the public internet must not be storable: the
// save file itself would cross the wire readable, because nothing else in
// OpenSave encrypts it.
func TestSettingsRefusesCleartextRelayOnThePublicInternet(t *testing.T) {
	ts := startTestServer(t)

	resp, body := ts.do(t, http.MethodPost, "/api/settings",
		map[string]any{"relayUrl": "ws://relay.example.com:8386"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — a cleartext public relay was accepted", resp.StatusCode)
	}
	if msg := string(body["error"]); !strings.Contains(msg, "wss://") {
		t.Errorf("the refusal should say what to use instead, got: %s", msg)
	}

	// And it must not have been written on the way to being refused.
	_, after := ts.do(t, http.MethodGet, "/api/settings", nil)
	if got := string(after["relayUrl"]); strings.Contains(got, "relay.example.com") {
		t.Errorf("the refused relay was stored anyway: %s", got)
	}
}

func TestSettingsAcceptsEncryptedAndLocalRelays(t *testing.T) {
	for _, url := range []string{
		"wss://relay.example.com",
		"ws://192.168.1.50:8386", // LAN: the network is the trust boundary
		"ws://localhost:8386",
	} {
		t.Run(url, func(t *testing.T) {
			ts := startTestServer(t)
			resp, _ := ts.do(t, http.MethodPost, "/api/settings", map[string]any{"relayUrl": url})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d for %s, want 200", resp.StatusCode, url)
			}
		})
	}
}

// The settings screen submits every field together. If the relay check ran on
// every save rather than on a change, anyone already holding a ws:// relay —
// stored by a build that allowed it — would find their device name, backup
// folder and everything else unsaveable, with an error naming the relay.
// Editing an unrelated field has to keep working.
func TestStoredCleartextRelayDoesNotBlockUnrelatedSettings(t *testing.T) {
	ts := startTestServer(t)

	// Plant the value the way an older build would have left it, bypassing
	// the route so the check under test is the only thing in play.
	settings, err := ts.daemon.Store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.RelayURL = "ws://legacy-relay.example.com:8386"
	if err := ts.daemon.Store.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}

	resp, body := ts.do(t, http.MethodPost, "/api/settings", map[string]any{"deviceName": "Renamed-PC"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: an unrelated edit was blocked by a grandfathered relay", resp.StatusCode)
	}
	if string(body["deviceName"]) != `"Renamed-PC"` {
		t.Errorf("deviceName = %s", body["deviceName"])
	}

	// But moving it to another bad address is still refused.
	resp, _ = ts.do(t, http.MethodPost, "/api/settings",
		map[string]any{"relayUrl": "ws://another-public-host.example.com"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 — a new cleartext relay slipped through", resp.StatusCode)
	}

	// And moving it to a safe one works, so nobody is stuck.
	resp, _ = ts.do(t, http.MethodPost, "/api/settings",
		map[string]any{"relayUrl": "wss://legacy-relay.example.com"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d: could not correct a grandfathered relay", resp.StatusCode)
	}
}
