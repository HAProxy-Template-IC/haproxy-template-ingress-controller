//go:build e2e

package e2ecluster

import "testing"

func TestIsolatedTrafficNeverFallsBackToAnotherClustersHostPorts(t *testing.T) {
	setIsolationEnv(t, validIsolationEnv())
	t.Cleanup(func() { trafficEndpoint.Store(nil) })
	if _, err := ResolveTrafficEndpoint(); err == nil {
		t.Fatal("isolated traffic must fail until its endpoint is configured")
	}
	want := TrafficEndpoint{Host: "192.0.2.10", HTTPPort: 30080, HTTPSPort: 30443}
	if err := SetTrafficEndpoint(want); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveTrafficEndpoint()
	if err != nil || got != want {
		t.Fatalf("traffic endpoint = %+v, %v; want %+v", got, err, want)
	}
}

func TestDefaultTrafficUsesExistingHostPorts(t *testing.T) {
	unsetIsolationEnv(t)
	t.Setenv("DOCKER_HOST", "")
	got, err := ResolveTrafficEndpoint()
	want := TrafficEndpoint{Host: "127.0.0.1", HTTPPort: 31080, HTTPSPort: 31443}
	if err != nil || got != want {
		t.Fatalf("traffic endpoint = %+v, %v; want %+v", got, err, want)
	}
}

func TestInvalidTrafficEndpointCannotBeSelected(t *testing.T) {
	for _, endpoint := range []TrafficEndpoint{
		{Host: "not-an-address", HTTPPort: 30080, HTTPSPort: 30443},
		{Host: "192.0.2.10", HTTPSPort: 30443},
		{Host: "192.0.2.10", HTTPPort: 30080},
	} {
		if err := SetTrafficEndpoint(endpoint); err == nil {
			t.Fatalf("accepted incomplete traffic endpoint %+v", endpoint)
		}
	}
}
