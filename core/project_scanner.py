"""Scans a project folder and builds a context summary for Daily mode."""

import os

_IGNORE_DIRS = {
    ".git", ".idea", ".vscode", "__pycache__", "node_modules",
    ".next", "dist", "build", "venv", ".venv", "env", ".env",
    ".tox", ".mypy_cache", ".pytest_cache", "target", "bin", "obj",
}

_KEY_FILES = {
    "README.md", "readme.md", "README.rst",
    "package.json", "pyproject.toml", "setup.py", "setup.cfg",
    "Cargo.toml", "go.mod", "pom.xml", "build.gradle",
    "requirements.txt", "Pipfile", "composer.json", "Gemfile",
    "Makefile", "Dockerfile", "docker-compose.yml",
    ".env.example", "CLAUDE.md",
}

_MAX_FILE_READ = 4000
_MAX_TOTAL_CONTEXT = 12000


def scan_project(path: str) -> str:
    """Return a text summary of the project at *path*."""
    if not path or not os.path.isdir(path):
        return ""

    parts: list[str] = []

    # 1. Project name
    parts.append(f"PROJECT: {os.path.basename(path)}")

    # 2. Directory tree (depth 2)
    tree = _build_tree(path, depth=2)
    if tree:
        parts.append(f"STRUCTURE:\n{tree}")

    # 3. Key files content
    key_contents = _read_key_files(path)
    if key_contents:
        parts.append(key_contents)

    summary = "\n\n".join(parts)
    if len(summary) > _MAX_TOTAL_CONTEXT:
        summary = summary[:_MAX_TOTAL_CONTEXT] + "\n...(truncated)"
    return summary


def _build_tree(root: str, depth: int) -> str:
    lines: list[str] = []
    _walk(root, "", lines, depth, 0)
    return "\n".join(lines)


def _walk(path: str, prefix: str, lines: list[str], max_depth: int, current: int):
    if current > max_depth:
        return
    try:
        entries = sorted(os.listdir(path))
    except PermissionError:
        return

    dirs = [e for e in entries if os.path.isdir(os.path.join(path, e)) and e not in _IGNORE_DIRS and not e.startswith(".")]
    files = [e for e in entries if os.path.isfile(os.path.join(path, e))]

    for f in files:
        lines.append(f"{prefix}{f}")
    for d in dirs:
        lines.append(f"{prefix}{d}/")
        _walk(os.path.join(path, d), prefix + "  ", lines, max_depth, current + 1)


def _read_key_files(root: str) -> str:
    parts: list[str] = []
    total = 0
    for name in _KEY_FILES:
        fp = os.path.join(root, name)
        if os.path.isfile(fp):
            try:
                with open(fp, "r", encoding="utf-8", errors="ignore") as f:
                    content = f.read(_MAX_FILE_READ)
                if content.strip():
                    section = f"--- {name} ---\n{content}"
                    if total + len(section) > _MAX_TOTAL_CONTEXT:
                        break
                    parts.append(section)
                    total += len(section)
            except (OSError, UnicodeDecodeError):
                continue
    if parts:
        return "KEY FILES:\n" + "\n\n".join(parts)
    return ""
