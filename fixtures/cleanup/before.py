"""Run a callback in a temporary workspace."""
from pathlib import Path
import shutil
import tempfile
from typing import Callable, TypeVar

T = TypeVar("T")

def run_job(root: Path, process: Callable[[Path], T]) -> T:
    workspace = Path(tempfile.mkdtemp(prefix="codeye-fixture-", dir=root))
    result = process(workspace)
    shutil.rmtree(workspace)
    return result
