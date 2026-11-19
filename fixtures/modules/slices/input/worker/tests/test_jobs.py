from jobs import run


def test_run_upper():
    assert run("index") == "INDEX"
