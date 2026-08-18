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

//go:build agentdocker

package agent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

const (
	startupBudget  = 60 * time.Second
	convergeBudget = 30 * time.Second
)

// env is one pod as the chart deploys it: an HAProxy container in master-worker
// mode and an agent container, sharing the config mount, with general/ on a
// mount of its own.
type env struct {
	t          *testing.T
	image      string
	configVol  string
	generalVol string
	haproxy    string
	agent      string
	client     *client.Client
	http       *http.Client
}

func newEnv(t *testing.T) *env {
	t.Helper()
	image := requireAgentImage(t)
	name := containerName(t)
	e := &env{
		t:          t,
		image:      image,
		configVol:  name + "-cfg",
		generalVol: name + "-general",
		haproxy:    name + "-haproxy",
		agent:      name + "-agent",
		http:       &http.Client{Timeout: 10 * time.Second},
	}
	t.Cleanup(e.teardown)
	e.createVolumes()
	e.seedBootstrap()
	e.startHAProxy()
	e.startAgent()
	return e
}

func containerName(t *testing.T) string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, t.Name())
	return fmt.Sprintf("haptic-agent-%s-%d", sanitized, time.Now().UnixNano()%1_000_000)
}

func (e *env) createVolumes() {
	mustDocker(e.t, "volume", "create", e.configVol)
	mustDocker(e.t, "volume", "create", e.generalVol)
}

// seedBootstrap writes the bootstrap config and the directory skeleton the
// haproxy container's start script creates in the chart.
func (e *env) seedBootstrap() {
	script := "set -e; mkdir -p " + baseDir + "/maps " + baseDir + "/ssl " + baseDir + "/general; cat > " + baseDir + "/" + configPath
	mustDockerInput(e.t, bootstrapConfig, append(e.runArgs("--rm", "-i"),
		"--entrypoint", "sh", e.image, "-c", script)...)
}

// runArgs prefixes a `docker run` with the two mounts every container of this
// pod shares.
func (e *env) runArgs(extra ...string) []string {
	args := []string{"run"}
	args = append(args, extra...)
	return append(args,
		"-v", e.configVol+":"+baseDir,
		"-v", e.generalVol+":"+baseDir+"/general")
}

func (e *env) startHAProxy() {
	mustDocker(e.t, append(e.runArgs("-d", "--name", e.haproxy,
		"-p", publishAddress()+"::"+strconv.Itoa(statsPort),
		"-p", publishAddress()+"::"+strconv.Itoa(httpPort),
		"-p", publishAddress()+"::"+strconv.Itoa(httpsPort)),
		"--entrypoint", "haproxy", e.image,
		"-dr", "-W", "-db", "-S", masterSocketPath+",level,admin", "--", baseDir+"/"+configPath)...)
	e.waitForWorker()
}

// waitForWorker blocks until the worker socket answers `show info`, which is
// the readiness gate the agent itself uses.
func (e *env) waitForWorker() {
	waitFor(e.t, "the HAProxy worker socket", startupBudget, func() error {
		out, err := runDocker(strings.NewReader("show info\n"),
			"exec", "-i", e.haproxy, "socat", "-t2", "stdio", "unix-connect:"+workerSocketPath)
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(out))
		}
		if !strings.Contains(out, "Name: HAProxy") {
			return fmt.Errorf("show info answered %q", strings.TrimSpace(out))
		}
		return nil
	})
}

func (e *env) startAgent() {
	mustDocker(e.t, append(e.runArgs("-d", "--name", e.agent,
		"-p", publishAddress()+"::"+strconv.Itoa(agentPort),
		"-e", "DATAPLANE_USERNAME="+agentUsername,
		"-e", "DATAPLANE_PASSWORD="+agentPassword),
		"--entrypoint", "/usr/local/bin/haptic", e.image,
		"agent",
		"--base-dir", baseDir,
		"--config", configPath,
		"--master-socket", "haproxy-master.sock",
		"--worker-socket", "haproxy-worker.sock",
		"--listen", ":"+strconv.Itoa(agentPort),
		"--reload-interval-min", "1s",
		"--state-file", ".haptic-agent.json",
		"--metrics-listen", ":9101")...)

	agentClient, err := client.New(&client.Config{
		BaseURL:            e.agentURL(),
		Username:           agentUsername,
		Password:           agentPassword,
		Timeout:            15 * time.Second,
		PerPodApplyTimeout: 2 * time.Minute,
	})
	if err != nil {
		e.t.Fatalf("agent client: %v", err)
	}
	e.client = agentClient
	e.t.Cleanup(e.client.Close)
	e.waitForReadyz()
}

