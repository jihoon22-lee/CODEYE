"""공개 프로젝트의 실제 결함 수정과 심은 결함으로 만든 프로젝트 규모 평가 사례.

하위 명령:
  validate [--split S]                         메타데이터·패치·라벨 구조 검사(네트워크·도구 불필요)
  digest [--split S]                           분할 digest(holdout 봉인용). 메타데이터와 패치만으로 계산
  digest --split spent --as-split holdout      소진된 분할을 봉인 당시의 분할 이름으로 계산한 이력 digest
  materialize <id> <before|after> <dest> --cache DIR   분석 입력 트리만 쓰고 트리 digest 출력(네트워크 필요)
  verify [--split S] [--case ID] --cache DIR [--run-checks ...]
                                               pin 해석·패치 적용·트리 digest·범위 줄 수·앵커·누설 검사,
                                               선택하면 사례별 검사를 실제 toolchain으로 실행
  forbidden --split S --cache DIR [--allow-text PATH ...] [--output FILE]
                                               모델 입력에 나오면 안 되는 라벨 문구 목록(JSON)

표준 라이브러리와 git만 쓴다. `python3 -I`로 실행한다. 저장소 내용은 저장소에 넣지 않고
`--cache` 디렉터리에 pin한 커밋만 받아 둔다. 분석 입력에는 `.git`, 사례 ID, 변형 이름, 라벨이 없다.
"""

import argparse
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
from pathlib import Path, PurePosixPath

HERE = Path(__file__).resolve().parent
CASES_DIR = HERE / "cases"
SCHEMA = "codeye-project-defect-case"
LANGUAGES = ("go", "python", "javascript", "java", "cpp", "rust")
ID_PREFIX = {"go": "GO", "python": "PY", "javascript": "JS", "java": "JV", "cpp": "CC", "rust": "RS"}
CATEGORIES = ("error_handling", "resource_lifecycle", "concurrency", "boundary_correctness", "api_contract",
              "data_integrity", "input_to_sink_security")
KINDS = ("real_fix", "seeded")
SPLITS = ("dev", "holdout", "spent")
SPENT_SPLIT = "spent"  # 한 번 평가에 쓰여 봉인이 소진된 분할. 이 분할의 평가는 독립 평가가 아니다
VARIANTS = ("before", "after")
VERIFICATION = ("verified", "partially_verified", "not_verified")
SOURCE_EXT = {
    "go": (".go",),
    "python": (".py",),
    "javascript": (".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx"),
    "java": (".java",),
    "cpp": (".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp"),
    "rust": (".rs",),
}
# 범위 줄 수는 테스트·예제·생성물이 아닌 주 소스만 센다.
NON_PRIMARY = re.compile(
    r"(^|/)(tests?|testing|testdata|test_data|__tests__|__mocks__|spec|specs|fixtures?|benches|benchmarks?|_?examples?|"
    r"fuzz|fuzzing|docs?|vendor|third_party|node_modules|dist|build)(/|$)"
    r"|(^|/)tests?\.(c|cc|cpp|rs)$"
    r"|(_test\.go|_test\.py|(^|/)test_[^/]*\.py|(^|/)conftest\.py|\.(test|spec)\.[cm]?[jt]sx?|(?-i:(Test|Tests|IT|TestCase)\.java)|"
    r"_test\.(c|cc|cpp)|_unittest\.(c|cc|cpp)|\.test-d\.ts|\.d\.ts)$", re.IGNORECASE)
MIN_SCOPE_LINES, MAX_SCOPE_LINES = 1500, 6000
FIRST_LINES = 50  # 양성 위치는 파일의 첫 50줄 밖에 있어야 한다
MIN_SPLIT = {"dev": 10, "holdout": 18, "spent": 8}
# 봉인 분할의 구성(README의 보류 분할 선정 규칙): 언어마다 3개, 종류 9/9, 범주마다 2~3개, 다른 파일이 필요한 사례 14개 이상.
HOLDOUT_PER_LANGUAGE = 3
HOLDOUT_KINDS = {"real_fix": 9, "seeded": 9}
HOLDOUT_CATEGORY_RANGE = (2, 3)
HOLDOUT_MIN_CROSS_FILE = 14
NGRAM = 5
REQUIRED_CASE = ("schema", "id", "split", "language", "kind", "category", "family", "needs_cross_file", "repo", "scope",
                 "before", "after", "trees", "label", "verification")
REQUIRED_LABEL = ("trigger", "expected_impact", "positive_locations", "context_locations", "known_counterevidence",
                  "fix_summary", "distinctive_tokens")
PROSE_FIELDS = ("trigger", "expected_impact", "fix_summary")
PERMISSIVE = {"MIT", "BSD-2-Clause", "BSD-3-Clause", "Apache-2.0", "ISC", "0BSD", "Unlicense", "Zlib", "BSL-1.0",
              "MIT OR Apache-2.0", "Apache-2.0 OR MIT", "PSF-2.0", "Python-2.0"}
# 심은 패치의 추가 줄에 나오면 정답을 암시하는 단어(holdout corpus와 같은 기준).
BANNED_STEMS = ("bug", "fixme", "todo", "hack", "leak", "defect", "vuln", "broken", "wrong", "incorrect", "oops",
                "mistake", "deadlock", "overflow", "underflow", "dangl", "inject", "traversal", "holdout", "corpus",
                "codeye", "flaw", "faulty", "bogus", "pitfall", "regress", "workaround", "seeded")
BANNED_WORDS = {"xxx", "race", "racy", "fix", "fixed", "fixes", "corrected", "before", "after", "variant", "label",
                "intentional", "intentionally", "deliberate", "deliberately", "beware", "caution", "danger", "gotcha",
                "subtle"}
TEXT_LIMIT = 2 << 20  # 누설 검사·정답 목록에서 읽는 파일 크기 상한

_HOLDOUT = None


def holdout():
    """정답 차단 규칙(codeye-words-v1)과 단어 분해는 기존 보류 corpus의 구현을 그대로 쓴다."""
    global _HOLDOUT
    if _HOLDOUT is None:
        import runpy
        _HOLDOUT = runpy.run_path(str(HERE.parent / "quality-holdout" / "corpus.py"), run_name="holdout_corpus")
    return _HOLDOUT


# ---------------------------------------------------------------- loading

def load_cases(split=None):
    cases = []
    for path in sorted(CASES_DIR.glob("*.json")):
        case = json.loads(path.read_text(encoding="utf-8"))
        case["_file"] = path.name
        cases.append(case)
    if split:
        cases = [c for c in cases if c.get("split") == split]
    return cases


