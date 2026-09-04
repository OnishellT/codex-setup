#!/usr/bin/env python3
"""Configure and run Qlty without changing reviewed source code."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys


COMMIT_MESSAGE = "Configure Qlty analysis"


def run(command, repo, check=False):
    result = subprocess.run(command, cwd=str(repo), text=True, capture_output=True,
                            env={**os.environ, "QLTY_TELEMETRY": "off"})
    if check and result.returncode:
        raise RuntimeError((result.stderr or result.stdout).strip() or "command failed")
    return result


def git(repo, *args, check=True):
    return run(["git", "-C", str(repo), *args], repo, check)


def repository(value):
    path = Path(value)
    if not path.is_absolute():
        raise RuntimeError("--repo debe ser una ruta absoluta")
    repo = path.resolve()
    if not repo.is_dir():
        raise RuntimeError("--repo debe ser un directorio existente")
    top = git(repo, "rev-parse", "--show-toplevel", check=False)
    if top.returncode or Path(top.stdout.strip()).resolve() != repo:
        raise RuntimeError("--repo debe ser la raíz de un repositorio Git")
    return repo


def qlty():
    executable = shutil.which("qlty")
    if not executable:
        raise RuntimeError("qlty no está disponible en PATH")
    return executable


def qlty_run(executable, repo, *args):
    return run([executable, "--no-upgrade-check", *args], repo)


def qlty_version(executable, repo):
    result = qlty_run(executable, repo, "--version")
    if result.returncode:
        raise RuntimeError((result.stderr or result.stdout).strip() or "no se pudo obtener la versión de qlty")
    return result.stdout.strip()


def validate(executable, repo):
    result = qlty_run(executable, repo, "config", "validate")
    if result.returncode:
        raise RuntimeError((result.stderr or result.stdout).strip() or "la configuración de qlty no es válida")


def clean(repo):
    return not git(repo, "status", "--porcelain=v1", "--untracked-files=all").stdout


def changed_paths(repo):
    raw = git(repo, "status", "--porcelain=v1", "-z", "--untracked-files=all").stdout
    entries = raw.split("\0")
    paths = []
    for entry in entries:
        if not entry:
            continue
        status, path = entry[:2], entry[3:]
        # Renames have a second NUL-delimited pathname; the checkout was clean,
        # so they are always rejected before it matters.
        paths.append((status, path))
    return paths


def branch_source(config):
    text = config.read_text(encoding="utf-8")
    # A branch pin makes an external source non-reproducible.  Qlty's source
    # tables use this key; accept tags or immutable revisions instead.
    return bool(re.search(r"(?mi)^\s*branch\s*=", text))


def setup(args):
    repo = repository(args.repo)
    executable = qlty()
    config = repo / ".qlty" / "qlty.toml"
    version = qlty_version(executable, repo)
    if config.exists():
        before = config.read_bytes()
        validate(executable, repo)
        if config.read_bytes() != before:
            raise RuntimeError("la validación modificó .qlty/qlty.toml")
        if branch_source(config):
            raise RuntimeError("la configuración usa una fuente externa basada en branch")
        print_json({"config": str(config), "repo": str(repo), "status": "existing", "version": version})
        return
    if not clean(repo):
        raise RuntimeError("el checkout debe estar limpio antes de inicializar qlty")
    result = qlty_run(executable, repo, "init", "--no")
    if result.returncode:
        raise RuntimeError((result.stderr or result.stdout).strip() or "qlty init falló")
    changes = changed_paths(repo)
    if not changes or any(status != "??" or not path.startswith(".qlty/") for status, path in changes):
        raise RuntimeError("qlty init dejó cambios fuera de .qlty o modificó archivos existentes")
    if not config.is_file():
        raise RuntimeError("qlty init no creó .qlty/qlty.toml")
    validate(executable, repo)
    if branch_source(config):
        raise RuntimeError("la configuración generada usa una fuente externa basada en branch")
    paths = [path for _, path in changes]
    git(repo, "add", "--", *paths)
    staged = git(repo, "diff", "--cached", "--name-only").stdout.splitlines()
    if sorted(staged) != sorted(paths) or any(not path.startswith(".qlty/") for path in staged):
        raise RuntimeError("sólo se pueden confirmar archivos nuevos dentro de .qlty")
    committed = git(repo, "commit", "-m", COMMIT_MESSAGE, "--only", "--", *paths, check=False)
    if committed.returncode:
        raise RuntimeError((committed.stderr or committed.stdout).strip() or "no se pudo confirmar la configuración de qlty")
    if not clean(repo):
        raise RuntimeError("el checkout no quedó limpio después de configurar qlty")
    print_json({"config": str(config), "repo": str(repo), "status": "configured", "version": version})


def parsed(result):
    try:
        return normalized(json.loads(result.stdout)), True
    except json.JSONDecodeError:
        return {"raw": result.stdout}, False


def normalized(value, path=()):
    if isinstance(value, dict):
        return {key: normalized(item, path + (key,)) for key, item in value.items()
                if not (key == "invocations" and path == ("runs",))
                and not (key == "notifications" and path[-2:] == ("tool", "driver"))}
    if isinstance(value, list):
        return [normalized(item, path) for item in value]
    return value


def has_results(payload):
    return isinstance(payload, dict) and any(
        run.get("results") for run in payload.get("runs", []) if isinstance(run, dict)
    )


def command(executable, repo, args):
    result = qlty_run(executable, repo, *args)
    payload, json_valid = parsed(result)
    status = "ok" if result.returncode == 0 and json_valid else "failed"
    return {"args": ["qlty", "--no-upgrade-check", *args], "json_valid": json_valid,
            "payload": payload, "returncode": result.returncode, "status": status,
            "stderr": result.stderr if status == "failed" else ""}


def print_json(value, output=None):
    text = json.dumps(value, indent=2, sort_keys=True) + "\n"
    if output:
        Path(output).write_text(text, encoding="utf-8")
    sys.stdout.write(text)


def output_path(value, repo):
    if not value:
        return None
    path = Path(value).resolve()
    if path == repo or repo in path.parents:
        raise RuntimeError("--output debe estar fuera del repositorio")
    return path


def scan(args):
    repo = repository(args.repo)
    executable = qlty()
    output = output_path(args.output, repo)
    if not clean(repo):
        raise RuntimeError("el checkout debe estar limpio antes de escanear qlty")
    config = repo / ".qlty" / "qlty.toml"
    if not config.is_file():
        raise RuntimeError("falta .qlty/qlty.toml")
    upstream = git(repo, "rev-parse", "--verify", args.upstream + "^{commit}", check=False)
    if upstream.returncode:
        raise RuntimeError("el upstream no existe: " + args.upstream)
    validate(executable, repo)
    report = {"commands": [], "config_sha256": hashlib.sha256(config.read_bytes()).hexdigest(),
              "head": git(repo, "rev-parse", "HEAD").stdout.strip(), "repo": str(repo), "schema": 1,
              "upstream_sha": upstream.stdout.strip(),
              "upstream": args.upstream, "version": qlty_version(executable, repo)}
    checks = [
        ("check", ["check", "--no-fix", "--no-progress", "--upstream", args.upstream, "--sarif"]),
        ("smells", ["smells", "--upstream", args.upstream, "--no-snippets", "--sarif"]),
        ("metrics", ["metrics", "--functions", "--upstream", args.upstream, "--quiet", "--json"]),
    ]
    failed = False
    for name, command_args in checks:
        item = command(executable, repo, command_args)
        item["name"] = name
        report["commands"].append(item)
        if name == "smells" and has_results(item["payload"]):
            item["status"] = "failed"
        failed |= item["status"] != "ok"
    report["status"] = "failed" if failed else "ok"
    print_json(report, output)
    if failed:
        raise RuntimeError("el escaneo de qlty falló o detectó smells")


def main():
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="action", required=True)
    setup_parser = commands.add_parser("setup")
    setup_parser.add_argument("--repo", required=True)
    scan_parser = commands.add_parser("scan")
    scan_parser.add_argument("--repo", required=True)
    scan_parser.add_argument("--upstream", required=True)
    scan_parser.add_argument("--output")
    args = parser.parse_args()
    try:
        {"setup": setup, "scan": scan}[args.action](args)
    except RuntimeError as error:
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
