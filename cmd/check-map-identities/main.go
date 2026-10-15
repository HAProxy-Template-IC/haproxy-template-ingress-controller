// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

// Command check-map-identities verifies benchmark snapshots against ADR-0031.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	haproxyv1alpha1 "github.com/haproxy-haptic/HAPTIC/pkg/apis/haproxytemplate/v1alpha1"
	"github.com/haproxy-haptic/HAPTIC/pkg/compression"
	"github.com/haproxy-haptic/HAPTIC/pkg/k8s/configpublisher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type inventory struct {
	Valid    map[string]string `json:"valid"`
	Rejected map[string]string `json:"rejected"`
}

func verifyMap(cfg *haproxyv1alpha1.HAProxyCfg, file *haproxyv1alpha1.HAProxyMapFile) error {
	owner := metav1.GetControllerOf(file)
	if owner == nil || owner.APIVersion != "haproxy-haptic.org/v1alpha1" || owner.Kind != "HAProxyCfg" ||
		owner.Name != cfg.Name || owner.UID != cfg.UID || cfg.UID == "" || file.Namespace != cfg.Namespace || file.DeletionTimestamp != nil {
		return errors.New("map is not owned by the current HAProxyCfg")
	}
	if cfg.Status.AuxiliaryFiles == nil {
		return errors.New("HAProxyCfg has no committed auxiliary files")
	}
	setID := cfg.Status.AuxiliaryFiles.SetID
	if !configpublisher.UsesContentIdentity(setID) {
		if file.Annotations[configpublisher.AuxiliarySetIDAnnotationKey] != setID {
			return errors.New("map belongs to a different auxiliary set")
		}
		return nil
	}
	content := file.Spec.Entries
	if file.Spec.Compressed {
		decoded, err := compression.Decompress(content)
		if err != nil {
			return fmt.Errorf("decode map content: %w", err)
		}
		content = decoded
	}
	checksum := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
	if checksum != file.Spec.Checksum {
		return errors.New("map content differs from its checksum")
	}
	suffix := configpublisher.AuxiliaryContentSuffix("HAProxyMapFile", file.Spec.Path, checksum, false)
	if file.Spec.Path == "" || (!strings.HasSuffix(file.Name, suffix) && !strings.Contains(file.Name, suffix+"-")) {
		return errors.New("map content identity differs from its committed name")
	}
	return nil
}

func checkFiles(cfgPath, mapsPath string) (*inventory, error) {
	var cfg haproxyv1alpha1.HAProxyCfg
	if err := readJSON(cfgPath, &cfg); err != nil {
		return nil, err
	}
	var files haproxyv1alpha1.HAProxyMapFileList
	if err := readJSON(mapsPath, &files); err != nil {
		return nil, err
	}
	report := &inventory{Valid: make(map[string]string), Rejected: make(map[string]string)}
	if cfg.Status.AuxiliaryFiles == nil {
		return report, nil
	}
	references := make(map[string]bool)
	for _, ref := range cfg.Status.AuxiliaryFiles.MapFiles {
		namespace := ref.Namespace
		if namespace == "" {
			namespace = cfg.Namespace
		}
		references[namespace+"/"+ref.Name] = true
	}
	for i := range files.Items {
		file := &files.Items[i]
		key := file.Namespace + "/" + file.Name
		if !references[key] {
			continue
		}
		if err := verifyMap(&cfg, file); err != nil {
			report.Rejected[key] = err.Error()
		} else {
			report.Valid[key] = string(file.UID)
		}
	}
	return report, nil
}

func readJSON(path string, target any) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open map evidence directory: %w", err)
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		return fmt.Errorf("read map identity evidence: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode map identity evidence: %w", err)
	}
	return nil
}

func run() error {
	if len(os.Args) != 3 {
		return errors.New("usage: check-map-identities <haproxycfg.json> <mapfiles.json>")
	}
	report, err := checkFiles(os.Args[1], os.Args[2])
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return fmt.Errorf("write map identity evidence: %w", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
