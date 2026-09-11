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

package cli

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkerSessionAcknowledgesEachSetupCommand(t *testing.T) {
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	done := make(chan error, 1)
	go func() {
		defer peer.Close()
		reader := bufio.NewReader(peer)
		for _, command := range []string{"prompt", "set severity-output number", experimentalPrefix, "show info float"} {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- err
				return
			}
			if line != command+"\n" {
				done <- fmt.Errorf("expected %q, got %q", command, line)
				return
			}
			reply := "\n> "
			if command == "show info float" {
				reply = "Pid: 123\nStart_time_sec: 1700000000.000000\n\n> "
			}
			if _, err := io.WriteString(peer, reply); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	session := &workerSession{ctx: t.Context(), conn: conn, reader: bufio.NewReader(conn)}
	err := session.identify(testWorkerInfo(123))
	require.NoError(t, err)
	require.NoError(t, <-done)
}

func TestWorkerSessionRequiresACompleteBoundedReply(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
		fails             bool
	}{
		{name: "acknowledged silence", reply: "\n\n> "},
		{name: "success", reply: "[6]: Server deleted\n\n> ", want: "[6]: Server deleted"},
		{name: "closed without acknowledgement", fails: true},
		{name: "truncated success", reply: "[6]: Server deleted\n", fails: true},
		{name: "different prompt", reply: "[6]: Server deleted\n\n123> ", fails: true},
		{name: "oversized", reply: strings.Repeat("x", maxSessionReplyBytes) + workerPrompt, fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := net.Pipe()
			defer conn.Close()
			defer peer.Close()
			go func() {
				defer peer.Close()
				_, err := bufio.NewReader(peer).ReadString('\n')
				if err == nil {
					_, _ = io.WriteString(peer, tc.reply)
				}
			}()
			session := &workerSession{ctx: t.Context(), conn: conn, reader: bufio.NewReader(conn)}
			got, err := session.execute("del server be/srv")
			if tc.fails {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestInfoRequiresCompleteWorkerIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		micros      int64
	}{
		{name: "microsecond start", reply: "Pid: 123\nStart_time_sec: 1700000000.000001", micros: 1_700_000_000_000_001},
		{name: "next microsecond", reply: "Pid: 123\nStart_time_sec: 1700000000.000002", micros: 1_700_000_000_000_002},
		{name: "missing start", reply: "Pid: 123"},
		{name: "missing pid", reply: "Start_time_sec: 1700000000.000001"},
		{name: "invalid start", reply: "Pid: 123\nStart_time_sec: invalid"},
		{name: "negative start", reply: "Pid: 123\nStart_time_sec: -1"},
		{name: "zero start", reply: "Pid: 123\nStart_time_sec: 0"},
		{name: "overflow start", reply: "Pid: 123\nStart_time_sec: 9999999999999999999999"},
		{name: "invalid pid", reply: "Pid: invalid\nStart_time_sec: 1700000000.000001"},
		{name: "negative pid", reply: "Pid: -1\nStart_time_sec: 1700000000.000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := parseInfo(tc.reply)
			if tc.micros == 0 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.micros, info.WorkerStartTimeUnixMicros)
			require.True(t, info.SameWorker(info))
		})
	}
}
