import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


REPO = Path(__file__).resolve().parents[2]
REFUSED = ('Error from server (InternalError): error when creating "routes.yaml": '
           'Internal error occurred: failed calling webhook "ingresses.validation.haptic": '
           'failed to call webhook: Post "https://haptic-webhook/validate": '
           'dial tcp 10.0.0.1:443: connect: connection refused')
NO_ENDPOINTS = ('Error from server (InternalError): error when creating "routes.yaml": '
                'Internal error occurred: failed calling webhook "ingresses.validation.haptic": '
                'no endpoints available for service "haptic-webhook"')
DENIED = ('Error from server (Forbidden): error when creating "routes.yaml": '
          'admission webhook "ingresses.validation.haptic" denied the request: invalid configuration')


class AdmissionReadinessTests(unittest.TestCase):
    def probe(self, responses, timeout=60):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            (work / "responses.json").write_text(json.dumps(responses))
            command = work / "kubectl"
            command.write_text('''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

work = Path(os.environ["PROBE_WORK"])
calls = work / "calls.jsonl"
count = len(calls.read_text().splitlines()) if calls.exists() else 0
with calls.open("a") as output:
    print(json.dumps(sys.argv[1:]), file=output)
responses = json.loads((work / "responses.json").read_text())
response = responses[min(count, len(responses) - 1)]
print("deployment/upgrade-backend created (server dry run)")
if response:
    print(response, file=sys.stderr)
    sys.exit(1)
''')
            command.chmod(0o755)
            env = dict(os.environ, PROBE_WORK=str(work), PATH=directory + os.pathsep + os.environ["PATH"])
            result = subprocess.run(["bash", "-euc", '''
source scripts/lib/admission.sh
k() { kubectl --context kind-readiness -n haptic "$@"; }
unset SECONDS
SECONDS=0
sleep() { SECONDS=$((SECONDS + 1)); }
wait_admission_ready routes.yaml "$1"
''', "bash", str(timeout)], cwd=REPO, env=env, text=True, capture_output=True, timeout=10)
            calls = [json.loads(line) for line in (work / "calls.jsonl").read_text().splitlines()]
            for call in calls:
                self.assertEqual(call[:6], ["--context", "kind-readiness", "-n", "haptic", "create", "--dry-run=server"])
                self.assertEqual(call[-2:], ["-f", "routes.yaml"])
                request_timeout = int(call[6].removeprefix("--request-timeout=").removesuffix("s"))
                self.assertGreater(request_timeout, 0)
                self.assertLessEqual(request_timeout, min(timeout, 10))
            return result, calls

    def test_ready_webhook_needs_one_dry_run(self):
        result, calls = self.probe([""])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 1)

    def test_waits_for_service_routing_before_accepting_readiness(self):
        result, calls = self.probe([REFUSED, NO_ENDPOINTS, ""])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 3)
        self.assertIn(REFUSED, result.stderr)

    def test_validation_and_other_failures_are_not_retried(self):
        for error in (DENIED, REFUSED + "\n" + DENIED, "error: malformed manifest", "error: Unauthorized"):
            with self.subTest(error=error):
                result, calls = self.probe([error, ""])
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(len(calls), 1)
                self.assertIn(error, result.stderr)

    def test_unreachable_webhook_fails_at_the_deadline(self):
        result, calls = self.probe([REFUSED], timeout=2)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 2)
        self.assertIn("readiness deadline", result.stderr)


if __name__ == "__main__":
    unittest.main()
