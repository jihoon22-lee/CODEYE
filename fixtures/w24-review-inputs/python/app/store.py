import json
import os


class Store:
    """A key-value store kept in one JSON file."""

    def __init__(self, path):
        self.path = path
        self.data = {}
        if os.path.exists(path):
            with open(path) as f:
                self.data = json.load(f)

    def get(self, key):
        return self.data[key]

    def put(self, key, value):
        self.data[key] = value
        self._save()

    def delete(self, key):
        self.data.pop(key, None)
        self._save()

    def _save(self):
        tmp = self.path + ".tmp"
        with open(tmp, "w") as f:
            json.dump(self.data, f)
        os.replace(tmp, self.path)
