import pytest

import jmespath

PEOPLE = {
    "people": [
        {"name": "a", "age": 30},
        {"name": "b", "age": 10},
        {"name": "c", "age": 50},
    ],
    "nobody": [],
}


@pytest.mark.parametrize(
    "expression, data",
    [
        ("min_by(`[]`, &age)", {}),
        ("max_by(`[]`, &age)", {}),
        ("min_by(nobody, &age)", PEOPLE),
        ("max_by(nobody, &age)", PEOPLE),
        ("people[?age > `100`] | max_by(@, &age)", PEOPLE),
    ],
)
def test_by_function_over_no_elements_gives_null(expression, data):
    assert jmespath.search(expression, data) is None


def test_by_functions_still_pick_the_extremes():
    assert jmespath.search("min_by(people, &age).name", PEOPLE) == "b"
    assert jmespath.search("max_by(people, &age).name", PEOPLE) == "c"


def test_plain_min_and_max_already_accept_no_elements():
    assert jmespath.search("min(`[]`)", {}) is None
    assert jmespath.search("max(`[]`)", {}) is None
