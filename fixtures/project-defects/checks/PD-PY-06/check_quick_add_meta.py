"""Independent check: a quick-add task response whose meta block has no section and no assignee.

The service sends such pairs as [null, null] (the project's own test data uses that shape). After
parsing, the task's meta must keep them as None, not as text.
"""
import sys
from pathlib import Path

# argv[1]: directory holding the project's third-party dependencies (installed by the check setup)
sys.path[:0] = [str(Path(__file__).resolve().parent)] + sys.argv[1:2]

from todoist_api_python.models import Task  # noqa: E402

RESPONSE = {
    "id": "6X7rM8997g3RQmvh",
    "content": "Buy milk",
    "description": "",
    "project_id": "6Jf8VQXxpwv56VQ7",
    "section_id": None,
    "parent_id": None,
    "labels": [],
    "priority": 1,
    "due": None,
    "deadline": None,
    "duration": None,
    "collapsed": False,
    "child_order": 3,
    "responsible_uid": None,
    "assigned_by_uid": None,
    "completed_at": None,
    "added_by_uid": "34567",
    "added_at": "2014-09-26T08:25:05.000000Z",
    "updated_at": "2016-01-02T21:00:30.000000Z",
    "meta": {
        "project": ["6Jf8VQXxpwv56VQ7", "Inbox"],
        "section": [None, None],
        "assignee": [None, None],
        "labels": {},
        "due": None,
        "deadline": None,
    },
}


def main():
    meta = Task.from_dict(RESPONSE).meta
    got = {"project": meta.project, "section": meta.section, "assignee": meta.assignee}
    print("parsed meta:", got)
    want = {"project": ("6Jf8VQXxpwv56VQ7", "Inbox"), "section": (None, None), "assignee": (None, None)}
    bad = [f"{k}={tuple(got[k])!r}" for k in want if tuple(got[k]) != want[k]]
    if bad:
        print("NULL META PAIR NOT KEPT: " + ", ".join(bad))
        return 1
    print("meta pairs keep null values")
    return 0


if __name__ == "__main__":
    sys.exit(main())
