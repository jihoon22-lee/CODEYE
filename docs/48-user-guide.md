# 48. 개인 사용 안내서

한 사람이 자기 Linux 머신에서 CODEYE를 쓰는 방법이다. 설치, 첫 시작, 프로젝트 등록과 분석, 보고서 읽기, 정지, 백업·복원, 업그레이드, 문제 해결을 다룬다. 동작 계약과 시험 결과는 [W25 기록](47-w25-packaging-and-operations.md)에 있다.

아래 명령은 모두 이 저장소의 W25 빌드에 있고, Linux(WSL2)·PostgreSQL 17.10에서 실제로 실행했다. 실행하지 않은 절차는 **(설계 절차, 시험 안 함)**으로 표시했다. 경로와 이름은 예시다.

## 1. 준비물

- **Linux.** `codeye start`·`stop`·`status`·`backup`·`restore`는 Linux 전용이다. Windows에서는 미지원 오류로 끝난다. RHEL 8에서는 실행하지 않았다([W25 §1](47-w25-packaging-and-operations.md#1-지원-범위)).
- **CODEYE 바이너리와 웹 자산.** 저장소에서 빌드한다. 외부에서 아무것도 내려받지 않는다.

  ```sh
  go build -o codeye ./cmd/codeye
  cd web && npm ci && npm run build      # 결과: web/dist
  ```

- **PostgreSQL.** CODEYE가 데이터베이스를 설치하거나 시작하지 않는다. 운영자가 서버를 준비하고 CODEYE 전용의 빈 데이터베이스와 사용자를 만든다. 백업·복원에는 같은 주 버전 이상의 `pg_dump`·`pg_restore`가 필요하다. 이 두 프로그램이 있는 디렉터리를 설정 `database.postgres_bin_dir`에 적는다. PATH에서는 찾지 않는다.
- **분석 도구.** Go 프로젝트에는 Go toolchain(`go vet`)이 필요하다. Staticcheck·cppcheck·Ruff 같은 도구는 설치한 것만 설정에 적는다. 적지 않은 도구는 보고서에 "미지원"으로 나온다. 0건 통과로 세지 않는다.
- **디스크.** 상태 디렉터리에는 소스 사본, 작업 사본, 로그, 산출물이 쌓인다. 크기 상한은 §8에서 정한다.

## 2. 설정 파일

기본 위치는 `~/.config/codeye/personal.json`이다(`XDG_CONFIG_HOME`을 따른다). 다른 위치는 모든 명령에 `--config PATH`로 준다. 시작점은 [examples/personal.json](../examples/personal.json)이다.

```json
{
  "schema_version": "0.1",
  "state_dir": "/home/user/.local/state/codeye",
  "listen": "127.0.0.1:7331",
  "web_dir": "/opt/codeye/web/dist",
  "database": {"mode": "external", "postgres_bin_dir": "/usr/pgsql-17/bin"},
  "source_roots": [{"label": "work", "path": "/home/user/src"}],
  "agent": {"max_workers": 2, "go": "/usr/local/go/bin/go"},
  "quota": {"workspace_bytes": 10737418240, "artifacts_bytes": 21474836480},
  "backup_dir": "/home/user/codeye-backups"
}
```

- 경로는 모두 절대 경로다. `state_dir`와 source root는 서로 포함하면 안 된다. 알 수 없는 필드가 있으면 설정 전체를 거부한다.
- `listen`은 loopback 주소만 받는다. 다른 사람이 접속하는 서버 배포는 개인 모드의 범위가 아니다.
- `web_dir`이 없으면 화면 없이 API만 뜬다.
- 필드 전체 표는 [W25 §2](47-w25-packaging-and-operations.md#2-설정-파일)에 있다.

**데이터베이스 연결은 설정에 쓰지 않는다.** 명령을 실행하는 셸에서 `CODEYE_DATABASE_URL` 환경변수로 준다. 형식은 `postgresql://USER:PASSWORD@127.0.0.1:5432/DBNAME?sslmode=disable`이다. loopback이 아니면 `sslmode`를 생략해 `verify-full`로 둔다. URL을 셸 기록에 남기지 않으려면 0600 파일에 두고 읽는다.

```sh
export CODEYE_DATABASE_URL="$(cat ~/.config/codeye/database-url)"
```

`PGPASSWORD` 같은 `PG*` 변수가 환경에 있으면 CODEYE가 DB 열기를 거부한다. 그 변수를 지운 셸에서 실행한다.

## 3. 첫 시작

```sh
codeye doctor --config ~/.config/codeye/personal.json   # 도구·DB·저장 공간 점검(선택)
codeye migrate                                           # 빈 DB에 schema 적용(처음과 업그레이드 때만)
codeye start                                             # controller와 로컬 에이전트 시작
codeye status                                            # running 확인
```

`codeye start`의 출력 예:

```text
created the personal user 72741cd6-…
registered source root "work" (8581fe6d-…)
login link written to /…/state/login-link (single use, expires about …Z); open the URL in it with a browser
CODEYE started: controller http://127.0.0.1:7331 (pid …), agent pid …; logs in /…/state/logs
```

로그인 링크 파일(0600)의 URL을 브라우저로 연다. 링크는 한 번만 쓸 수 있고 만료 시각이 있다. 새 링크가 필요하면 `codeye stop` 뒤 `codeye start`를 하거나 다음 명령을 쓴다. 사용자 ID는 `state_dir/personal.json`에 있다.

```sh
codeye login-link --user USER_ID --output /…/state/login-link --origin http://127.0.0.1:7331
```

migration 전에 `start`하면 `database schema is outdated (applied 0, this binary expects 9); back it up and run "codeye migrate" explicitly`로 끝난다. start는 schema를 바꾸지 않는다.

## 4. 프로젝트와 source root

**source root**는 분석할 프로젝트가 들어 있는 상위 디렉터리다. 에이전트는 설정에 적힌 root 안만 읽는다. 다음 순서로 root를 추가한다.

1. 설정 `source_roots`에 `{"label": "more", "path": "/home/user/more"}`를 더한다.
2. `codeye stop` 뒤 `codeye start`를 한다.

start는 root를 등록한다. 에이전트의 root가 바뀌었으므로 다시 등록한다. 이전 에이전트 상태는 지우지 않고 이름만 바꿔 둔다.

```text
registered source root "more" (b54bb497-…)
re-enrolling the local agent: the configured source roots changed (previous state kept in /…/state/agent.retired-…)
```

**프로젝트**는 화면의 "프로젝트 등록"에서 만든다. "서버의 로컬 폴더"를 고르고 source root와 root 아래 상대 경로(예: `hello`, root 자체는 `.`)를 넣는다. 프로젝트 설명이나 요구사항 문서는 필요 없다. CODEYE가 문서·환경·소스를 직접 조사한다.

저장소의 빌드 스크립트나 테스트를 실행하는 분석기는 root마다 허가가 필요하다(`"repository_code": true`). 기본은 허가하지 않음이다.

## 5. 분석 실행

프로젝트를 고르고 "분석 요청 등록"을 누른다. 요청은 대기열에 저장되고 에이전트가 가져가 실행한다. 화면은 단계(소스 확보, 환경 분석, 문서 검토, 정적 분석, 코드 분석, 보고서 생성)별 상태와 진행률을 보여 준다.

- **일부만 완료(partial)**: 일부 작업을 하지 못했다는 뜻이다. 화면에 이유가 나온다. 예: "노드 운영자가 이 분석 도구 경로를 지정하지 않아 실행하지 않음", "소스의 모델 전송이 승인되지 않아 제외됨".
- **대기 사유**: 시작하지 않은 run에는 이유가 표시된다. 저장 공간 상한에 걸리면 "저장 공간 상한에 도달해 새 작업을 시작하지 않습니다"가 나온다(§8).
- 예약 실행은 같은 화면의 "예약 실행"에서 cron 식과 시간대로 등록한다.

## 6. 보고서 읽기

"보고서 열기"를 누르면 게시된 리비전을 보여 준다.

- **요약.** 도구 진단, 발견 사항, 조치 검토, 미확정, 반증됨의 수다. 수치는 저장된 구조화 데이터에서 계산한다. 모델의 서술로 바뀌지 않는다.
- **분석 범위와 한계.** 하지 못한 검토와 그 이유다. 여기 적힌 범위는 "문제 없음"이 아니다.
- **영역별 발견 사항.** 발견마다 발생 조건, 심각도 근거, 영향, 개선 방향, 확인 방법, 근거가 있다. 근거는 snapshot의 파일 위치와 hash, 도구 원시 출력 산출물이다.
- **사람 판정.** 발견에 "확인함", "위험 수용", "오탐" 같은 판정과 사유를 기록한다. 기록은 감사 이력으로 남고 수정·삭제되지 않는다. 다음 실행의 비교에 반영된다.
- **이전 실행과 비교.** 검사가 빠져서 사라진 발견은 "해결"로 세지 않는다.
- **내보내기.** JSON·Markdown·HTML이다. "코드 포함"을 고르지 않으면 코드 발췌를 빼고 내보낸다.

## 7. 모델 검토 설정

모델 설정이 없으면 문서·코드의 모델 검토 단계는 실행하지 않는다. 보고서에는 "모델 검토 미수행"으로 나온다. 다른 모델이나 인터넷 서비스로 자동 전환하지 않는다.

승인된 모델을 쓰려면 설정에 다음 두 가지를 넣는다. **(W25에서는 설정 검증과 에이전트 인자 연결만 시험했다. 모델 호출은 하지 않았다.)**

```json
"agent": {
  "model": {"opencode": "/opt/opencode/bin/opencode", "name": "PROVIDER/MODEL",
            "relay_file": "/home/user/.config/codeye/relay.json", "review_pack": "review-v1"}
},
"source_roots": [{"label": "work", "path": "/home/user/src", "model_review": true}]
```

- `opencode`는 설치한 OpenCode CLI의 절대 경로다. `name`은 그 CLI에 구성된 모델 이름이다.
- `relay_file`은 승인된 로컬 relay를 쓸 때만 넣는다. relay 구동과 비용 장부는 [QA 예산 relay](r0-openai-gateway.md)를 따른다.
- `model_review: true`인 root의 코드만 모델에 보낸다. 기본은 보내지 않음이다.
- `review_pack`의 기본은 `review-v1`이다. `review-r1`은 승격 전 후보다.
- 모델 연결은 먼저 `codeye model probe`로 확인한다([W10 기록](32-w10-opencode.md)). 이 명령은 실제 추론을 요청하므로 비용이 들 수 있다.

## 8. 저장 공간

설정 `quota`에 영역별 상한(바이트)을 둔다. 영역은 소스 사본(`snapshot_bytes`), 작업 사본(`workspace_bytes`), 도구 cache(`cache_bytes`), 로그(`logs_bytes`), 분석 산출물(`artifacts_bytes`)이다. 0이나 생략은 상한 없음이다.

- 상한에 도달하면 **새 분석만** 시작하지 않는다. 실행 중인 분석은 끝까지 하며, 그동안 상한을 넘을 수 있다. 보고서와 산출물은 지우지 않는다.
- 사용량은 `codeye status`(적용 기준)와 `codeye doctor --config PATH`(지금 디렉터리를 잰 값과 파일시스템 여유)로 본다.

```text
storage workspace  0 bytes limit=2000000000 exceeded=false (node_heartbeat)
storage artifacts  19459 bytes limit=1000000000 exceeded=false (database_accounting)
```

- 공간이 부족하면 운영자가 정리하거나 상한을 늘린다. 상한을 늘리려면 설정을 바꾸고 `codeye stop` → `codeye start`를 한다. CODEYE는 오래된 파일을 자동으로 지우지 않는다.

## 9. 정지

```sh
codeye stop
```

1. 에이전트에 drain을 요청한다. 새 작업을 받지 않고 실행 중인 분석을 끝낸다.
2. `stop.drain_timeout_seconds`(기본 300초) 안에 끝나지 않으면 실행 중인 분석을 취소하고 정리를 확인한다. 취소된 run은 `failed`로 남는다. 다시 분석하려면 새로 요청한다.
3. 에이전트가 끝나면 controller를 멈춘다.

마지막 줄의 `outcome`을 확인한다.

| outcome | 뜻과 조치 |
|---|---|
| `stopped` | 정상 정지 |
| `not_running` | 이미 멈춰 있었다 |
| `recovery_required` | 정리를 확인하지 못했다. 에이전트가 아직 실행 중이면 controller도 남겨 둔다. `codeye status`와 `state_dir/logs`를 확인하고 잠시 뒤 `codeye stop`을 다시 한다 |
| `failed` | controller가 멈추지 않았다. 강제 종료하지 않았다 |

CODEYE는 프로세스를 강제 종료(SIGKILL)하지 않는다. 기록한 프로세스와 시작 시각이 같은 프로세스에만 신호를 보낸다.

## 10. 백업과 복원

**백업.** 실행 중에도 할 수 있다. 다만 정지 뒤에 하면 진행 중인 분석이 없어 복원 결과가 깔끔하다.

```sh
codeye stop
codeye backup                     # backup_dir/codeye-<UTC 시각>에 만든다
codeye backup --output /mnt/backup/codeye-1   # 위치를 직접 줄 때(없거나 빈 디렉터리)
```

```text
backup written to /…/codeye-backups/codeye-20261118T191428Z
  database: migration 9, PostgreSQL 17.10, dump 175706 bytes sha256 8ea9902…
  artifacts: 14 contents, 19418 bytes (finalized at the same snapshot)
  rows: projects 1, runs 1, reports 1, evidence 0, schedules 0
```

백업 세트에는 DB dump와 확정된 산출물이 같은 시점으로 들어 있다. 연결 정보와 비밀번호는 없지만 프로젝트·보고서 같은 운영 데이터가 있다. 세트는 권한을 지켜 보관한다.

**복원.** 새 빈 데이터베이스와 새 `state_dir`에만 복원한다. 기존 설치를 덮어쓰지 않는다.

1. 빈 데이터베이스를 만든다. `codeye migrate`는 하지 않는다.
2. 새 `state_dir`(필요하면 다른 `listen` 포트)로 설정 파일을 만든다.
3. 다음을 실행한다.

```sh
export CODEYE_DATABASE_URL="$(cat ~/.config/codeye/database-url-restored)"
codeye restore --config ~/.config/codeye/restored.json --from /…/codeye-backups/codeye-20261118T191428Z
codeye start --config ~/.config/codeye/restored.json
```

```text
restored /…/codeye-20261118T191428Z (created 2026-11-18T19:14:28Z, migration 9)
  verified: dump and 14 artifact contents by sha256; every restored artifact row has its bytes
  rows: projects 1, runs 1, reports 1, evidence 0, finding reviews 1, schedules 0
  fenced: 1 nodes revoked, 1 sessions revoked, 0 leased attempts -> recovery_required (0 reservations kept)
next: codeye start (the local agent enrolls as a new node)
```

복원은 세트의 hash부터 모두 확인한다. 이전 노드, 로그인 세션, 쓰지 않은 링크는 무효가 된다. 그래서 복원본 start가 새 로그인 링크를 쓰고, 에이전트는 새 노드로 등록한다. 백업 때 실행 중이던 분석은 `recovery_required`로 남는다. 그 run은 다시 요청한다.

## 11. 업그레이드 (설계 절차, 시험 안 함)

W25 이전 버전에는 개인 모드가 없어서 버전 간 업그레이드를 시험하지 못했다. 각 명령은 실행해 보았다.

```sh
codeye stop
codeye backup
# 새 codeye 바이너리와 web/dist로 교체
codeye migrate
codeye start
```

migration은 `codeye migrate`만 한다. 새 바이너리의 start는 schema가 다르면 시작하지 않는다.

## 12. 문제 해결

| 증상(출력) | 원인과 조치 |
|---|---|
| `CODEYE_DATABASE_URL is not set (database.mode external)` | 셸에 DB URL이 없다. §2 |
| `database unavailable: invalid input`, status의 `database=false` | URL 형식이 틀렸거나 환경에 `PGPASSWORD` 같은 `PG*` 변수가 있다. 그 변수를 지운다(§2) |
| `database schema is outdated (applied N, this binary expects M)…` | 백업한 뒤 `codeye migrate` |
| `config is not valid: …` | 설정 오류. 메시지가 필드를 알려 준다(예: `agent.toolchain_path entries must be absolute, clean paths`) |
| `database.mode "managed" is not implemented …` | 관리형 PostgreSQL은 없다. `external`로 두고 DB를 직접 준비한다 |
| `CODEYE is already running (controller …)` | 이미 실행 중이다. 종료 코드 0 |
| `CODEYE is partially running; run codeye stop first, then codeye start` | 한쪽만 살아 있다. `codeye stop` 뒤 start |
| `the listen address … is already in use by a process codeye start did not record` | 다른 프로그램이 포트를 쓴다. `listen`을 바꾸거나 그 프로그램을 멈춘다 |
| `an agent that codeye start did not record holds …/agent/agent.lock …` | 수동으로 띄운 `codeye agent`가 같은 상태 디렉터리를 쓴다. 그 프로세스를 직접 멈춘다 |
| `removed stale controller record (pid … pid_reused)` | 이전 프로세스가 비정상 종료됐다. 기록만 지웠고 신호는 보내지 않았다. 정상 |
| `status`의 `issue: stale_record`, 종료 코드 1 | 위와 같다. `codeye start`가 정리한다 |
| `the agent did not enroll and report a heartbeat in time` | `state_dir/logs/agent.log` 확인. 도구 경로·source root 경로가 맞는지 본다 |
| 분석이 시작되지 않고 저장 공간 상한 문구가 나온다 | §8. `codeye status`의 `exceeded=true` 영역을 정리하거나 상한을 늘린다 |
| stop의 `outcome: recovery_required` | §9 |
| `the target database is not empty; restore only into a new, empty database` | 복원 대상 DB가 비어 있지 않다. 새 DB를 만든다 |
| `the target state_dir already has personal.json; restore needs a fresh state_dir` | 새 `state_dir`를 쓴다 |
| `backup verification failed: …` | 세트가 손상됐거나 완성되지 않았다(manifest 없음). 다른 세트를 쓴다 |
| 로그인 링크가 만료됐다 | §3의 `codeye login-link` 또는 stop·start |
| `codeye start/stop/status manage processes on Linux only (W25)…` | Windows·macOS는 개인 모드를 지원하지 않는다 |

점검 순서: `codeye status --format json` → `codeye doctor --config PATH` → `state_dir/logs/controller.log`, `agent.log`. 로그와 출력에는 DB 비밀번호와 토큰이 나오지 않는다. 문의할 때 설정 파일과 로그를 그대로 보내도 연결 정보는 들어 있지 않다. 다만 경로와 프로젝트 이름은 들어 있다.
