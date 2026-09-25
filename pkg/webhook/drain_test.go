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

package webhook

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
)

func TestDrainValidatesLateRequestsAndRestartsQuietPeriod(t *testing.T) {
	server := newTestServer(t, &ServerConfig{})
	server.RegisterValidator("v1.ConfigMap", func(*ValidationContext) (bool, string, []string, error) {
		return false, "invalid configuration", nil, nil
	})
	synctest.Test(t, func(t *testing.T) {
		done := make(chan error, 1)
		go func() { done <- server.WaitForQuiet(t.Context(), 2*time.Second) }()
		synctest.Wait()
		time.Sleep(time.Second)
		response, request := configMapAdmissionRequest(t)
		server.handleValidation(response, request)
		require.Equal(t, "close", response.Header().Get("Connection"))
		var review admissionv1.AdmissionReview
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &review))
		require.False(t, review.Response.Allowed)
		require.Equal(t, "invalid configuration", review.Response.Result.Message)
		time.Sleep(time.Second)
		select {
		case <-done:
			t.Fatal("drain ignored a late admission request")
		default:
		}
		time.Sleep(time.Second)
		require.NoError(t, <-done)
	})
}

func TestDrainWaitsForActiveValidation(t *testing.T) {
	server := newTestServer(t, &ServerConfig{})
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		server.RegisterValidator("v1.ConfigMap", func(*ValidationContext) (bool, string, []string, error) {
			<-release
			return true, "", nil, nil
		})
		response, request := configMapAdmissionRequest(t)
		finished := make(chan struct{})
		go func() {
			server.handleValidation(response, request)
			close(finished)
		}()
		synctest.Wait()
		done := make(chan error, 1)
		go func() { done <- server.WaitForQuiet(t.Context(), 2*time.Second) }()
		synctest.Wait()
		time.Sleep(5 * time.Second)
		select {
		case <-done:
			t.Fatal("drain abandoned an active admission request")
		default:
		}
		close(release)
		<-finished
		time.Sleep(2 * time.Second)
		require.NoError(t, <-done)
		var review admissionv1.AdmissionReview
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &review))
		require.True(t, review.Response.Allowed)
	})
}

func TestDrainDeadlineBoundsContinuousTraffic(t *testing.T) {
	server := newTestServer(t, &ServerConfig{})
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		server.activity.start()
		defer server.activity.finish()
		started := time.Now()
		require.ErrorIs(t, server.WaitForQuiet(ctx, 2*time.Second), context.DeadlineExceeded)
		require.Equal(t, 3*time.Second, time.Since(started))
	})
}

func TestShutdownValidatesRequestsStillUploading(t *testing.T) {
	server := newTestServer(t, &ServerConfig{BindAddress: "127.0.0.1"})
	server.config.Port = 0
	server.RegisterValidator("v1.ConfigMap", func(*ValidationContext) (bool, string, []string, error) {
		return true, "", nil, nil
	})
	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- server.Start(runCtx) }()
	select {
	case <-server.Listening():
	case err := <-served:
		t.Fatalf("listener failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not start")
	}
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(server.config.CertPEM))
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", server.Addr(), &tls.Config{
		MinVersion: tls.VersionTLS12, RootCAs: roots,
	})
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, request := configMapAdmissionRequest(t)
	body, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.NoError(t, request.Body.Close())
	_, err = fmt.Fprintf(conn, "POST /validate HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\n\r\n%s", len(body), body[:1])
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		server.activity.mu.Lock()
		defer server.activity.mu.Unlock()
		return server.activity.active == 1
	}, time.Second, time.Millisecond)

	shutdownCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	drained := make(chan error, 1)
	go func() { drained <- server.Shutdown(shutdownCtx) }()
	<-server.shutdownStarted
	select {
	case err := <-served:
		t.Fatalf("listener retired validators before the request finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	_, err = conn.Write(body[1:])
	require.NoError(t, err)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	var review admissionv1.AdmissionReview
	require.NoError(t, json.NewDecoder(response.Body).Decode(&review))
	require.True(t, review.Response.Allowed)
	require.NoError(t, <-drained)
	require.NoError(t, <-served)
}
