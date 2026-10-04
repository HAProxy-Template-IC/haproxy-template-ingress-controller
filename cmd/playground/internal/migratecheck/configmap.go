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

package migratecheck

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ConfigMapCoverage classifies the keys of a source controller's own
// ConfigMap, the fleet-wide settings that never appear on an Ingress.
type ConfigMapCoverage struct {
	Detect   ConfigMapDetect    `json:"detect"`
	Settings []ConfigMapSetting `json:"settings"`
}

// ConfigMapDetect attributes a pasted ConfigMap to the source: by exact name,
// or by carrying every one of Labels plus at least one declared key. The key
// requirement keeps sibling ConfigMaps that share the controller's labels
// (TCP/UDP service maps) out of the report.
type ConfigMapDetect struct {
	Names  []string          `json:"names,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// ConfigMapSetting is one row of the source's ConfigMap migration table: the
// keys it covers, the HAPTIC setting replacing them, and the verdict. Setting
// and Note are Markdown with links relative to the docs root.
type ConfigMapSetting struct {
	Keys    []ConfigMapKey `json:"keys"`
	Setting string         `json:"setting,omitempty"`
	Status  Status         `json:"status"`
	Note    string         `json:"note,omitempty"`
}

// ConfigMapKey is one source ConfigMap key. A Name ending in "*" matches every
// key with that prefix; an exact Name elsewhere in the table wins over it.
type ConfigMapKey struct {
	Name    string `json:"name"`
	Default string `json:"default,omitempty"`
}

// ConfigMap is one pasted ConfigMap, reduced to the fields detection and
// classification read.
type ConfigMap struct {
	Namespace string
	Name      string
	Labels    map[string]string
	Data      map[string]string
	// KeyProblems maps a data key to why its pasted value isn't a valid
	// ConfigMap value; Data still holds the value in string form.
	KeyProblems map[string]string
	// Problem is set when data itself can't be read; Data is then empty.
	Problem string
}

// ConfigMapFinding is one classified key of one ConfigMap.
type ConfigMapFinding struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Status  Status `json:"status"`
	Setting string `json:"setting,omitempty"`
	Note    string `json:"note,omitempty"`
	// Problem says why the pasted value isn't a valid ConfigMap value.
	Problem string `json:"problem,omitempty"`
}

// ConfigMapReport carries the findings for one ConfigMap under one source,
// sorted by key. Every data key is a finding: the whole ConfigMap belongs to
// the source controller.
type ConfigMapReport struct {
	Namespace string             `json:"namespace"`
	Name      string             `json:"name"`
	Findings  []ConfigMapFinding `json:"findings,omitempty"`
	// Problem says why data couldn't be read at all.
	Problem string `json:"problem,omitempty"`
}

const undeclaredConfigMapKeyNote = "Not in HAPTIC's ConfigMap table, so no HAPTIC setting replaces it. Check whether you rely on what it does."

const nonStringValueProblem = "The value isn't a string, so the API server rejects this ConfigMap. Quote it."

// ConfigMapFromUnstructured reduces a ConfigMap object to the fields Classify
// needs. Pasted YAML often leaves values unquoted (keep-alive: 75), which the
// API server would reject; such keys are still classified, with the value in
// string form and a KeyProblems entry, rather than dropped.
func ConfigMapFromUnstructured(u *unstructured.Unstructured) ConfigMap {
	cm := ConfigMap{
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
		Labels:    u.GetLabels(),
	}
	raw, ok := u.Object["data"]
	if !ok || raw == nil {
		return cm
	}
	data, ok := raw.(map[string]any)
	if !ok {
		cm.Problem = fmt.Sprintf("data is a %s, not a map of keys to string values, so no key can be checked.", jsonKind(raw))
		return cm
	}
	cm.Data = make(map[string]string, len(data))
	for key, value := range data {
		if str, isString := value.(string); isString {
			cm.Data[key] = str
			continue
		}
		cm.Data[key] = scalarString(value)
		if cm.KeyProblems == nil {
			cm.KeyProblems = map[string]string{}
		}
		cm.KeyProblems[key] = nonStringValueProblem
	}
	return cm
}

// scalarString renders a non-string YAML value the way it was written:
// 75, true, null, or JSON for a nested list or map.
func scalarString(value any) string {
	if value == nil {
		return "null"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func jsonKind(value any) string {
	switch value.(type) {
	case []any:
		return "list"
	case string:
		return "string"
	case bool:
		return "boolean"
	default:
		return "number"
	}
}

func (c *ConfigMapCoverage) lookup(key string) (*ConfigMapSetting, bool) {
	var prefixMatch *ConfigMapSetting
	for i := range c.Settings {
		for _, k := range c.Settings[i].Keys {
			if k.Name == key {
				return &c.Settings[i], true
			}
			if prefix, ok := strings.CutSuffix(k.Name, "*"); ok && prefixMatch == nil && strings.HasPrefix(key, prefix) {
				prefixMatch = &c.Settings[i]
			}
		}
	}
	return prefixMatch, prefixMatch != nil
}

func (c *ConfigMapCoverage) matches(cm *ConfigMap) bool {
	for _, name := range c.Detect.Names {
		if cm.Name == name {
			return true
		}
	}
	if len(c.Detect.Labels) == 0 {
		return false
	}
	for k, v := range c.Detect.Labels {
		if cm.Labels[k] != v {
			return false
		}
	}
	for key := range cm.Data {
		if _, ok := c.lookup(key); ok {
			return true
		}
	}
	return false
}

func (c *ConfigMapCoverage) classify(cm *ConfigMap) ConfigMapReport {
	report := ConfigMapReport{Namespace: cm.Namespace, Name: cm.Name, Problem: cm.Problem}
	keys := make([]string, 0, len(cm.Data))
	for key := range cm.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		finding := ConfigMapFinding{
			Key: key, Value: cm.Data[key], Status: StatusUnknown,
			Note: undeclaredConfigMapKeyNote, Problem: cm.KeyProblems[key],
		}
		if setting, ok := c.lookup(key); ok {
			finding.Status = setting.Status
			finding.Setting = setting.Setting
			finding.Note = setting.Note
		}
		report.Findings = append(report.Findings, finding)
	}
	return report
}

func validateConfigMapCoverage(source string, c *ConfigMapCoverage) error {
	if len(nonemptyStrings(c.Detect.Names)) != len(c.Detect.Names) {
		return fmt.Errorf("migration coverage source %q has an empty ConfigMap name", source)
	}
	if len(c.Detect.Names) == 0 && len(c.Detect.Labels) == 0 {
		return fmt.Errorf("migration coverage source %q has no ConfigMap detection rules", source)
	}
	if len(c.Settings) == 0 {
		return fmt.Errorf("migration coverage source %q has no ConfigMap settings", source)
	}
	seen := map[string]struct{}{}
	for i := range c.Settings {
		if err := validateConfigMapSetting(source, i, &c.Settings[i], seen); err != nil {
			return err
		}
	}
	return nil
}

func validateConfigMapSetting(source string, index int, setting *ConfigMapSetting, seen map[string]struct{}) error {
	if len(setting.Keys) == 0 {
		return fmt.Errorf("migration coverage source %q ConfigMap setting %d has no keys", source, index)
	}
	for _, key := range setting.Keys {
		if strings.TrimSpace(key.Name) == "" || strings.TrimSuffix(key.Name, "*") == "" {
			return fmt.Errorf("migration coverage source %q ConfigMap setting %d has an empty key", source, index)
		}
		if _, dup := seen[key.Name]; dup {
			return fmt.Errorf("migration coverage source %q declares ConfigMap key %q twice", source, key.Name)
		}
		seen[key.Name] = struct{}{}
	}
	if !setting.Status.valid() {
		return fmt.Errorf("migration coverage source %q ConfigMap key %q has invalid status %q", source, setting.Keys[0].Name, setting.Status)
	}
	if setting.Status != StatusSupported && strings.TrimSpace(setting.Note) == "" {
		return fmt.Errorf("migration coverage source %q ConfigMap key %q has no note", source, setting.Keys[0].Name)
	}
	return nil
}
