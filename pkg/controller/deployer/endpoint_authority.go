// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import "gitlab.com/haproxy-haptic/haptic/pkg/dataplane"

// endpointAuthority identifies every input that gives a deployer client and
// its cached observations authority over one HAProxy pod.
type endpointAuthority struct {
	url                  string
	username             string
	password             string
	podName              string
	podNamespace         string
	podUID               string
	podRuntimeID         string
	detectedMajorVersion int
	detectedMinorVersion int
	detectedFullVersion  string
}

func endpointAuthorityOf(endpoint *dataplane.Endpoint) endpointAuthority {
	return endpointAuthority{
		url:                  endpoint.URL,
		username:             endpoint.Username,
		password:             endpoint.Password,
		podName:              endpoint.PodName,
		podNamespace:         endpoint.PodNamespace,
		podUID:               endpoint.PodUID,
		podRuntimeID:         endpoint.PodRuntimeID,
		detectedMajorVersion: endpoint.DetectedMajorVersion,
		detectedMinorVersion: endpoint.DetectedMinorVersion,
		detectedFullVersion:  endpoint.DetectedFullVersion,
	}
}

func endpointAuthoritySet(endpoints []dataplane.Endpoint) map[endpointAuthority]struct{} {
	authorities := make(map[endpointAuthority]struct{}, len(endpoints))
	for i := range endpoints {
		authorities[endpointAuthorityOf(&endpoints[i])] = struct{}{}
	}
	return authorities
}

func equalEndpointAuthoritySets(left, right map[endpointAuthority]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for authority := range left {
		if _, ok := right[authority]; !ok {
			return false
		}
	}
	return true
}
