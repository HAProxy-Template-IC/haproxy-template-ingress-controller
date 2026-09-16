package e2ecluster

import (
	"fmt"
	"net"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
)

type TrafficEndpoint struct {
	Host      string
	HTTPPort  int
	HTTPSPort int
}

var trafficEndpoint atomic.Pointer[TrafficEndpoint]

// SetTrafficEndpoint selects the owned cluster's NodePorts before tests start.
func SetTrafficEndpoint(endpoint TrafficEndpoint) error {
	if net.ParseIP(endpoint.Host).To4() == nil || endpoint.HTTPPort < 1 || endpoint.HTTPPort > 65535 ||
		endpoint.HTTPSPort < 1 || endpoint.HTTPSPort > 65535 {
		return fmt.Errorf("invalid e2e traffic endpoint: %+v", endpoint)
	}
	trafficEndpoint.Store(&endpoint)
	return nil
}

// ResolveTrafficEndpoint refuses host-port fallback for an isolated cluster.
func ResolveTrafficEndpoint() (TrafficEndpoint, error) {
	if endpoint := trafficEndpoint.Load(); endpoint != nil {
		return *endpoint, nil
	}
	config, err := Load()
	if err != nil {
		return TrafficEndpoint{}, err
	}
	if !config.ExposeHostPorts {
		return TrafficEndpoint{}, fmt.Errorf("traffic endpoint for isolated cluster %q is not configured", config.ClusterName)
	}
	host := "127.0.0.1"
	if kindutil.IsDockerInDocker() {
		host = kindutil.GetDindHostname()
	}
	addresses, err := net.LookupIP(host)
	if err != nil {
		return TrafficEndpoint{}, fmt.Errorf("lookup %q: %w", host, err)
	}
	for _, address := range addresses {
		if ipv4 := address.To4(); ipv4 != nil {
			return TrafficEndpoint{Host: ipv4.String(), HTTPPort: 31080, HTTPSPort: 31443}, nil
		}
	}
	return TrafficEndpoint{}, fmt.Errorf("no IPv4 address for %q", host)
}
