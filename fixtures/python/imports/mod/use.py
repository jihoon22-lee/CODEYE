"""Imports that are not installed: a coverage gap, never a defect."""

import requests
import yaml

import not_a_real_module_pkg


class Session(requests.Session):
    pass


def get(url: str) -> int:
    return not_a_real_module_pkg.fetch(url).status


def load(s: str) -> int:
    return yaml.safe_load(s)
