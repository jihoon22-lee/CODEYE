from app.store import Store


def test_put_then_get(tmp_path):
    store = Store(str(tmp_path / "s.json"))
    store.put("a", "1")
    assert store.get("a") == "1"


def test_missing_key(tmp_path):
    store = Store(str(tmp_path / "s.json"))
    assert store.get("missing") is None