func (e *env) agentURL() string {
	return fmt.Sprintf("http://%s:%d", connectHost(), publishedPort(e.t, e.agent, agentPort))
}

func (e *env) waitForReadyz() {
	waitFor(e.t, "the agent's /readyz", startupBudget, func() error {
		status, body, err := e.get(e.agentURL() + "/readyz")
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("/readyz answered %d: %s", status, body)
		}
		return nil
	})
}

func (e *env) teardown() {
	if e.t.Failed() {
		e.t.Logf("agent logs:\n%s", e.logs(e.agent))
		e.t.Logf("haproxy logs:\n%s", e.logs(e.haproxy))
	}
	for _, container := range []string{e.agent, e.haproxy} {
		_, _ = runDocker(nil, "rm", "-f", container)
	}
	for _, volume := range []string{e.configVol, e.generalVol} {
		_, _ = runDocker(nil, "volume", "rm", "-f", volume)
	}
}

func (e *env) logs(container string) string {
	out, _ := runDocker(nil, "logs", "--tail", "200", container)
	return out
}

// restartHAProxy replaces the container the way a kubelet restart does: the
// same mounts, the bootstrap config back on disk, a foreign worker for the
// agent to notice.
func (e *env) restartHAProxy() {
	mustDocker(e.t, "rm", "-f", e.haproxy)
	e.seedBootstrap()
	e.startHAProxy()
}

// worker runs one command on the worker stats socket, the socket the agent uses
// for every runtime command.
func (e *env) worker(command string) string {
	e.t.Helper()
	return e.socket(workerSocketPath, command)
}

func (e *env) master(command string) string {
	e.t.Helper()
	return e.socket(masterSocketPath, command)
}

func (e *env) socket(socket, command string) string {
	e.t.Helper()
	out, err := runDocker(strings.NewReader(command+"\n"),
		"exec", "-i", e.haproxy, "socat", "-t5", "stdio", "unix-connect:"+socket)
	if err != nil {
		e.t.Fatalf("%q on %s: %v\n%s", command, socket, err, out)
	}
	return out
}

// workerPID is the identity a reload changes; a runtime apply must leave it alone.
func (e *env) workerPID() int {
	e.t.Helper()
	for _, line := range strings.Split(e.worker("show info"), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "Pid: "); ok {
			pid, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				e.t.Fatalf("show info reported Pid %q: %v", value, err)
			}
			return pid
		}
	}
	e.t.Fatalf("show info carried no Pid:\n%s", e.worker("show info"))
	return 0
}

// read returns a file from the pod's tree, path relative to the base dir.
func (e *env) read(path string) string {
	e.t.Helper()
	return mustDocker(e.t, "exec", e.haproxy, "cat", baseDir+"/"+path)
}

// digest is the on-disk fingerprint an assertion compares before and after a
// refused apply.
func (e *env) digest(path string) string {
	e.t.Helper()
	out := mustDocker(e.t, "exec", e.haproxy, "sha256sum", baseDir+"/"+path)
	return strings.Fields(out)[0]
}

func (e *env) exists(path string) bool {
	e.t.Helper()
	_, err := runDocker(nil, "exec", e.haproxy, "test", "-e", baseDir+"/"+path)
	return err == nil
}

// device is the mount a path lives on; general/ must be its own.
// mountPoints lists the mount points below the base directory the way the
// agent's own probe reads them, from /proc/self/mountinfo. Two docker volumes
// usually share one device (so st_dev cannot tell them apart) and `stat %m`
// misreports a volume nested under a symlinked directory; a hardlink still
// cannot cross the mount, which is what the per-mount journal is about.
func (e *env) mountPoints() []string {
	e.t.Helper()
	resolved := strings.TrimSpace(mustDocker(e.t, "exec", e.haproxy, "readlink", "-f", baseDir))
	out := mustDocker(e.t, "exec", e.haproxy, "cat", "/proc/self/mountinfo")
	var points []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && strings.HasPrefix(fields[4], resolved+"/") {
			points = append(points, fields[4])
		}
	}
	return points
}

func (e *env) listAll(dir string) string {
	e.t.Helper()
	out, _ := runDocker(nil, "exec", e.haproxy, "ls", "-a", baseDir+"/"+dir)
	return out
}

func (e *env) get(url string) (int, string, error) {
	resp, err := e.http.Get(url)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(body), err
}

