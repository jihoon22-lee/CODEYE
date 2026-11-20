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

    corpus = ROOT / "fixtures/quality-holdout/corpus.py"
    env = {"PATH": os.environ.get("PATH", ""), "LANG": "C.UTF-8"}
    result = subprocess.run([sys.executable, "-I", str(corpus), "validate"], capture_output=True, text=True, timeout=300, env=env)
    tail = " | ".join((result.stdout + result.stderr).strip().splitlines()[-5:])
    ensure(result.returncode == 0, "holdout corpus validate failed: " + tail)
    out = subprocess.run([sys.executable, "-I", str(corpus), "digest"], capture_output=True, text=True, timeout=300, env=env)
    ensure(out.returncode == 0 and out.stdout.split(), "holdout corpus digest failed")
    sealed = "99904b7bdf1239eb3f0732a465a43ec51ff735594f15f23af11ec701bbc424be"
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


def check_doc_names(markdown_files) -> None:
    """Documents are organized by topic: no numbered file names (labels: check_stage_labels)."""
    for path in markdown_files:
        relative = path.relative_to(ROOT).as_posix()
        if relative.startswith("docs/"):
            ensure(not re.match(r"\d", path.name), "Numbered document name: " + relative)


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


# Labels that name a build stage, a release or a pack generation instead of
# what a thing does. Contents and tracked path names of every text file are
# checked, in either case: W and two digits as a word or name part (also after
# _, @, / or -), R and a digit before - or _ (case IDs, relay names), _R and a
# digit at a word end, "review" joined to R/V and a digit, the holdout's
# earlier directory name, CODEYE_ with R/W and a digit, and the names of the
# removed pack selection. tests: scripts/test_stage_labels.py.
STAGE_PATTERNS = (
    re.compile(r"(?<![A-Za-z0-9])[Ww]\d{2}(?!\d)"),
    re.compile(r"(?<![A-Za-z0-9])[Rr]\d-[A-Za-z]"),
    re.compile(r"(?<![A-Za-z0-9])[Rr]\d_|_[Rr]\d(?![A-Za-z0-9])"),
    re.compile(r"(?i)review[-._][rv]\d|r\d-quality|codeye_[rw]\d"),
    # Split literals so this file does not match itself.
    re.compile(r"(?i)role" r"pack|role[ -]pack|legacy[ -]pack|--review" r"-pack|review" r"_pack"),
)

# Spellings that contain such a pattern but name something else: a GNU target
# triplet of the C/C++ toolchain probe. API and upstream versions (/api/v1,
# /v1/responses, /api/v3), module versions (pgx/v5), analyzer format IDs
# (go-vet-json-v1), schema_version values and the regression case names
# (Q01-before) do not match the patterns and need no entry.
STAGE_KEEP = ("x86_64-w64-mingw32",)

# Dependency lock files carry content hashes; a hash is not a name.
LOCK_HASH = re.compile(r"\bsha(?:1|256|384|512)-[A-Za-z0-9+/=]+|\bh1:[A-Za-z0-9+/=]+")

def stage_labels(text: str) -> list:
    """The stage-style labels in text, after the keep-list and lock-file hashes are removed."""
    text = LOCK_HASH.sub("", text)
    for keep in STAGE_KEEP:
        text = text.replace(keep, "")
    return [m.group(0) for pattern in STAGE_PATTERNS for m in pattern.finditer(text)]


def tracked_files() -> list:
    """Tracked paths (git ls-files), or every file outside build output when git is unavailable."""
    import subprocess

    try:
        out = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, capture_output=True, check=True, timeout=60).stdout
        return sorted(p for p in out.decode("utf-8").split("\0") if p)
    except (OSError, subprocess.SubprocessError):
        ignored = {".git", ".claude", ".codeye", ".venv", "node_modules", "dist", "bin", "coverage", "__pycache__"}
        found = []
        for directory, dirs, files in os.walk(ROOT):
            dirs[:] = sorted(name for name in dirs if name not in ignored)
            found.extend((Path(directory) / name).relative_to(ROOT).as_posix() for name in files)
        return sorted(found)


def check_stage_labels(paths=None) -> int:
    """No tracked text file names or contains a stage-style label (STAGE_PATTERNS)."""
    count = 0
    for relative in tracked_files() if paths is None else paths:
        found = stage_labels(relative)
        ensure(not found, "Stage label in path " + relative + ": " + ", ".join(found))
        path = ROOT / relative
        if not path.is_file():
            continue
        data = path.read_bytes()
        if b"\0" in data[:8192]:
            continue
        try:
            text = data.decode("utf-8")
        except UnicodeDecodeError:
            continue
        found = stage_labels(text)
        ensure(not found, "Stage label in " + relative + ": " + ", ".join(sorted(set(found))))
        count += 1
    return count
