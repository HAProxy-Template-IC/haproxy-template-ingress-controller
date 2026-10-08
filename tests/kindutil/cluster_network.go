// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const networkCommand = "network"

const networkOwnerLabel = "haproxy-haptic.test-owner"
const dockerNetworkSetting = "KIND_EXPERIMENTAL_DOCKER_NETWORK"

func (c *Cluster) createNetwork(ctx context.Context) error {
	name := c.Environment[dockerNetworkSetting]
	if name == "" || name == "kind" {
		return nil
	}
	if !clusterNamePattern.MatchString(name) {
		return fmt.Errorf("invalid test network name %q", name)
	}
	existing, err := c.run(ctx, "docker", networkCommand, "ls", "--filter", "name=^"+name+"$", "--format", "{{.ID}}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(existing.Stdout) != "" {
		return fmt.Errorf("network %s already exists; choose a fresh name", name)
	}
	bridge, err := c.run(ctx, "docker", networkCommand, dockerInspect, "bridge", "--format", "{{json .Options}}")
	if err != nil {
		return err
	}
	var options map[string]string
	if err := json.Unmarshal([]byte(bridge.Stdout), &options); err != nil {
		return err
	}
	var subnet [7]byte
	if _, err := rand.Read(subnet[:]); err != nil {
		return err
	}
	ipv6 := fmt.Sprintf("fd%02x:%02x%02x:%02x%02x:%02x%02x::/64", subnet[0], subnet[1], subnet[2], subnet[3], subnet[4], subnet[5], subnet[6])
	args := []string{networkCommand, "create", "--driver=bridge", "--opt", "com.docker.network.bridge.enable_ip_masquerade=true", "--ipv6", "--subnet", ipv6, "--label", networkOwnerLabel + "=" + c.Owner}
	if mtu := options["com.docker.network.driver.mtu"]; mtu != "" {
		args = append(args, "--opt", "com.docker.network.driver.mtu="+mtu)
	}
	_, err = c.run(ctx, "docker", append(args, name)...)
	return err
}

func (c *Cluster) closeNetwork(ctx context.Context) error {
	name := c.Environment[dockerNetworkSetting]
	if name == "" || name == "kind" {
		return nil
	}
	owned, err := c.run(ctx, "docker", networkCommand, "ls", "--filter", "label="+networkOwnerLabel+"="+c.Owner, "--no-trunc", "--format", "{{.ID}}")
	if err != nil {
		return err
	}
	ids := strings.Fields(owned.Stdout)
	if len(ids) == 0 {
		return nil
	}
	inspection, err := c.run(ctx, "docker", append([]string{networkCommand, dockerInspect}, ids...)...)
	if err != nil {
		return err
	}
	var networks []struct {
		ID         string `json:"Id"`
		Name       string
		Labels     map[string]string
		Containers map[string]any
	}
	if err := json.Unmarshal([]byte(inspection.Stdout), &networks); err != nil {
		return err
	}
	if len(networks) != len(ids) {
		return errors.New("incomplete test network ownership response")
	}
	for i := range networks {
		network := &networks[i]
		if network.ID != ids[i] || network.Name != name || network.Labels[networkOwnerLabel] != c.Owner || len(network.Containers) != 0 {
			return fmt.Errorf("network %s is foreign or still in use; refusing deletion", ids[i])
		}
	}
	_, err = c.run(ctx, "docker", append([]string{networkCommand, "rm"}, ids...)...)
	return err
}
