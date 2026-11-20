"""Findings fixture: intentionally defective code for the Python pack tests."""

import os
import subprocess


def run(cmd: str) -> int:
    return subprocess.call(cmd, shell=True)


def size(x: int) -> str:
    return x


def compare(value):
    if value == None:
        return []
    try:
        return [value]
    except:
        return None


def append_to(item, bucket=[]):
    bucket.append(item)
    return bucket


def label(name: str) -> str:
    # é 😀 non-ASCII before the finding pins column units
    return "é😀" + undefined_suffix
