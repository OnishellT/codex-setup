import json
import hashlib
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


HELPER = Path(__file__).parents[1] / "prewalk" / "quality.py"


def run(*args, cwd=None, check=True, env=None):
    result = subprocess.run(args, cwd=cwd, text=True, capture_output=True,
                            env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1", **(env or {})})
    if check and result.returncode:
        raise AssertionError(result.stderr)
    return result


class QualityTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.repo = Path(self.temp.name) / "repo"
        self.repo.mkdir()
        run("git", "init", "-q", "-b", "main", cwd=self.repo)
        run("git", "config", "user.email", "test@example.invalid", cwd=self.repo)
        run("git", "config", "user.name", "Test", cwd=self.repo)
        (self.repo / "app.txt").write_text("base\n")
        run("git", "add", "app.txt", cwd=self.repo)
        run("git", "commit", "-qm", "base", cwd=self.repo)
        self.bin = Path(self.temp.name) / "bin"
        self.bin.mkdir()
        self.log = Path(self.temp.name) / "qlty.log"
        fake = self.bin / "qlty"
        fake.write_text("""#!/usr/bin/env python3
import json, os, pathlib, sys
log = pathlib.Path(os.environ['QLTY_LOG'])
log.open('a').write(json.dumps({'args': sys.argv[1:], 'telemetry': os.environ.get('QLTY_TELEMETRY')}) + '\\n')
args = sys.argv[1:]
if args == ['--no-upgrade-check', '--version']:
 print('qlty test 1'); sys.exit()
if args == ['--no-upgrade-check', 'config', 'validate']:
 sys.exit(int(os.environ.get('QLTY_VALIDATE_FAIL', '0')))
if args == ['--no-upgrade-check', 'init', '--no']:
 root = pathlib.Path.cwd(); (root / '.qlty').mkdir();
 (root / '.qlty' / 'qlty.toml').write_text(os.environ.get('QLTY_CONFIG', 'config_version = "1"\\n'))
 if os.environ.get('QLTY_OUTSIDE'): (root / 'outside.txt').write_text('bad')
 sys.exit()
if os.environ.get('QLTY_INVALID_JSON') and any(name in args for name in ('check', 'smells', 'metrics')):
 print('{not json'); sys.exit()
if 'smells' in args:
 print(json.dumps({'runs': [{'results': [{}] if os.environ.get('QLTY_SMELLS') else []}]})); sys.exit()
if 'metrics' in args and os.environ.get('QLTY_METRICS_FAIL'): sys.exit(3)
if 'check' in args and os.environ.get('QLTY_CHECK_FAIL'): sys.exit(2)
print(json.dumps({'runs': []} if 'sarif' in args else {'functions': []}))
""")
        fake.chmod(0o755)

    def tearDown(self):
        self.temp.cleanup()

    def helper(self, *args, check=True, env=None):
        environment = {"PATH": str(self.bin) + os.pathsep + os.environ["PATH"], "QLTY_LOG": str(self.log)}
        environment.update(env or {})
        return run(sys.executable, str(HELPER), *args, check=check, env=environment)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def test_existing_config_is_preserved_and_validated(self):
        config = self.repo / ".qlty" / "qlty.toml"
        config.parent.mkdir()
        config.write_bytes(b"config_version = '1'\n# preserve\n")
        before = config.read_bytes()
        result = self.helper("setup", "--repo", str(self.repo))
        self.assertEqual(config.read_bytes(), before)
        self.assertEqual(json.loads(result.stdout)["status"], "existing")
        self.assertIn(["--no-upgrade-check", "config", "validate"], [call["args"] for call in self.calls()])
        config.write_text("[[sources]]\nbranch = 'main'\n")
        self.assertNotEqual(self.helper("setup", "--repo", str(self.repo), check=False).returncode, 0)
        self.assertEqual(config.read_text(), "[[sources]]\nbranch = 'main'\n")

    def test_init_validates_and_commits_only_qlty(self):
        result = self.helper("setup", "--repo", str(self.repo))
        self.assertEqual(json.loads(result.stdout)["status"], "configured")
        self.assertEqual(run("git", "log", "-1", "--format=%s", cwd=self.repo).stdout.strip(), "Configure Qlty analysis")
        self.assertEqual(run("git", "show", "--format=", "--name-only", "HEAD", cwd=self.repo).stdout.splitlines(), [".qlty/qlty.toml"])
        self.assertFalse(run("git", "status", "--porcelain", cwd=self.repo).stdout)
        self.assertTrue(all(call["telemetry"] == "off" for call in self.calls()))

    def test_rejects_branch_sources_and_changes_outside_qlty(self):
        bad = self.helper("setup", "--repo", str(self.repo), check=False,
                          env={"QLTY_CONFIG": "[[sources]]\nbranch = 'main'\n"})
        self.assertNotEqual(bad.returncode, 0)
        self.assertEqual(run("git", "rev-list", "--count", "HEAD", cwd=self.repo).stdout.strip(), "1")
        self.temp.cleanup()
        self.setUp()
        outside = self.helper("setup", "--repo", str(self.repo), check=False, env={"QLTY_OUTSIDE": "1"})
        self.assertNotEqual(outside.returncode, 0)
        self.assertTrue((self.repo / "outside.txt").exists())

    def test_scan_uses_exact_arguments_and_stable_report(self):
        self.helper("setup", "--repo", str(self.repo))
        output = Path(self.temp.name) / "report.json"
        result = self.helper("scan", "--repo", str(self.repo), "--upstream", "main", "--output", str(output))
        report = json.loads(result.stdout)
        self.assertEqual(report, json.loads(output.read_text()))
        self.assertEqual(report["status"], "ok")
        self.assertEqual(report["head"], run("git", "rev-parse", "HEAD", cwd=self.repo).stdout.strip())
        self.assertEqual(report["upstream_sha"], report["head"])
        self.assertEqual(report["config_sha256"], hashlib.sha256((self.repo / ".qlty" / "qlty.toml").read_bytes()).hexdigest())
        self.assertEqual([item["args"] for item in report["commands"]], [
            ["qlty", "--no-upgrade-check", "check", "--no-fix", "--no-progress", "--upstream", "main", "--sarif"],
            ["qlty", "--no-upgrade-check", "smells", "--upstream", "main", "--no-snippets", "--sarif"],
            ["qlty", "--no-upgrade-check", "metrics", "--functions", "--upstream", "main", "--quiet", "--json"],
        ])

    def test_scan_failures_do_not_pass(self):
        self.helper("setup", "--repo", str(self.repo))
        self.assertNotEqual(self.helper("scan", "--repo", str(self.repo), "--upstream", "main", check=False,
                                        env={"QLTY_CHECK_FAIL": "1"}).returncode, 0)
        self.assertNotEqual(self.helper("scan", "--repo", str(self.repo), "--upstream", "main", check=False,
                                        env={"QLTY_SMELLS": "1"}).returncode, 0)
        self.assertNotEqual(self.helper("scan", "--repo", str(self.repo), "--upstream", "main", check=False,
                                        env={"QLTY_METRICS_FAIL": "1"}).returncode, 0)
        invalid = self.helper("scan", "--repo", str(self.repo), "--upstream", "main", check=False,
                              env={"QLTY_INVALID_JSON": "1"})
        self.assertNotEqual(invalid.returncode, 0)
        report = json.loads(invalid.stdout)
        self.assertEqual(report["status"], "failed")
        self.assertTrue(all(not item["json_valid"] and item["status"] == "failed"
                            for item in report["commands"]))
        self.assertNotEqual(self.helper("scan", "--repo", str(self.repo), "--upstream", "missing", check=False).returncode, 0)

    def test_scan_rejects_output_inside_repo(self):
        self.helper("setup", "--repo", str(self.repo))
        result = self.helper("scan", "--repo", str(self.repo), "--upstream", "main", "--output",
                             str(self.repo / "report.json"), check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.repo / "report.json").exists())


if __name__ == "__main__":
    unittest.main()
