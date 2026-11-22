#!/usr/bin/env python3
"""Build and verify the CODEYE offline pack (docs/OPERATIONS.md#폐쇄망-배포팩).

  offline_pack.py build [--out DIR] [--version V] [--goos linux] [--goarch amd64] [--skip-web-build] [--allow-dirty]
  offline_pack.py verify ARCHIVE.tar.gz|DIR
  offline_pack.py check-web DIR

build runs on a connected build machine (Go and Node.js with web/node_modules
installed): it builds the web app and a static codeye binary (CGO_ENABLED=0)
and writes one archive codeye-<version>-<os>-<arch>.tar.gz with MANIFEST.json
and SHA256SUMS, plus its .sha256. The archive is byte-for-byte the same for the
same commit, Go and Node.js versions: sorted entries, the commit time as every
timestamp, owner 0, gzip without a name or time. verify checks every hash and
that no file is missing or added. On the target `sha256sum -c SHA256SUMS` in the
unpacked directory does the same with coreutils only. check-web fails when the
built web app names an external host it could load from.

Stdlib only.
"""
from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import json
import os
import posixpath
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path
from urllib.parse import quote, urlsplit

ROOT = Path(__file__).resolve().parent.parent
SCHEMA_VERSION = "0.1"
MANIFEST = "MANIFEST.json"
SUMS = "SHA256SUMS"
# User-facing documentation and examples, kept in their repository layout.
DOCS = ["CHANGELOG.md", "docs/ANALYZERS.md", "docs/API.md", "docs/ARCHITECTURE.md", "docs/EXECUTION.md",
        "docs/GITHUB.md", "docs/GUIDE.md", "docs/OPERATIONS.md", "docs/REPORT.md",
        "docs/REQUIREMENTS.md", "docs/REVIEW.md", "docs/SECURITY.md"]
EXAMPLES = ["examples/config-offline.json"]
INSTALL_TEMPLATE = ROOT / "scripts" / "offline_pack_install.md"
SOURCE_URL = "https://github.com/jihoon22-lee/CODEYE"
WEB_NOTICES = ".vite/license.md"
NOTICES = "THIRD_PARTY_NOTICES"
MARKDOWN_LINK = re.compile(r"\]\(([^()\s]+)\)")

# Hosts a built web app may name without loading anything from them: XML and
# SVG namespaces, schema identifiers, React's error decoder link (shown as
# text) and the example repository address in an input placeholder. Anything
# else fails check-web.
WEB_TEXT_HOSTS = {
    "www.w3.org": "XML/SVG namespace identifiers",
    "json-schema.org": "JSON Schema $schema identifiers",
    "codeye.invalid": "schema $id identifiers (reserved TLD)",
    "react.dev": "React error decoder links in error messages",
    "github.com": "the placeholder of the repository address field",
}
URL = re.compile(rb"(?i)\b(?:https?|wss?|ftp)://([a-z0-9.-]+)")
# A load that names no scheme: //host in src/href/url()/import.
SCHEMELESS = re.compile(rb"""(?i)(?:\b(?:src|href|action|poster|data)\s*=\s*["']?|url\(\s*["']?|@import\s+["']|\bimport\s*\(\s*["'])//([a-z0-9.-]+)""")
REPORT_NOTICE = re.compile(r"assets/LICENSE-[A-Za-z0-9_-]{8}\.(tabler-core|tabler-icons|bootstrap|noto-sans-kr)")