def find_case(cases, case_id):
    for case in cases:
        if case["id"] == case_id:
            return case
    raise SystemExit(f"unknown case: {case_id}")


def variants(case):
    return VARIANTS


def public(case):
    return {k: v for k, v in case.items() if not k.startswith("_")}


# ---------------------------------------------------------------- unified diffs (no fuzz, no git)

HUNK = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")


def parse_diff(text):
    """[{old, new, hunks: [{old_start, old_len, new_start, new_len, lines}]}] of a unified diff."""
    files, current, hunk = [], None, None
    lines = text.split("\n")
    if lines and lines[-1] == "":
        lines.pop()
    i = 0
    while i < len(lines):
        line = lines[i]
        if line.startswith("--- ") and i + 1 < len(lines) and lines[i + 1].startswith("+++ "):
            old = line[4:].split("\t")[0]
            new = lines[i + 1][4:].split("\t")[0]
            current = {"old": None if old == "/dev/null" else old[2:] if old.startswith("a/") else old,
                       "new": None if new == "/dev/null" else new[2:] if new.startswith("b/") else new, "hunks": []}
            files.append(current)
            hunk = None
            i += 2
            continue
        m = HUNK.match(line)
        if m:
            if current is None:
                raise ValueError("hunk before file header")
            hunk = {"old_start": int(m.group(1)), "old_len": 1 if m.group(2) is None else int(m.group(2)),
                    "new_start": int(m.group(3)), "new_len": 1 if m.group(4) is None else int(m.group(4)),
                    "lines": []}
            current["hunks"].append(hunk)
            i += 1
            continue
        if hunk is not None and line[:1] in (" ", "+", "-"):
            hunk["lines"].append(line)
        elif hunk is not None and line.startswith("\\"):
            hunk["lines"].append(line)
        elif line.startswith(("diff --git", "index ", "new file mode", "deleted file mode", "similarity", "rename ",
                              "old mode", "new mode")):
            hunk = None
        elif line.strip():
            raise ValueError(f"unexpected diff line {i + 1}: {line[:60]!r}")
        i += 1
    for f in files:
        for h in f["hunks"]:
            body = [l for l in h["lines"] if not l.startswith("\\")]
            old = sum(l[:1] in (" ", "-") for l in body)
            new = sum(l[:1] in (" ", "+") for l in body)
            if old != h["old_len"] or new != h["new_len"]:
                raise ValueError(f"{f['new'] or f['old']}: hunk @@ -{h['old_start']} counts do not match its body")
    return files


def diff_path(entry):
    return entry["new"] or entry["old"]


def segments(h):
    """A hunk as ('same', old, new) lines and ('change', old_start, old_count, new_start, new_count) runs."""
    out, o, n = [], h["old_start"], h["new_start"]
    run = None
    for l in h["lines"]:
        tag = l[:1]
        if tag == "\\":
            continue
        if tag == " ":
            if run:
                out.append(("change", *run))
                run = None
            out.append(("same", o, n))
            o, n = o + 1, n + 1
            continue
        if run is None:
            run = [o, 0, n, 0]
        if tag == "-":
            run[1] += 1
            o += 1
        else:
            run[3] += 1
            n += 1
    if run:
        out.append(("change", *run))
    return out


def changed_lines(entry, side):
    """Changed line numbers on one side ('old' or 'new'). A pure insertion/deletion marks the adjacent lines."""
    out = set()
    for h in entry["hunks"]:
        for seg in segments(h):
            if seg[0] != "change":
                continue
            _, o, oc, n, nc = seg
            start, count = (o, oc) if side == "old" else (n, nc)
            if count:
                out.update(range(start, start + count))
            else:
                out.update((max(1, start - 1), start))
    return out


def hunk_sides(h):
    """(old lines, new lines) of a hunk with line endings; a '\\ No newline' marker drops the ending."""
    old, new = [], []
    body = h["lines"]
    for i, l in enumerate(body):
        tag = l[:1]
        if tag == "\\":
            continue
        text = l[1:] + ("" if i + 1 < len(body) and body[i + 1].startswith("\\") else "\n")
        if tag in (" ", "-"):
            old.append(text)
        if tag in (" ", "+"):
            new.append(text)
    return old, new


def apply_diff(text, entry, reverse=False):
    """Apply one file's hunks exactly (context must match at the stated line). reverse undoes them."""
    src = text.splitlines(keepends=True)
    out, cursor = [], 0
    for h in entry["hunks"]:
        old_lines, new_lines = hunk_sides(h)
        start = h["old_start"]
        if reverse:
            old_lines, new_lines = new_lines, old_lines
            start = h["new_start"]
        index = start - 1 if old_lines else start
        if index < cursor or src[index:index + len(old_lines)] != old_lines:
            raise ValueError(f"{diff_path(entry)}: hunk at line {start} does not apply")
        out.extend(src[cursor:index])
        out.extend(new_lines)
        cursor = index + len(old_lines)
    out.extend(src[cursor:])
    return "".join(out)


def map_line(line, entry, side):
    """Map a line on `side` ('old' or 'new') of the diff to the other side; changed lines map to the
    replacement block. Returns (start, end) on the other side."""
    delta = 0
    for h in entry["hunks"]:
        a_start, a_len = (h["old_start"], h["old_len"]) if side == "old" else (h["new_start"], h["new_len"])
        b_start, b_len = (h["new_start"], h["new_len"]) if side == "old" else (h["old_start"], h["old_len"])
        if line < a_start:
            break
        if line < a_start + a_len:
            for seg in segments(h):
                if seg[0] == "same":
                    a, b = (seg[1], seg[2]) if side == "old" else (seg[2], seg[1])
                    if a == line:
                        return b, b
                    continue
                _, o, oc, n, nc = seg
                a, ac, b, bc = (o, oc, n, nc) if side == "old" else (n, nc, o, oc)
                if a <= line < a + ac:
                    return (b, b + bc - 1) if bc else (max(1, b - 1), b)
        delta = (b_start + b_len) - (a_start + a_len)
    return line + delta, line + delta


def map_range(start, end, entry, side):
    s, _ = map_line(start, entry, side)
    _, e = map_line(end, entry, side)
    return min(s, e), max(s, e)


def read_text(rel):
    path = (HERE / rel).resolve()
    if HERE not in path.parents:
        raise ValueError(f"path outside the corpus directory: {rel}")
    return path.read_text(encoding="utf-8")


