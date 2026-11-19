"""mypy plugin canary: executing it writes PLUGIN_EXECUTED next to it."""

import pathlib

from mypy.plugin import Plugin

pathlib.Path(__file__).with_name("PLUGIN_EXECUTED").write_text("1")


def plugin(version):
    return Plugin
