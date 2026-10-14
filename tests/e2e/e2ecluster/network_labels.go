// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package e2ecluster

import (
	"encoding/json"
	"fmt"
	"os"
)

const networkLabelsEnv = "HAPTIC_E2E_NETWORK_LABELS"

func loadNetworkLabels(isolated bool) (map[string]string, error) {
	value, set := os.LookupEnv(networkLabelsEnv)
	if !set {
		return map[string]string{}, nil
	}
	if !isolated {
		return nil, fmt.Errorf("%s requires a complete e2e isolation tuple", networkLabelsEnv)
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(value), &labels); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object of string labels: %w", networkLabelsEnv, err)
	}
	if labels == nil {
		return nil, fmt.Errorf("%s must be a JSON object of string labels", networkLabelsEnv)
	}
	return labels, nil
}
