import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("check_no_lua", ROOT / "scripts/check-no-lua.py")
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


class NoLuaTests(unittest.TestCase):
    def test_rejects_embedded_deadline_implementation(self):
        hooks = [
            'lua-load-per-thread /files/deadline.lua',
            'http-request lua.gateway-deadline-start',
            'tcp-response content lua.gateway-deadline-wait',
            'tune.lua.bool-sample-conversion normal',
            'local result = core.register_action("deadline", {}, function() end)',
            'http-request use-service lua.deadline',
            'http-request set-var(txn.value) lua.deadline',
            'http-request set-var(txn.value) str(input),lua.deadline',
            'lua-prepend-path /files/?.lua',
            'make USE_LUA=1',
        ]
        for hook in hooks:
            with self.subTest(hook=hook):
                self.assertTrue(CHECKER.violations("charts/haptic/charts/gateway/45-timeouts.yaml",
                                                   hook.encode()))

    def test_rejects_standalone_and_disguised_bytecode(self):
        for name, content in [("scripts/helper.lua", b"return 1"),
                              ("charts/haptic/helper.LUAC", b"bytecode"),
                              ("cmd/helper.bin", b"\x1bLua\x54"),
                              ("cmd/helper.bin", b"\x1bLJ\x02")]:
            with self.subTest(name=name, content=content):
                self.assertTrue(CHECKER.violations(name, content))

    def test_upstream_fixtures_are_pinned_not_directory_exemptions(self):
        for name in CHECKER.COMPATIBILITY_FIXTURES:
            with self.subTest(name=name):
                content = (ROOT / name).read_bytes()
                self.assertFalse(CHECKER.violations(name, content))
                self.assertTrue(CHECKER.violations(name, content + b"\n"))
                self.assertTrue(CHECKER.violations(name.replace("mailers/", "mailers/copy-"), content))

    def test_generic_transport_and_schemas_do_not_enable_lua(self):
        for content in [b'files["custom.lua"] = "operator content"',
                        b'case "allow", "lua", "reject":',
                        b'"mailers/mailers-with-alerts-lua.cfg"',
                        b'http-request set-timeout server 500ms',
                        b'Never add Lua-based HAPTIC features.']:
            with self.subTest(content=content):
                self.assertFalse(CHECKER.violations("pkg/example.go", content))

    def test_checks_git_inventory_with_spaces_and_deleted_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            hook = root / "config with spaces.cfg"
            hook.write_text("lua-load /files/deadline.lua\n")
            with mock.patch.object(CHECKER.subprocess, "check_output",
                                   return_value=b"config with spaces.cfg\0deleted.lua\0"):
                self.assertEqual(len(CHECKER.check_repository(root)), 1)
                hook.unlink()
                self.assertFalse(CHECKER.check_repository(root))

    def test_git_failure_cannot_report_a_clean_repository(self):
        with mock.patch.object(CHECKER.subprocess, "check_output", side_effect=OSError("git failed")):
            with self.assertRaisesRegex(OSError, "git failed"):
                CHECKER.check_repository(ROOT)


if __name__ == "__main__":
    unittest.main()
