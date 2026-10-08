// Package kindutil provides shared utilities for creating and configuring
// Kind (Kubernetes in Docker) clusters in test environments.
package kindutil

import (
	"os"
	"strings"
)

// BaseKindConfig is the default kind cluster configuration with increased pod limits.
// The default maxPods (110) is too restrictive for integration tests running in parallel.
//
// etcd runs with --unsafe-no-fsync (same dual-version patch pattern as the
// cert SAN below; see etcdNoFsyncPatches): on hosts with slow fsync (Docker
// Desktop's VM disk, shared CI runners) etcd commit latency under parallel
// pod churn makes kube-controller-manager lose its leader-election lease,
// which kills the ServiceAccount controller mid-suite and surfaces as
// apiserver "TLS handshake timeout" / "serviceaccount not found" test
// failures. Durability is irrelevant for a throwaway test cluster — kind's
// own CI runs etcd the same way.
const BaseKindConfig = `kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: KubeletConfiguration
    maxPods: 500
kubeadmConfigPatchesJSON6902:
` + etcdNoFsyncPatches

// etcdNoFsyncPatches makes etcd skip fsync — appended to the
// kubeadmConfigPatchesJSON6902 list of both cluster configs (see the
// BaseKindConfig comment for the why). Dual kubeadm versions for the same
// reason as the cert-SAN patches above: kind applies the one matching the
// node's k8s version and skips the other. v1beta3 etcd extraArgs is a
// map[string]string; v1beta4 changed it to a []{name,value} list — hence
// the differing "value:" shapes. The patch adds the whole /etcd object:
// kind's generated kubeadm config has no /etcd/local node (verified —
// adding at /etcd/local/extraArgs fails with "doc is missing path"), and
// kubeadm defaults dataDir itself when unset.
const etcdNoFsyncPatches = `  - group: kubeadm.k8s.io
    version: v1beta3
    kind: ClusterConfiguration
    patch: |
      - op: add
        path: /etcd
        value:
          local:
            extraArgs:
              unsafe-no-fsync: "true"
  - group: kubeadm.k8s.io
    version: v1beta4
    kind: ClusterConfiguration
    patch: |
      - op: add
        path: /etcd
        value:
          local:
            extraArgs:
              - name: unsafe-no-fsync
                value: "true"
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
	host, err := remoteDockerHost(os.Getenv("DOCKER_HOST"))
	if err == nil && host != "" {
		return host
	}
	return "docker"
}

// GetNodePortHost returns the hostname for accessing NodePort services.
// In Docker-in-Docker, this is the dind hostname; otherwise localhost.
func GetNodePortHost() string {
	if IsDockerInDocker() {
		return GetDindHostname()
	}
	return "localhost"
}
