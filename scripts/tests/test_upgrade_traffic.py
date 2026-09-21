import os
from pathlib import Path
import signal
import subprocess
import tempfile
import unittest


REPO = Path(__file__).resolve().parents[2]


class UpgradeTrafficTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.work = Path(self.directory.name)
        self.env = dict(os.environ, WORK=str(self.work), ARTIFACTS=str(self.work), NS="haptic",
                        CTX="kind-upgrade-test", PATH=str(self.work) + os.pathsep + os.environ["PATH"])
        self.write_command("kubectl", '''#!/usr/bin/env python3
import os
from pathlib import Path
import signal
import sys

work = Path(os.environ["WORK"])
with (work / "pids").open("a") as output:
    print(os.getpid(), file=output)
assert sys.argv[1:6] == ["--context", "kind-upgrade-test", "-n", "haptic", "port-forward"]
assert sys.argv[-2:] == [":http", ":https"]
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
if os.environ.get("FORWARD_FAILURE"):
    print("forwarding failed", flush=True)
    sys.exit(1)
print("Warning: unrelated diagnostic", flush=True)
print("Forwarding from 127.0.0.1:23443 -> " + os.environ.get("POD_HTTPS_PORT", "443"), flush=True)
print("Forwarding from 127.0.0.1:23080 -> " + os.environ.get("POD_HTTP_PORT", "80"), flush=True)
signal.pause()
''')
        self.write_command("curl", '''#!/usr/bin/env python3
import json
import sys

resolve = sys.argv[sys.argv.index("--resolve") + 1]
assert resolve in ["http.upgrade.test:23080:127.0.0.1", "tls.upgrade.test:23443:127.0.0.1"], resolve
print(json.dumps({"environment": {"HOSTNAME": "upgrade-backend-test"}, "http": {"originalUrl": "/upgrade-check"}}))
''')
        self.addCleanup(self.stop_forwarders)

    def write_command(self, name, body):
        path = self.work / name
        path.write_text(body)
        path.chmod(0o755)

    def forward_pids(self):
        path = self.work / "pids"
        return [int(pid) for pid in path.read_text().splitlines()] if path.exists() else []

    def stop_forwarders(self):
        for pid in self.forward_pids():
            try:
                os.kill(pid, signal.SIGTERM)
            except ProcessLookupError:
                pass

    def run_traffic(self):
        return subprocess.run(["bash", "-c", '''
set -euo pipefail
source scripts/lib/upgrade-traffic.sh
k() {
  case "$1 $2" in
    "get deployments") echo '{"items":[{"metadata":{"name":"haptic-haproxy"}}]}' ;;
    "get pods") echo '{"items":[{"metadata":{"name":"first"}},{"metadata":{"name":"second"}}]}' ;;
    "get pod") printf '{"spec":{"containers":[{"ports":[{"name":"http","containerPort":%s},{"name":"https","containerPort":%s}]}]}}' "${POD_HTTP_PORT:-80}" "${POD_HTTPS_PORT:-443}" ;;
    "get secret") printf 'Y2VydA==' ;;
    "port-forward "*) kubectl --context "$CTX" -n haptic "$@" ;;
    *) return 0 ;;
  esac
}
info() { :; }
fail() { echo "$*" >&2; return 1; }
wait_upgrade_traffic upgrade
'''], cwd=REPO, env=self.env, capture_output=True, text=True, timeout=15)

    def test_both_pods_use_current_forwarding_ports_and_stop_forwarders(self):
        (self.work / "forward.log").write_text(
            "Forwarding from 127.0.0.1:10080 -> 80\nForwarding from 127.0.0.1:10443 -> 443\n")
        result = self.run_traffic()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.forward_pids()), 2)
        self.assertEqual(len(list(self.work.glob("forward.*.log"))), 2)
        for pid in self.forward_pids():
            with self.assertRaises(ProcessLookupError):
                os.kill(pid, 0)

    def test_failed_forwarder_never_probes_routes(self):
        self.env["FORWARD_FAILURE"] = "1"
        result = self.run_traffic()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("forwarding failed", result.stdout)
        self.assertFalse((self.work / "response.json").exists())

    def test_released_chart_container_ports(self):
        self.env.update(POD_HTTP_PORT="8080", POD_HTTPS_PORT="8443")
        result = self.run_traffic()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
