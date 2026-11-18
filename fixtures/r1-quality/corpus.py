"""R1 다언어 보류 평가 사례를 만들고, 정답과 분리된 실행 검사로 결함 여부를 확인한다.

하위 명령:
  materialize <id> <before|after> <dest> [--documented]  분석 입력만 쓰고 입력 트리 digest를 출력
  validate                                              구조·위치·digest·누설 검사(도구 불필요)
  verify [--go ... --cxx ...] [--repeat N]              validate + 실제 도구로 전후·정상 사례 실행
  digest                                                보류 집합 봉인용 corpus digest 출력
  rehash                                                corpus.json에 저장된 변형별 digest 갱신
  forbidden [--allow-text PATH ...] [--output FILE]     모델 입력에 나오면 안 되는 정답 문구 목록(JSON)

표준 라이브러리만 사용하며 `python3 -I`로 실행한다. 도구는 플래그나 CODEYE_TEST_* 환경변수의
절대 경로로만 받는다. PATH에서 도구를 찾지 않는다.
"""

import argparse
import difflib
import hashlib
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import textwrap
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path, PurePosixPath

HERE = Path(__file__).resolve().parent
CASES = HERE / "corpus.json"
MARKER = "{{CONTRACT}}"
LANGUAGES = ("cpp", "python", "javascript", "go", "rust")
ID_PREFIX = {"cpp": "CPP", "python": "PY", "javascript": "JS", "go": "GO", "rust": "RS"}
CATEGORIES = (
    "error_handling",
    "resource_lifetime",
    "concurrency_async",
    "boundaries_arithmetic",
    "api_misuse",
    "state_invariants",
    "input_validation",
)
KINDS = ("defect_pair", "tricky_normal")
DETECTION = ("static_likely", "semantic_only")
TOOL_ENV = {
    "go": "CODEYE_TEST_GO",
    "python": "CODEYE_TEST_PYTHON",
    "node": "CODEYE_TEST_NODE",
    "rustc": "CODEYE_TEST_RUSTC",
    "cxx": "CODEYE_TEST_CXX",
}
# rustc는 링크 단계에 C/C++ 드라이버가 필요하다. 별도 PATH 탐색 없이 CODEYE_TEST_CXX를 링커로 쓴다.
LANGUAGE_TOOLS = {"go": ("go",), "python": ("python",), "javascript": ("node",), "rust": ("rustc", "cxx"), "cpp": ("cxx",)}
MIN_PAIRS, MIN_NORMALS, MIN_CROSS_FILE = 8, 2, 3
MIN_LINES, MAX_LINES = 30, 150
NGRAM = 5
PASS_SENTINEL = "CHECK-PASS"
FAIL_SENTINEL = "CHECK-FAIL"
RESERVED = {"README.md", "go.mod", "Cargo.toml", "package.json", "pyproject.toml", "CMakeLists.txt", "tsconfig.json"}
REQUIRED_CASE = ("id", "language", "split", "family", "category", "kind", "detection", "cross_file", "project",
                 "contract", "contract_file", "files", "digests", "check", "label")
REQUIRED_LABEL = ("trigger", "expected_impact", "positive_locations", "known_counterevidence", "corrected_variant",
                  "reviewer_notes", "distinctive_tokens")
PROSE_FIELDS = ("trigger", "expected_impact", "reviewer_notes")
# 입력에 나오면 정답이나 변형을 암시하는 단어. 식별자는 camelCase/snake_case 단위로 나눠 비교한다.
BANNED_STEMS = ("bug", "fixme", "todo", "hack", "leak", "defect", "vuln", "broken", "wrong", "incorrect", "oops",
                "mistake", "deadlock", "overflow", "underflow", "dangl", "inject", "traversal", "holdout", "corpus",
                "tricky", "counterevidence", "codeye", "flaw", "faulty", "bogus", "pitfall", "regress", "workaround")
BANNED_WORDS = {"xxx", "race", "racy", "fix", "fixed", "fixes", "corrected", "before", "after", "variant", "label",
                "labels", "trigger", "triggers", "intentional", "intentionally", "deliberate", "deliberately",
                "careful", "carefully", "beware", "caution", "danger", "dangerous", "gotcha", "subtle", "check-pass",
                "check-fail"}
TEST_PATH = re.compile(r"(^|[._/-])(tests?|specs?|conftest|testdata|fixtures?)([._/-]|$)", re.IGNORECASE)


# ---------------------------------------------------------------- materialize

def load_cases():
    return json.loads(CASES.read_text(encoding="utf-8"))


def find_case(cases, case_id):
    for case in cases:
        if case["id"] == case_id:
            return case
    raise SystemExit(f"unknown case: {case_id}")


