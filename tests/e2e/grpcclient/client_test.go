// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build e2e

package grpcclient

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

func TestWaitForConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, <-served)
	})

	for _, name := range []string{"ready", "canceled", "closed"} {
		t.Run(name, func(t *testing.T) {
			conn, err := grpc.NewClient("passthrough:///"+listener.Addr().String(),
				grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			switch name {
			case "canceled":
				cancel()
				require.ErrorIs(t, waitForConnection(ctx, conn), context.Canceled)
			case "closed":
				require.NoError(t, conn.Close())
				require.ErrorContains(t, waitForConnection(ctx, conn), "shut down")
			case "ready":
				require.NoError(t, waitForConnection(ctx, conn))
				require.Equal(t, connectivity.Ready, conn.GetState())
			}
		})
	}
}
