import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
LIBRARIES = {
    "api-gateway": "libapi_gateway_plugin",
    "coraza": "libcoraza_plugin",
    "external-auth": "libexternal_auth_plugin",
    "fingerprinting": "libfingerprinting_plugin",
    "maxmind": "libmaxmind_plugin",
    "mirror": "libmirror_plugin",
    "rate-limit": "librate_limit_plugin",
    "sso-auth": "libhaproxy_spoa_hub_plugin_sso_auth",
}
ARCHES = ("amd64", "arm64", "armv7")
COMMAND_STUB = r'''
import hashlib, json, os, pathlib, re, sys, urllib.parse
args = sys.argv[1:]
command = pathlib.Path(sys.argv[0]).name
state = pathlib.Path(os.environ["TEST_STATE"])
mode = os.environ["TEST_MODE"]
with (state / "calls.jsonl").open("a") as stream:
    stream.write(json.dumps({"command": command, "args": args}) + "\n")

def option(name, default=None):
    return args[args.index(name) + 1] if name in args else default

if command == "curl":
    plugin, version, name = urllib.parse.urlsplit(args[-1]).path.split("/")[-3:]
    if mode == "transport-failure" or (mode == "transport-after-manifest" and name != "SHA256SUMS"):
        sys.exit(7)
    headers = option("--dump-header")
    if headers:
        pathlib.Path(headers).write_text(
            "HTTP/2 302\r\nLocation: https://example.test/?token=redirect-secret\r\n"
            "Set-Cookie: session=cookie-secret\r\n\r\n"
            "HTTP/2 409\r\nx-request-id: request-safe-123\r\n"
            "X-Request-ID: \x1b[31munsafe-id\r\n"
            "X-Request-ID: whitespace invalid\r\n"
            "X-Request-ID: nonascii-\u00e4\r\n"
            "X-Request-ID: " + "oversized" * 40 + "\r\n\r\n"
            if mode == "http-failure" else "HTTP/2 200\r\nx-request-id: previous-download-id\r\n\r\n")
    if mode == "http-failure":
        sys.exit(22)
    libraries = json.loads(os.environ["TEST_LIBRARIES"])
    glibc = os.environ["TEST_GLIBC"]
    filenames = [f"{libraries[plugin]}-{arch}-glibc{glibc}.so"
                 for arch in ("amd64", "arm64", "armv7")]
    def payload(filename):
        return f"{plugin}:{version}:{filename}\n".encode()
    if name == "SHA256SUMS":
        content = "" if mode == "missing-architecture" else "".join(
            hashlib.sha256(payload(filename)).hexdigest() + "  " + filename + "\n"
            for filename in filenames)
        content = content.encode()
    elif name.endswith(".cosign.bundle"):
        identity = f"https://gitlab.com/haproxy-haptic/haproxy-spoa-hub-plugin-{plugin}//.gitlab-ci.yml@refs/tags/{version}"
        if mode == "wrong-tag":
            identity = identity.rsplit("/", 1)[0] + "/v999.0.0"
        elif mode == "wrong-project":
            identity = identity.replace("/haproxy-haptic/", "/untrusted-project/")
        content = json.dumps({"identity": identity,
                              "issuer": "https://untrusted.test" if mode == "wrong-issuer"
                              else "https://gitlab.com"}).encode()
    else:
        content = b"corrupted" if mode == "checksum-mismatch" else payload(name)
    pathlib.Path(option("--output")).write_bytes(content)
elif command == "cosign":
    assert args[0] == "verify-blob"
    assert not any(flag.startswith("--insecure") or flag == "--ignore-sct" for flag in args)
    bundle = json.loads(pathlib.Path(option("--bundle")).read_text())
    identity = option("--certificate-identity")
    identity_matches = identity == bundle["identity"] if identity is not None else bool(
        re.fullmatch(option("--certificate-identity-regexp", ""), bundle["identity"]))
    if (not identity_matches or option("--certificate-oidc-issuer") != bundle["issuer"]
            or mode == "invalid-signature"):
        print("signature verification rejected", file=sys.stderr)
        sys.exit(1)
else:
    sys.exit("unexpected command")
'''