def variants(case):
    return ("before", "after") if case["kind"] == "defect_pair" else ("before",)


def text_of(entry):
    return "\n".join(entry["lines"]) + "\n"


def wrap(text, prefix):
    width = max(40, 78 - len(prefix.expandtabs(4)))
    out = []
    for index, paragraph in enumerate(text.split("\n\n")):
        if index:
            out.append(prefix.rstrip())
        out.extend(prefix + line for line in textwrap.wrap(paragraph, width=width, break_long_words=False))
    return out


def inject_contract(content, contract):
    lines = content.split("\n")
    hits = [i for i, line in enumerate(lines) if MARKER in line]
    if len(hits) != 1:
        raise ValueError(f"contract marker count {len(hits)}")
    line = lines[hits[0]]
    prefix, _, suffix = line.partition(MARKER)
    if suffix.strip():
        raise ValueError("text after contract marker")
    lines[hits[0]:hits[0] + 1] = wrap(contract, prefix)
    return "\n".join(lines)


def manifests(case):
    project = case["project"]
    name = project["name"]
    language = case["language"]
    if language == "go":
        return {"go.mod": f"module example.com/{name}\n\ngo 1.23\n"}
    if language == "rust":
        return {"Cargo.toml": f'[package]\nname = "{name}"\nversion = "0.1.0"\nedition = "2021"\n\n[dependencies]\n'}
    if language == "python":
        return {"pyproject.toml": f'[project]\nname = "{name}"\nversion = "0.1.0"\nrequires-python = ">=3.11"\n'}
    if language == "javascript":
        out = {"package.json": json.dumps({"name": name, "version": "0.1.0", "private": True, "type": "module"},
                                          indent=2) + "\n"}
        if project.get("typescript"):
            options = {"target": "es2023", "module": "nodenext", "strict": True, "noEmit": True,
                       "allowImportingTsExtensions": True}
            out["tsconfig.json"] = json.dumps({"compilerOptions": options, "include": ["src"]}, indent=2) + "\n"
        return out
    if language == "cpp":
        sources = sorted(p for p in case_paths(case, "before") if p.endswith(".cpp"))
        body = (f"cmake_minimum_required(VERSION 3.16)\nproject({name} LANGUAGES CXX)\n"
                "set(CMAKE_CXX_STANDARD 17)\nset(CMAKE_CXX_STANDARD_REQUIRED ON)\n"
                f"add_library({name} STATIC {' '.join(sources)})\n"
                f"target_include_directories({name} PUBLIC include)\n")
        if project.get("threads"):
            body += f"find_package(Threads REQUIRED)\ntarget_link_libraries({name} PUBLIC Threads::Threads)\n"
        return {"CMakeLists.txt": body}
    raise ValueError("unknown language " + language)


def readme(case):
    project = case["project"]
    return (f"# {project['title']}\n\n" + "\n".join(textwrap.wrap(project["summary"], 78)) + "\n\n## Behaviour\n\n"
            + "\n".join(wrap(case["contract"], "")) + "\n")


def case_paths(case, variant):
    return [e["path"] for e in case["files"]["shared"]] + [e["path"] for e in case["files"].get(variant, [])]


def render(case, variant, documented=False):
    """분석 입력 파일 {상대 경로: 내용}. 정답·검사·변형 이름은 포함하지 않는다."""
    if variant not in variants(case):
        raise ValueError(f"{case['id']} has no {variant} variant")
    files = {}
    for entry in case["files"]["shared"] + case["files"].get(variant, []):
        files[entry["path"]] = text_of(entry)
    files[case["contract_file"]] = inject_contract(files[case["contract_file"]], case["contract"])
    files.update(manifests(case))
    if documented:
        files["README.md"] = readme(case)
    return files


def tree_digest(files):
    digest = hashlib.sha256()
    for path in sorted(files):
        data = files[path].encode("utf-8")
        digest.update(path.encode("utf-8") + b"\0" + hashlib.sha256(data).hexdigest().encode("ascii") + b"\n")
    return digest.hexdigest()


def write_tree(destination, files):
    destination = Path(destination)
    if destination.exists() and any(destination.iterdir()):
        raise SystemExit(f"destination is not empty: {destination}")
    destination.mkdir(mode=0o755, parents=True, exist_ok=True)
    for path, content in files.items():
        target = destination / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")


def materialize(destination, case, variant, documented=False):
    files = render(case, variant, documented)
    write_tree(destination, files)
    return tree_digest(files)


def digest_key(variant, documented):
    return variant + ("_documented" if documented else "")


def computed_digests(case):
    return {digest_key(v, d): tree_digest(render(case, v, d)) for v in variants(case) for d in (False, True)}


