// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deployer

import (
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

// clientKey is everything that gives a client authority over one pod: a
// changed URL or credential pair is a different agent, never a reused pool.
type clientKey struct {
	url      string
	username string
	password string
}

// agentClients keeps one keep-alive client per pod, so a deployment reuses the
// connection the previous one opened instead of paying a handshake per apply.
type agentClients struct {
	stateTimeout time.Duration
	applyTimeout time.Duration

	mu      sync.Mutex
	clients map[clientKey]*agentclient.Client
}

func newAgentClients(stateTimeout, applyTimeout time.Duration) *agentClients {
	return &agentClients{
		stateTimeout: stateTimeout,
		applyTimeout: applyTimeout,
		clients:      map[clientKey]*agentclient.Client{},
	}
}

// For returns the client for this endpoint, creating it on first use.
func (a *agentClients) For(endpoint *dataplane.Endpoint) (*agentclient.Client, error) {
	key := clientKey{url: endpoint.URL, username: endpoint.Username, password: endpoint.Password}

	a.mu.Lock()
	defer a.mu.Unlock()
	if client, ok := a.clients[key]; ok {
		return client, nil
	}
	client, err := agentclient.New(&agentclient.Config{
		BaseURL:            endpoint.URL,
		Username:           endpoint.Username,
		Password:           endpoint.Password,
		Timeout:            a.stateTimeout,
		PerPodApplyTimeout: a.applyTimeout,
	})
	if err != nil {
		return nil, err
	}
	a.clients[key] = client
	return client, nil
}

// Retain closes the clients of pods that are no longer part of the fleet.
func (a *agentClients) Retain(endpoints []dataplane.Endpoint) {
	keep := make(map[clientKey]struct{}, len(endpoints))
	for i := range endpoints {
		keep[clientKey{
			url:      endpoints[i].URL,
			username: endpoints[i].Username,
			password: endpoints[i].Password,
		}] = struct{}{}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for key, client := range a.clients {
		if _, wanted := keep[key]; !wanted {
			client.Close()
			delete(a.clients, key)
		}
	}
}

// Close releases every pooled connection; the next deployment reconnects.
func (a *agentClients) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, client := range a.clients {
		client.Close()
		delete(a.clients, key)
	}
}