def case_diff(case):
    """(entries, side) where side is the side of the diff that equals the before variant."""
    if case["kind"] == "seeded":
        return parse_diff(read_text(case["before"]["patch"])), "new"
    return parse_diff(read_text(case["fix_diff"])), "old"


# ---------------------------------------------------------------- label targets (offline)

def targets(case, variant):
    """Label locations a finding is compared with. Before: positive_locations. After: the same ranges
    carried over the stored diff (after_mapped). Offline: no repository is needed."""
    label = case["label"]
    out = []
    if variant == "before":
        for l in label["positive_locations"]:
            out.append({"kind": "positive", "path": l["path"], "start_line": l["start_line"], "end_line": l["end_line"],
                        "symbol": l.get("symbol", "")})
        return out
    entries, side = case_diff(case)
    by_path = {diff_path(e): e for e in entries}
    for l in label["positive_locations"]:
        s, e = l["start_line"], l["end_line"]
        entry = by_path.get(l["path"])
        if entry is not None:
            s, e = map_range(s, e, entry, side)
        out.append({"kind": "after_mapped", "path": l["path"], "start_line": s, "end_line": e,
                    "symbol": l.get("symbol", "")})
    return out


def scope_paths(case):
    return [case["scope"]] if case["scope"] not in (".", "") else []


# ---------------------------------------------------------------- digests (offline)