def canonical(cases):
    return json.dumps(cases, sort_keys=True, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def corpus_digest(cases):
    digest = hashlib.sha256()
    digest.update(b"corpus.json\0" + hashlib.sha256(canonical(cases)).hexdigest().encode("ascii") + b"\n")
    for case in sorted(cases, key=lambda c: c["id"]):
        for key, value in sorted(computed_digests(case).items()):
            digest.update(f"{case['id']}\0{key}\0{value}\n".encode("utf-8"))
    return digest.hexdigest()


# ---------------------------------------------------------------- leakage scan

def words(text):
    return re.findall(r"[a-z0-9]+", text.lower())


def subwords(text):
    out = set()
    for token in re.findall(r"[A-Za-z0-9]+", text):
        out.add(token.lower())
        for part in re.findall(r"[A-Z]+(?![a-z])|[A-Z]?[a-z]+|[0-9]+", token):
            out.add(part.lower())
    return out


def banned_hits(text):
    tokens = subwords(text)
    for phrase in re.findall(r"check-(?:pass|fail)", text.lower()):
        tokens.add(phrase)
    hits = {t for t in tokens if t in BANNED_WORDS}
    hits |= {t for t in tokens for stem in BANNED_STEMS if t.startswith(stem)}
    return sorted(hits)


def label_prose(case):
    label = case["label"]
    texts = [label[field] for field in PROSE_FIELDS]
    texts += [item["note"] for item in label["known_counterevidence"]]
    texts += [item.get("note", "") for item in label.get("suspect_locations", [])]
    texts.append(case["check"]["summary"])
    return [t for t in texts if t]


def leakage(case, inputs):
    """inputs: {variant: {path: content}}. 누설 항목 목록을 돌려준다."""
    hits = []
    label = case["label"]
    corpus_words = {}
    for variant, files in inputs.items():
        for path, content in files.items():
            where = f"{variant}:{path}"
            if TEST_PATH.search(path):
                hits.append(f"{where}: test-like path")
            for word in banned_hits(path + "\n" + content):
                hits.append(f"{where}: suspicious word '{word}'")
            if case["id"].lower() in content.lower() or case["family"].lower() in content.lower():
                hits.append(f"{where}: case id or family")
            if MARKER in content:
                hits.append(f"{where}: unreplaced contract marker")
            corpus_words[where] = " " + " ".join(words(content)) + " "
            tokens = subwords(content)
            for token in label["distinctive_tokens"]:
                t = token.lower()
                if re.fullmatch(r"[a-z0-9]+", t):
                    found = t in tokens
                elif re.fullmatch(r"[a-z0-9]+( [a-z0-9]+)+", t):
                    found = (" " + t + " ") in corpus_words[where]
                else:
                    found = t in content.lower()
                if found:
                    hits.append(f"{where}: distinctive label token '{token}'")
    for text in label_prose(case):
        seq = words(text)
        for i in range(len(seq) - NGRAM + 1):
            gram = " " + " ".join(seq[i:i + NGRAM]) + " "
            for where, joined in corpus_words.items():
                if gram in joined:
                    hits.append(f"{where}: label phrase '{gram.strip()}'")
    return sorted(set(hits))


def leakage_selftest(case):
    """스캐너가 실제로 누설을 잡는지 심어 둔 토큰으로 확인한다."""
    files = render(case, "before")
    path = case["contract_file"]
    planted = []
    token = case["label"]["distinctive_tokens"][0]
    phrase = " ".join(words(case["label"]["expected_impact"])[:NGRAM])
    for extra in (f"\n// {token}\n", f"\n# {phrase}\n", "\n// cleanup workaround\n"):
        trial = dict(files)
        trial[path] = trial[path] + extra
        planted.append(bool(leakage(case, {"before": trial})))
    trial = dict(files)
    trial["tests/x.txt"] = "x\n"
    planted.append(bool(leakage(case, {"before": trial})))
    clean = not leakage(case, {"before": files})
    return all(planted) and clean, len(planted)


# ---------------------------------------------------------------- forbidden phrases (W24 answer blocking)

FORBIDDEN_SCHEMA = "codeye-forbidden-phrases"
FORBIDDEN_VERSION = 1
# Matching rule shared with the relay (scripts/r0_openai_gateway.py). The modes are the leakage() rules:
#   subword   - one [a-z0-9]+ token, compared with subwords() (whole tokens and camelCase/snake_case parts)
#   words     - several tokens, compared as a run of words() separated by single spaces
#   substring - anything else, compared with the lowercased text
NORMALIZATION = "codeye-words-v1"


def match_mode(text):
    t = text.lower()
    if re.fullmatch(r"[a-z0-9]+", t):
        return "subword"
    if re.fullmatch(r"[a-z0-9]+( [a-z0-9]+)+", t):
        return "words"
    return "substring"


class TextIndex:
    """Normalized view of a set of texts, for the three match modes."""

    def __init__(self):
        self.subwords, self.joined, self.lower = set(), [], []

    def add(self, text):
        self.subwords |= subwords(text)
        self.joined.append(" " + " ".join(words(text)) + " ")
        self.lower.append(text.lower())

    def contains(self, mode, text):
        if mode == "subword":
            return text in self.subwords
        if mode == "words":
            return any(" " + text + " " in j for j in self.joined)
        return any(text in low for low in self.lower)


def allowed_texts(paths):
    """Explicit operator-supplied texts that may legitimately reach the model (instruction packs, tool messages)."""
    out = []
    for path in paths:
        path = Path(path)
        files = sorted(p for p in path.rglob("*") if p.is_file()) if path.is_dir() else [path]
        for f in files:
            data = f.read_bytes()
            out.append((str(f), hashlib.sha256(data).hexdigest(), data.decode("utf-8", errors="replace")))
    return out


def forbidden_list(cases, allow_paths=()):
    """Label n-grams and distinctive tokens that occur in no materialized input (any case, any variant,
    with or without README) and in no allowed text. Generic BANNED_* words are not exported."""
    inputs = TextIndex()
    for case in cases:
        for variant in variants(case):
            for documented in (False, True):
                for path, content in render(case, variant, documented).items():
                    inputs.add(path + "\n" + content)
    allowed = TextIndex()
    sources = allowed_texts(allow_paths)
    for _, _, text in sources:
        allowed.add(text)
    entries, dropped = {}, {"in_inputs": 0, "in_allowed_text": 0}
    for case in sorted(cases, key=lambda c: c["id"]):
        candidates = []
        for index, token in enumerate(case["label"]["distinctive_tokens"]):
            candidates.append((f"{case['id']}/token/{index}", "distinctive_token", token.lower()))
        for field_index, text in enumerate(label_prose(case)):
            seq = words(text)
            for i in range(len(seq) - NGRAM + 1):
                candidates.append((f"{case['id']}/prose/{field_index}.{i}", "label_prose", " ".join(seq[i:i + NGRAM])))
        seen = set()
        for entry_id, source, text in candidates:
            mode = match_mode(text)
            if (mode, text) in seen:
                continue
            seen.add((mode, text))
            if inputs.contains(mode, text):
                dropped["in_inputs"] += 1
                continue
            if allowed.contains(mode, text):
                dropped["in_allowed_text"] += 1
                continue
            key = (mode, text)
            if key in entries:  # the same phrase in two cases: keep the first id, list both cases
                entries[key]["cases"] = sorted(set(entries[key]["cases"]) | {case["id"]})
                continue
            entries[key] = {"id": entry_id, "cases": [case["id"]], "source": source, "mode": mode, "text": text}
    return {
        "schema": FORBIDDEN_SCHEMA,
        "version": FORBIDDEN_VERSION,
        "normalization": NORMALIZATION,
        "ngram": NGRAM,
        "corpus_digest": corpus_digest(cases),
        "allowed_texts": [{"name": Path(name).name, "sha256": sha} for name, sha, _ in sources],
        "dropped": dropped,
        "entries": sorted(entries.values(), key=lambda e: e["id"]),
    }


# ---------------------------------------------------------------- structure

def fail(errors, case, message):
    errors.append(f"{case.get('id', '?')}: {message}")


def changed_before_lines(before, after):
    """after에서 바뀐 before 줄 번호(1부터). 삽입은 인접 줄로 본다."""
    out = set()
    matcher = difflib.SequenceMatcher(a=before.split("\n"), b=after.split("\n"), autojunk=False)
    for tag, i1, i2, _, _ in matcher.get_opcodes():
        if tag in ("replace", "delete"):
            out.update(range(i1 + 1, i2 + 1))
        elif tag == "insert":
            out.update((i1, i1 + 1))
    return out


def check_location(errors, case, files, item, what):
    path = item.get("path")
    if path not in files:
        fail(errors, case, f"{what} path {path} not in materialized input")
        return None
    lines = files[path].split("\n")
    start, end = item.get("start_line"), item.get("end_line")
    if not (isinstance(start, int) and isinstance(end, int) and 1 <= start <= end <= len(lines)):
        fail(errors, case, f"{what} {path} bad range {start}-{end}")
        return None
    anchors = item.get("anchors", {})
    if anchors.get("start") not in lines[start - 1] or anchors.get("end", anchors.get("start")) not in lines[end - 1]:
        fail(errors, case, f"{what} {path}:{start}-{end} anchors do not match")
    short = re.split(r"[.:]+", item.get("symbol", ""))[-1]
    if not short or short not in files[path]:
        fail(errors, case, f"{what} symbol {item.get('symbol')} not found in {path}")
    return set(range(start, end + 1))


def validate_case(case, errors):
    for field in REQUIRED_CASE:
        if field not in case:
            fail(errors, case, "missing field " + field)
            return
    language = case["language"]
    if language not in LANGUAGES:
        fail(errors, case, "unknown language")
        return
    if not re.fullmatch(rf"R1-{ID_PREFIX[language]}-\d{{2}}", case["id"]):
        fail(errors, case, "id does not match language")
    if case["split"] != "holdout":
        fail(errors, case, "split must be holdout")
    if case["kind"] not in KINDS or case["category"] not in CATEGORIES or case["detection"] not in DETECTION:
        fail(errors, case, "bad kind/category/detection")
    if not re.fullmatch(r"[a-z0-9]+(-[a-z0-9]+)+", case["family"]):
        fail(errors, case, "family must be a kebab-case slug")
    name = case["project"]["name"]
    if case["id"].lower() in name.lower() or any(w in name.lower() for w in case["family"].split("-") if len(w) >= 6):
        fail(errors, case, "project name carries the id or family")
    label = case["label"]
    for field in REQUIRED_LABEL:
        if field not in label:
            fail(errors, case, "label missing " + field)
            return
    if not label["distinctive_tokens"]:
        fail(errors, case, "label needs distinctive tokens")
    if len(case["contract"]) < 80:
        fail(errors, case, "contract too short to state the input domain")
    files = case["files"]
    shared = [e["path"] for e in files["shared"]]
    for variant in variants(case):
        own = [e["path"] for e in files.get(variant, [])]
        if set(own) & set(shared) or len(set(own + shared)) != len(own + shared):
            fail(errors, case, f"{variant}: duplicate or shared/variant overlap")
    if case["kind"] == "defect_pair":
        if sorted(e["path"] for e in files["before"]) != sorted(e["path"] for e in files["after"]) or not files["before"]:
            fail(errors, case, "before/after must change the same non-empty file set")
        if label["corrected_variant"] != {"variant": "after", "changed_files": sorted(e["path"] for e in files["after"])}:
            fail(errors, case, "corrected_variant must reference after and its changed files")
        if not label["positive_locations"]:
            fail(errors, case, "defect pair needs positive locations")
    else:
        if files.get("after") or files.get("before"):
            fail(errors, case, "tricky normal keeps all files in shared")
        if label["corrected_variant"] is not None or label["positive_locations"]:
            fail(errors, case, "tricky normal has no corrected variant or positive locations")
        if not label.get("suspect_locations"):
            fail(errors, case, "tricky normal needs suspect locations")
    for entry in [*files["shared"], *files.get("before", []), *files.get("after", [])]:
        p = PurePosixPath(entry["path"])
        if p.is_absolute() or ".." in p.parts or entry["path"] in RESERVED or p.parts[0] == "_check":
            fail(errors, case, "bad input path " + entry["path"])
    try:
        rendered = {v: render(case, v) for v in variants(case)}
        documented = {v: render(case, v, True) for v in variants(case)}
    except (ValueError, KeyError) as exc:
        fail(errors, case, f"render failed: {exc}")
        return
    if case["digests"] != computed_digests(case):
        fail(errors, case, "stored digests differ from materialized inputs (run rehash after review)")
    for variant, tree in rendered.items():
        code = sum(content.count("\n") for path, content in tree.items() if path not in RESERVED)
        if not MIN_LINES <= code <= MAX_LINES:
            fail(errors, case, f"{variant}: {code} code lines outside {MIN_LINES}-{MAX_LINES}")
    before = rendered["before"]
    if case["kind"] == "defect_pair":
        after = rendered["after"]
        changed = sorted(p for p in before if before[p] != after.get(p))
        if changed != label["corrected_variant"]["changed_files"]:
            fail(errors, case, f"files that differ {changed} != declared changed files")
        hit_diff = False
        for item in label["positive_locations"]:
            span = check_location(errors, case, before, item, "positive")
            if span and item["path"] in changed and span & changed_before_lines(before[item["path"]], after[item["path"]]):
                hit_diff = True
        if not hit_diff:
            fail(errors, case, "no positive location intersects the before/after diff")
    for item in label.get("suspect_locations", []):
        check_location(errors, case, before, item, "suspect")
    paths = set()
    for item in label["known_counterevidence"]:
        if not item.get("note"):
            fail(errors, case, "counterevidence needs a note")
        if item.get("path"):
            tree = rendered.get(item.get("variant", "before"))
            if tree is None:
                fail(errors, case, "counterevidence variant missing")
                continue
            check_location(errors, case, tree, item, "counterevidence")
            paths.add(item["path"])
    if case["kind"] == "tricky_normal" and not paths:
        fail(errors, case, "tricky normal must point to counterevidence locations")
    if case["cross_file"]:
        code_files = [p for p in before if p not in RESERVED]
        located = {i["path"] for i in label["positive_locations"] + label.get("suspect_locations", [])} | paths
        located |= set(label["corrected_variant"]["changed_files"]) if label["corrected_variant"] else set()
        if len(code_files) < 2 or len(located) < 2:
            fail(errors, case, "cross_file case must locate evidence in two files")
    check = case["check"]
    for field in ("summary", "files", "build", "run", "failure_signature"):
        if field not in check:
            fail(errors, case, "check missing " + field)
            return
    re.compile(check["failure_signature"])
    for entry in check["files"]:
        if entry["path"] in before or PurePosixPath(entry["path"]).is_absolute() or ".." in entry["path"]:
            fail(errors, case, "check file collides with input " + entry["path"])
    for hit in leakage(case, {**{v: t for v, t in rendered.items()}, **{v + "+doc": t for v, t in documented.items()}}):
        fail(errors, case, "leakage " + hit)


def validate(cases):
    errors = []
    ids = [c.get("id") for c in cases]
    if len(ids) != len(set(ids)):
        errors.append("duplicate case ids")
    families = [c.get("family") for c in cases]
    if len(families) != len(set(families)):
        errors.append("duplicate family slugs")
    for case in cases:
        validate_case(case, errors)
    counts = {}
    for language in LANGUAGES:
        mine = [c for c in cases if c.get("language") == language]
        counts[language] = {
            "defect_pair": sum(c.get("kind") == "defect_pair" for c in mine),
            "tricky_normal": sum(c.get("kind") == "tricky_normal" for c in mine),
            "cross_file": sum(bool(c.get("cross_file")) for c in mine),
            "static_likely": sum(c.get("detection") == "static_likely" for c in mine),
            "categories": len({c.get("category") for c in mine}),
        }
        row = counts[language]
        if row["defect_pair"] < MIN_PAIRS or row["tricky_normal"] < MIN_NORMALS or row["cross_file"] < MIN_CROSS_FILE:
            errors.append(f"{language}: counts below minimum {row}")
        if not 0 < row["static_likely"] < row["defect_pair"] + row["tricky_normal"]:
            errors.append(f"{language}: needs a mix of static_likely and semantic_only cases")
    return errors, counts


# ---------------------------------------------------------------- execution

def toolchains(args):
    found, skipped = {}, {}
    for tool, env in TOOL_ENV.items():
        value = getattr(args, tool, None) or os.environ.get(env)
        if not value:
            continue
        path = Path(value)
        if not path.is_absolute() or not path.is_file() or not os.access(path, os.X_OK):
            raise SystemExit(f"{tool}: not an absolute executable path: {value}")
        found[tool] = str(path)
    for language, tools in LANGUAGE_TOOLS.items():
        missing = [TOOL_ENV[t] for t in tools if t not in found]
        if missing:
            skipped[language] = "unset " + ", ".join(missing)
    return found, skipped


def tool_version(tool, path):
    argv = [path, "version"] if tool == "go" else [path, "--version"]
    try:
        result = subprocess.run(argv, capture_output=True, text=True, timeout=30, env={"PATH": str(Path(path).parent)})
    except (OSError, subprocess.TimeoutExpired) as exc:
        return f"unavailable ({exc})"
    return (result.stdout or result.stderr).strip().splitlines()[0] if (result.stdout or result.stderr) else "?"


def language_env(language, tools, home, shared):
    env = {"HOME": str(home), "TMPDIR": str(home), "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8"}
    if language == "go":
        env.update(PATH=str(Path(tools["go"]).parent), GOTOOLCHAIN="local", GOPROXY="off", GOFLAGS="", GOWORK="off",
                   GOENV="off", GOTELEMETRY="off", CGO_ENABLED="0", GOCACHE=str(shared / "gocache"),
                   GOPATH=str(home / "gopath"))
    elif language == "python":
        env.update(PYTHONDONTWRITEBYTECODE="1", PYTHONUTF8="1")
    elif language == "javascript":
        env.update(PATH=str(Path(tools["node"]).parent))
    elif language in ("rust", "cpp"):
        env.update(PATH=str(Path(tools["cxx"]).parent),
                   ASAN_OPTIONS="detect_leaks=1:abort_on_error=0:halt_on_error=1",
                   UBSAN_OPTIONS="halt_on_error=1:print_stacktrace=1", RUST_BACKTRACE="0")
    return env


def expand(value, tools, run, out):
    for key, path in tools.items():
        value = value.replace("{" + key + "}", path)
    return value.replace("{run}", str(run)).replace("{out}", str(out))


def run_process(argv, cwd, env, timeout):
    process = subprocess.Popen(argv, cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               text=True, errors="replace", start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=timeout)
        return process.returncode, stdout, stderr, False
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        stdout, stderr = process.communicate()
        return None, stdout, stderr, True
    finally:
        try:
            os.killpg(process.pid, signal.SIGKILL)  # 남은 자식 프로세스 정리
        except ProcessLookupError:
            pass


def execute(case, variant, tools, scratch, shared):
    """결과: ('pass'|'violation'|'error', 설명)."""
    root = Path(tempfile.mkdtemp(prefix="c-", dir=scratch))
    source, run, out, home = root / "in", root / "run", root / "out", root / "home"
    files = render(case, variant)
    write_tree(source, files)
    shutil.copytree(source, run)
    for entry in case["check"]["files"]:
        target = run / entry["path"]
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text_of(entry), encoding="utf-8")
    out.mkdir()
    home.mkdir()
    env = language_env(case["language"], tools, home, shared)
    check = case["check"]
    log = []
    for step in check["build"]:
        argv = [expand(a, tools, run, out) for a in step["argv"]]
        code, stdout, stderr, timed_out = run_process(argv, run, {**env, **step.get("env", {})}, 600)
        if code != 0:
            return "error", f"build step failed ({'timeout' if timed_out else code}): {argv[0]}\n{stdout}{stderr}"[-3000:]
    step = check["run"]
    argv = [expand(a, tools, run, out) for a in step["argv"]]
    code, stdout, stderr, timed_out = run_process(argv, run, {**env, **step.get("env", {})},
                                                  check.get("timeout_seconds", 60))
    output = stdout + stderr
    if tree_digest(files) != tree_digest({p: (source / p).read_text(encoding="utf-8") for p in files}):
        return "error", "input tree changed during check"
    shutil.rmtree(root, ignore_errors=True)
    if timed_out:
        return "error", "outer timeout (harness watchdog missing or hung)\n" + output[-2000:]
    signature = re.search(check["failure_signature"], output)
    if code == 0 and PASS_SENTINEL in stdout and FAIL_SENTINEL not in output and not signature:
        return "pass", ""
    if code != 0 and signature:
        return "violation", signature.group(0)
    log.append(output[-3000:])
    return "error", f"unclassified exit={code}\n" + "\n".join(log)


