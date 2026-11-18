"""Imports that are not installed: a coverage gap, never a defect."""

import requests
import yaml

import not_a_real_module_w17


class Session(requests.Session):
    pass


def get(url: str) -> int:
    return not_a_real_module_w17.fetch(url).status


def load(s: str) -> int:
    return yaml.safe_load(s)
