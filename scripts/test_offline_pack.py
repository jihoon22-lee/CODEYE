"""offline_pack.py: the web check, the archive's determinism and verify."""
import hashlib
import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import offline_pack  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent


class WebCheck(unittest.TestCase):
    def scan(self, files):
        with tempfile.TemporaryDirectory() as d:
            for name, body in files.items():
                p = Path(d) / name
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_bytes(body)
            return offline_pack.check_web(Path(d))

    def test_identifiers_and_text_links_pass(self):
        self.assertEqual(self.scan({
            "index.html": b'<script type="module" src="/assets/a.js"></script><link rel="stylesheet" href="/assets/a.css">',
            "assets/a.js": b'const ns="http://www.w3.org/2000/svg";var u=`https://react.dev/errors/`+e;$schema:`https://json-schema.org/draft/2020-12/schema`',
            "assets/a.css": b"body{background:url(/assets/x.png)}",
        }), [])

    def test_every_kind_of_external_load_fails(self):
        problems = self.scan({
            "index.html": b'<link href="https://fonts.googleapis.com/css2?family=Inter" rel="stylesheet"><script src="//cdn.jsdelivr.net/x.js"></script>',
            "assets/a.css": b"@import url(https://fonts.example.com/a.css);.x{background:url(//img.example.net/a.png)}",
            "assets/a.js": b'fetch("https://telemetry.example.org/v1");\n//# sourceMappingURL=https://maps.example.com/a.js.map',
        })
        text = "\n".join(problems)
        for host in ("fonts.googleapis.com", "cdn.jsdelivr.net", "fonts.example.com", "img.example.net", "telemetry.example.org", "maps.example.com"):
            self.assertIn(host, text)
        self.assertIn("source map from a remote host", text)

    def test_the_built_web_app_when_present(self):
        dist = ROOT / "web" / "dist"
        if not (dist / "index.html").is_file():
            self.skipTest("web/dist not built")
        self.assertEqual(offline_pack.check_web(dist), [])


class Archive(unittest.TestCase):
    def stage(self, root: Path) -> Path:
        stage = root / "stage"
        (stage / "web").mkdir(parents=True)
        (stage / "codeye").write_bytes(b"\x7fELF binary")
        (stage / "codeye").chmod(0o755)
        (stage / "web" / "index.html").write_text("<html></html>")
        files = []
        for p in sorted(x for x in stage.rglob("*") if x.is_file()):
            rel = p.relative_to(stage).as_posix()
            files.append({"path": rel, "size": p.stat().st_size, "sha256": offline_pack.sha256_file(p), "mode": "0644"})
        manifest = {"schema_version": "0.1", "name": "codeye", "version": "v", "commit": "c", "build_time": "t", "os": "linux", "arch": "amd64", "go_version": "go", "files": files}
        raw = json.dumps(manifest).encode()
        (stage / offline_pack.MANIFEST).write_bytes(raw)
        sums = [f"{f['sha256']}  {f['path']}" for f in files] + [f"{hashlib.sha256(raw).hexdigest()}  {offline_pack.MANIFEST}"]
        (stage / offline_pack.SUMS).write_text("\n".join(sums) + "\n")
        return stage

    def test_same_input_same_bytes_and_verified(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            stage = self.stage(root)
            a, b = root / "a.tar.gz", root / "b.tar.gz"
            offline_pack.write_archive(stage, "codeye-v-linux-amd64", a, 1700000000)
            (stage / "web" / "index.html").touch()  # a new file time does not change the archive
            offline_pack.write_archive(stage, "codeye-v-linux-amd64", b, 1700000000)
            self.assertEqual(a.read_bytes(), b.read_bytes())
            self.assertEqual(offline_pack.verify(a), [])
            self.assertEqual(offline_pack.verify(stage), [])

    def test_changed_missing_and_added_files_fail(self):
        with tempfile.TemporaryDirectory() as d:
            stage = self.stage(Path(d))
            (stage / "web" / "index.html").write_text("<html>changed</html>")
            (stage / "extra.txt").write_text("x")
            (stage / "codeye").unlink()
            problems = "\n".join(offline_pack.verify(stage))
            self.assertIn("web/index.html: hash or size differs", problems)
            self.assertIn("extra.txt: not listed", problems)
            self.assertIn("codeye: missing", problems)


class Linking(unittest.TestCase):
    def test_elf_program_headers(self):
        import struct
        # A 64-bit little-endian ELF header with one program header of the given type.
        def elf(ptype):
            head = bytearray(64)
            head[:4], head[4], head[5] = b"\x7fELF", 2, 1
            struct.pack_into("<Q", head, 0x20, 64)
            struct.pack_into("<HH", head, 0x36, 56, 1)
            ph = bytearray(56)
            struct.pack_into("<I", ph, 0, ptype)
            return bytes(head + ph)
        with tempfile.TemporaryDirectory() as d:
            for ptype, want in ((1, "static"), (3, "dynamic"), (2, "dynamic")):
                p = Path(d) / f"e{ptype}"
                p.write_bytes(elf(ptype))
                self.assertEqual(offline_pack.elf_linking(p), want)


if __name__ == "__main__":
    unittest.main()