def run(argv, **kw):
    return subprocess.run(argv, check=True, capture_output=True, text=True, **kw).stdout.strip()


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def check_web(directory: Path) -> list[str]:
    """Return one problem per external host the built web app could load from."""
    problems = []
    for path in sorted(p for p in directory.rglob("*") if p.is_file()):
        data = path.read_bytes()
        rel = path.relative_to(directory).as_posix()
        # Vite emits this Markdown for distribution notices, not an application
        # asset. The pack moves it outside web/dist.
        if rel == WEB_NOTICES and data.startswith(b"# Licenses\n"):
            continue
        # Vite copies these licenses as download assets. Only the unchanged
        # notice may name its publisher or the license site without using them.
        notice = REPORT_NOTICE.fullmatch(rel)
        if notice and data == (ROOT / "internal/report/reportui" / f"LICENSE.{notice[1]}").read_bytes():
            continue
        for m in SCHEMELESS.finditer(data):
            problems.append(f"{rel}: scheme-relative load from {m.group(1).decode()}")
        for m in URL.finditer(data):
            host = m.group(1).decode().lower().rstrip(".")
            if host not in WEB_TEXT_HOSTS:
                problems.append(f"{rel}: external URL host {host}")
        if b"sourceMappingURL=http" in data:
            problems.append(f"{rel}: source map from a remote host")
    return sorted(set(problems))


def elf_linking(path: Path) -> str:
    """static when the ELF file has no program interpreter and no dynamic section."""
    data = path.read_bytes()
    if data[:4] != b"\x7fELF":
        return "not_elf"
    is64, little = data[4] == 2, data[5] == 1
    end = "<" if little else ">"
    if is64:
        phoff, = struct.unpack_from(end + "Q", data, 0x20)
        phentsize, phnum = struct.unpack_from(end + "HH", data, 0x36)
    else:
        phoff, = struct.unpack_from(end + "I", data, 0x1C)
        phentsize, phnum = struct.unpack_from(end + "HH", data, 0x2A)
    for i in range(phnum):
        ptype, = struct.unpack_from(end + "I", data, phoff + i * phentsize)
        if ptype in (2, 3):  # PT_DYNAMIC, PT_INTERP
            return "dynamic"
    return "static"


def elf_machine(path: Path) -> int:
    with path.open("rb") as f:
        head = f.read(20)
    if len(head) < 20 or head[:4] != b"\x7fELF" or head[5] not in (1, 2):
        return 0
    return int.from_bytes(head[18:20], "little" if head[5] == 1 else "big")


def release_doc(text: str, path: str, commit: str, *, destination: str | None = None) -> str:
    """Keep shipped links local and point source-only references at the commit."""
    shipped = set(DOCS + EXAMPLES + ["INSTALL.md"])

    def replace(m: re.Match) -> str:
        target = urlsplit(m[1])
        if target.scheme or target.netloc or not target.path or target.path.startswith("/"):
            return m[0]
        resolved = posixpath.normpath(posixpath.join(posixpath.dirname(path), target.path))
        if resolved in shipped:
            if destination is None:
                return m[0]
            relative = posixpath.relpath(resolved, posixpath.dirname(destination) or ".")
            return f"]({target._replace(path=relative).geturl()})"
        if resolved == ".." or resolved.startswith("../"):
            return m[0]
        kind = "tree" if (ROOT / resolved).is_dir() else "blob"
        link = f"{SOURCE_URL}/{kind}/{commit}/{quote(resolved, safe='/')}"
        if target.fragment:
            link += "#" + quote(target.fragment, safe="-%")
        return f"]({link})"

    return MARKDOWN_LINK.sub(replace, text)


