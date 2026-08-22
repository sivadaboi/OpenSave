package cloud

import (
	"strings"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// The relay's OAuth proxy pairs whatever client id it is handed with the
// secret it holds — ours. That is the only pairing it can make, so it can
// only ever serve the built-in client.
//
// Routing used to turn on whether a secret was saved, not on whose client it
// was. Someone who entered their own client id and left the secret blank —
// reasonable, the flow is PKCE — had their id sent to the proxy and paired
// with our secret. Google rejected the mismatch, sign-in failed inside a token
// exchange with nothing naming the cause, and the workaround recommended for
// the verification limits was the thing that did not work.
func TestTokenCredsNeverProxiesSomebodyElsesClient(t *testing.T) {
	svc, st := newTestService(t)
	setCloudConfig(t, st, func(c *store.CloudConfig) {
		c.CustomClientIDs = map[string]string{"google_drive": "their-client.apps.googleusercontent.com"}
		c.CustomClientSecrets = map[string]string{"google_drive": "their-secret"}
	})

	id, secret, viaProxy, err := svc.tokenCreds("google_drive")
	if err != nil {
		t.Fatalf("tokenCreds: %v", err)
	}
	if viaProxy {
		t.Error("a user's own client was routed through the relay proxy, which holds a secret " +
			"belonging to a different client — the provider rejects that pair")
	}
	if id != "their-client.apps.googleusercontent.com" {
		t.Errorf("client id = %q, want theirs", id)
	}
	if secret != "their-secret" {
		t.Errorf("secret = %q, want theirs", secret)
	}

	// The half-configured case is the one that actually broke, and it must
	// not reach the proxy either. Without this the test name claims a
	// property it does not check: the old routing also went direct once a
	// secret was present, so the case above alone passes either way.
	setCloudConfig(t, st, func(c *store.CloudConfig) {
		c.CustomClientSecrets = map[string]string{}
	})
	if _, _, viaProxy, err := svc.tokenCreds("google_drive"); viaProxy {
		t.Error("a user's own client id with no secret was routed through the proxy")
	} else if err == nil {
		t.Error("a user's own client id with no secret was accepted silently")
	}
}

// A client id without its secret cannot work, and the failure has to be named
// where the user can act on it rather than surfacing as a token exchange error.
func TestTokenCredsRejectsGoogleClientWithoutSecret(t *testing.T) {
	svc, st := newTestService(t)
	setCloudConfig(t, st, func(c *store.CloudConfig) {
		c.CustomClientIDs = map[string]string{"google_drive": "their-client.apps.googleusercontent.com"}
	})

	_, _, viaProxy, err := svc.tokenCreds("google_drive")
	if err == nil {
		t.Fatal("accepted a custom Google client id with no secret")
	}
	if viaProxy {
		t.Error("fell back to the proxy, which would pair their id with our secret")
	}
	for _, want := range []string{"client secret", "Settings"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q so the user can act on it, got: %v", want, err)
		}
	}
}

// The built-in client still goes through the proxy — that is what keeps its
// secret out of the shipped binary.
func TestTokenCredsUsesProxyForTheBuiltInClient(t *testing.T) {
	svc, _ := newTestService(t)

	id, secret, viaProxy, err := svc.tokenCreds("google_drive")
	if err != nil {
		t.Fatalf("tokenCreds: %v", err)
	}
	if !viaProxy {
		t.Error("the built-in Google client must use the proxy; its secret is not in the app")
	}
	if id != defaultClientIDs["google_drive"] {
		t.Errorf("client id = %q, want the built-in one", id)
	}
	if secret != "" {
		t.Errorf("secret = %q, want empty — the relay supplies it", secret)
	}
}

// A secret saved with no client id cannot be the built-in client's, so it must
// not be paired with the built-in id.
func TestTokenCredsIgnoresAStraySecret(t *testing.T) {
	svc, st := newTestService(t)
	setCloudConfig(t, st, func(c *store.CloudConfig) {
		c.CustomClientSecrets = map[string]string{"google_drive": "a-secret-for-some-other-client"}
	})

	id, secret, viaProxy, err := svc.tokenCreds("google_drive")
	if err != nil {
		t.Fatalf("tokenCreds: %v", err)
	}
	if secret != "" {
		t.Errorf("a secret with no client id was carried through as %q", secret)
	}
	if !viaProxy || id != defaultClientIDs["google_drive"] {
		t.Error("should fall back to the built-in client and its proxy")
	}
}

// Providers other than Google have no proxy, and their own clients are used
// directly whether or not a secret came with them.
func TestTokenCredsNonGoogleNeverProxies(t *testing.T) {
	svc, st := newTestService(t)
	setCloudConfig(t, st, func(c *store.CloudConfig) {
		c.CustomClientIDs = map[string]string{"dropbox": "their-dropbox-app"}
	})

	id, _, viaProxy, err := svc.tokenCreds("dropbox")
	if err != nil {
		t.Fatalf("tokenCreds: %v", err)
	}
	if viaProxy {
		t.Error("dropbox has no proxy path")
	}
	if id != "their-dropbox-app" {
		t.Errorf("client id = %q, want theirs", id)
	}
}

// OneDrive ships no built-in client, so an unconfigured one has to say so
// rather than reporting an empty id downstream.
func TestTokenCredsReportsAMissingClient(t *testing.T) {
	svc, _ := newTestService(t)
	if _, _, _, err := svc.tokenCreds("onedrive"); err == nil {
		t.Fatal("expected an error for a provider with no built-in and no custom client")
	}
}