def canonical(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def case_digest(case):
    digest = hashlib.sha256()
    digest.update(b"case\0" + hashlib.sha256(canonical(public(case))).hexdigest().encode("ascii") + b"\n")
    for rel in sorted(attached_files(case)):
        digest.update(rel.encode() + b"\0" + hashlib.sha256((HERE / rel).read_bytes()).hexdigest().encode() + b"\n")
    return digest.hexdigest()


def attached_files(case):
    out = []
    if case.get("kind") == "seeded" and case.get("before", {}).get("patch"):
        out.append(case["before"]["patch"])
    if case.get("fix_diff"):
        out.append(case["fix_diff"])
    for entry in (case.get("check") or {}).get("files", []):
        out.append(entry["source"])
    return out


def corpus_digest(cases):
    """Digest over every case's metadata, stored tree digests and attached patch/check files."""
    digest = hashlib.sha256()
    for case in sorted(cases, key=lambda c: c["id"]):
        digest.update(f"{case['id']}\0{case_digest(case)}\n".encode("utf-8"))
    return digest.hexdigest()


def split_digest(split, as_split=None):
    """Digest of one split. as_split renames the cases' split field before hashing: the spent split
    reproduces the digest it was sealed with under its earlier name."""
    cases = load_cases(split)
    if as_split:
        cases = [{**c, "split": as_split} for c in cases]
    return corpus_digest(cases)


# ---------------------------------------------------------------- fetching (online, cache directory)

def git_env(cache):
    home = Path(cache) / ".home"
    home.mkdir(parents=True, exist_ok=True)
    return {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": str(home), "LANG": "C.UTF-8",
            "GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_ASKPASS": "/bin/false"}


def git(cache, repo_dir, *args, input=None, timeout=600, check=True):
    result = subprocess.run(["git", "-C", str(repo_dir), *args], capture_output=True, input=input,
                            env=git_env(cache), timeout=timeout)
    if check and result.returncode:
        raise RuntimeError(f"git {args[0]} failed ({result.returncode}): "
                           + result.stderr.decode(errors="replace").strip()[-400:])
    return result


def repo_dir(cache, url):
    m = re.fullmatch(r"https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?", url)
    if not m:
        raise ValueError(f"repository URL must be https://github.com/OWNER/NAME: {url}")
    return Path(cache) / f"{m.group(1)}__{m.group(2)}.git"


def has_commit(cache, directory, sha):
    return git(cache, directory, "cat-file", "-e", sha + "^{commit}", check=False).returncode == 0


def fetch(cache, url, sha):
    """Make sure commit `sha` (and its tree) is in the cache. Fails clearly when offline."""
    cache = Path(cache)
    cache.mkdir(parents=True, exist_ok=True)
    directory = repo_dir(cache, url)
    if not directory.exists():
        subprocess.run(["git", "init", "--bare", "-q", str(directory)], check=True, env=git_env(cache),
                       capture_output=True)
        git(cache, directory, "remote", "add", "origin", url)
    if has_commit(cache, directory, sha):
        return directory
    result = git(cache, directory, "fetch", "-q", "--depth=1", "--no-tags", "origin", sha, check=False, timeout=1800)
    if result.returncode or not has_commit(cache, directory, sha):
        raise SystemExit(f"cannot fetch {url} {sha[:12]} into {cache} (network needed for materialize/verify): "
                         + result.stderr.decode(errors="replace").strip()[-300:])
    return directory


def commit_parents(cache, directory, sha):
    body = git(cache, directory, "cat-file", "-p", sha).stdout.decode()
    return [line.split()[1] for line in body.split("\n\n", 1)[0].splitlines() if line.startswith("parent ")]


def tree_blobs(cache, directory, sha):
    """{path: (mode, blob)} of regular files; symlinks and submodules are reported separately."""
    out, skipped = {}, []
    data = git(cache, directory, "ls-tree", "-r", "-z", "--full-tree", sha).stdout.decode("utf-8", "surrogateescape")
    for entry in data.split("\0"):
        if not entry:
            continue
        meta, path = entry.split("\t", 1)
        mode, kind, blob = meta.split()
        if kind == "blob" and mode in ("100644", "100755"):
            out[path] = (mode, blob)
        else:
            skipped.append(path)
    return out, skipped


def read_blobs(cache, directory, blobs):
    """{blob: bytes} through one `git cat-file --batch`."""
    unique = sorted(set(blobs))
    if not unique:
        return {}
    data = git(cache, directory, "cat-file", "--batch", input=("\n".join(unique) + "\n").encode()).stdout
    out, pos = {}, 0
    for blob in unique:
        header_end = data.index(b"\n", pos)
        name, kind, size = data[pos:header_end].decode().split()
        size = int(size)
        out[name] = data[header_end + 1:header_end + 1 + size]
        pos = header_end + 1 + size + 1
    return out


def render(case, variant, cache):
    """{path: (mode, bytes)} of one variant's whole tree, without .git. Online through the cache."""
    if variant not in VARIANTS:
        raise ValueError(variant)
    url = case["repo"]["url"]
    sha = case[variant]["commit"]
    directory = fetch(cache, url, sha)
    blobs, skipped = tree_blobs(cache, directory, sha)
    contents = read_blobs(cache, directory, [b for _, b in blobs.values()])
    files = {p: (m, contents[b]) for p, (m, b) in blobs.items()}
    if case["kind"] == "seeded" and variant == "before":
        for entry in parse_diff(read_text(case["before"]["patch"])):
            path = diff_path(entry)
            if entry["old"] is None:
                files[path] = ("100644", apply_diff("", entry).encode("utf-8"))
                continue
            mode, data = files[entry["old"]]
            files[path] = (mode, apply_diff(data.decode("utf-8"), entry).encode("utf-8"))
    return files, skipped


def tree_digest(files):
    digest = hashlib.sha256()
    for path in sorted(files):
        digest.update(path.encode("utf-8", "surrogateescape") + b"\0"
                      + hashlib.sha256(files[path][1]).hexdigest().encode("ascii") + b"\n")
    return digest.hexdigest()


def write_tree(destination, files):
    destination = Path(destination)
    if destination.exists() and any(destination.iterdir()):
        raise SystemExit(f"destination is not empty: {destination}")
    destination.mkdir(mode=0o755, parents=True, exist_ok=True)
    for path, (mode, data) in files.items():
        rel = PurePosixPath(path)
        if rel.is_absolute() or ".." in rel.parts or rel.parts[0] == ".git":
            continue
        target = destination / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        os.chmod(target, 0o755 if mode == "100755" else 0o644)


def materialize(destination, case, variant, cache, check=True):
    """Write the variant's tree (no .git, no case id, no label) and return its digest. With check the
    digest must equal the stored one, so a run never analyses an unreviewed tree."""
    files, _ = render(case, variant, cache)
    digest = tree_digest(files)
    stored = case["trees"][variant]["digest"]
    if check and digest != stored:
        raise SystemExit(f"{case['id']} {variant}: tree digest {digest[:12]} differs from the stored {stored[:12]}")
    write_tree(destination, files)
    return digest


# ---------------------------------------------------------------- scope size

def primary_source(case, path):
    scope = case["scope"].strip("/")
    inside = scope in ("", ".") or path == scope or path.startswith(scope + "/")
    rel = path[len(scope) + 1:] if scope not in ("", ".") and path.startswith(scope + "/") else path
    return inside and path.endswith(SOURCE_EXT[case["language"]]) and not NON_PRIMARY.search(rel)


def scope_lines(case, files):
    count, n = 0, 0
    for path, (_, data) in files.items():
        if primary_source(case, path):
            count += data.count(b"\n") + (0 if data.endswith(b"\n") or not data else 1)
            n += 1
    return count, n


# ---------------------------------------------------------------- leakage

def label_prose(case):
    label = case["label"]
    texts = [label[f] for f in PROSE_FIELDS if label.get(f)]
    texts += [item["note"] for item in label["known_counterevidence"] if item.get("note")]
    texts += [item["note"] for item in label["context_locations"] if item.get("note")]
    texts += [(case.get("check") or {}).get("summary", "")]
    return [t for t in texts if t]


def text_files(files, limit=TEXT_LIMIT):
    for path, (_, data) in sorted(files.items()):
        if len(data) > limit or b"\0" in data[:4096]:
            continue
        yield path, data.decode("utf-8", errors="replace")


def leakage(case, trees):
    """trees: {variant: files}. Label n-grams and distinctive tokens that occur in the inputs."""
    h = holdout()
    hits = set()
    for variant, files in trees.items():
        index = h["TextIndex"]()
        for path, text in text_files(files):
            index.add(path + "\n" + text)
        for token in case["label"]["distinctive_tokens"]:
            t = token.lower()
            if index.contains(h["match_mode"](t), t):
                hits.add(f"{variant}: distinctive label token '{token}'")
        for text in label_prose(case):
            seq = h["words"](text)
            for i in range(len(seq) - NGRAM + 1):
                gram = " ".join(seq[i:i + NGRAM])
                if index.contains("words", gram):
                    hits.add(f"{variant}: label phrase '{gram}'")
    return sorted(hits)


def patch_hints(case):
    """Offline leakage on a seeded patch: added lines must not carry hint words or label wording."""
    h = holdout()
    hits = []
    if case["kind"] != "seeded":
        return hits
    added = [l[1:] for e in parse_diff(read_text(case["before"]["patch"])) for hk in e["hunks"]
             for l in hk["lines"] if l.startswith("+")]
    text = "\n".join(added)
    tokens = h["subwords"](text)
    hits += [f"patch adds hint word '{t}'" for t in sorted(tokens) if t in BANNED_WORDS
             or any(t.startswith(s) for s in BANNED_STEMS)]
    index = h["TextIndex"]()
    index.add(text)
    for token in case["label"]["distinctive_tokens"]:
        t = token.lower()
        if index.contains(h["match_mode"](t), t):
            hits.append(f"patch adds distinctive label token '{token}'")
    for prose in label_prose(case):
        seq = h["words"](prose)
        for i in range(len(seq) - NGRAM + 1):
            if index.contains("words", " ".join(seq[i:i + NGRAM])):
                hits.append(f"patch adds label phrase '{' '.join(seq[i:i + NGRAM])}'")
    return sorted(set(hits))


# ---------------------------------------------------------------- validate (offline)

def fail(errors, case, message):
    errors.append(f"{case.get('id', case.get('_file', '?'))}: {message}")


def in_scope(case, path):
    scope = case["scope"].strip("/")
    return scope in ("", ".") or path == scope or path.startswith(scope + "/")


def check_location(errors, case, item, what):
    path = item.get("path")
    p = PurePosixPath(path or "")
    if not path or p.is_absolute() or ".." in p.parts:
        fail(errors, case, f"{what}: bad path {path!r}")
        return None
    start, end = item.get("start_line"), item.get("end_line")
    if not (isinstance(start, int) and isinstance(end, int) and 1 <= start <= end):
        fail(errors, case, f"{what} {path}: bad range {start}-{end}")
        return None
    anchors = item.get("anchors") or {}
    if not anchors.get("start"):
        fail(errors, case, f"{what} {path}:{start}: needs an anchors.start text (checked online by verify)")
    return set(range(start, end + 1))


def validate_case(case, errors):
    for field in REQUIRED_CASE:
        if field not in case:
            fail(errors, case, "missing field " + field)
            return
    if case["schema"] != SCHEMA:
        fail(errors, case, "schema must be " + SCHEMA)
    language = case["language"]
    if language not in LANGUAGES:
        fail(errors, case, "unknown language")
        return
    if case["_file"] != case["id"] + ".json":
        fail(errors, case, "file name must be <id>.json")
    if not re.fullmatch(rf"PD-{ID_PREFIX[language]}-\d{{2}}", case["id"]):
        fail(errors, case, "id must be PD-<language>-NN")
    if case["split"] not in SPLITS or case["kind"] not in KINDS or case["category"] not in CATEGORIES:
        fail(errors, case, "bad split/kind/category")
    if not re.fullmatch(r"[a-z0-9]+(-[a-z0-9]+)+", case["family"]):
        fail(errors, case, "family must be a kebab-case slug")
    if not isinstance(case["needs_cross_file"], bool):
        fail(errors, case, "needs_cross_file must be a boolean")
    repo = case["repo"]
    for field in ("url", "license", "license_file"):
        if not repo.get(field):
            fail(errors, case, "repo needs " + field)
    if repo.get("license") not in PERMISSIVE:
        fail(errors, case, f"licence {repo.get('license')!r} is not in the permissive list")
    try:
        repo_dir("/nonexistent", repo.get("url", ""))
    except ValueError as exc:
        fail(errors, case, str(exc))
    scope = PurePosixPath(case["scope"])
    if scope.is_absolute() or ".." in scope.parts or case["scope"].endswith("/"):
        fail(errors, case, "scope must be a relative directory without a trailing slash")
    for v in VARIANTS:
        if not re.fullmatch(r"[0-9a-f]{40}", str(case[v].get("commit"))):
            fail(errors, case, f"{v}.commit must be a full SHA-1")
        tree = case["trees"].get(v) or {}
        if not re.fullmatch(r"[0-9a-f]{64}", str(tree.get("digest"))):
            fail(errors, case, f"trees.{v}.digest missing (run verify --record after review)")
        lines = tree.get("scope_lines")
        if not (isinstance(lines, int) and MIN_SCOPE_LINES <= lines <= MAX_SCOPE_LINES):
            fail(errors, case, f"trees.{v}.scope_lines {lines} outside {MIN_SCOPE_LINES}-{MAX_SCOPE_LINES}")
    if case["kind"] == "seeded":
        if case["before"]["commit"] != case["after"]["commit"] or not case["before"].get("patch"):
            fail(errors, case, "seeded: before and after pin the same commit and before has a patch")
        if case.get("fix_diff"):
            fail(errors, case, "seeded: the patch is the diff; no fix_diff")
    else:
        if case["before"]["commit"] == case["after"]["commit"] or case["before"].get("patch"):
            fail(errors, case, "real_fix: before is the fix commit's parent, after the fix commit, no patch")
        if not case.get("fix_diff") or not case.get("fix_commit_url"):
            fail(errors, case, "real_fix needs fix_diff and fix_commit_url")
    label = case["label"]
    for field in REQUIRED_LABEL:
        if field not in label:
            fail(errors, case, "label missing " + field)
            return
    for field in PROSE_FIELDS:
        if len(label[field]) < 40:
            fail(errors, case, f"label.{field} too short")
    if not label["distinctive_tokens"]:
        fail(errors, case, "label needs distinctive tokens")
    try:
        entries, side = case_diff(case)
    except (ValueError, OSError, KeyError) as exc:
        fail(errors, case, f"diff unreadable: {exc}")
        return
    if not entries or not all(e["hunks"] for e in entries):
        fail(errors, case, "diff has no hunks")
        return
    by_path = {diff_path(e): e for e in entries}
    if not any(in_scope(case, p) for p in by_path):
        fail(errors, case, "the diff changes no file inside the scope")
    if case["kind"] == "seeded":
        total = sum(1 for e in entries for h in e["hunks"] for l in h["lines"] if l[:1] in "+-")
        if total > 40:
            fail(errors, case, f"seeded patch changes {total} lines; keep it to a single behaviour")
    hit = False
    if not label["positive_locations"]:
        fail(errors, case, "needs positive locations")
    for item in label["positive_locations"]:
        span = check_location(errors, case, item, "positive")
        if span is None:
            continue
        if item["start_line"] <= FIRST_LINES:
            fail(errors, case, f"positive {item['path']}:{item['start_line']} is within the first {FIRST_LINES} lines")
        if not in_scope(case, item["path"]):
            fail(errors, case, f"positive {item['path']} is outside the scope")
        entry = by_path.get(item["path"])
        if entry and span & changed_lines(entry, side):
            hit = True
    if not hit:
        fail(errors, case, "no positive location intersects the changed lines of the before side of the diff")
    files = {i["path"] for i in label["positive_locations"]}
    for item in label["context_locations"]:
        if check_location(errors, case, item, "context") is not None and not item.get("note"):
            fail(errors, case, "context location needs a note (why it is needed)")
        files.add(item.get("path"))
    if case["needs_cross_file"] and len(files) < 2:
        fail(errors, case, "needs_cross_file: positive and context locations must name two files")
    for item in label["known_counterevidence"]:
        if not item.get("note"):
            fail(errors, case, "counterevidence needs a note")
        if item.get("path"):
            check_location(errors, case, item, "counterevidence")
    verification = case["verification"]
    if verification.get("status") not in VERIFICATION or not verification.get("summary"):
        fail(errors, case, "verification needs status and summary")
    if verification.get("status") == "verified" and not case.get("check"):
        fail(errors, case, "verified needs a check that verify --run-checks can repeat")
    check = case.get("check")
    if check:
        for field in ("summary", "argv", "failure_signature"):
            if field not in check:
                fail(errors, case, "check missing " + field)
        try:
            re.compile(check.get("failure_signature", ""))
        except re.error as exc:
            fail(errors, case, f"failure_signature: {exc}")
        for entry in check.get("files", []):
            if not (HERE / entry["source"]).is_file():
                fail(errors, case, "check file missing " + entry["source"])
    for hint in patch_hints(case):
        fail(errors, case, "leakage " + hint)
    for rel in attached_files(case):
        if not (HERE / rel).is_file():
            fail(errors, case, "missing attached file " + rel)


def validate(cases, partial=False):
    """Structure of every case; unless partial, also the corpus-wide rules (sizes, split disjointness, spread)."""
    errors = []
    ids = [c.get("id") for c in cases]
    if len(ids) != len(set(ids)):
        errors.append("duplicate case ids")
    families = [c.get("family") for c in cases]
    if len(families) != len(set(families)):
        errors.append("duplicate family slugs (a family belongs to one case)")
    for case in cases:
        validate_case(case, errors)
    counts = {"cases": len(cases), "by_split": {}, "by_language": {}, "by_kind": {}, "by_category": {},
              "needs_cross_file": sum(bool(c.get("needs_cross_file")) for c in cases),
              "verification": {}}
    for c in cases:
        for key, field in (("by_split", "split"), ("by_language", "language"), ("by_kind", "kind"),
                           ("by_category", "category")):
            counts[key][c.get(field)] = counts[key].get(c.get(field), 0) + 1
        status = (c.get("verification") or {}).get("status")
        counts["verification"][status] = counts["verification"].get(status, 0) + 1
    projects = {}
    family_split = {}
    for c in cases:
        projects.setdefault((c.get("repo") or {}).get("url", "").lower().removesuffix(".git"), set()).add(c.get("split"))
        family_split.setdefault(c.get("family"), set()).add(c.get("split"))
    for url, splits in sorted(projects.items()):
        if len(splits) > 1:
            errors.append(f"project {url} appears in both splits")
    if not partial:
        for s in SPLITS:
            if counts["by_split"].get(s, 0) < MIN_SPLIT[s]:
                errors.append(f"split {s}: {counts['by_split'].get(s, 0)} cases, need at least {MIN_SPLIT[s]}")
        missing = [l for l in LANGUAGES if not counts["by_language"].get(l)]
        if missing:
            errors.append("languages without a case: " + ", ".join(missing))
        absent = [c for c in CATEGORIES if not counts["by_category"].get(c)]
        if absent:
            errors.append("categories without a case: " + ", ".join(absent))
        kinds = counts["by_kind"]
        if cases and not 0.35 <= kinds.get("real_fix", 0) / len(cases) <= 0.65:
            errors.append(f"kinds should be about half each: {kinds}")
        if cases and counts["needs_cross_file"] * 2 <= len(cases):
            errors.append("most cases should need a second file (needs_cross_file)")
        for s in SPLITS:
            langs = {c["language"] for c in cases if c.get("split") == s}
            if len(langs) < 4:
                errors.append(f"split {s} covers only {sorted(langs)}")
        errors += holdout_composition([c for c in cases if c.get("split") == "holdout"])
    return errors, counts


def holdout_composition(cases):
    """The sealed split's fixed shape (README, 보류 분할 선정 규칙). Empty when the split has no case."""
    if not cases:
        return []
    errors = []
    for language in LANGUAGES:
        n = sum(c.get("language") == language for c in cases)
        if n != HOLDOUT_PER_LANGUAGE:
            errors.append(f"holdout: {n} {language} cases, need {HOLDOUT_PER_LANGUAGE}")
        categories = [c.get("category") for c in cases if c.get("language") == language]
        if len(categories) != len(set(categories)):
            errors.append(f"holdout: {language} cases repeat a category")
    for kind, want in HOLDOUT_KINDS.items():
        n = sum(c.get("kind") == kind for c in cases)
        if n != want:
            errors.append(f"holdout: {n} {kind} cases, need {want}")
    low, high = HOLDOUT_CATEGORY_RANGE
    for category in CATEGORIES:
        n = sum(c.get("category") == category for c in cases)
        if not low <= n <= high:
            errors.append(f"holdout: {n} {category} cases, need {low}-{high}")
    cross = sum(bool(c.get("needs_cross_file")) for c in cases)
    if cross < HOLDOUT_MIN_CROSS_FILE:
        errors.append(f"holdout: {cross} cases need a second file, at least {HOLDOUT_MIN_CROSS_FILE}")
    return errors


# ---------------------------------------------------------------- verify (online)

def line_at(files, path, line):
    if path not in files:
        return None
    lines = files[path][1].decode("utf-8", errors="replace").split("\n")
    return lines[line - 1] if 1 <= line <= len(lines) else None


def check_anchors(errors, case, files, item, what):
    anchors = item.get("anchors") or {}
    first = line_at(files, item["path"], item["start_line"])
    last = line_at(files, item["path"], item["end_line"])
    if first is None or last is None:
        fail(errors, case, f"{what} {item['path']}:{item['start_line']}-{item['end_line']} not in the tree")
        return
    if anchors.get("start") not in first or anchors.get("end", anchors.get("start")) not in last:
        fail(errors, case, f"{what} {item['path']}:{item['start_line']}-{item['end_line']} anchors do not match")
    short = re.split(r"[.:]+", item.get("symbol") or "")[-1]
    if item.get("symbol") and short not in files[item["path"]][1].decode("utf-8", errors="replace"):
        fail(errors, case, f"{what} symbol {item['symbol']} not found in {item['path']}")


def verify_case(case, cache, errors, record=False):
    """Online checks of one case. Returns {variant: files} for further checks."""
    url = case["repo"]["url"]
    trees = {}
    for v in VARIANTS:
        try:
            files, skipped = render(case, v, cache)
        except (ValueError, KeyError, RuntimeError) as exc:
            fail(errors, case, f"{v}: cannot render: {exc}")
            return None
        trees[v] = files
        digest = tree_digest(files)
        lines, n = scope_lines(case, files)
        if record:
            case["trees"][v] = {"digest": digest, "scope_lines": lines, "scope_files": n, "files": len(files)}
        elif digest != case["trees"][v]["digest"]:
            fail(errors, case, f"{v}: tree digest {digest[:12]} != stored {case['trees'][v]['digest'][:12]}")
        if not MIN_SCOPE_LINES <= lines <= MAX_SCOPE_LINES:
            fail(errors, case, f"{v}: scope has {lines} primary source lines")
        if not any(in_scope(case, p) for p in files):
            fail(errors, case, f"{v}: scope directory {case['scope']} not in the tree")
        if skipped:
            print(f"  {case['id']} {v}: {len(skipped)} symlinks/submodules not written")
    directory = repo_dir(cache, url)
    if case["kind"] == "real_fix":
        parents = commit_parents(cache, directory, case["after"]["commit"])
        if parents != [case["before"]["commit"]]:
            fail(errors, case, f"before.commit is not the single parent of the fix commit (parents {parents})")
        entries, _ = case_diff(case)
        for e in entries:
            path = diff_path(e)
            old = trees["before"].get(e["old"]) if e["old"] else None
            new = trees["after"].get(e["new"]) if e["new"] else None
            try:
                applied = apply_diff(old[1].decode("utf-8") if old else "", e)
            except ValueError as exc:
                fail(errors, case, f"fix_diff does not apply to the before tree: {exc}")
                continue
            if new is None or applied != new[1].decode("utf-8"):
                fail(errors, case, f"fix_diff applied to {path} does not give the after file")
    for item in case["label"]["positive_locations"]:
        check_anchors(errors, case, trees["before"], item, "positive")
    for item in case["label"]["context_locations"]:
        check_anchors(errors, case, trees[item.get("variant", "before")], item, "context")
    for item in case["label"]["known_counterevidence"]:
        if item.get("path"):
            check_anchors(errors, case, trees[item.get("variant", "before")], item, "counterevidence")
    for t in targets(case, "after"):
        if line_at(trees["after"], t["path"], t["end_line"]) is None:
            fail(errors, case, f"after_mapped {t['path']}:{t['start_line']}-{t['end_line']} not in the after tree")
    lic = case["repo"]["license_file"]
    if lic not in trees["before"]:
        fail(errors, case, f"licence file {lic} not in the tree")
    for hit in leakage(case, trees):
        fail(errors, case, "leakage " + hit)
    return trees


TOOL_FLAGS = ("go", "python", "node", "java", "javac", "cxx", "cc", "cargo", "rustc", "cmake")


def toolchains(args):
    found = {}
    for tool in TOOL_FLAGS:
        value = getattr(args, tool, None)
        if not value:
            continue
        path = Path(value)
        if not path.is_absolute() or not path.is_file() or not os.access(path, os.X_OK):
            raise SystemExit(f"--{tool}: not an absolute executable path: {value}")
        found[tool] = str(path)
    return found


def run_process(argv, cwd, env, timeout):
    process = subprocess.Popen(argv, cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True, errors="replace", start_new_session=True)
    try:
        out, _ = process.communicate(timeout=timeout)
        return process.returncode, out, False
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        out, _ = process.communicate()
        return None, out, True
    finally:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass


def run_check(case, variant, files, tools, scratch, network):
    """Run the case's check on a work copy of one variant. ('pass'|'violation'|'error', detail).
    The analysis tree itself is never built or tested; the work copy is deleted afterwards."""
    check = case["check"]
    needed = sorted(set(re.findall(r"\{([a-z]+)\}", json.dumps(check))) - {"work", "home", "scratch"})
    missing = [t for t in needed if t not in tools]
    if missing:
        return "skipped", "toolchain not given: " + ", ".join(missing)
    root = Path(tempfile.mkdtemp(prefix="check-", dir=scratch))
    work, home = root / "work", root / "home"
    write_tree(work, files)
    home.mkdir()
    for entry in check.get("files", []):
        target = work / entry["path"]
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(HERE / entry["source"], target)
    shared = Path(scratch) / "shared"
    shared.mkdir(exist_ok=True)
    path_dirs = sorted({str(Path(p).parent) for p in tools.values()}) + ["/usr/bin", "/bin"]
    env = {"PATH": ":".join(path_dirs), "HOME": str(home), "TMPDIR": str(home), "LANG": "C.UTF-8",
           "LC_ALL": "C.UTF-8", "PYTHONDONTWRITEBYTECODE": "1", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=mod",
           "GOCACHE": str(shared / "gocache"), "GOMODCACHE": str(shared / "gomod"), "GOPATH": str(shared / "gopath"),
           "GOTELEMETRY": "off", "GOPROXY": "https://proxy.golang.org" if network else "off",
           "CARGO_HOME": str(shared / "cargo"), "CARGO_TARGET_DIR": str(shared / ("target-" + case["id"])),
           "CARGO_NET_OFFLINE": "false" if network else "true", "npm_config_cache": str(shared / "npm")}

    def expand(value):
        for key, path in tools.items():
            value = value.replace("{" + key + "}", path)
        return value.replace("{work}", str(work)).replace("{home}", str(home)).replace("{scratch}", str(scratch))

    env.update({k: expand(v) for k, v in check.get("env", {}).items()})
    cwd = work / check.get("cwd", ".")
    log = []
    for step in check.get("setup", []):
        code, out, timed_out = run_process([expand(a) for a in step], cwd, env, check.get("timeout_seconds", 900))
        if code != 0:
            shutil.rmtree(root, ignore_errors=True)
            return "error", f"setup failed ({'timeout' if timed_out else code}): {' '.join(step)[:200]}\n{out[-2500:]}"
    code, out, timed_out = run_process([expand(a) for a in check["argv"]], cwd, env, check.get("timeout_seconds", 900))
    subprocess.run(["chmod", "-R", "u+w", str(root)], capture_output=True)
    shutil.rmtree(root, ignore_errors=True)
    if timed_out:
        return "error", "timeout\n" + out[-2000:]
    signature = re.search(check["failure_signature"], out)
    if code == 0 and not signature:
        return "pass", ""
    if code != 0 and signature:
        return "violation", signature.group(0)
    log.append(out[-3000:])
    return "error", f"unclassified exit={code}\n" + "\n".join(log)


def tool_version(tool, path):
    argv = [path, "version"] if tool == "go" else [path, "-version"] if tool in ("java", "javac") else [path, "--version"]
    try:
        r = subprocess.run(argv, capture_output=True, text=True, timeout=30)
    except (OSError, subprocess.TimeoutExpired) as exc:
        return f"unavailable ({exc})"
    text = (r.stdout or r.stderr).strip()
    return text.splitlines()[0] if text else "?"


def verify(cases, args):
    errors = []
    tools = toolchains(args)
    for tool, path in sorted(tools.items()):
        print(f"toolchain {tool}: {path} ({tool_version(tool, path)})")
    results = []
    scratch = Path(tempfile.mkdtemp(prefix="pdc-verify-"))
    try:
        for case in cases:
            before = len(errors)
            trees = verify_case(case, args.cache, errors, record=args.record)
            outcome = {}
            if trees and args.run_checks and case.get("check"):
                for v, want in (("before", "violation"), ("after", "pass")):
                    got, detail = run_check(case, v, trees[v], tools, scratch, args.network)
                    outcome[v] = got
                    if got != want:
                        print(f"  {case['id']} {v}: got {got}, want {want}\n" + textwrap.indent(detail[-2500:], "    "))
                        # a check that could not run (toolchain not given) is not a pass
                        fail(errors, case, f"check {v}: {got}" + (f" ({detail})" if got == "skipped" else ""))
            elif args.run_checks:
                outcome = {"check": "none"}
            ok = len(errors) == before
            results.append(ok)
            print(f"{'OK ' if ok else 'BAD'} {case['id']:<10} {case['language']:<10} {case['kind']:<8} "
                  f"lines {case['trees']['before'].get('scope_lines')} "
                  + " ".join(f"{k}={v}" for k, v in outcome.items()), flush=True)
            if args.record and ok:
                path = CASES_DIR / case["_file"]
                path.write_text(json.dumps(public(case), indent=1, ensure_ascii=False) + "\n", encoding="utf-8")
    finally:
        subprocess.run(["chmod", "-R", "u+w", str(scratch)], capture_output=True)
        shutil.rmtree(scratch, ignore_errors=True)
    structural, _ = validate(cases, partial=bool(args.case or args.split))
    errors = structural + errors
    for error in errors:
        print("INVALID", error)
    print(f"summary: {sum(results)}/{len(results)} cases verified online, {len(errors)} errors")
    if errors:
        raise SystemExit(1)


# ---------------------------------------------------------------- forbidden phrases

def forbidden_list(cases, cache, split, allow_paths=()):
    """Label n-grams and distinctive tokens of the split that occur in no materialized input of the
    split (any case, either variant) and in no allowed text. Same format and matching rule as the
    held-out corpus, so scripts/model_budget_relay.py loads it unchanged."""
    h = holdout()
    inputs = h["TextIndex"]()
    for case in cases:
        for v in VARIANTS:
            files, _ = render(case, v, cache)
            for path, text in text_files(files):
                inputs.add(path + "\n" + text)
    sources = h["allowed_texts"](allow_paths)
    allowed = []
    for name, _, text in sources:
        index = h["TextIndex"]()
        index.add(text)
        allowed.append((name, index))
    entries, dropped = {}, {"in_inputs": [], "in_allowed_text": []}
    for case in sorted(cases, key=lambda c: c["id"]):
        candidates = [(f"{case['id']}/token/{i}", "distinctive_token", t.lower())
                      for i, t in enumerate(case["label"]["distinctive_tokens"])]
        for field_index, text in enumerate(label_prose(case)):
            seq = h["words"](text)
            for i in range(len(seq) - NGRAM + 1):
                candidates.append((f"{case['id']}/prose/{field_index}.{i}", "label_prose", " ".join(seq[i:i + NGRAM])))
        seen = set()
        for entry_id, source, text in candidates:
            mode = h["match_mode"](text)
            if (mode, text) in seen:
                continue
            seen.add((mode, text))
            if inputs.contains(mode, text):
                dropped["in_inputs"].append(entry_id)
                continue
            exempting = [name for name, index in allowed if index.contains(mode, text)]
            if exempting:
                dropped["in_allowed_text"].append({"id": entry_id, "mode": mode, "text": text, "allowed_by": exempting})
                continue
            key = (mode, text)
            if key in entries:
                entries[key]["cases"] = sorted(set(entries[key]["cases"]) | {case["id"]})
                continue
            entries[key] = {"id": entry_id, "cases": [case["id"]], "source": source, "mode": mode, "text": text}
    return {"schema": h["FORBIDDEN_SCHEMA"], "version": h["FORBIDDEN_VERSION"], "normalization": h["NORMALIZATION"],
            "ngram": NGRAM, "corpus_digest": corpus_digest(cases), "split": split,
            "allowed_texts": [{"name": name, "sha256": sha} for name, sha, _ in sources],
            "dropped": dropped, "entries": sorted(entries.values(), key=lambda e: e["id"])}


# ---------------------------------------------------------------- cli

def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    v = sub.add_parser("validate", help="offline structure, label and patch checks")
    v.add_argument("--split", choices=SPLITS)
    d = sub.add_parser("digest", help="split digest (offline)")
    d.add_argument("--split", choices=SPLITS)
    d.add_argument("--as-split", choices=SPLITS, help="hash the split's cases under this split name (spent history)")
    m = sub.add_parser("materialize", help="write one variant's analysis input (network or a filled cache)")
    m.add_argument("case_id")
    m.add_argument("variant", choices=VARIANTS)
    m.add_argument("dest", type=Path)
    m.add_argument("--cache", type=Path, required=True)
    vf = sub.add_parser("verify", help="online: pins, patches, tree digests, scope size, anchors, leakage")
    vf.add_argument("--cache", type=Path, required=True)
    vf.add_argument("--split", choices=SPLITS)
    vf.add_argument("--case", action="append")
    vf.add_argument("--record", action="store_true", help="store tree digests and scope sizes (author use)")
    vf.add_argument("--run-checks", action="store_true", help="run each case's check on work copies of both variants")
    vf.add_argument("--network", action="store_true", help="checks may download dependencies (Go proxy, crates)")
    for tool in TOOL_FLAGS:
        vf.add_argument("--" + tool, help=f"absolute path of {tool} for --run-checks")
    f = sub.add_parser("forbidden", help="label phrases the relay must block (needs the cache)")
    f.add_argument("--split", choices=SPLITS, required=True)
    f.add_argument("--cache", type=Path, required=True)
    f.add_argument("--allow-text", action="append", default=[], type=Path)
    f.add_argument("--output", type=Path)
    args = parser.parse_args()
    if args.command == "validate":
        cases = load_cases(args.split)
        errors, counts = validate(cases, partial=bool(args.split))
        for error in errors:
            print("INVALID", error)
        print("counts:", json.dumps(counts, sort_keys=True))
        if errors:
            raise SystemExit(1)
        print(f"PASS: {len(cases)} cases valid (offline: metadata, patches, label locations)")
    elif args.command == "digest":
        print(split_digest(args.split, args.as_split) if args.split else corpus_digest(load_cases()))
    elif args.command == "materialize":
        print(materialize(args.dest, find_case(load_cases(), args.case_id), args.variant, args.cache))
    elif args.command == "verify":
        cases = load_cases(args.split)
        if args.case:
            cases = [find_case(cases, c) for c in args.case]
        verify(cases, args)
    elif args.command == "forbidden":
        doc = forbidden_list(load_cases(args.split), args.cache, args.split, args.allow_text)
        text = json.dumps(doc, indent=1, ensure_ascii=False, sort_keys=True) + "\n"
        if args.output:
            if args.output.exists():
                raise SystemExit(f"output exists: {args.output}")
            args.output.write_text(text, encoding="utf-8")
            print(f"{len(doc['entries'])} entries; dropped {len(doc['dropped']['in_inputs'])} in inputs, "
                  f"{len(doc['dropped']['in_allowed_text'])} in allowed text; sha256 "
                  f"{hashlib.sha256(text.encode('utf-8')).hexdigest()}", file=sys.stderr)
        else:
            sys.stdout.write(text)


if __name__ == "__main__":
    main()
