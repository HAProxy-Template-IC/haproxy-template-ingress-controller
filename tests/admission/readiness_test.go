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

package admission

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/tests/kubeexec"
	"gitlab.com/haproxy-haptic/haptic/tests/process"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

const (
	refused       = `Error from server (InternalError): error when creating "routes.yaml": Internal error occurred: failed calling webhook "ingresses.validation.haptic": failed to call webhook: Post "https://haptic-webhook/validate": dial tcp 10.0.0.1:443: connect: connection refused`
	noEndpoints   = `Error from server (InternalError): error when creating "routes.yaml": Internal error occurred: failed calling webhook "ingresses.validation.haptic": no endpoints available for service "haptic-webhook"`
	denied        = `Error from server (Forbidden): error when creating "routes.yaml": admission webhook "ingresses.validation.haptic" denied the request: invalid configuration`
	clientTimeout = `error when creating "routes.yaml": Post "https://docker:36487/apis/networking.k8s.io/v1/namespaces/haptic/ingresses?dryRun=All&fieldManager=kubectl-create&fieldValidation=Strict&timeout=10s": context deadline exceeded`
)

func TestAdmissionConnectionClassification(t *testing.T) {
	for _, message := range []string{refused, noEndpoints, clientTimeout, refused + "\n" + clientTimeout, "Warning: deprecated\n" + refused + "\n"} {
		assert.True(t, ConnectionPending(message), "%s", message)
	}
	assert.False(t, ConnectionPending(""))
	assert.False(t, ConnectionPending("Warning: deprecated"))
	for _, message := range []string{denied, denied + ": context deadline exceeded", "error: malformed manifest", "error: Unauthorized", refused + " extra", "unknown error"} {
		assert.False(t, ConnectionPending(message), "%s", message)
		assert.False(t, ConnectionPending(refused+"\n"+message), "a terminal error must dominate: %s", message)
	}
}

type probeRunner struct {
	run func(context.Context, *process.Command) (process.Result, error)
}

func (r probeRunner) Run(ctx context.Context, command *process.Command) (process.Result, error) {
	return r.run(ctx, command)
}

func (probeRunner) Start(context.Context, *process.Command) (process.Running, error) {
	return nil, errors.New("admission must run a completed create dry run")
}

func TestAdmissionProbePreservesServerCreateAndClusterScope(t *testing.T) {
	for _, timeout := range []time.Duration{3 * time.Second, 30 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			manifest := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: dry-run\n")
			path := filepath.Join(t.TempDir(), "owned.kubeconfig")
			calls := 0
			runner := probeRunner{run: func(ctx context.Context, command *process.Command) (process.Result, error) {
				calls++
				require.Equal(t, "kubectl", command.Name)
				require.Equal(t, []string{"--kubeconfig", path, "--context", "kind-owned", "--namespace", "haptic", "create", "--dry-run=server"}, command.Args[:8])
				require.Equal(t, []string{"-f", "-"}, command.Args[9:])
				budget, err := time.ParseDuration(strings.TrimPrefix(command.Args[8], "--request-timeout="))
				require.NoError(t, err)
				require.Positive(t, budget)
				require.LessOrEqual(t, budget, min(timeout, 10*time.Second))
				body, err := io.ReadAll(command.Stdin)
				require.NoError(t, err)
				require.Equal(t, manifest, body)
				_, present := ctx.Deadline()
				require.True(t, present)
				return process.Result{Stdout: "configmap/dry-run created (server dry run)"}, nil
			}}
			client := kubeexec.Client{Runner: runner, Kubeconfig: path, Context: "kind-owned", Namespace: "haptic"}
			require.NoError(t, Wait(t.Context(), client, manifest, timeout))
			require.Equal(t, 1, calls)
		})
	}
}

func TestAdmissionProbeErrorOutcomes(t *testing.T) {
	for _, message := range []string{refused, noEndpoints, clientTimeout, denied, refused + "\n" + denied, clientTimeout + "\n" + denied, denied + "\n" + refused, "error: malformed manifest", "error: Unauthorized", ""} {
		t.Run(message, func(t *testing.T) {
			cause := errors.New("kubectl exited 1")
			client := kubeexec.Client{Kubeconfig: filepath.Join(t.TempDir(), "owned.kubeconfig"), Context: "kind-owned", Runner: probeRunner{run: func(context.Context, *process.Command) (process.Result, error) {
				return process.Result{Stderr: message, ExitCode: 1}, cause
			}}}
			outcome, err := Probe(t.Context(), client, []byte("fixture"))
			require.ErrorIs(t, err, cause)
			require.ErrorContains(t, err, message)
			if message == refused || message == noEndpoints || message == clientTimeout {
				assert.Equal(t, testutil.PollPending, outcome)
			} else {
				assert.Equal(t, testutil.PollFailed, outcome)
			}
		})
	}
}