def verify(cases, args):
    errors, counts = validate(cases)
    selftest_ok, planted = leakage_selftest(cases[0])
    tools, skipped = toolchains(args)
    skipped_cases = set()
    print("toolchains:")
    for tool, path in sorted(tools.items()):
        print(f"  {tool}: {path} ({tool_version(tool, path)})")
    selected = [c for c in cases if (not args.language or c["language"] == args.language)
                and (not args.case or c["id"] in args.case)]
    for case in cases:
        if case not in selected and case["language"] not in skipped:
            skipped_cases.add(case["id"])
    jobs = []
    for case in selected:
        if case["language"] in skipped:
            continue
        expected = {"before": "violation", "after": "pass"} if case["kind"] == "defect_pair" else {"before": "pass"}
        for variant, want in expected.items():
            for attempt in range(args.repeat):
                jobs.append((case, variant, want, attempt))
    results = {}
    with tempfile.TemporaryDirectory(prefix="codeye-r1-") as tmp:
        scratch = Path(tmp)
        shared = scratch / "shared"
        shared.mkdir()
        # Go 빌드 캐시를 먼저 채워 병렬 실행에서 표준 라이브러리 재컴파일을 줄인다.
        with ThreadPoolExecutor(max_workers=args.jobs) as pool:
            futures = {pool.submit(execute, case, variant, tools, scratch, shared): (case, variant, want, attempt)
                       for case, variant, want, attempt in jobs}
            for future, (case, variant, want, attempt) in futures.items():
                outcome, detail = future.result()
                results.setdefault((case["id"], variant), []).append((outcome, want, detail))
    mismatches = 0
    for case in cases:
        if case["language"] in skipped or case["id"] in skipped_cases:
            reason = skipped.get(case["language"], "not selected")
            print(f"SKIP {case['id']:<10} {case['language']:<10} {case['kind']:<13} ({reason})")
            continue
        parts = []
        ok = True
        for variant in variants(case):
            outcomes = results[(case["id"], variant)]
            seen = sorted({o for o, _, _ in outcomes})
            want = outcomes[0][1]
            good = seen == [want]
            ok &= good
            parts.append(f"{variant}={'/'.join(seen)}")
            if not good:
                for outcome, _, detail in outcomes:
                    if outcome != want:
                        print(f"  {case['id']} {variant}: got {outcome}, want {want}\n" + textwrap.indent(detail, "    "))
                        break
        mismatches += not ok
        print(f"{'OK  ' if ok else 'BAD '} {case['id']:<10} {case['language']:<10} {case['kind']:<13} {' '.join(parts)}")
    for language, reason in sorted(skipped.items()):
        print(f"SKIPPED language {language}: {reason} (not counted as passed)")
    for error in errors:
        print("INVALID", error)
    print(f"leakage scanner self-test: {'ok' if selftest_ok else 'FAILED'} ({planted} planted leaks detected, clean input clean)")
    executed = sum(1 for c in cases if c["language"] not in skipped and c["id"] not in skipped_cases)
    print(f"summary: {executed - mismatches}/{executed} executed cases as expected, {len(cases) - executed} skipped, "
          f"{len(errors)} structural/leakage errors, repeat={args.repeat}")
    print("counts:", json.dumps(counts, sort_keys=True))
    print("corpus digest:", corpus_digest(cases))
    if executed == 0:
        print("verify: no case executed; give at least one toolchain (nothing is reported as passed)")
    if mismatches or errors or not selftest_ok or executed == 0 or (args.require_all and (skipped or skipped_cases)):
        raise SystemExit(1)


