"""Bandit suppression: nosec with a test id, one active."""

import subprocess  # nosec B404


def hidden(cmd):
    return subprocess.call(cmd, shell=True)  # nosec B602


def active(cmd):
    return subprocess.call(cmd, shell=True)
