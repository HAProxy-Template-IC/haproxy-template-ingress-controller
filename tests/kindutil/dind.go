// Package kindutil provides shared utilities for creating and configuring
// Kind (Kubernetes in Docker) clusters in test environments.
package kindutil

import (
	"os"
	"strings"
)

// DindKindConfig is the kind cluster configuration for Docker-in-Docker environments.
// It binds the API server to 0.0.0.0 and adds "docker" as a certificate SAN.
const DindKindConfig = `kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  apiServerAddress: "0.0.0.0"
kubeadmConfigPatchesJSON6902:
  - group: kubeadm.k8s.io
    version: v1beta3
    kind: ClusterConfiguration
    patch: |
      - op: add
        path: /apiServer/certSANs/-
        value: docker
`

// IsDockerInDocker returns true if running in a Docker-in-Docker environment.
// This is detected by checking if DOCKER_HOST is set to a tcp:// URL,
// which is typical for GitLab CI dind services.
func IsDockerInDocker() bool {
	dockerHost := os.Getenv("DOCKER_HOST")
	return strings.HasPrefix(dockerHost, "tcp://")
}

// GetDindHostname extracts the hostname from DOCKER_HOST.
// For "tcp://docker:2376" it returns "docker".
func GetDindHostname() string {
	dockerHost := os.Getenv("DOCKER_HOST")
	// Remove tcp:// prefix
	if strings.HasPrefix(dockerHost, "tcp://") {
		hostPort := strings.TrimPrefix(dockerHost, "tcp://")
		// Remove port suffix
		if idx := strings.LastIndex(hostPort, ":"); idx != -1 {
			return hostPort[:idx]
		}
		return hostPort
	}
	return "docker" // fallback
}

// GetNodePortHost returns the hostname for accessing NodePort services.
// In Docker-in-Docker, this is the dind hostname; otherwise localhost.
func GetNodePortHost() string {
	if IsDockerInDocker() {
		return GetDindHostname()
	}
	return "localhost"
}

// PatchKubeconfigForDind replaces localhost/0.0.0.0 with the dind hostname
// in the kubeconfig server URL.
func PatchKubeconfigForDind(kubeconfig string) string {
	hostname := GetDindHostname()
	// Replace 0.0.0.0 (used when apiServerAddress is set to 0.0.0.0)
	kubeconfig = strings.ReplaceAll(kubeconfig, "https://0.0.0.0:", "https://"+hostname+":")
	// Replace 127.0.0.1 (default)
	kubeconfig = strings.ReplaceAll(kubeconfig, "https://127.0.0.1:", "https://"+hostname+":")
	// Replace localhost
	kubeconfig = strings.ReplaceAll(kubeconfig, "https://localhost:", "https://"+hostname+":")
	return kubeconfig
}
