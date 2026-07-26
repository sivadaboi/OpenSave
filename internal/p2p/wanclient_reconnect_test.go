package p2p

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeepWarmURL(t *testing.T) {
	cases := map[string]string{
		"wss://opensave-relay.onrender.com":  "https://opensave-relay.onrender.com/health",
		"wss://opensave-relay.onrender.com/": "https://opensave-relay.onrender.com/health",
		"ws://127.0.0.1:8386":                "http://127.0.0.1:8386/health",
		"https://relay.example.com":          "https://relay.example.com/health",
	}
	for in, want := range cases {
		if got := keepWarmURL(in); got != want {
			t.Errorf("keepWarmURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The relay host sleeps an instance that stops receiving HTTP requests, and
// WebSocket frames don't reset that timer — so a connected client has to poke
// /health itself or the relay disappears underneath it every ~15 minutes.
func TestKeepWarmPingsHealthEndpoint(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			hits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	old := wanKeepWarmInterval
	wanKeepWarmInterval = 10 * time.Millisecond
	defer func() { wanKeepWarmInterval = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := &WanClient{}
	go w.keepWarm(ctx, srv.URL)

	deadline := time.After(3 * time.Second)
	for hits.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("keepWarm made %d /health requests, want at least 3", hits.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Cancelling the connection context must stop the pings: otherwise every
	// reconnect would leak another warmer goroutine for the process lifetime.
	cancel()
	time.Sleep(50 * time.Millisecond)
	settled := hits.Load()
	time.Sleep(100 * time.Millisecond)
	if grew := hits.Load() - settled; grew > 1 {
		t.Errorf("keepWarm kept pinging after ctx cancel: %d more requests", grew)
	}
}

func TestReconnectDelayBacksOffAndCaps(t *testing.T) {
	// Jitter makes each call random within [delay/2, delay), so assert on
	// bounds rather than exact values.
	for _, tc := range []struct {
		failures         int
		minWant, maxWant time.Duration
	}{
		{0, 2500 * time.Millisecond, 5 * time.Second},
		{1, 5 * time.Second, 10 * time.Second},
		{2, 10 * time.Second, 20 * time.Second},
		{3, 20 * time.Second, 40 * time.Second},
		{4, 30 * time.Second, 60 * time.Second},  // capped
		{20, 30 * time.Second, 60 * time.Second}, // still capped, no overflow
	} {
		for i := 0; i < 50; i++ {
			got := reconnectDelay(tc.failures)
			if got < tc.minWant || got >= tc.maxWant {
				t.Fatalf("reconnectDelay(%d) = %v, want within [%v, %v)",
					tc.failures, got, tc.minWant, tc.maxWant)
			}
		}
	}
}

// Every client in every room drops at the same instant when the relay cycles.
// Without jitter they would all retry in lockstep and stampede the instance
// while it is still cold.
func TestReconnectDelayIsJittered(t *testing.T) {
	seen := map[time.Duration]bool{}
	for i := 0; i < 100; i++ {
		seen[reconnectDelay(3)] = true
	}
	if len(seen) < 10 {
		t.Errorf("reconnectDelay produced only %d distinct values over 100 calls — jitter looks broken", len(seen))
	}
}