# ---------------------------------------------------------------- cli

def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    m = sub.add_parser("materialize", help="write analysis inputs only")
    m.add_argument("case_id")
    m.add_argument("variant", choices=("before", "after"))
    m.add_argument("dest", type=Path)
    m.add_argument("--documented", action="store_true", help="also write README.md with the contract")
    sub.add_parser("validate", help="structure, locations, digests and leakage scan")
    v = sub.add_parser("verify", help="validate and run every check with real toolchains")
    for tool, env in TOOL_ENV.items():
        v.add_argument("--" + tool, help=f"absolute path (default ${env})")
    v.add_argument("--repeat", type=int, default=1, help="runs per variant, to expose nondeterminism")
    v.add_argument("--jobs", type=int, default=4)
    v.add_argument("--require-all", action="store_true", help="fail when a language or case is skipped")
    v.add_argument("--language", choices=LANGUAGES, help="run checks for one language only")
    v.add_argument("--case", action="append", help="run checks for this case id only (repeatable)")
    sub.add_parser("digest", help="print the corpus digest")
    sub.add_parser("rehash", help="rewrite stored per-variant digests in corpus.json")
    f = sub.add_parser("forbidden", help="label phrases and tokens the relay must block (never in inputs)")
    f.add_argument("--allow-text", action="append", default=[], type=Path,
                   help="file or directory of text that may legitimately reach the model (repeatable)")
    f.add_argument("--output", type=Path, help="write the JSON here (default stdout)")
    args = parser.parse_args()
    cases = load_cases()
    if args.command == "materialize":
        print(materialize(args.dest, find_case(cases, args.case_id), args.variant, args.documented))
    elif args.command == "validate":
        errors, counts = validate(cases)
        selftest_ok, planted = leakage_selftest(cases[0])
        for error in errors:
            print("INVALID", error)
        print("counts:", json.dumps(counts, sort_keys=True))
        print(f"leakage scanner self-test: {'ok' if selftest_ok else 'FAILED'} ({planted} planted)")
        if errors or not selftest_ok:
            raise SystemExit(1)
        print(f"PASS: {len(cases)} cases valid, 0 leakage hits")
    elif args.command == "verify":
        verify(cases, args)
    elif args.command == "digest":
        print(corpus_digest(cases))
    elif args.command == "forbidden":
        doc = forbidden_list(cases, args.allow_text)
        text = json.dumps(doc, indent=1, ensure_ascii=False, sort_keys=True) + "\n"
        if args.output:
            if args.output.exists():
                raise SystemExit(f"output exists: {args.output}")
            args.output.write_text(text, encoding="utf-8")
            print(f"{len(doc['entries'])} entries, dropped {doc['dropped']}, sha256 "
                  f"{hashlib.sha256(text.encode('utf-8')).hexdigest()}", file=sys.stderr)
        else:
            sys.stdout.write(text)
    elif args.command == "rehash":
        for case in cases:
            case["digests"] = computed_digests(case)
        CASES.write_text(json.dumps(cases, indent=1, ensure_ascii=False) + "\n", encoding="utf-8")
        print(corpus_digest(cases))


if __name__ == "__main__":
    main()
