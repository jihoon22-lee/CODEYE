from jobs import run_all


def main(argv):
    retries = int(argv[1]) if len(argv) > 1 else 3
    return run_all(retries)
