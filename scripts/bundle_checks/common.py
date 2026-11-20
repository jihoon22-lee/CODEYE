"""Helpers shared by every contract check."""

from __future__ import annotations

import json
from pathlib import Path, PurePosixPath
import re

try:
    import yaml
    from jsonschema import Draft202012Validator, FormatChecker, ValidationError
    from referencing import Registry, Resource
except ImportError as exc:
    raise SystemExit("Install the approved packages in scripts/requirements.txt: " + str(exc))


ROOT = Path(__file__).resolve().parents[2]


def holdout_corpus(root: Path = ROOT) -> Path:
    """The holdout corpus script, fixtures/quality-holdout/corpus.py. A checkout
    that keeps the holdout under another directory name has it as the one
    fixture corpus.py beside the regression set; its contents are not read here."""
    named = root / "fixtures/quality-holdout/corpus.py"
    if named.is_file():
        return named
    found = [p for p in sorted((root / "fixtures").glob("*/corpus.py")) if p.parent.name != "regression"]
    ensure(len(found) == 1, "holdout corpus not found under fixtures/")
    return found[0]


def ensure(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def json_file(path: Path):
    return json.loads(path.read_text(encoding="utf-8"))


def yaml_file(path: Path):
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def within_root(path: Path) -> Path:
    resolved = path.resolve()
    ensure(resolved.is_relative_to(ROOT), "Path leaves repository: " + str(path))
    return resolved


def source_location(location: dict, root: Path = ROOT) -> Path:
    name = location["path"]
    relative = PurePosixPath(name)
    ensure(not relative.is_absolute() and ".." not in relative.parts, "Unsafe source path")
    ensure("\\" not in name and not re.match(r"^[A-Za-z]:", name), "Non-canonical source path")
    path = (root / name).resolve()
    ensure(path.is_relative_to(root.resolve()), "Evidence leaves source root: " + name)
    ensure(path.is_file(), "Missing example source: " + name)
    lines = path.read_text(encoding="utf-8").splitlines()
    ensure(1 <= location["start_line"] <= location["end_line"] <= len(lines), "Invalid line range: " + name)
    return path


def expect_invalid(name: str, function, data) -> None:
    try:
        function(data)
    except (ValueError, ValidationError):
        return
    raise ValueError("Negative case was incorrectly accepted: " + name)


def make_validators():
    schemas = {}
    resources = []
    for path in sorted((ROOT / "schemas").glob("*.json")):
        schema = json_file(path)
        Draft202012Validator.check_schema(schema)
        schemas[path.name] = schema
        resources.append((schema["$id"], Resource.from_contents(schema)))
    registry = Registry().with_resources(resources)
    return {name: Draft202012Validator(schema, registry=registry, format_checker=FormatChecker()) for name, schema in schemas.items()}
