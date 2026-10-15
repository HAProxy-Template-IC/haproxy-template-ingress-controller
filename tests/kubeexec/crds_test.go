// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kubeexec

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/haproxy-haptic/HAPTIC/tests/process"
	"github.com/haproxy-haptic/HAPTIC/tests/testutil"
)

const establishedStatus = `{"conditions":[{"type":"Established","status":"True"}]}`

func crdList(status string) string {
	return fmt.Sprintf(`{"items":[{"metadata":{"name":"routes.example.org"},"status":%s}]}`, status)
}

func TestCRDEstablishmentRequiresEveryResource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   string
		names  []string
		result testutil.PollResult
		err    string
	}{
		{name: "null status", data: crdList(`null`), result: testutil.PollPending},
		{name: "absent conditions", data: crdList(`{}`), result: testutil.PollPending},
		{name: "null conditions", data: crdList(`{"conditions":null}`), result: testutil.PollPending},
		{name: "empty conditions", data: crdList(`{"conditions":[]}`), result: testutil.PollPending},
		{name: "not established", data: crdList(`{"conditions":[{"type":"Established","status":"False"}]}`), result: testutil.PollPending},
		{name: "unrelated true condition", data: crdList(`{"conditions":[{"type":"NamesAccepted","status":"True"}]}`), result: testutil.PollPending},
		{name: "established", data: crdList(establishedStatus), result: testutil.PollSucceeded},
		{name: "qualified name", data: crdList(establishedStatus), names: []string{"crd/routes.example.org"}, result: testutil.PollSucceeded},
		{name: "missing CRD", data: `{"items":[]}`, result: testutil.PollFailed, err: "missing or deleting"},
		{name: "second CRD missing", data: crdList(establishedStatus), names: []string{"routes.example.org", "other.example.org"}, result: testutil.PollFailed, err: "other.example.org"},
		{name: "second CRD pending", data: `{"items":[{"metadata":{"name":"ready.example.org"},"status":` + establishedStatus + `},{"metadata":{"name":"routes.example.org"},"status":{"conditions":null}}]}`, names: []string{"ready.example.org", "routes.example.org"}, result: testutil.PollPending},
		{name: "rejected names", data: crdList(`{"conditions":[{"type":"NamesAccepted","status":"False","message":"conflict"}]}`), result: testutil.PollFailed, err: "names rejected: conflict"},
		{name: "malformed conditions", data: crdList(`{"conditions":"invalid"}`), result: testutil.PollFailed, err: "decode CRDs"},
		{name: "malformed JSON", data: "invalid", result: testutil.PollFailed, err: "decode CRDs"},
		{name: "deleting", data: `{"items":[{"metadata":{"name":"routes.example.org","deletionTimestamp":"2026-10-13T00:00:00Z"},"status":` + establishedStatus + `}]}`, result: testutil.PollFailed, err: "missing or deleting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names := tc.names
			if names == nil {
				names = []string{"routes.example.org"}
			}
			result, err := crdsEstablished([]byte(tc.data), names)
			require.Equal(t, tc.result, result)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type crdRunner struct {
	run func(context.Context, *process.Command) (process.Result, error)
}

func (r crdRunner) Run(ctx context.Context, command *process.Command) (process.Result, error) {
	return r.run(ctx, command)
}

func (crdRunner) Start(context.Context, *process.Command) (process.Running, error) {
	return nil, errors.New("unexpected asynchronous command")
}

func TestWaitCRDsEstablishedObservesNullThenEstablished(t *testing.T) {
	calls := 0
	path := filepath.Join(t.TempDir(), "kubeconfig")
	client := Client{Kubeconfig: path, Context: "kind-owned"}
	client.Runner = crdRunner{run: func(ctx context.Context, command *process.Command) (process.Result, error) {
		require.NoError(t, ctx.Err())
		require.Equal(t, "kubectl", command.Name)
		require.Equal(t, []string{"--kubeconfig", path, "--context", "kind-owned", "get", "crd", "-o", "json"}, command.Args)
		calls++
		status := establishedStatus
		if calls == 1 {
			status = `{"conditions":null}`
		}
		return process.Result{Stdout: crdList(status)}, nil
	}}
	require.NoError(t, client.WaitCRDsEstablished(t.Context(), []string{"routes.example.org"}, time.Second))
	require.Equal(t, 2, calls)
}

func TestWaitCRDsEstablishedPreservesFailureAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		timeout time.Duration
	}{
		{name: "API failure", err: errors.New("forbidden"), timeout: time.Second},
		{name: "deadline", timeout: time.Nanosecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := Client{Kubeconfig: filepath.Join(t.TempDir(), "kubeconfig"), Context: "kind-owned"}
			client.Runner = crdRunner{run: func(context.Context, *process.Command) (process.Result, error) {
				calls++
				return process.Result{Stdout: crdList(`{"conditions":null}`)}, tc.err
			}}
			err := client.WaitCRDsEstablished(t.Context(), []string{"routes.example.org"}, tc.timeout)
			require.Error(t, err)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, 1, calls)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
	require.ErrorContains(t, (Client{}).WaitCRDsEstablished(t.Context(), nil, time.Minute), "at least one")
}
