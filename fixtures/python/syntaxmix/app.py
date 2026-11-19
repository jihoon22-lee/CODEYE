import subprocess


def total(values: list[int]) -> str:
    return sum(values)


def run(cmd: str) -> None:
    try:
        subprocess.call(cmd, shell=True)
    except:
        pass
