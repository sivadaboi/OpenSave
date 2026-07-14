// Package upnp forwards a TCP port on the local internet gateway via
// UPnP IGD — for self-hosters exposing a relay without manual router
// configuration (port of scripts/upnp-port-forward.js).
package upnp

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/huin/goupnp/dcps/internetgateway1"
	"github.com/huin/goupnp/dcps/internetgateway2"
)

const mappingDescription = "OpenSave Relay"

// portMapper is the subset of IGD service methods we need, satisfied by
// both WANIPConnection and WANPPPConnection clients across IGD v1/v2.
type portMapper interface {
	AddPortMapping(remoteHost string, externalPort uint16, protocol string, internalPort uint16, internalClient string, enabled bool, description string, leaseDuration uint32) error
	DeletePortMapping(remoteHost string, externalPort uint16, protocol string) error
	GetExternalIPAddress() (string, error)
}

// discoverClients finds every WAN connection service on the LAN, trying
// IGDv2 first, then v1, both IP and PPP variants.
func discoverClients(ctx context.Context) []portMapper {
	var clients []portMapper
	if v2ip, _, err := internetgateway2.NewWANIPConnection2ClientsCtx(ctx); err == nil {
		for _, c := range v2ip {
			clients = append(clients, c)
		}
	}
	if v1ip, _, err := internetgateway1.NewWANIPConnection1ClientsCtx(ctx); err == nil {
		for _, c := range v1ip {
			clients = append(clients, c)
		}
	}
	if v1ppp, _, err := internetgateway1.NewWANPPPConnection1ClientsCtx(ctx); err == nil {
		for _, c := range v1ppp {
			clients = append(clients, c)
		}
	}
	return clients
}

// localIPv4 finds this machine's outbound LAN address.
func localIPv4() (string, error) {
	conn, err := net.Dial("udp4", "192.0.2.1:9") // no traffic sent; just picks the route
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

// Forward maps external:port -> thisMachine:port (TCP) on the gateway.
// Returns the gateway's external IP when available.
func Forward(ctx context.Context, port int) (externalIP string, err error) {
	clients := discoverClients(ctx)
	if len(clients) == 0 {
		return "", fmt.Errorf("no UPnP-capable gateway found on this network")
	}
	localIP, err := localIPv4()
	if err != nil {
		return "", fmt.Errorf("resolve local address: %w", err)
	}

	var errs []string
	for _, client := range clients {
		if err := client.AddPortMapping("", uint16(port), "TCP", uint16(port), localIP, true, mappingDescription, 0); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		ip, _ := client.GetExternalIPAddress()
		return ip, nil
	}
	return "", fmt.Errorf("all gateways refused the mapping: %s", strings.Join(errs, "; "))
}

// Remove deletes the mapping created by Forward.
func Remove(ctx context.Context, port int) error {
	clients := discoverClients(ctx)
	if len(clients) == 0 {
		return fmt.Errorf("no UPnP-capable gateway found on this network")
	}
	var errs []string
	for _, client := range clients {
		if err := client.DeletePortMapping("", uint16(port), "TCP"); err == nil {
			return nil
		} else {
			errs = append(errs, err.Error())
		}
	}
	return fmt.Errorf("could not remove mapping: %s", strings.Join(errs, "; "))
}
