import os
import subprocess


def size(x: int) -> str:
    return x


def run(cmd):
    return subprocess.call(cmd, shell=True)
