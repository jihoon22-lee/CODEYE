"""Constructs two engines of the pack report (docs/ANALYZERS.md#엔진-중복)."""


def undefined():
    return missing_name


def redefined():
    return 1


def redefined():
    return 2


def fmt():
    return "{} {}".format(1)


def always(x):
    assert (x, "message")


def pointless(x):
    x == 1


__all__ = [redefined]
