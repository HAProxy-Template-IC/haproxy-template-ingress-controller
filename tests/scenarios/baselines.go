// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"time"

	"golang.org/x/mod/semver"
	"sigs.k8s.io/yaml"
)

const requiredBaseline = "0.2.0-alpha.3"
const chartRegistryPath = "haproxy-haptic/haptic/charts/haptic"

var stableVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var targetVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

func UpgradeBaselines(tags []string, target string) ([]string, error) {
	if !targetVersion.MatchString(target) || !semver.IsValid("v"+target) {
		return nil, fmt.Errorf("version under test is not semver: %q", target)
	}
	stable := make([]string, 0, len(tags))
	for _, tag := range tags {
		if stableVersion.MatchString(tag) {
			stable = append(stable, tag)
		}
	}
	if len(stable) == 0 {
		return nil, errors.New("no published stable chart versions found")
	}
	if !slices.Contains(tags, requiredBaseline) {
		return nil, fmt.Errorf("missing required upgrade baseline %s", requiredBaseline)
	}
	slices.SortFunc(stable, func(a, b string) int { return semver.Compare("v"+a, "v"+b) })
	result := make([]string, 0, len(stable)+1)
	for _, tag := range stable {
		if semver.Compare("v"+tag, "v"+target) <= 0 {
			result = append(result, tag)
		}
	}
	return append(result, requiredBaseline), nil
}

func DiscoverUpgradeBaselines(ctx context.Context, client *http.Client, target string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	var auth struct {
		Token string `json:"token"`
	}
	if err := fetchRegistryJSON(ctx, client, "https://gitlab.com/jwt/auth?service=container_registry&scope=repository:"+chartRegistryPath+":pull", "", &auth); err != nil {
		return nil, err
	}
	if auth.Token == "" {
		return nil, errors.New("chart registry returned no pull token")
	}
	var listing struct {
		Tags []string `json:"tags"`
	}
	if err := fetchRegistryJSON(ctx, client, "https://registry.gitlab.com/v2/"+chartRegistryPath+"/tags/list?n=10000", auth.Token, &listing); err != nil {
		return nil, err
	}
	return UpgradeBaselines(listing.Tags, target)
}

func fetchRegistryJSON(ctx context.Context, client *http.Client, address, token string, value any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, http.NoBody)
	if err != nil {
		return err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("chart registry returned HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(value)
}

func CheckUpgradeMatrix(published []string, content []byte) error {
	var config struct {
		Upgrade struct {
			Parallel struct {
				Matrix []struct {
					Versions []string `json:"BASELINE_CHART_VERSION"`
				} `json:"matrix"`
			} `json:"parallel"`
		} `json:"test-chart-upgrade"`
	}
	if err := yaml.Unmarshal(content, &config); err != nil {
		return err
	}
	var matrix []string
	for _, entry := range config.Upgrade.Parallel.Matrix {
		matrix = append(matrix, entry.Versions...)
	}
	var missing, unknown []string
	for _, version := range published {
		if !slices.Contains(matrix, version) {
			missing = append(missing, version)
		}
	}
	for _, version := range matrix {
		if !slices.Contains(published, version) {
			unknown = append(unknown, version)
		}
	}
	if len(published) == 0 || len(missing) != 0 || len(unknown) != 0 {
		return fmt.Errorf("upgrade matrix differs from published baselines: missing=%v unknown=%v; update test-chart-upgrade.parallel.matrix", missing, unknown)
	}
	return nil
}
