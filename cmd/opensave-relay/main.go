// Command opensave-relay runs the standalone WAN relay server.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/opensave/opensave/relay"
)

func main() {
	cfg := relay.Config{
		Port:               envInt("PORT", 8386),
		MaxPerRoom:         envInt("MAX_PER_ROOM", 20),
		GoogleClientSecret: os.Getenv("GOOGLE_DRIVE_CLIENT_SECRET"),
	}

	srv := relay.New(cfg)
	addr, err := srv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay failed to start: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OpenSave WAN Relay listening on %s (health: http://%s/health)\n", addr, addr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nshutting down...")
	srv.Stop()
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
