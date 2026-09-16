//go:build e2e

package e2e

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/e2ecluster"
)

func configureTrafficEndpoint(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
	if e2eCluster.ExposeHostPorts {
		return ctx, nil
	}
	client, err := cfg.NewClient()
	if err != nil {
		return ctx, err
	}
	clientset, err := newClientsetForE2E(client.RESTConfig())
	if err != nil {
		return ctx, err
	}
	node, err := clientset.CoreV1().Nodes().Get(ctx, ClusterName+"-control-plane", metav1.GetOptions{})
	if err != nil {
		return ctx, fmt.Errorf("read owned cluster node: %w", err)
	}
	service, err := clientset.CoreV1().Services(ControllerNamespace).Get(ctx, HelmReleaseName+"-haproxy", metav1.GetOptions{})
	if err != nil {
		return ctx, fmt.Errorf("read HAProxy traffic service: %w", err)
	}
	var endpoint e2ecluster.TrafficEndpoint
	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeInternalIP {
			endpoint.Host = address.Address
			break
		}
	}
	for _, port := range service.Spec.Ports {
		switch port.Name {
		case HTTPPortName:
			endpoint.HTTPPort = int(port.NodePort)
		case "https":
			endpoint.HTTPSPort = int(port.NodePort)
		}
	}
	return ctx, e2ecluster.SetTrafficEndpoint(endpoint)
}
