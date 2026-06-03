//go:build acceptance

// Copyright 2025 Philipp Hossner
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

package acceptance

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

const (
	// webhookCertMountPath mirrors the chart's mount point for the webhook
	// TLS Secret (charts/haptic/templates/deployment.yaml).
	webhookCertMountPath = "/etc/webhook/certs"
	// webhookServiceName matches the WEBHOOK_SERVICE_NAME env the controller
	// deployment fixture sets, and the Service the API server would route to.
	webhookServiceName = "haptic-webhook"
	// proberPodName is the in-cluster pod used to read the webhook's served
	// certificate over TLS (it ships the openssl CLI).
	proberPodName = "cert-prober"
)

// TestWebhookCertHotRotation verifies that rotating the webhook's TLS-cert
// Secret makes the webhook serve the NEW certificate WITHOUT the controller
// pod restarting — i.e. the server reloads its certificate from the mounted
// file on the fly (a tls.Config GetCertificate callback) rather than binding
// it once at startup.
//
// This is RED until the webhook server reads its certificate through a
// reloading GetCertificate callback (the "hot-rotation" feature). Against a
// build that loads the cert once at startup, the served serial never changes
// after rotation and the rotation poll times out.
//
// The controller deployment mounts the cert Secret at /etc/webhook/certs and
// points the controller at it via WEBHOOK_CERT_DIR.
func TestWebhookCertHotRotation(t *testing.T) {
	feature := features.New("Webhook TLS cert hot-rotation").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()

			namespace := envconf.RandomName("test-webhook-cert-rot", 24)
			t.Logf("Using test namespace: %s", namespace)
			ctx = StoreNamespaceInContext(ctx, namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err, "create client")

			dnsName := webhookServiceDNS(namespace)

			// Initial certificate A.
			certA, keyA, err := genSelfSignedServerCert(0x1111AA, dnsName)
			require.NoError(t, err, "generate cert A")

			// Namespace + RBAC + credentials Secret (controller needs the
			// latter to start regardless of the webhook).
			require.NoError(t, client.Resources().Create(ctx,
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}), "create namespace")
			require.NoError(t, createControllerRBAC(ctx, client, namespace), "create RBAC")
			require.NoError(t, client.Resources().Create(ctx, NewSecret(namespace, ControllerSecretName)), "create credentials secret")

			// Webhook TLS Secret carrying the REAL cert A (the shared
			// NewWebhookCertSecret fixture ships a placeholder that cannot
			// complete a handshake, so we build our own).
			require.NoError(t, client.Resources().Create(ctx,
				newTLSSecret(namespace, WebhookCertSecretName, certA, keyA)), "create webhook cert secret")

			// CRD + webhook-enabled controller deployment + webhook Service.
			require.NoError(t, client.Resources().Create(ctx,
				NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)), "create CRD")
			require.NoError(t, client.Resources().Create(ctx,
				webhookEnabledControllerDeployment(namespace)), "create controller deployment")
			require.NoError(t, client.Resources().Create(ctx,
				NewWebhookService(namespace, webhookServiceName)), "create webhook service")

			// In-cluster prober that can read the served certificate.
			require.NoError(t, client.Resources().Create(ctx, newProberPod(namespace)), "create prober pod")

			return ctx
		}).
		Assess("serves the rotated certificate without restarting the controller", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()

			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)
			client, err := cfg.NewClient()
			require.NoError(t, err)
			dnsName := webhookServiceDNS(namespace)

			// Controller and prober must be up before we probe TLS.
			require.NoError(t, WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultPodReadyTimeout), "controller pod ready")
			require.NoError(t, WaitForPodReady(ctx, client, namespace, "app="+proberPodName, DefaultPodReadyTimeout), "prober pod ready")

			// Sanity: the webhook is serving SOME certificate (cert A).
			serialA := pollServedSerial(ctx, t, namespace, dnsName, func(string) bool { return true }, 2*time.Minute)
			t.Logf("webhook initially serves cert serial %q", serialA)

			// Snapshot controller-pod identity so we can prove later that the
			// rotation did not recreate the pod or restart the container.
			before, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)
			beforeUID := before.UID
			beforeRestarts := containerRestarts(before, "controller")

			// Rotate the Secret to certificate B (distinct serial).
			certB, keyB, err := genSelfSignedServerCert(0x2222BB, dnsName)
			require.NoError(t, err, "generate cert B")
			rotateTLSSecret(ctx, t, client, namespace, WebhookCertSecretName, certB, keyB)
			t.Log("rotated webhook cert Secret to cert B")

			// The webhook must begin serving cert B on its own. kubelet syncs
			// mounted Secret files lazily (up to ~1 min), so allow a few
			// minutes for the change to surface, then settle.
			serialB := pollServedSerial(ctx, t, namespace, dnsName,
				func(s string) bool { return s != serialA }, 4*time.Minute)
			t.Logf("webhook now serves cert serial %q (was %q)", serialB, serialA)
			require.NotEqual(t, serialA, serialB, "webhook must serve the rotated certificate")

			// And it must have done so WITHOUT a pod/container restart — the
			// whole point of hot-rotation.
			after, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)
			assert.Equal(t, beforeUID, after.UID, "controller pod must not be recreated during cert rotation")
			assert.Equal(t, beforeRestarts, containerRestarts(after, "controller"), "controller container must not restart during cert rotation")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if namespace, err := GetNamespaceFromContext(ctx); err == nil {
				if client, cerr := cfg.NewClient(); cerr == nil {
					_ = client.Resources().Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
				}
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// webhookServiceDNS returns the in-cluster DNS name of the webhook Service.
func webhookServiceDNS(namespace string) string {
	return fmt.Sprintf("%s.%s.svc", webhookServiceName, namespace)
}

// webhookEnabledControllerDeployment is the standard controller deployment
// plus the webhook wiring: the TLS Secret mounted as files at
// /etc/webhook/certs, with WEBHOOK_CERT_DIR pointing the controller at them.
func webhookEnabledControllerDeployment(namespace string) *appsv1.Deployment {
	d := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)

	c := &d.Spec.Template.Spec.Containers[0]
	c.Env = append(c.Env,
		corev1.EnvVar{Name: "WEBHOOK_CERT_DIR", Value: webhookCertMountPath},
	)
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
		Name:      "webhook-certs",
		MountPath: webhookCertMountPath,
		ReadOnly:  true,
	})
	d.Spec.Template.Spec.Volumes = append(d.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: "webhook-certs",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: WebhookCertSecretName},
		},
	})
	return d
}

