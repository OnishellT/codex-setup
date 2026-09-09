#!/usr/bin/env python3
"""Create isolated Git worktrees, then integrate their commits conservatively."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


GITIGNORE = ".env\n.env.*\n!.env.example\n!.env.*.example\n__pycache__/\n"


def git(repo, *args, check=True):
    result = subprocess.run(["git", "-C", str(repo), *args], text=True,
                            capture_output=True)
    if check and result.returncode:
        raise RuntimeError((result.stderr or result.stdout).strip())
    return result


def git_identity(repo):
    for key in ("user.name", "user.email"):
        value = git(repo, "config", "--get", key, check=False).stdout.strip()
        if not value:
            raise RuntimeError("falta identidad Git: configura user.name y user.email antes de preparar el repositorio")


def git_marker(path):
    while True:
        marker = path / ".git"
        if marker.exists() or marker.is_symlink():
            return True
        if path.parent == path:
            return False
        path = path.parent


def prepare(args):
    supplied = Path(args.repo)
    if not supplied.is_absolute():
        raise RuntimeError("--repo debe ser una ruta absoluta")
    repo = supplied.resolve()
    if not repo.is_dir():
        raise RuntimeError("--repo debe ser un directorio existente")
    bare = git(repo, "rev-parse", "--is-bare-repository", check=False)
    if bare.returncode == 0 and bare.stdout.strip() == "true":
        raise RuntimeError("los repositorios bare no son compatibles con worktrees")
    top = git(repo, "rev-parse", "--show-toplevel", check=False)
    if top.returncode == 0:
        root = Path(top.stdout.strip()).resolve()
        if git(root, "rev-parse", "--is-bare-repository").stdout.strip() == "true":
            raise RuntimeError("los repositorios bare no son compatibles con worktrees")
        if git(root, "rev-parse", "--verify", "HEAD", check=False).returncode == 0:
            print(json.dumps({"repo": str(root), "status": "existing"}, sort_keys=True))
            return
        if root != repo:
            raise RuntimeError("repositorio Git sin commit: ejecuta prepare en su raíz antes de crear worktrees")
        entries = [path for path in root.iterdir() if path.name != ".git"]
        if any(path.name != ".gitignore" for path in entries):
            raise RuntimeError("repositorio Git sin commit contiene archivos; inspecciona y autoriza una línea base inicial")
        if (entries and (entries[0].is_symlink() or not entries[0].is_file() or
                        entries[0].read_text() != GITIGNORE)):
            raise RuntimeError("repositorio Git sin commit contiene archivos; inspecciona y autoriza una línea base inicial")
        staged = git(root, "ls-files", "--cached").stdout.splitlines()
        if any(path != ".gitignore" for path in staged):
            raise RuntimeError("repositorio Git sin commit contiene archivos; inspecciona y autoriza una línea base inicial")
        cached_ignore = git(root, "show", ":.gitignore", check=False)
        if cached_ignore.returncode == 0 and cached_ignore.stdout != GITIGNORE:
            raise RuntimeError("repositorio Git sin commit contiene archivos; inspecciona y autoriza una línea base inicial")
    else:
        if git_marker(repo):
            raise RuntimeError("metadatos Git inválidos; inspecciona y repara .git antes de preparar")
        if any(repo.iterdir()):
            raise RuntimeError("directorio sin Git no vacío; inspecciona y autoriza una línea base inicial")
        git_identity(repo)
        git(repo, "init", "-q")
        root = repo
        entries = []
    git_identity(root)
    ignore = root / ".gitignore"
    if not ignore.exists():
        ignore.write_text(GITIGNORE)
    git(root, "add", "--", ".gitignore")
    result = git(root, "commit", "-qm", "Initial commit", "--only", "--", ".gitignore", check=False)
    if result.returncode:
        raise RuntimeError("no se pudo crear el commit inicial: " + (result.stderr or result.stdout).strip())
    print(json.dumps({"repo": str(root), "status": "bootstrapped"}, sort_keys=True))


def clean(repo):
    return not git(repo, "status", "--porcelain=v1", "--untracked-files=all").stdout.strip()


def operation(repo):
    for name in ("MERGE_HEAD", "CHERRY_PICK_HEAD", "REBASE_HEAD", "REVERT_HEAD",
                 "rebase-merge", "rebase-apply", "sequencer"):
        path = Path(git(repo, "rev-parse", "--git-path", name).stdout.strip())
        if not path.is_absolute():
            path = Path(repo) / path
        if path.exists():
            return name
    return None


def require_ready(repo):
    if not clean(repo):
        raise RuntimeError("el checkout debe estar limpio, incluidos archivos no rastreados")
    if active := operation(repo):
        raise RuntimeError("operación Git en curso: " + active)


def tracked(repo, path):
    return git(repo, "ls-files", "--error-unmatch", "--", str(path), check=False).returncode == 0


def instruction_warnings(repo):
    warnings = []
    for path in repo.rglob("AGENTS.md"):
        if ".git" not in path.parts and not tracked(repo, path.relative_to(repo)):
            warnings.append("instrucción no rastreada no estará en worktrees: " + str(path))
    for path in repo.rglob("AGENTS.override.md"):
        if ".git" not in path.parts and not tracked(repo, path.relative_to(repo)):
            warnings.append("instrucción no rastreada no estará en worktrees: " + str(path))
    return warnings


def write_manifest(path, value):
    temp = path.with_suffix(".tmp")
    temp.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
    temp.replace(path)


def create(args):
    repo = Path(args.repo).resolve()
    top = Path(git(repo, "rev-parse", "--show-toplevel").stdout.strip()).resolve()
    if top != repo:
        raise RuntimeError("--repo debe ser la raíz del repositorio Git")
    require_ready(repo)
    branch = git(repo, "symbolic-ref", "--quiet", "--short", "HEAD", check=False).stdout.strip()
    if not branch:
        raise RuntimeError("HEAD detached: selecciona una rama antes de crear worktrees")
    if (repo / ".gitmodules").exists() or git(repo, "submodule", "status").stdout.strip():
        raise RuntimeError("submódulos no soportados por este helper")
    limit = min(args.max_workers, 4)
    if not 1 <= args.workers <= limit:
        raise RuntimeError("workers debe estar entre 1 y %d" % limit)
    base = git(repo, "rev-parse", "HEAD").stdout.strip()
    if args.session_parent:
        parent = Path(args.session_parent).resolve()
        private = False
    else:
        codex_home = Path(os.environ.get("CODEX_HOME") or (Path.home() / ".codex")).resolve()
        parent = codex_home / "worktrees" / "prewalk"
        if parent.exists() and parent.is_symlink():
            raise RuntimeError("la raíz privada de sesiones no puede ser un enlace simbólico")
        parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        private = True
    if not parent.is_dir() or parent == repo or repo in parent.parents:
        raise RuntimeError("--session-parent debe existir y quedar fuera del checkout")
    if private and parent.stat().st_mode & 0o077:
        raise RuntimeError("la raíz privada de sesiones debe ser accesible sólo por el usuario")
    session = Path(tempfile.mkdtemp(prefix="codex-prewalk-", dir=str(parent))).resolve()
    token = session.name
    workers = []
    try:
        for number in range(1, args.workers + 1):
            name = "worker-%d" % number
            path = session / name
            worker_branch = "codex-prewalk/%s/%s" % (token, name)
            git(repo, "worktree", "add", "-b", worker_branch, str(path), base)
            workers.append({"name": name, "path": str(path), "branch": worker_branch})
        integration = session / "integration"
        integration_branch = "codex-prewalk/%s/integration" % token
        git(repo, "worktree", "add", "-b", integration_branch, str(integration), base)
    except Exception as error:
        raise RuntimeError("%s; creación incompleta preservada en %s: revisa y elimina manualmente" %
                           (error, session)) from error
    manifest = {
        "version": 1, "repo": str(repo), "base": base, "branch": branch,
        "session": str(session), "workers": workers,
        "integration": {"path": str(integration), "branch": integration_branch},
        "warnings": instruction_warnings(repo), "integrated": {},
    }
    path = session / "manifest.json"
    write_manifest(path, manifest)
    print(json.dumps({"manifest": str(path), **manifest}, sort_keys=True))


def load(path):
    raw_path = Path(path)
    if raw_path.is_symlink():
        raise RuntimeError("manifest enlazado no permitido")
    path = raw_path.resolve()
    value = json.loads(path.read_text())
    required = {"version", "repo", "base", "branch", "session", "workers", "integration", "integrated"}
    if (not isinstance(value, dict) or value.get("version") != 1 or
            not required <= value.keys() or not isinstance(value["workers"], list) or
            not isinstance(value["integration"], dict) or not isinstance(value["integrated"], dict)):
        raise RuntimeError("manifest inválido")
    if not all(isinstance(value[key], str) for key in ("repo", "base", "branch", "session")):
        raise RuntimeError("manifest inválido")
    raw_session = Path(value["session"])
    if raw_session.is_symlink():
        raise RuntimeError("sesión enlazada no permitida")
    session = raw_session.resolve()
    if path.parent != session or not session.is_dir() or path.is_symlink():
        raise RuntimeError("manifest fuera de su sesión")
    prefix = "codex-prewalk/%s/" % session.name
    names = set()
    if not 1 <= len(value["workers"]) <= 4:
        raise RuntimeError("workers inválidos en manifest")
    for item in value["workers"]:
        if (not isinstance(item, dict) or not {"name", "path", "branch"} <= item.keys() or
                not all(isinstance(item[key], str) for key in ("name", "path", "branch")) or
                not re.fullmatch(r"worker-[1-4]", item["name"]) or
                item["name"] in names):
            raise RuntimeError("ruta de worktree inválida en manifest")
        names.add(item["name"])
        raw_worker = Path(item["path"])
        if (raw_worker.is_symlink() or raw_worker.resolve() != session / item["name"] or
                item["branch"] != prefix + item["name"]):
            raise RuntimeError("identidad de worker inválida en manifest")
    integration = value["integration"]
    if (not {"path", "branch"} <= integration.keys() or
            not all(isinstance(integration[key], str) for key in ("path", "branch"))):
        raise RuntimeError("identidad de integración inválida en manifest")
    raw_integration = Path(integration["path"])
    if (raw_integration.is_symlink() or
            raw_integration.resolve() != session / "integration" or
            integration["branch"] != prefix + "integration"):
        raise RuntimeError("identidad de integración inválida en manifest")
    if names != {"worker-%d" % number for number in range(1, len(names) + 1)}:
        raise RuntimeError("workers inválidos en manifest")
    if (not set(value["integrated"]) <= names or
            any(not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40,64}", commit)
                for commit in value["integrated"].values())):
        raise RuntimeError("integración inválida en manifest")
    return path, value


def check_worktree(item, repo, base):
    path = Path(item["path"]).resolve()
    if Path(git(path, "rev-parse", "--show-toplevel").stdout.strip()).resolve() != path:
        raise RuntimeError("worktree inválido: " + str(path))
    if git(path, "branch", "--show-current").stdout.strip() != item["branch"]:
        raise RuntimeError("rama inesperada en " + str(path))
    common = Path(git(path, "rev-parse", "--git-common-dir").stdout.strip())
    if not common.is_absolute():
        common = path / common
    expected_common = Path(git(repo, "rev-parse", "--git-common-dir").stdout.strip())
    if not expected_common.is_absolute():
        expected_common = Path(repo) / expected_common
    if common.resolve() != expected_common.resolve():
        raise RuntimeError("worktree pertenece a otro repositorio: " + str(path))
    require_ready(path)
    if git(path, "merge-base", "--is-ancestor", base, "HEAD", check=False).returncode:
        raise RuntimeError("historia de worker no desciende de base: " + str(path))
    if git(path, "rev-list", "--merges", base + "..HEAD").stdout.strip():
        raise RuntimeError("worker contiene merge commits: " + str(path))
    return path


def overlapping_paths(workers, base):
    owners = {}
    for worker, path in workers:
        changed = git(path, "diff", "--name-only", "-z", "--no-renames",
                      base + "..HEAD").stdout.split("\0")
        for name in filter(None, changed):
            owners.setdefault(name, []).append(worker["name"])
    return {name: names for name, names in owners.items() if len(names) > 1}


def integrate(args):
    manifest_path, manifest = load(args.manifest)
    repo = Path(manifest["repo"]).resolve()
    if Path(git(repo, "rev-parse", "--show-toplevel").stdout.strip()).resolve() != repo:
        raise RuntimeError("repositorio del manifest no coincide")
    integration = check_worktree(manifest["integration"], repo, manifest["base"])
    workers = [(worker, check_worktree(worker, repo, manifest["base"]))
               for worker in manifest["workers"]]
    overlaps = overlapping_paths(workers, manifest["base"])
    if overlaps:
        details = ", ".join("%s (%s)" % (name, ", ".join(names))
                            for name, names in sorted(overlaps.items()))
        raise RuntimeError("solapamiento entre workers antes de integrar: " + details)
    for worker, path in workers:
        commits = git(path, "rev-list", "--reverse", manifest["base"] + "..HEAD").stdout.split()
        pending_hashes = {line[2:] for line in
                          git(integration, "cherry", "HEAD", worker["branch"]).stdout.splitlines()
                          if line.startswith("+ ")}
        pending = [commit for commit in commits if commit in pending_hashes]
        if pending:
            result = git(integration, "cherry-pick", *pending, check=False)
            if result.returncode:
                raise RuntimeError("conflicto al integrar %s; se preserva para resolver manualmente: %s" %
                                   (worker["name"], (result.stderr or result.stdout).strip()))
        manifest["integrated"][worker["name"]] = git(path, "rev-parse", "HEAD").stdout.strip()
        write_manifest(manifest_path, manifest)
    print(json.dumps({"integration": str(integration), "head": git(integration, "rev-parse", "HEAD").stdout.strip()}))


def finish(args):
    _, manifest = load(args.manifest)
    repo = Path(manifest["repo"]).resolve()
    integration = check_worktree(manifest["integration"], repo, manifest["base"])
    for worker in manifest["workers"]:
        path = check_worktree(worker, repo, manifest["base"])
        head = git(path, "rev-parse", "HEAD").stdout.strip()
        if manifest["integrated"].get(worker["name"]) != head:
            raise RuntimeError("worker sin integrar: " + worker["name"])
    require_ready(repo)
    if git(repo, "branch", "--show-current").stdout.strip() != manifest["branch"]:
        raise RuntimeError("la rama original cambió; no se puede fast-forward")
    original_head = git(repo, "rev-parse", "HEAD").stdout.strip()
    integration_head = git(integration, "rev-parse", "HEAD").stdout.strip()
    if original_head == integration_head:
        print(json.dumps({"repo": str(repo), "head": original_head}))
        return
    if original_head != manifest["base"]:
        raise RuntimeError("HEAD original cambió; no se puede fast-forward")
    result = git(repo, "merge", "--ff-only", manifest["integration"]["branch"], check=False)
    if result.returncode:
        raise RuntimeError("fast-forward rechazado: " + (result.stderr or result.stdout).strip())
    print(json.dumps({"repo": str(repo), "head": git(repo, "rev-parse", "HEAD").stdout.strip()}))


def main():
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    prepare_parser = commands.add_parser("prepare")
    prepare_parser.add_argument("--repo", required=True)
    create_parser = commands.add_parser("create")
    create_parser.add_argument("--repo", required=True)
    create_parser.add_argument("--workers", type=int, required=True)
    create_parser.add_argument("--max-workers", type=int, default=4)
    create_parser.add_argument("--session-parent")
    integrate_parser = commands.add_parser("integrate")
    integrate_parser.add_argument("--manifest", required=True)
    finish_parser = commands.add_parser("finish")
    finish_parser.add_argument("--manifest", required=True)
    args = parser.parse_args()
    {"prepare": prepare, "create": create, "integrate": integrate, "finish": finish}[args.command](args)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError) as error:
        print("worktrees: " + str(error), file=sys.stderr)
        sys.exit(1)
