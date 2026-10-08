// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

var admissionWarning = regexp.MustCompile(`(?m)^Warning:`)

func (s *Session) defaultsAdmission(ctx context.Context, timeout time.Duration) error {
	configs, err := readJSON[api.HAProxyTemplateConfigList](ctx, s, "get", "haproxytemplateconfigs", "-l", "app.kubernetes.io/instance="+s.Release)
	if err != nil {
		return err
	}
	libraries, err := readJSON[api.HAProxyTemplateLibraryList](ctx, s, "get", "haproxytemplatelibraries", "-l", "app.kubernetes.io/instance="+s.Release)
	if err != nil {
		return err
	}
	if err := checkDefaultLibraries(configs.Items, libraries.Items); err != nil {
		return err
	}
	name := configs.Items[0].Name
	if err := s.defaultsReplace(ctx, name, timeout); err != nil {
		return err
	}
	return poll(ctx, timeout, time.Second, "load gate accepted current generation", func(ctx context.Context) (testutil.PollResult, error) {
		config, err := readJSON[api.HAProxyTemplateConfig](ctx, s, "get", "haproxytemplateconfig", name)
		if err != nil {
			return testutil.PollPending, err
		}
		if err := currentValidation(&config); err != nil {
			return testutil.PollPending, err
		}
		return testutil.PollSucceeded, nil
	})
}

func checkDefaultLibraries(configs []api.HAProxyTemplateConfig, libraries []api.HAProxyTemplateLibrary) error {
	if len(configs) != 1 {
		return fmt.Errorf("expected one HAProxyTemplateConfig, found %d", len(configs))
	}
	config := &configs[0]
	count := len(config.Spec.ValidationTests)
	revisions := make(map[string]string, len(libraries))
	for i := range libraries {
		count += len(libraries[i].Spec.ValidationTests)
		revisions[libraries[i].Name] = libraries[i].Spec.Revision
	}
	if count == 0 {
		return errors.New("default config and libraries carry no validation tests")
	}
	if len(config.Spec.LibraryRefs) == 0 {
		return errors.New("default config references no libraries")
	}
	for _, ref := range config.Spec.LibraryRefs {
		revision, found := revisions[ref.Name]
		if !found || revision != ref.Revision {
			return fmt.Errorf("library %s revision=%q, expected %q", ref.Name, revision, ref.Revision)
		}
	}
	return nil
}

func (s *Session) defaultsReplace(ctx context.Context, name string, timeout time.Duration) error {
	attempts := 0
	return poll(ctx, timeout, time.Second, "config dry-run replace", func(ctx context.Context) (testutil.PollResult, error) {
		attempts++
		config, err := s.Kube(ctx, nil, "get", "haproxytemplateconfig", name, "-o", "json")
		if err != nil {
			return testutil.PollFailed, err
		}
		result, err := s.Kube(ctx, strings.NewReader(config.Stdout), "replace", "--dry-run=server", "-f", "-")
		if err != nil {
			if attempts < 5 && strings.Contains(result.Stdout+result.Stderr, "please apply your changes to the latest version") {
				return testutil.PollPending, err
			}
			return testutil.PollFailed, err
		}
		if admissionWarning.MatchString(result.Stdout + result.Stderr) {
			return testutil.PollFailed, fmt.Errorf("config admission emitted warnings: %s%s", result.Stdout, result.Stderr)
		}
		return testutil.PollSucceeded, nil
	})
}

func currentValidation(config *api.HAProxyTemplateConfig) error {
	for _, condition := range config.Status.Conditions {
		if condition.Type == "Validated" && condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == config.Generation {
			return nil
		}
	}
	return fmt.Errorf("%s is not Validated at generation %d", config.Name, config.Generation)
}
