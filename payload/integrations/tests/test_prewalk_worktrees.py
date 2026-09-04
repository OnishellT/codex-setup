import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


HELPER = Path(__file__).parents[1] / "prewalk" / "worktrees.py"


def run(*args, cwd=None, check=True, env=None):
    result = subprocess.run(args, cwd=cwd, text=True, capture_output=True,
                            env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1", **(env or {})})
    if check and result.returncode:
        raise AssertionError(result.stderr)
    return result


class WorktreeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.repo = Path(self.temp.name) / "repo"
        self.repo.mkdir()
        run("git", "init", "-q", "-b", "main", cwd=self.repo)
        run("git", "config", "user.email", "test@example.invalid", cwd=self.repo)
        run("git", "config", "user.name", "Test", cwd=self.repo)
        (self.repo / "app.txt").write_text("base\n")
        run("git", "add", ".", cwd=self.repo)
        run("git", "commit", "-qm", "base", cwd=self.repo)

    def tearDown(self):
        self.temp.cleanup()

    def helper(self, *args, check=True, env=None):
        return run(sys.executable, str(HELPER), *args, check=check, env=env)

    def identity_env(self):
        config = Path(self.temp.name) / "gitconfig"
        config.write_text("[user]\n\tname = Test\n\temail = test@example.invalid\n")
        return {"GIT_CONFIG_GLOBAL": str(config), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_COUNT": "0"}

    def create(self):
        result = self.helper("create", "--repo", str(self.repo), "--workers", "2")
        return json.loads(result.stdout)

    def commit(self, path, name, text, env=None):
        path = Path(path)
        (path / name).write_text(text)
        run("git", "add", name, cwd=path, env=env)
        run("git", "commit", "-qm", name, cwd=path, env=env)

    def test_create_integrate_finish_and_rerun(self):
        data = self.create()
        self.commit(data["workers"][0]["path"], "one.txt", "one\n")
        self.commit(data["workers"][0]["path"], "one-more.txt", "one more\n")
        self.commit(data["workers"][1]["path"], "two.txt", "two\n")
        manifest = data["manifest"]
        self.assertNotEqual(self.helper("finish", "--manifest", manifest, check=False).returncode, 0)
        self.helper("integrate", "--manifest", manifest)
        self.helper("integrate", "--manifest", manifest)
        self.commit(data["workers"][0]["path"], "three.txt", "three\n")
        self.helper("integrate", "--manifest", manifest)
        self.helper("finish", "--manifest", manifest)
        self.helper("finish", "--manifest", manifest)
        self.assertEqual((self.repo / "one.txt").read_text(), "one\n")
        self.assertEqual((self.repo / "one-more.txt").read_text(), "one more\n")
        self.assertEqual((self.repo / "two.txt").read_text(), "two\n")
        self.assertEqual((self.repo / "three.txt").read_text(), "three\n")

    def test_rejects_dirty_and_changed_base(self):
        (self.repo / "untracked").write_text("x")
        self.assertNotEqual(self.helper("create", "--repo", str(self.repo), "--workers", "2", check=False).returncode, 0)
        (self.repo / "untracked").unlink()
        data = self.create()
        (self.repo / "later.txt").write_text("later")
        run("git", "add", ".", cwd=self.repo)
        run("git", "commit", "-qm", "later", cwd=self.repo)
        self.assertNotEqual(self.helper("finish", "--manifest", data["manifest"], check=False).returncode, 0)

    def test_allows_three_and_four_workers_but_rejects_more_than_four(self):
        for workers in (3, 4):
            with self.subTest(workers=workers):
                data = json.loads(self.helper("create", "--repo", str(self.repo),
                                              "--workers", str(workers),
                                              "--max-workers", "9").stdout)
                self.assertEqual([worker["name"] for worker in data["workers"]],
                                 ["worker-%d" % number for number in range(1, workers + 1)])
        self.assertNotEqual(self.helper("create", "--repo", str(self.repo), "--workers", "5",
                                        "--max-workers", "9", check=False).returncode, 0)

    def test_warns_for_untracked_agent_instructions(self):
        (self.repo / "AGENTS.md").write_text("local rule")
        (self.repo / ".gitignore").write_text("nested/AGENTS.override.md\n")
        run("git", "add", "AGENTS.md", ".gitignore", cwd=self.repo)
        run("git", "commit", "-qm", "tracked instructions", cwd=self.repo)
        (self.repo / "nested").mkdir()
        (self.repo / "nested" / "AGENTS.override.md").write_text("untracked rule")
        data = self.create()
        self.assertEqual(len(data["warnings"]), 1)
        self.assertIn("AGENTS.override.md", data["warnings"][0])

    def test_session_parent_stays_outside_checkout(self):
        parent = Path(self.temp.name) / "sessions"
        parent.mkdir()
        result = self.helper("create", "--repo", str(self.repo), "--workers", "2",
                             "--session-parent", str(parent))
        self.assertEqual(Path(json.loads(result.stdout)["session"]).parent, parent)
        self.assertNotEqual(self.helper("create", "--repo", str(self.repo), "--workers", "2",
                                        "--session-parent", str(self.repo), check=False).returncode, 0)

    def test_overlap_is_rejected_before_any_integration(self):
        data = self.create()
        for worker, text in zip(data["workers"], ("one\n", "two\n")):
            self.commit(worker["path"], "app.txt", text)
        result = self.helper("integrate", "--manifest", data["manifest"], check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("solapamiento", result.stderr)
        integration = Path(data["integration"]["path"])
        self.assertEqual(run("git", "rev-parse", "HEAD", cwd=integration).stdout.strip(), data["base"])
        self.assertEqual(run("git", "status", "--porcelain", cwd=integration).stdout, "")
        self.assertEqual(json.loads(Path(data["manifest"]).read_text())["integrated"], {})

    def test_non_overlapping_directory_file_conflict_is_preserved(self):
        data = self.create()
        self.commit(data["workers"][0]["path"], "target", "file\n")
        child = Path(data["workers"][1]["path"]) / "target"
        child.mkdir()
        self.commit(data["workers"][1]["path"], "target/child.txt", "child\n")
        result = self.helper("integrate", "--manifest", data["manifest"], check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("conflicto", result.stderr)
        integration = Path(data["integration"]["path"])
        self.assertTrue(run("git", "status", "--porcelain", cwd=integration).stdout)

    def test_prepare_bootstraps_empty_directory_and_is_idempotent(self):
        repo = Path(self.temp.name) / "new"
        repo.mkdir()
        first = json.loads(self.helper("prepare", "--repo", str(repo), env=self.identity_env()).stdout)
        second = json.loads(self.helper("prepare", "--repo", str(repo), env=self.identity_env()).stdout)
        self.assertEqual(first, {"repo": str(repo), "status": "bootstrapped"})
        self.assertEqual(second, {"repo": str(repo), "status": "existing"})
        self.assertEqual((repo / ".gitignore").read_text(), ".env\n.env.*\n!.env.example\n!.env.*.example\n__pycache__/\n")
        self.assertEqual(run("git", "log", "--oneline", cwd=repo).stdout.count("\n"), 1)

    def test_prepare_ignores_env_secrets_but_not_examples(self):
        repo = Path(self.temp.name) / "ignored"
        repo.mkdir()
        self.helper("prepare", "--repo", str(repo), env=self.identity_env())
        (repo / ".env").write_text("TOKEN=keep\n")
        (repo / ".env.local").write_text("TOKEN=keep\n")
        (repo / ".env.example").write_text("TOKEN=example\n")
        self.assertEqual(run("git", "check-ignore", ".env", ".env.local", cwd=repo).returncode, 0)
        self.assertNotEqual(run("git", "check-ignore", ".env.example", cwd=repo, check=False).returncode, 0)
        run("git", "add", ".env.example", cwd=repo)
        self.assertIn(".env.example", run("git", "diff", "--cached", "--name-only", cwd=repo).stdout)

    def test_prepare_recovers_unborn_empty_repo(self):
        repo = Path(self.temp.name) / "unborn"
        repo.mkdir()
        run("git", "init", "-q", cwd=repo)
        data = json.loads(self.helper("prepare", "--repo", str(repo), env=self.identity_env()).stdout)
        self.assertEqual(data, {"repo": str(repo), "status": "bootstrapped"})
        self.assertEqual(run("git", "rev-parse", "--verify", "HEAD", cwd=repo).returncode, 0)

    def test_prepare_refuses_unborn_indexed_files_and_symlink_ignore(self):
        repo = Path(self.temp.name) / "indexed"
        repo.mkdir()
        run("git", "init", "-q", cwd=repo)
        secret = repo / ".env"
        secret.write_text("TOKEN=keep\n")
        run("git", "add", ".env", cwd=repo)
        secret.unlink()
        result = self.helper("prepare", "--repo", str(repo), check=False, env=self.identity_env())
        self.assertNotEqual(result.returncode, 0)
        self.assertNotEqual(run("git", "rev-parse", "--verify", "HEAD", cwd=repo, check=False).returncode, 0)
        staged_ignore = Path(self.temp.name) / "staged-ignore"
        staged_ignore.mkdir()
        run("git", "init", "-q", cwd=staged_ignore)
        ignore = staged_ignore / ".gitignore"
        ignore.write_text("user content\n")
        run("git", "add", ".gitignore", cwd=staged_ignore)
        ignore.write_text(".env\n.env.*\n!.env.example\n!.env.*.example\n__pycache__/\n")
        result = self.helper("prepare", "--repo", str(staged_ignore), check=False, env=self.identity_env())
        self.assertNotEqual(result.returncode, 0)
        linked = Path(self.temp.name) / "linked"
        linked.mkdir()
        run("git", "init", "-q", cwd=linked)
        outside = Path(self.temp.name) / "outside-ignore"
        outside.write_text("outside\n")
        (linked / ".gitignore").symlink_to(outside)
        result = self.helper("prepare", "--repo", str(linked), check=False, env=self.identity_env())
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(outside.read_text(), "outside\n")

    def test_prepare_refuses_nested_unborn_and_broken_parent_metadata(self):
        unborn = Path(self.temp.name) / "unborn-parent"
        unborn.mkdir()
        run("git", "init", "-q", cwd=unborn)
        child = unborn / "child"
        child.mkdir()
        result = self.helper("prepare", "--repo", str(child), check=False, env=self.identity_env())
        self.assertNotEqual(result.returncode, 0)
        self.assertNotEqual(run("git", "rev-parse", "--verify", "HEAD", cwd=unborn, check=False).returncode, 0)
        broken = Path(self.temp.name) / "broken"
        broken.mkdir()
        (broken / ".git").write_text("not a gitdir\n")
        nested = broken / "child"
        nested.mkdir()
        result = self.helper("prepare", "--repo", str(nested), check=False, env=self.identity_env())
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((nested / ".git").exists())

    def test_prepare_reuses_parent_repo_and_leaves_dirty_checkout_alone(self):
        nested = self.repo / "nested"
        nested.mkdir()
        (self.repo / "dirty.txt").write_text("keep\n")
        data = json.loads(self.helper("prepare", "--repo", str(nested)).stdout)
        self.assertEqual(data, {"repo": str(self.repo), "status": "existing"})
        self.assertFalse((nested / ".git").exists())
        self.assertEqual((self.repo / "dirty.txt").read_text(), "keep\n")
        self.assertIn("?? dirty.txt", run("git", "status", "--porcelain", cwd=self.repo).stdout)

    def test_prepare_refuses_nonempty_directory_and_missing_identity(self):
        nonempty = Path(self.temp.name) / "nonempty"
        nonempty.mkdir()
        secret = nonempty / ".env"
        secret.write_text("TOKEN=keep\n")
        result = self.helper("prepare", "--repo", str(nonempty), check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no vacío", result.stderr)
        self.assertEqual(secret.read_text(), "TOKEN=keep\n")
        self.assertFalse((nonempty / ".git").exists())
        missing = Path(self.temp.name) / "missing"
        missing.mkdir()
        env = {"GIT_CONFIG_GLOBAL": str(missing / "gitconfig"), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_COUNT": "0"}
        result = self.helper("prepare", "--repo", str(missing), check=False, env=env)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("falta identidad Git", result.stderr)
        self.assertFalse((missing / ".git").exists())

    def test_prepare_create_integrate_finish_workflow(self):
        repo = Path(self.temp.name) / "workflow"
        repo.mkdir()
        env = self.identity_env()
        self.helper("prepare", "--repo", str(repo), env=env)
        data = json.loads(self.helper("create", "--repo", str(repo), "--workers", "1").stdout)
        self.commit(data["workers"][0]["path"], "done.txt", "done\n", env=env)
        self.helper("integrate", "--manifest", data["manifest"], env=env)
        self.helper("finish", "--manifest", data["manifest"], env=env)
        self.assertEqual((repo / "done.txt").read_text(), "done\n")


if __name__ == "__main__":
    unittest.main()
