"""Type checker suppression families."""


def by_type_ignore() -> int:
    return "x"  # type: ignore[return-value]


def by_pyright_ignore() -> int:
    return "y"  # pyright: ignore[reportReturnType]


def active() -> int:
    return "z"
