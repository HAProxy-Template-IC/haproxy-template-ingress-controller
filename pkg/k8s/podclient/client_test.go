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

package podclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestPodHTTPDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	address, err := netip.ParseAddrPort(server.Listener.Addr().String())
	require.NoError(t, err)
	_, err = getForwardedLoopback(t.Context(), address.Port(), "/", 1024)
	require.ErrorContains(t, err, "HTTP 302")
	require.Zero(t, redirected.Load())
}

func TestReadResponseRejectsOversizeInsteadOfTruncating(t *testing.T) {
	for _, limit := range []int64{0, 3, 4} {
		data, err := readResponse(strings.NewReader("abc"), limit)
		require.NoError(t, err)
		require.Equal(t, "abc", string(data))
	}
	data, err := readResponse(strings.NewReader("abcd"), 3)
	require.ErrorContains(t, err, "exceeds")
	require.Nil(t, data)
	buffer := responseBuffer{limit: 3}
	_, err = buffer.Write([]byte("ab"))
	require.NoError(t, err)
	_, err = buffer.Write([]byte("cd"))
	require.ErrorContains(t, err, "exceeds")
	require.Equal(t, "ab", buffer.buffer.String())
	_, err = io.Copy(&buffer, io.LimitReader(strings.NewReader("cd"), 2))
	require.ErrorContains(t, err, "exceeds")
	require.Equal(t, "ab", buffer.buffer.String())
}

func TestNamedPodReadDoesNotRequireReadiness(t *testing.T) {
	client := New(&rest.Config{Host: "https://cluster.invalid"}, fake.NewClientset(), "ns", "", 6060)
	client.getFromPod = func(ctx context.Context, name, path string, limit int64) ([]byte, error) {
		require.Equal(t, "not-ready", name)
		require.Equal(t, "/debug/vars/pipeline", path)
		require.Equal(t, int64(1024), limit)
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		return []byte("{}"), nil
	}
	_, err := client.GetFromPod(t.Context(), "not-ready", "/debug/vars/pipeline", 1024)
	require.NoError(t, err)
	_, err = client.GetFromPod(t.Context(), "not-ready", "/", -1)
	require.ErrorContains(t, err, "negative")
}

func TestReadyPodReadFallsBackAndRotates(t *testing.T) {
	ready := func(name string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name}, Status: corev1.PodStatus{
			Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		}}
	}
	client := New(&rest.Config{Host: "https://cluster.invalid"}, fake.NewClientset(ready("a"), ready("b")), "ns", "", 6060)
	var attempts []string
	client.getFromPod = func(_ context.Context, name, _ string, _ int64) ([]byte, error) {
		attempts = append(attempts, name)
		if name == "a" {
			return nil, errors.New("unavailable")
		}
		return []byte("ok"), nil
	}
	_, err := client.Get(t.Context(), "/")
	require.NoError(t, err)
	_, err = client.Get(t.Context(), "/")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "b"}, attempts)
}
