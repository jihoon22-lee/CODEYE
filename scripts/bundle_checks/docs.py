"""Holdout corpus, Markdown link, document naming and source naming checks."""

from __future__ import annotations

import os
from pathlib import Path
import re
import sys

from .common import ROOT, ensure


def check_holdout_corpus() -> None:
    """보류 corpus의 구조·위치·digest·누설을 도구 없이 확인하고 봉인 digest와 대조한다."""
    import subprocess

    corpus = ROOT / "fixtures/r1-quality/corpus.py"
    env = {"PATH": os.environ.get("PATH", ""), "LANG": "C.UTF-8"}
    result = subprocess.run([sys.executable, "-I", str(corpus), "validate"], capture_output=True, text=True, timeout=300, env=env)
    tail = " | ".join((result.stdout + result.stderr).strip().splitlines()[-5:])
    ensure(result.returncode == 0, "holdout corpus validate failed: " + tail)
    out = subprocess.run([sys.executable, "-I", str(corpus), "digest"], capture_output=True, text=True, timeout=300, env=env)
    ensure(out.returncode == 0 and out.stdout.split(), "holdout corpus digest failed")
    sealed = "ab776f5f68cf34ece0dd32b0f18dab962d07fad1131a35c32a6149eebc045fe4"
    ensure(out.stdout.split()[-1] == sealed, "holdout corpus digest differs from the sealed digest; a changed holdout needs a new seal recorded in docs/EVALUATION.md")
    print(f"PASS: holdout corpus (structure, locations, digests, leakage scan; sealed digest {sealed[:12]})")


def heading_anchors(text: str) -> set:
    """Anchors a Markdown renderer derives from the headings of a document."""
    seen = {}
    result = set()
    fenced = False
    for line in text.splitlines():
        if line.lstrip().startswith("```"):
            fenced = not fenced
            continue
        match = None if fenced else re.match(r"#{1,6}\s+(.*?)\s*#*\s*$", line)
        if not match:
            continue
        title = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", match.group(1))
        slug = re.sub(r"[^\w\- ]", "", title.lower()).replace(" ", "-")
        count = seen.get(slug, 0)
        seen[slug] = count + 1
        result.add(slug if count == 0 else f"{slug}-{count}")
    return result


# The holdout results are kept verbatim as recorded, so their wording is exempt.
STAGE_LABEL_EXEMPT = {"docs/EVALUATION-RESULTS.md"}


def check_doc_names(markdown_files) -> None:
    """Documents are organized by topic: no numbered file names, no build-stage labels in the text."""
    for path in markdown_files:
        relative = path.relative_to(ROOT).as_posix()
        if relative.startswith("docs/"):
            ensure(not re.match(r"\d", path.name), "Numbered document name: " + relative)
        if relative in STAGE_LABEL_EXEMPT or relative.startswith("fixtures/"):
            continue
        label = re.search(r"(?<![\w-])W[0-3]\d(?![\w-])", path.read_text(encoding="utf-8"))
        ensure(label is None, "Build-stage label in " + relative + ": " + (label.group(0) if label else ""))


def check_code_doc_refs() -> int:
    """Source, schema and example files cite documents by path and section anchor; each must resolve."""
    ignored = {".git", ".claude", ".codeye", ".venv", "node_modules", "dist", "bin", "coverage", "__pycache__"}
    suffixes = {".go", ".py", ".ts", ".tsx", ".yaml", ".yml", ".json", ".sql", ".css", ".html"}
    anchors = {}
    count = 0
    for directory, dirs, files in os.walk(ROOT):
        dirs[:] = sorted(name for name in dirs if name not in ignored)
        for name in sorted(files):
            path = Path(directory) / name
            if path.suffix not in suffixes:
                continue
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            for match in re.finditer(r"(?<![\w./-])docs/([A-Z][A-Z-]*\.md)(?:#([\w-]+))?", text):
                doc = ROOT / "docs" / match.group(1)
                where = str(path.relative_to(ROOT)) + ": " + match.group(0)
                ensure(doc.is_file(), "Reference to a missing document in " + where)
                if match.group(2):
                    if doc not in anchors:
                        anchors[doc] = heading_anchors(doc.read_text(encoding="utf-8"))
                    ensure(match.group(2).lower() in anchors[doc], "Reference to a missing section in " + where)
                count += 1
    return count


# Places where a build-stage label is data, not a name: applied migrations are
# checksummed, fixture inputs are pinned by captured tool output, and the
# holdout records are kept verbatim.
STAGE_NAME_EXEMPT = ("fixtures/", "migrations/sql/", "scripts/testdata/", "docs/EVALUATION-RESULTS.md")


def check_source_names() -> int:
    """Source files are named and commented by what they do, not by the build stage that added them."""
    ignored = {".git", ".claude", ".codeye", ".venv", "node_modules", "dist", "bin", "coverage", "__pycache__"}
    suffixes = {".go", ".py", ".ts", ".tsx", ".yaml", ".yml", ".json", ".css", ".html", ".sql"}
    label = re.compile(r"(?<![\w-])W[0-3]\d(?![\w-])|CODEYE_(?:W\d\d|R\d)_")
    count = 0
    for directory, dirs, files in os.walk(ROOT):
        dirs[:] = sorted(name for name in dirs if name not in ignored)
        for name in sorted(files):
            path = Path(directory) / name
            relative = path.relative_to(ROOT).as_posix()
            if path.suffix not in suffixes or relative.startswith(STAGE_NAME_EXEMPT) or name == "package-lock.json":
                continue
            ensure(not re.match(r"(?:w\d\d|r[0-2])[_-]", name), "Build-stage file name: " + relative)
            try:
                found = label.search(path.read_text(encoding="utf-8"))
            except UnicodeDecodeError:
                continue
            ensure(found is None, "Build-stage label in " + relative + ": " + (found.group(0) if found else ""))
            count += 1
    return count