def copy_go_notices(stage: Path, env: dict[str, str]) -> None:
    """Read the Go toolchain and linked package modules' upstream notices locally."""
    notices = stage / NOTICES / "go"
    toolchain = Path(run(["go", "env", "GOROOT"], cwd=ROOT, env=env))
    (notices / "toolchain").mkdir(parents=True)
    shutil.copyfile(toolchain / "LICENSE", notices / "toolchain" / "LICENSE")
    shutil.copyfile(toolchain / "PATENTS", notices / "toolchain" / "PATENTS")
    template = "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}"
    listing = run(["go", "list", "-deps", "-f", template, "./cmd/codeye"], cwd=ROOT, env=env)
    modules = {}
    for line in listing.splitlines():
        if line:
            name, version, directory = line.split("\t", 2)
            modules[(name, version)] = Path(directory)
    for (name, version), directory in sorted(modules.items()):
        upstream = [p for p in sorted(directory.iterdir()) if p.is_file() and not p.is_symlink() and
                    p.name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE", "PATENTS"))]
        if not upstream:
            raise ValueError(f"no upstream notice found for Go module {name}@{version}")
        dest = notices / "modules" / f"{name}@{version}"
        dest.mkdir(parents=True)
        for p in upstream:
            shutil.copyfile(p, dest / p.name)


def embedded_inventory() -> dict:
    """What the binary embeds (go:embed): migrations and prompt packs."""
    migrations = [{"name": p.name, "sha256": sha256_file(p)} for p in sorted((ROOT / "migrations/sql").glob("*.sql"))]
    packs = []
    for d in sorted((ROOT / "prompts").iterdir()):
        meta = d / "pack.yaml"
        if not meta.is_file():
            continue
        text = meta.read_text(encoding="utf-8")
        ident = re.search(r"^id:\s*(\S+)", text, re.M)
        version = re.search(r"^version:\s*(\S+)", text, re.M)
        files = [p for p in sorted(d.rglob("*")) if p.is_file() and (p.suffix in (".md", ".yaml"))]
        packs.append({"id": ident.group(1) if ident else d.name, "version": version.group(1) if version else "", "files": len(files),
                      "sha256": hashlib.sha256(b"".join(p.relative_to(d).as_posix().encode() + b"\0" + p.read_bytes() + b"\0" for p in files)).hexdigest()})
    return {"migrations": migrations, "prompt_packs": packs}


def write_archive(stage: Path, top: str, target: Path, mtime: int) -> None:
    """A deterministic tar.gz of stage under top/: sorted, fixed owner and time."""
    entries = sorted(p for p in stage.rglob("*"))
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.PAX_FORMAT) as tar:
        def add(path: Path, name: str):
            info = tarfile.TarInfo(name)
            info.mtime, info.uid, info.gid, info.uname, info.gname = mtime, 0, 0, "", ""
            if path.is_dir():
                info.type, info.mode = tarfile.DIRTYPE, 0o755
                tar.addfile(info)
                return
            info.size = path.stat().st_size
            info.mode = 0o755 if os.access(path, os.X_OK) else 0o644
            with path.open("rb") as f:
                tar.addfile(info, f)
        add(stage, top)
        for p in entries:
            add(p, top + "/" + p.relative_to(stage).as_posix())
    with target.open("wb") as out:
        with gzip.GzipFile(filename="", mode="wb", fileobj=out, mtime=0, compresslevel=9) as gz:
            gz.write(raw.getvalue())


