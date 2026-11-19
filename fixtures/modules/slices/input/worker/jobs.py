import time


def run_all(retries):
    done = []
    for job in ("index", "compact"):
        for attempt in range(retries):
            try:
                done.append(run(job))
                break
            except RuntimeError:
                time.sleep(2 ** attempt)
    return done


def run(job):
    if not job:
        raise RuntimeError("empty job")
    return job.upper()