class SPOAPluginPreparationTests(unittest.TestCase):
    def run_preparation(self, mode):
        with tempfile.TemporaryDirectory(prefix="haptic-spoa-plugins-test-") as temp:
            repo = Path(temp)
            (repo / "scripts").mkdir()
            (repo / "bin").mkdir()
            shutil.copyfile(ROOT / "scripts/prep-spoa-plugins.sh", repo / "scripts/prep-spoa-plugins.sh")
            shutil.copyfile(ROOT / "versions-spoa.env", repo / "versions-spoa.env")
            version_result = subprocess.run(
                ["bash", "-c", 'source versions-spoa.env; for pin_name in ${!SPOA_PLUGIN_@}; do '
                 'printf "%s=%s\\n" "$pin_name" "${!pin_name}"; done'],
                cwd=repo, text=True, capture_output=True, check=True)
            versions = dict(line.split("=", 1) for line in version_result.stdout.splitlines())
            for command in ("curl", "cosign"):
                executable = repo / "bin" / command
                executable.write_text(f"#!{sys.executable}\n" + COMMAND_STUB)
                executable.chmod(0o755)
            env = os.environ | {
                "PATH": str(repo / "bin") + os.pathsep + os.environ["PATH"],
                "TEST_STATE": str(repo), "TEST_MODE": mode,
                "TEST_LIBRARIES": json.dumps(LIBRARIES), "TEST_GLIBC": versions["SPOA_PLUGIN_GLIBC_VERSION"],
            }
            result = subprocess.run(["bash", "scripts/prep-spoa-plugins.sh"], cwd=repo,
                                    env=env, text=True, capture_output=True, timeout=30, check=False)
            calls = [json.loads(line) for line in (repo / "calls.jsonl").read_text().splitlines()]
            staged = {str(path.relative_to(repo)): hashlib.sha256(path.read_bytes()).hexdigest()
                      for path in (repo / "plugins").glob("*/*.so")}
            return result, calls, staged, versions

    def test_verifies_every_chart_pinned_plugin_with_exact_tag_identity(self):
        result, calls, staged, versions = self.run_preparation("valid")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("verified 24 plugin .so files", result.stdout)
        self.assertEqual(set(staged), {f"plugins/{arch}/{name}.so"
                                       for arch in ARCHES for name in LIBRARIES.values()})
        verifications = [call["args"] for call in calls if call["command"] == "cosign"]
        downloads = [call["args"] for call in calls if call["command"] == "curl"]
        self.assertEqual(len(verifications), len(ARCHES) * len(LIBRARIES))
        for arguments in verifications:
            with self.subTest(target=arguments[1]):
                self.assertIn("--certificate-identity", arguments)
                self.assertNotIn("--certificate-identity-regexp", arguments)
                bundle = arguments[arguments.index("--bundle") + 1]
                download = next(args for args in downloads if args[args.index("--output") + 1] == bundle)
                plugin, version, _ = download[-1].split("/")[-3:]
                expected = (f"https://gitlab.com/haproxy-haptic/haproxy-spoa-hub-plugin-{plugin}"
                            f"//.gitlab-ci.yml@refs/tags/{version}")
                self.assertEqual(arguments[arguments.index("--certificate-identity") + 1], expected)
                self.assertEqual(arguments[arguments.index("--certificate-oidc-issuer") + 1],
                                 "https://gitlab.com")
        for arguments in downloads:
            plugin, version, _ = arguments[-1].split("/")[-3:]
            pin = "SPOA_PLUGIN_" + plugin.upper().replace("-", "_") + "_VERSION"
            self.assertEqual(version, versions[pin])
            self.assertEqual(arguments[arguments.index("--retry") + 1], "8")
            self.assertEqual(arguments[arguments.index("--retry-delay") + 1], "5")
            self.assertEqual(arguments[arguments.index("--retry-max-time") + 1], "120")
            self.assertIn("--retry-all-errors", arguments)

    def test_rejects_untrusted_or_wrong_version_signature(self):
        for mode in ("wrong-tag", "wrong-project", "wrong-issuer", "invalid-signature"):
            with self.subTest(mode=mode):
                result, calls, _, _ = self.run_preparation(mode)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("signature verification rejected", result.stderr)
                self.assertEqual(sum(call["command"] == "cosign" for call in calls), 1)
                self.assertNotIn("OK: verified", result.stdout)

    def test_rejects_invalid_checksum_and_missing_architecture(self):
        for mode, message in (("checksum-mismatch", "SHA256 mismatch"),
                              ("missing-architecture", "not present in upstream SHA256SUMS")):
            with self.subTest(mode=mode):
                result, calls, _, _ = self.run_preparation(mode)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertFalse(any(call["command"] == "cosign" for call in calls))

    def test_http_failure_reports_only_safe_request_diagnostics(self):
        result, calls, _, _ = self.run_preparation("http-failure")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("download failed:", result.stderr)
        self.assertIn("HTTP status: 409", result.stderr)
        self.assertIn("request-safe-123", result.stderr)
        for excluded in ("redirect-secret", "cookie-secret", "unsafe-id", "\x1b",
                         "whitespace", "nonascii", "oversized"):
            self.assertNotIn(excluded, result.stdout + result.stderr)
        self.assertEqual(len(calls), 1)

    def test_transport_failure_is_not_accepted_or_verified(self):
        for mode, expected_calls in (("transport-failure", 1), ("transport-after-manifest", 2)):
            with self.subTest(mode=mode):
                result, calls, _, _ = self.run_preparation(mode)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("download failed:", result.stderr)
                self.assertEqual(len(calls), expected_calls)
                self.assertTrue(all(call["command"] == "curl" for call in calls))
                self.assertNotIn("OK: verified", result.stdout)
                self.assertNotIn("HTTP status:", result.stderr)
                self.assertNotIn("previous-download-id", result.stderr)


if __name__ == "__main__":
    unittest.main()