def build(args) -> int:
    goos, goarch = args.goos, args.goarch
    commit = run(["git", "-C", str(ROOT), "rev-parse", "HEAD"])
    dirty = bool(run(["git", "-C", str(ROOT), "status", "--porcelain", "--untracked-files=all"]))
    if dirty and not args.allow_dirty:
        print("the working tree has uncommitted changes; commit them or pass --allow-dirty", file=sys.stderr)
        return 2
    epoch = int(os.environ.get("SOURCE_DATE_EPOCH") or run(["git", "-C", str(ROOT), "log", "-1", "--format=%ct"]))
    build_time = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(epoch))
    # The version comes from the commit (git describe); no release number is invented.
    version = args.version or run(["git", "-C", str(ROOT), "describe", "--tags", "--always", "--dirty"])
    if not re.fullmatch(r"[0-9A-Za-z][0-9A-Za-z._+-]{0,63}", version):
        print("version must be 1..64 of [0-9A-Za-z._+-]", file=sys.stderr)
        return 2
    env = dict(os.environ, CGO_ENABLED="0", GOOS=goos, GOARCH=goarch, GOFLAGS="", GOTOOLCHAIN="local", GOWORK="off")
    if goarch == "amd64":
        env["GOAMD64"] = "v1"
    elif goarch == "arm64":
        env["GOARM64"] = "v8.0"
    go_version = run(["go", "env", "GOVERSION"], cwd=ROOT, env=env)
    # The Node.js release that builds the web app (unknown with --skip-web-build).
    node_version = "" if args.skip_web_build else run(["node", "--version"])
    if not args.skip_web_build:
        if not (ROOT / "web/node_modules").is_dir():
            print("web/node_modules is missing; run `npm --prefix web ci` (or `npm --prefix web ci --offline` from a filled npm cache)", file=sys.stderr)
            return 2
        subprocess.run(["npm", "--prefix", str(ROOT / "web"), "run", "build"], check=True, stdout=subprocess.DEVNULL)
    dist = ROOT / "web/dist"
    if not (dist / "index.html").is_file():
        print("web/dist/index.html is missing; build the web app", file=sys.stderr)
        return 2
    if not (dist / WEB_NOTICES).is_file() or not (dist / WEB_NOTICES).read_bytes().startswith(b"# Licenses\n"):
        print(f"web/dist/{WEB_NOTICES} is missing or invalid; rebuild the web app with the current source", file=sys.stderr)
        return 2
    problems = check_web(dist)
    if problems:
        print("the web app names external hosts:\n  " + "\n  ".join(problems), file=sys.stderr)
        return 1
    top = f"codeye-{version}-{goos}-{goarch}"
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="codeye-pack-") as tmp:
        stage = Path(tmp) / top
        stage.mkdir()
        exe = "codeye.exe" if goos == "windows" else "codeye"
        ldflags = f"-s -w -X main.buildVersion={version} -X main.buildCommit={commit} -X main.buildTime={build_time}"
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", str(stage / exe), "./cmd/codeye"],
                       cwd=ROOT, env=env, check=True)
        linking = elf_linking(stage / exe) if goos == "linux" else "not_checked"
        if goos == "linux" and linking != "static":
            print(f"the binary is {linking}, expected a static ELF (CGO_ENABLED=0)", file=sys.stderr)
            return 1
        machine = {"amd64": 62, "arm64": 183}.get(goarch)
        if goos == "linux" and machine is not None and elf_machine(stage / exe) != machine:
            print(f"the binary ELF machine does not match {goarch}", file=sys.stderr)
            return 1
        shutil.copytree(dist, stage / "web" / "dist")
        (stage / NOTICES).mkdir()
        (stage / "web" / "dist" / WEB_NOTICES).replace(stage / NOTICES / "web.md")
        try:
            copy_go_notices(stage, env)
        except ValueError as err:
            print(err, file=sys.stderr)
            return 1
        for rel in DOCS:
            (stage / rel).parent.mkdir(parents=True, exist_ok=True)
            text = (ROOT / rel).read_text(encoding="utf-8")
            (stage / rel).write_text(release_doc(text, rel, commit), encoding="utf-8")
        for rel in EXAMPLES:
            (stage / rel).parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / rel, stage / rel)
        install = release_doc(INSTALL_TEMPLATE.read_text(encoding="utf-8"),
                              INSTALL_TEMPLATE.relative_to(ROOT).as_posix(), commit, destination="INSTALL.md")
        (stage / "INSTALL.md").write_text(install.replace("{{version}}", version).replace("{{top}}", top).replace("{{os}}", goos).replace("{{arch}}", goarch), encoding="utf-8")
        files = []
        for p in sorted(x for x in stage.rglob("*") if x.is_file()):
            rel = p.relative_to(stage).as_posix()
            files.append({"path": rel, "size": p.stat().st_size, "sha256": sha256_file(p), "mode": "0755" if os.access(p, os.X_OK) else "0644"})
        manifest = {
            "schema_version": SCHEMA_VERSION, "name": "codeye", "version": version, "commit": commit, "dirty": dirty,
            "build_time": build_time, "os": goos, "arch": goarch, "go_version": go_version, "node_version": node_version or None,
            "cgo_enabled": False, "linking": linking, "embedded": embedded_inventory(),
            "not_included": ["analyzer tools", "language toolchains", "PostgreSQL", "OpenCode", "project dependencies"],
            "files": files,
        }
        (stage / MANIFEST).write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        lines = [f"{f['sha256']}  {f['path']}" for f in files] + [f"{sha256_file(stage / MANIFEST)}  {MANIFEST}"]
        (stage / SUMS).write_text("\n".join(sorted(lines, key=lambda line: line.split("  ", 1)[1])) + "\n", encoding="utf-8")
        archive = out / f"{top}.tar.gz"
        write_archive(stage, top, archive, epoch)
    digest = sha256_file(archive)
    (out / f"{top}.tar.gz.sha256").write_text(f"{digest}  {archive.name}\n", encoding="utf-8")
    print(f"{archive} {archive.stat().st_size} bytes sha256 {digest}")
    return 0