// statsURL and httpURL address the published ports of the HAProxy container.
func (e *env) statsURL(path string) string {
	return fmt.Sprintf("http://%s:%d%s", connectHost(), publishedPort(e.t, e.haproxy, statsPort), path)
}

func (e *env) httpURL(path string) string {
	return fmt.Sprintf("http://%s:%d%s", connectHost(), publishedPort(e.t, e.haproxy, httpPort), path)
}

func (e *env) httpsAddr() string {
	return fmt.Sprintf("%s:%d", connectHost(), publishedPort(e.t, e.haproxy, httpsPort))
}

// requestWithHost sends one request through the HTTP frontend with a chosen
// Host header, which is what the routing map keys on.
func (e *env) requestWithHost(host, path string) (int, http.Header, string) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.httpURL(path), http.NoBody)
	if err != nil {
		e.t.Fatalf("build request: %v", err)
	}
	req.Host = host
	resp, err := e.http.Do(req)
	if err != nil {
		e.t.Fatalf("GET %s with Host %s: %v", path, host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, resp.Header, string(body)
}

// peerCertificate completes a TLS handshake for one SNI and returns the leaf
// HAProxy served, which is how a rotation is observed without a reload. The
// suite mints its own self-signed leaves, so the handshake is an observation,
// never a trust decision — do not "fix" the verification setting.
func (e *env) peerCertificate(sni string) *x509.Certificate {
	e.t.Helper()
	var leaf *x509.Certificate
	waitFor(e.t, "a TLS handshake for "+sni, convergeBudget, func() error {
		conn, err := tls.Dial("tcp", e.httpsAddr(), &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		})
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		chain := conn.ConnectionState().PeerCertificates
		if len(chain) == 0 {
			return errors.New("handshake carried no certificate")
		}
		leaf = chain[0]
		return nil
	})
	return leaf
}

// waitForReady polls the status frontend until /ready answers the expected
// status: a reload is complete only once the new worker serves it.
func (e *env) waitForReady(want int) {
	e.t.Helper()
	waitFor(e.t, fmt.Sprintf("/ready to answer %d", want), convergeBudget, func() error {
		status, body, err := e.get(e.statsURL("/ready"))
		if err != nil {
			return err
		}
		if status != want {
			return fmt.Errorf("/ready answered %d (%s)", status, strings.TrimSpace(body))
		}
		return nil
	})
}

// statRow reads one `show stat` row by column name, so a column added in a
// later HAProxy release cannot silently shift an assertion.
func (e *env) statRow(backend, server string) (map[string]string, bool) {
	e.t.Helper()
	var header []string
	for _, line := range strings.Split(e.worker("show stat"), "\n") {
		if names, ok := strings.CutPrefix(line, "# "); ok {
			header = strings.Split(strings.TrimSpace(names), ",")
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 || fields[0] != backend || fields[1] != server {
			continue
		}
		row := make(map[string]string, len(header))
		for i, value := range fields {
			if i < len(header) {
				row[header[i]] = value
			}
		}
		return row, true
	}
	return nil, false
}

func (e *env) mustStatRow(backend, server string) map[string]string {
	e.t.Helper()
	row, ok := e.statRow(backend, server)
	if !ok {
		e.t.Fatalf("show stat has no row for %s/%s:\n%s", backend, server, e.worker("show stat"))
	}
	return row
}

// haproxyAtLeast compares the version under test with a "major.minor" bound, so
// a test for a directive introduced later skips on the older bracket.
func haproxyAtLeast(bound string) bool {
	parse := func(version string) (int, int) {
		parts := strings.SplitN(version, ".", 3)
		major, _ := strconv.Atoi(parts[0])
		minor := 0
		if len(parts) > 1 {
			minor, _ = strconv.Atoi(parts[1])
		}
		return major, minor
	}
	haveMajor, haveMinor := parse(haproxyVersion())
	wantMajor, wantMinor := parse(bound)
	if haveMajor != wantMajor {
		return haveMajor > wantMajor
	}
	return haveMinor >= wantMinor
}

// mapEntries parses `show map <path>` into key → value, dropping the entry id
// HAProxy prefixes each line with. Values keep every byte, including trailing
// spaces, because that is exactly what the payload form is there to preserve.
func mapEntries(output string) map[string]string {
	entries := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "0x") {
			continue
		}
		fields := strings.SplitN(line, " ", 3)
		switch len(fields) {
		case 2:
			entries[fields[1]] = ""
		case 3:
			entries[fields[1]] = fields[2]
		}
	}
	return entries
}
