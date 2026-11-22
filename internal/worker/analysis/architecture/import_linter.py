"""Run built-in Import Linter contracts on a staged Python source tree."""
import contextlib
import importlib.util
import json
import os
import re
import sys
from pathlib import Path

from importlinter import __version__, configuration
from importlinter.application import use_cases

BUILTIN = {"forbidden", "layers", "independence", "protected", "private_modules", "acyclic_siblings"}
MODULE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*")


def edges(value):
    if isinstance(value, dict):
        importer, imported = value.get("importer"), value.get("imported")
        lines = value.get("line_numbers", [])
        if isinstance(importer, str) and isinstance(imported, str):
            for line in lines:
                if isinstance(line, int) and line > 0:
                    yield {"importer": importer, "imported": imported, "line": line}
        for member in value.values():
            yield from edges(member)
    elif isinstance(value, (list, tuple)):
        for member in value:
            yield from edges(member)


def inspect():
    options = None
    configured = ""
    for candidate in sys.argv[1:]:
        try:
            options = use_cases.read_user_options(candidate)
            configured = candidate
            break
        except FileNotFoundError:
            continue
    if not options:
        return {"status": "configuration_absent"}

    session = options.session_options
    contracts = options.contracts_options
    if "contract_types" in session or any(c.get("type") not in BUILTIN for c in contracts):
        return {"status": "custom_contract_unsupported", "configuration": configured}
    if not contracts or len(contracts) > 128:
        return {"status": "contract_count_unsupported", "configuration": configured}
    roots = session.get("root_packages")
    if not isinstance(roots, list) or not roots or len(roots) > 32:
        return {"status": "package_roots_unsupported", "configuration": configured}
    if any(not isinstance(root, str) or "." in root or not MODULE.fullmatch(root)
           or importlib.util.find_spec(root) is not None for root in roots):
        return {"status": "package_roots_unsupported", "configuration": configured}
    if str(session.get("include_external_packages", "false")).lower() != "false":
        return {"status": "external_contract_unsupported", "configuration": configured}

    stage = Path.cwd()
    layouts = [
        p for p in (stage, stage / "src")
        if all((p / root / "__init__.py").is_file() for root in roots)
    ]
    if len(layouts) != 1:
        return {"status": "package_roots_unavailable", "configuration": configured}
    code_root = layouts[0]

    def refuse_staged_execution(event, args):
        if event == "exec":
            filename = args[0].co_filename
            if os.path.isabs(filename) and os.path.commonpath((str(stage), filename)) == str(stage):
                raise RuntimeError("analysis must not execute project code")

    sys.addaudithook(refuse_staged_execution)
    sys.path.append(str(code_root))
    report = use_cases.create_report(options, cache_dir=None)
    if report.could_not_run or len(report.contracts) != len(contracts):
        return {"status": "contract_invalid", "configuration": configured}

    checks = []
    for contract, check in report.get_contracts_and_checks():
        entries = []
        for edge in edges(check.metadata):
            if edge not in entries:
                entries.append(edge)
            if len(entries) == 128:
                break
        checks.append({
            "id": contract.contract_options.get("id") or "",
            "kind": contract.contract_options["type"],
            "kept": check.kept,
            "edges": entries,
            "warnings": len(check.warnings),
        })
    return {
        "status": "checked",
        "configuration": configured,
        "root": "." if code_root == stage else "src",
        "version": __version__,
        "modules": sorted(report.graph.modules),
        "checks": checks,
    }


configuration.configure()
use_cases._register_built_in_contract_types()
try:
    with contextlib.redirect_stdout(sys.stderr):
        result = inspect()
except Exception:
    result = {"status": "contract_check_failed"}
print(json.dumps(result, ensure_ascii=True))
