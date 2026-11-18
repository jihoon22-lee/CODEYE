import sys

from app.store import Store


def main(argv):
    store = Store(argv[1])
    if len(argv) == 4:
        store.put(argv[2], argv[3])
    print(store.get(argv[2]))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
