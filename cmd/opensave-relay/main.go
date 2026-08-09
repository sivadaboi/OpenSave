// Command opensave-relay runs the standalone WAN relay server.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/opensave/opensave/internal/version"
	"github.com/opensave/opensave/relay"
)

// The relay took any arguments you gave it, ignored them, and started a
// server. Someone self-hosting typed `opensave-relay config set relay-url …`
// — reasonable, since that is roughly what the client command looks like —
// and got "listen tcp 0.0.0.0:10000: bind: address already in use", because
// the arguments went nowhere and the only thing left to do was start a second
// relay next to the one already running. The error described neither what
// they asked for nor what was wrong with it.
//
// Two different binaries with similar names is the underlying trap:
// opensave-relay is the server, opensave is the client, and relay-url is a
// CLIENT setting. Nothing can stop the two being confused, but the relay can
// at least recognise a client command and say where it belongs.
const usage = `opensave-relay — the OpenSave WAN relay server

It takes no commands. Everything it has is set by environment variable:

  PORT=8386                          port to listen on
  MAX_PER_ROOM=20                    most devices allowed in one room
  GOOGLE_DRIVE_CLIENT_SECRET=…       optional OAuth token proxy

  opensave-relay                     start it
  PORT=9000 opensave-relay           on another port

Health check: GET /health — reports room and client counts, nothing else.

Looking for relay-url? That is a setting on the machines whose saves you are
syncing, not on the relay. Run this on each of THOSE, using the opensave
client:

  opensave config set relay-url wss://relay.example.com
  opensave relay join <room-code>

The relay never joins a room and has nothing to sign in to — rooms exist
because clients ask for them. See docs/RELAY.md.`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			fmt.Println(usage)
			return
		case "-v", "--version", "version":
			fmt.Printf("opensave-relay %s\n", version.Version)
			return
		default:
			// Non-zero, and on stderr: a typo in a systemd unit or a compose
			// file should fail loudly rather than start a server that ignores
			// half its command line.
			fmt.Fprintf(os.Stderr, "opensave-relay: unexpected argument %q\n\n%s\n", os.Args[1], usage)
			os.Exit(2)
		}
	}

	cfg := relay.Config{
		Port:               envInt("PORT", 8386),
		MaxPerRoom:         envInt("MAX_PER_ROOM", 20),
		GoogleClientSecret: os.Getenv("GOOGLE_DRIVE_CLIENT_SECRET"),
	}

	srv := relay.New(cfg)
	addr, err := srv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay failed to start: %v\n", err)
		// "address already in use" is nearly always a relay already running,
		// and nearly always someone who does not realise it. Say so, rather
		// than leaving them to infer it from an errno.
		if isAddrInUse(err) {
			fmt.Fprintf(os.Stderr,
				"\nSomething is already listening on that port — most likely a relay you started earlier.\n"+
					"Check with: curl http://localhost:%d/health\n"+
					"Run on another port with: PORT=9000 opensave-relay\n", cfg.Port)
		}
		os.Exit(1)
	}
	fmt.Printf("OpenSave WAN Relay listening on %s (health: http://%s/health)\n", addr, addr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nshutting down...")
	srv.Stop()
}

// isAddrInUse reports whether a listen failure was a port clash. Matched on
// the message rather than syscall.EADDRINUSE so it reads the same on Windows,
// where the equivalent errno has a different name and value.
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsFold(msg, "address already in use") ||
		containsFold(msg, "only one usage of each socket address")
}

func containsFold(haystack, needle string) bool {
	h, n := []byte(haystack), []byte(needle)
	if len(n) > len(h) {
		return false
	}
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(n) <= len(h); i++ {
		ok := true
		for j := range n {
			if lower(h[i+j]) != lower(n[j]) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