// newProberPod runs an idle openssl-capable container we exec into to read the
// webhook's served certificate over a real TLS handshake.
func newProberPod(namespace string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proberPodName,
			Namespace: namespace,
			Labels:    map[string]string{"app": proberPodName},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "prober",
					Image:   "alpine/openssl",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}
}

// newTLSSecret builds a kubernetes.io/tls Secret with the standard
// tls.crt / tls.key keys (consumed both by the controller's API-fetch path
// and, when mounted, as files for the reloading server).
func newTLSSecret(namespace, name string, certPEM, keyPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": keyPEM,
		},
	}
}

// rotateTLSSecret overwrites the cert/key of an existing TLS Secret in place.
func rotateTLSSecret(ctx context.Context, t *testing.T, client klient.Client, namespace, name string, certPEM, keyPEM []byte) {
	t.Helper()
	var sec corev1.Secret
	require.NoError(t, client.Resources().Get(ctx, name, namespace, &sec), "get webhook cert secret")
	if sec.Data == nil {
		sec.Data = map[string][]byte{}
	}
	sec.Data["tls.crt"] = certPEM
	sec.Data["tls.key"] = keyPEM
	require.NoError(t, client.Resources().Update(ctx, &sec), "update webhook cert secret")
}

// pollServedSerial repeatedly reads the certificate the webhook serves until
// accept(serial) holds, returning that serial. It fails the test on timeout
// or context cancellation.
func pollServedSerial(ctx context.Context, t *testing.T, namespace, dnsName string, accept func(string) bool, timeout time.Duration) string {
	t.Helper()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	deadline := time.After(timeout)

	var last string
	for {
		serial, err := servedSerial(ctx, namespace, dnsName)
		if err == nil && serial != "" {
			last = serial
			if accept(serial) {
				return serial
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out after %s waiting for the webhook to serve an accepted cert serial; last seen=%q", timeout, last)
			return ""
		case <-ctx.Done():
			t.Fatalf("context cancelled while polling served cert serial: %v (last seen=%q)", ctx.Err(), last)
			return ""
		case <-ticker.C:
		}
	}
}

// servedSerial execs openssl inside the prober to read the serial number of
// the leaf certificate the webhook currently serves.
func servedSerial(ctx context.Context, namespace, dnsName string) (string, error) {
	script := fmt.Sprintf(
		"echo | openssl s_client -connect %s:443 -servername %s -showcerts 2>/dev/null | openssl x509 -noout -serial 2>/dev/null",
		dnsName, dnsName)
	out, err := ExecInPod(ctx, Clientset(), namespace, proberPodName, "prober", []string{"sh", "-c", script})
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	out = strings.TrimPrefix(out, "serial=")
	return strings.TrimSpace(out), nil
}

// containerRestarts returns the restart count of the named container, or -1 if
// the container status is not present yet.
func containerRestarts(pod *corev1.Pod, container string) int32 {
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == container {
			return pod.Status.ContainerStatuses[i].RestartCount
		}
	}
	return -1
}

// genSelfSignedServerCert builds a self-signed server certificate with the
// given serial number and DNS SAN. Distinct serials let the test detect a
// rotation purely from the served certificate.
func genSelfSignedServerCert(serial int64, dnsName string) (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generating key: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{dnsName},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("creating certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}
