"""A nested file whose name reads as an option."""

import subprocess


def run(cmd: str) -> str:
    return subprocess.call(cmd, shell=True)
