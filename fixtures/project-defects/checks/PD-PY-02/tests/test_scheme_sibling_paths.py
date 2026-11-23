import io

import pytest

from installer.destinations import SchemeDictionaryDestination

SCHEMES = ("purelib", "platlib", "headers", "scripts", "data")


def make_destination(root):
    scheme_dict = {name: str(root / "prefix" / name) for name in SCHEMES}
    return SchemeDictionaryDestination(
        scheme_dict=scheme_dict,
        interpreter="/usr/bin/python3",
        script_kind="posix",
    )


@pytest.mark.parametrize(
    "member",
    [
        "../purelib-extra/planted.py",
        "../purelibs/planted.py",
        "sub/../../purelib2/planted.py",
    ],
)
def test_sibling_directory_sharing_the_name_prefix_is_rejected(tmp_path, member):
    destination = make_destination(tmp_path)
    with pytest.raises(ValueError):
        destination.write_file(
            "purelib", member, io.BytesIO(b"value = 1\n"), is_executable=False
        )
    planted = [p for p in (tmp_path / "prefix").rglob("planted.py")]
    assert planted == []


def test_plain_member_is_still_written(tmp_path):
    destination = make_destination(tmp_path)
    destination.write_file(
        "purelib", "pkg/mod.py", io.BytesIO(b"value = 1\n"), is_executable=False
    )
    assert (tmp_path / "prefix" / "purelib" / "pkg" / "mod.py").read_bytes() == b"value = 1\n"


def test_parent_escape_is_still_rejected(tmp_path):
    destination = make_destination(tmp_path)
    with pytest.raises(ValueError):
        destination.write_file(
            "purelib", "../../outside.py", io.BytesIO(b"value = 1\n"), is_executable=False
        )
