#!/usr/bin/env python3
"""기존 서비스와 분리된 임시 Native PostgreSQL에서 W01 통합 테스트."""
from contextlib import contextmanager
import json
import sys
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]


@contextmanager
def temporary_postgres(base):
    """Start a throwaway PostgreSQL under base on a free loopback port.

    Yields (url, env, probe_env): env is os.environ without PG* variables plus
    GOTOOLCHAIN=local; probe_env adds libpq variables for psql. The server is
    stopped on exit. Shared by this smoke and scripts/r1_eval.py (W24).
    """
    password = secrets.token_hex(24)
    password_file = base / "password"
    password_file.write_text(password)
    password_file.chmod(0o600)
    # OS가 선택한 빈 포트를 사용한다. 경쟁으로 선점되면 실패하며 기존 DB에 연결하지 않는다.
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    env = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
    env["GOTOOLCHAIN"] = "local"
    with (base / "postgres.log").open("w+") as log:
        subprocess.run(["initdb", "-D", str(base / "data"), "-U", "codeye_test",
                        "--auth=scram-sha-256", "--pwfile", str(password_file),
                        "--no-locale", "-E", "UTF8"], env=env, stdout=log, stderr=log, check=True)
        password_file.unlink()
        process = subprocess.Popen(["postgres", "-D", str(base / "data"), "-h", "127.0.0.1",
                                    "-p", str(port), "-k", str(base)], env=env, stdout=log, stderr=log)
        try:
            url = f"postgresql://codeye_test:{password}@127.0.0.1:{port}/postgres?sslmode=disable"
            probe_env = dict(env, PGHOST="127.0.0.1", PGPORT=str(port), PGUSER="codeye_test",
                             PGPASSWORD=password, PGDATABASE="postgres", PGSSLMODE="disable", PGCONNECT_TIMEOUT="1")
            for _ in range(100):
                if process.poll() is not None:
                    raise RuntimeError("임시 PostgreSQL 시작 실패; 기존 서비스는 변경하지 않았습니다")
                result = subprocess.run(["psql", "-X", "-w", "-At", "-c", "SELECT 1"],
                                        env=probe_env, capture_output=True, timeout=2)
                if result.returncode == 0:
                    break
                time.sleep(0.1)
            else:
                raise RuntimeError("임시 PostgreSQL 준비 시간 초과")
            yield url, env, probe_env
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)


def main():
    for tool in ("initdb", "postgres", "psql", "go"):
        if shutil.which(tool) is None:
            raise SystemExit(f"필요한 Native 개발 도구가 PATH에 없습니다: {tool}")
    with tempfile.TemporaryDirectory(prefix="codeye-db-test-") as directory:
        base = Path(directory)
        with temporary_postgres(base) as (url, env, probe_env):
            password = url.split(":", 2)[2].split("@", 1)[0]
            env["CODEYE_TEST_DATABASE_URL"] = url
            # --smokes-only (W25): skip the Go suite to rerun only the selected smokes; the
            # final line then does not claim the storage integration tests ran.
            go_suite = "--smokes-only" not in sys.argv[1:]
            if go_suite:
                subprocess.run(["go", "test", "-race", "-count=1", "./..."], cwd=ROOT, env=env, check=True)
            subprocess.run(["go", "build", "-o", str(base / "codeye"), "./cmd/codeye"], cwd=ROOT, env=env, check=True)
            env["CODEYE_DATABASE_URL"] = url
            for _ in range(2):
                migration = subprocess.run([str(base / "codeye"), "migrate"], cwd=ROOT,
                                           env=env, capture_output=True, timeout=65)
                assert migration.returncode == 0, "명시적 CLI migration 실패"
                assert password not in migration.stdout.decode() + migration.stderr.decode(), "credential 노출"
            result = subprocess.run([str(base / "codeye"), "doctor", "--format", "json", "--work-dir", str(base / "work")],
                                    cwd=ROOT, env=env, capture_output=True, timeout=60)
            report = json.loads(result.stdout)
            check = next(c for c in report["checks"] if c["id"] == "database")
            assert result.returncode == 1 and check["status"] == "available", "진단 상태 불일치"
            assert password not in result.stdout.decode() and password not in result.stderr.decode(), "credential 노출"
            from jsonschema import Draft202012Validator
            Draft202012Validator(json.loads((ROOT / "schemas/doctor.schema.json").read_text())).validate(report)
            if "--web" in sys.argv[1:]:
                import browser_smoke
                browser_smoke.run(base, ROOT, env)
            if "--nodes" in sys.argv[1:]:
                import node_smoke
                node_smoke.run(base, ROOT, env, browser="--web" in sys.argv[1:])
            if "--scheduler" in sys.argv[1:]:
                import scheduler_smoke
                # A separate database keeps the node/worker observations independent of other smokes.
                subprocess.run(["psql", "-X", "-w", "-v", "ON_ERROR_STOP=1", "-c", "CREATE DATABASE scheduler_test"],
                               env=probe_env, capture_output=True, check=True, timeout=10)
                scheduler_url = url.replace("/postgres?", "/scheduler_test?")
                scheduler_env = dict(env, CODEYE_DATABASE_URL=scheduler_url, CODEYE_TEST_DATABASE_URL=scheduler_url)
                subprocess.run([str(base / "codeye"), "migrate"], cwd=base, env=scheduler_env, check=True)
                scheduler_smoke.run(base, ROOT, scheduler_env)
            if "--personal" in sys.argv[1:]:
                import personal_smoke
                personal_smoke.run(base, ROOT, env, probe_env)
            if "--r0-pilot" in sys.argv[1:]:
                import r0_pilot
                r0_pilot.run(base, ROOT, env)
            if go_suite:
                print("PASS: Native PostgreSQL migration·저장 통합·인증/query·CLI 재적용·JSON schema·비밀값 미노출")
            else:
                print("PASS(--smokes-only): migration·CLI·JSON schema·선택한 smoke. Go 저장 통합 시험은 실행하지 않음")


if __name__ == "__main__":
    main()
