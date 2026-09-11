package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
)

type sidecarConfig struct {
	testName string
	kind     string
	name     string
	content  string
	image    string
}

const vectorSidecar = "vector"

type sidecarManifest struct {
	Kind     string
	Metadata metav1.ObjectMeta
	Data     map[string]string
	pod      *corev1.PodSpec
}

func decodeSidecarManifest(data json.RawMessage) (sidecarManifest, error) {
	var header struct {
		Kind     string            `json:"kind"`
		Metadata metav1.ObjectMeta `json:"metadata"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return sidecarManifest{}, err
	}
	object := sidecarManifest{Kind: header.Kind, Metadata: header.Metadata}
	switch header.Kind {
	case "ConfigMap":
		var configMap corev1.ConfigMap
		if err := json.Unmarshal(data, &configMap); err != nil {
			return sidecarManifest{}, err
		}
		object.Data = configMap.Data
	case "Pod":
		var pod corev1.Pod
		if err := json.Unmarshal(data, &pod); err != nil {
			return sidecarManifest{}, err
		}
		object.pod = &pod.Spec
	case "Deployment", "StatefulSet", "DaemonSet", "Job":
		var workload struct {
			Spec struct {
				Template corev1.PodTemplateSpec `json:"template"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(data, &workload); err != nil {
			return sidecarManifest{}, err
		}
		object.pod = &workload.Spec.Template.Spec
	}
	return object, nil
}

func decodeSidecarManifests(manifests map[string]string) ([]sidecarManifest, error) {
	var objects []sidecarManifest
	for _, name := range slices.Sorted(maps.Keys(manifests)) {
		decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(manifests[name]), 4096)
		for {
			var data json.RawMessage
			if err := decoder.Decode(&data); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return nil, fmt.Errorf("reading rendered %s for sidecar validation: %w", name, err)
			}
			if len(data) == 0 {
				continue
			}
			object, err := decodeSidecarManifest(data)
			if err != nil {
				return nil, fmt.Errorf("reading rendered %s for sidecar validation: %w", name, err)
			}
			objects = append(objects, object)
		}
	}
	return objects, nil
}

func collectSidecarConfigs(manifests map[string]string, results *testrunner.TestResults) ([]sidecarConfig, error) {
	workloads := maps.Clone(manifests)
	maps.DeleteFunc(workloads, func(name, _ string) bool { return filepath.Base(name) == "NOTES.txt" })
	chartObjects, err := decodeSidecarManifests(workloads)
	if err != nil {
		return nil, err
	}
	var configs []sidecarConfig
	seen := map[sidecarConfig]bool{}
	for i := range results.TestResults {
		test := &results.TestResults[i]
		vector, err := collectVectorConfigs(chartObjects, test)
		if err != nil {
			return nil, fmt.Errorf("test %s: %w", test.TestName, err)
		}
		varnish, err := collectVarnishConfigs(test)
		if err != nil {
			return nil, fmt.Errorf("test %s: %w", test.TestName, err)
		}
		for _, config := range append(vector, varnish...) {
			key := config
			key.testName = ""
			if !seen[key] {
				seen[key] = true
				configs = append(configs, config)
			}
		}
	}
	return configs, nil
}

func collectVectorConfigs(objects []sidecarManifest, test *testrunner.TestResult) ([]sidecarConfig, error) {
	var configs []sidecarConfig
	for _, name := range slices.Sorted(maps.Keys(test.RenderedFiles)) {
		if filepath.Base(name) != "vector.yaml" {
			continue
		}
		images := map[string]bool{}
		for i := range objects {
			if spec := objects[i].pod; spec != nil {
				for j := range spec.Containers {
					container := &spec.Containers[j]
					if container.Name == vectorSidecar {
						images[container.Image] = true
					}
				}
			}
		}
		image, err := sidecarImage(vectorSidecar, images)
		if err != nil {
			return nil, err
		}
		configs = append(configs, sidecarConfig{
			testName: test.TestName, kind: vectorSidecar, name: "vector.yaml", content: test.RenderedFiles[name], image: image,
		})
	}
	return configs, nil
}

func collectVarnishConfigs(test *testrunner.TestResult) ([]sidecarConfig, error) {
	objects, err := decodeSidecarManifests(test.RenderedK8sResources)
	if err != nil {
		return nil, err
	}
	var configs []sidecarConfig
	for i := range objects {
		object := &objects[i]
		if object.Kind != "ConfigMap" {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(object.Data)) {
			if !strings.HasSuffix(name, ".vcl") {
				continue
			}
			if filepath.Base(name) != name {
				return nil, fmt.Errorf("ConfigMap %s has invalid VCL key %q; use a file name without directories", object.Metadata.Name, name)
			}
			image, err := sidecarImage("varnish", configMapImages(objects, object, "varnish"))
			if err != nil {
				return nil, fmt.Errorf("ConfigMap %s/%s: %w", object.Metadata.Namespace, object.Metadata.Name, err)
			}
			configs = append(configs, sidecarConfig{
				testName: test.TestName, kind: "varnish", name: name, content: object.Data[name], image: image,
			})
		}
	}
	return configs, nil
}

func configMapImages(objects []sidecarManifest, configMap *sidecarManifest, containerName string) map[string]bool {
	images := map[string]bool{}
	for i := range objects {
		object := &objects[i]
		spec := object.pod
		if spec == nil || object.Metadata.Namespace != configMap.Metadata.Namespace {
			continue
		}
		for j := range spec.Containers {
			container := &spec.Containers[j]
			if container.Name == containerName && mountsConfigMap(spec, container, configMap.Metadata.Name) {
				images[container.Image] = true
			}
		}
	}
	return images
}

func mountsConfigMap(spec *corev1.PodSpec, container *corev1.Container, name string) bool {
	for i := range spec.Volumes {
		volume := &spec.Volumes[i]
		if volume.ConfigMap == nil || volume.ConfigMap.Name != name {
			continue
		}
		for _, mount := range container.VolumeMounts {
			if mount.Name == volume.Name {
				return true
			}
		}
	}
	return false
}

func sidecarImage(kind string, images map[string]bool) (string, error) {
	if len(images) != 1 || images[""] {
		return "", fmt.Errorf("cannot select one rendered %s image; render a workload with an explicit image for this config", kind)
	}
	image := slices.Collect(maps.Keys(images))[0]
	env := "HAPTIC_" + strings.ToUpper(kind) + "_IMAGE"
	if override := os.Getenv(env); override != "" && override != image {
		return "", fmt.Errorf("%s=%s differs from rendered image %s; set the image in Helm values and unset %s", env, override, image, env)
	}
	return image, nil
}
