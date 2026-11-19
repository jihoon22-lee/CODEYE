"""R0 Go 품질 평가 입력을 만들고 별도 정답으로 전후 동작을 확인한다."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

CASES = Path(__file__).with_name("corpus.json")


def materialize(destination, case, variant, documented):
    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    (destination / "go.mod").write_text("module sample.test/metrics\n\ngo 1.23\n")
    imports = "".join(f'import "{name}"\n' for name in case["imports"])
    # 공개 함수 계약만 소스의 일부다. trigger, 정답, 정오 변형명, 검증 테스트는 배제한다.
    content = f"package metrics\n\n{imports}\n// {case['contract']}\n{case[variant]}\n"
    source = destination / "metrics.go"
    source.write_text(content)
    subprocess.run(["gofmt", "-w", source], check=True)
    if documented:
        (destination / "README.md").write_text(f"# Metrics\n\n{case['contract']}\n")
    return hashlib.sha256(source.read_bytes()).hexdigest()


def check(cases):
    with tempfile.TemporaryDirectory(prefix="codeye-corpus-") as tmp:
        base = Path(tmp)
        env = dict(
            os.environ, GOWORK="off", GOPROXY="off", GOTOOLCHAIN="local", GOFLAGS=""
        )
        for case in cases:
            for variant in ("before", "after"):
                path = base / case["id"] / variant
                materialize(path, case, variant, documented=False)
                # 이 파일이 포함된 디렉터리는 분석 요청에 사용하지 않는다.
                imports = ['"testing"']
                for name in ["time", "io", "errors"]:
                    if name + "." in case["check"]:
                        imports.append(f'"{name}"')
                test = (
                    "package metrics\nimport (\n"
                    + "\n".join(imports)
                    + "\n)\nfunc TestContract(t *testing.T) {\n"
                    + case["check"]
                    + "\n}\n"
                )
                (path / "contract_test.go").write_text(test)
                result = subprocess.run(
                    [
                        "go",
                        "test",
                        "-timeout=10s",
                        "-count=1",
                        "-run",
                        "^TestContract$",
                        ".",
                    ],
                    cwd=path,
                    env=env,
                    capture_output=True,
                    text=True,
                    timeout=35,
                )
                if variant == "before":
                    valid = (
                        result.returncode == 1
                        and "--- FAIL: TestContract" in result.stdout
                        and not result.stderr
                    )
                else:
                    valid = result.returncode == 0
                if not valid:
                    raise RuntimeError(
                        f"{case['id']}/{variant}: expected contract result missing\n{result.stdout}{result.stderr}"
                    )
    print(f"PASS: 합성 쌍 {len(cases)}개에서 원래 결함과 수정 후 동작 확인")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check", action="store_true", help="분석기와 분리된 fixture 자체 시험"
    )
    parser.add_argument("--case", help="생성할 사례 ID")
    parser.add_argument("--variant", choices=["before", "after"])
    parser.add_argument("--output", type=Path, help="새로 만들 분석 입력 디렉터리")
    parser.add_argument("--documented", action="store_true")
    args = parser.parse_args()
    cases = json.loads(CASES.read_text())
    if args.check:
        check(cases)
    elif args.case and args.variant and args.output:
        selected = next((c for c in cases if c["id"] == args.case), None)
        if selected is None:
            parser.error("등록되지 않은 사례")
        print(materialize(args.output, selected, args.variant, args.documented))
    else:
        parser.error("--check 또는 --case, --variant, --output 조합 필요")
