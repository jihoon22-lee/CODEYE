"""Drives the textorium binary through `new` with a Hugo site and checks where the post file lands.

Usage: new_post_section.py CARGO WORKDIR

A plain section must produce WORKDIR/site/content/<section>/<slug>.md. A section made of parent
segments must not produce a file outside WORKDIR/site/content; the command may reject it."""
import json
import os
import subprocess
import sys
from pathlib import Path

cargo, work = sys.argv[1], Path(sys.argv[2]).resolve()
home = work / "driver-home"
site = work / "site"
content = site / "content"
(home / ".config" / "textorium").mkdir(parents=True, exist_ok=True)
content.mkdir(parents=True, exist_ok=True)
(home / ".config" / "textorium" / "config.json").write_text(json.dumps(
    {"site_name": "demo", "site_path": str(site), "content_dir": "content", "ssg": "hugo"}))
env = dict(os.environ, HOME=str(home))
env.pop("EDITOR", None)


def new_post(title, section):
    argv = [cargo, "run", "--locked", "-q", "--", "new", title, "--category", section, "--no-edit"]
    r = subprocess.run(argv, cwd=work, env=env, capture_output=True, text=True, timeout=600)
    print(f"$ textorium new {title!r} --category {section!r} -> exit {r.returncode}")
    print((r.stdout + r.stderr).strip()[-800:])
    return r.returncode


failures = []
code = new_post("Plain Note", "notes")
if code != 0 or not (content / "notes" / "plain-note.md").is_file():
    failures.append("plain section did not create content/notes/plain-note.md")

new_post("Climb Out", "../../outside")
escaped = [p for p in work.rglob("climb-out.md")]
outside = [p for p in escaped if content not in p.resolve().parents]
for p in outside:
    failures.append(f"post written outside the content directory: {p.relative_to(work)}")

if failures:
    for f in failures:
        print("SECTION CHECK FAILED: " + f)
    sys.exit(1)
print("section check passed")