def read_pack(target: Path) -> tuple[dict[str, bytes], list[str]]:
    """The files of an archive or directory by path below its top directory."""
    problems: list[str] = []
    files: dict[str, bytes] = {}
    if target.is_dir():
        for p in sorted(target.rglob("*")):
            rel = p.relative_to(target).as_posix()
            if p.is_symlink() or not (p.is_file() or p.is_dir()):
                problems.append(f"{rel}: not a regular file or directory")
            elif p.is_file():
                files[rel] = p.read_bytes()
        return files, problems
    with tarfile.open(target, "r:gz") as tar:
        tops = set()
        for m in tar.getmembers():
            parts = m.name.split("/")
            if m.name.startswith("/") or ".." in parts or not m.name:
                problems.append(f"{m.name}: unsafe path")
                continue
            tops.add(parts[0])
            if m.isdir():
                continue
            if not m.isfile():
                problems.append(f"{m.name}: not a regular file")
                continue
            if len(parts) < 2:
                problems.append(f"{m.name}: outside the top directory")
                continue
            files["/".join(parts[1:])] = tar.extractfile(m).read()
        if len(tops) != 1:
            problems.append("the archive must hold exactly one top directory")
    return files, problems


def verify(target: Path) -> list[str]:
    files, problems = read_pack(target)
    raw = files.pop(MANIFEST, None)
    sums = files.pop(SUMS, None)
    if raw is None or sums is None:
        return problems + [f"{MANIFEST} or {SUMS} is missing"]
    try:
        manifest = json.loads(raw)
    except ValueError:
        return problems + [f"{MANIFEST} is not JSON"]
    for key in ("schema_version", "name", "version", "commit", "build_time", "os", "arch", "go_version", "files"):
        if key not in manifest:
            problems.append(f"{MANIFEST} lacks {key}")
    listed = {}
    for f in manifest.get("files", []):
        listed[f["path"]] = f
        data = files.get(f["path"])
        if data is None:
            problems.append(f"{f['path']}: missing")
        elif len(data) != f["size"] or hashlib.sha256(data).hexdigest() != f["sha256"]:
            problems.append(f"{f['path']}: hash or size differs from {MANIFEST}")
    for extra in sorted(set(files) - set(listed)):
        problems.append(f"{extra}: not listed in {MANIFEST}")
    want = {f"{f['sha256']}  {p}" for p, f in listed.items()} | {f"{hashlib.sha256(raw).hexdigest()}  {MANIFEST}"}
    if set(sums.decode("utf-8", "replace").splitlines()) != want:
        problems.append(f"{SUMS} does not match {MANIFEST}")
    return problems


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="command", required=True)
    b = sub.add_parser("build")
    b.add_argument("--out", default=str(ROOT / ".codeye" / "pack"))
    b.add_argument("--version", default="")
    b.add_argument("--goos", default="linux")
    b.add_argument("--goarch", default="amd64")
    b.add_argument("--skip-web-build", action="store_true", help="use the existing web/dist")
    b.add_argument("--allow-dirty", action="store_true")
    v = sub.add_parser("verify")
    v.add_argument("target", type=Path)
    c = sub.add_parser("check-web")
    c.add_argument("directory", type=Path)
    args = parser.parse_args(argv)
    if args.command == "build":
        return build(args)
    if args.command == "verify":
        problems = verify(args.target)
        for p in problems:
            print("FAIL:", p, file=sys.stderr)
        if not problems:
            print(f"OK: {args.target} matches its {MANIFEST}")
        return 1 if problems else 0
    problems = check_web(args.directory)
    for p in problems:
        print("FAIL:", p, file=sys.stderr)
    if not problems:
        print(f"OK: {args.directory} names no external host it could load from")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
