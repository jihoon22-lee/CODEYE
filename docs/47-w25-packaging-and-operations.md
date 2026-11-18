# 47. W25 개인 패키징과 운영

W25는 한 사람이 한 머신에서 CODEYE를 Native로 운영하는 경로다. 설정 파일 하나로 controller와 로컬 에이전트를 시작하고 멈춘다. 상태와 저장 공간을 확인하고, 근거를 잃지 않게 백업하고 복원한다. 사용자용 절차는 [개인 사용 안내서](48-user-guide.md)에 있다. 이 문서는 계약, 판단, 시험과 실행 결과를 적는다.

| 범위 | 상태 |
|---|---|
| `codeye start`·`stop`·`status`(개인 모드) | Linux에서 구현·검증. Windows는 빌드되며 명시적 미지원 오류를 낸다(§1) |
| 데이터베이스 | 운영자가 준비한 PostgreSQL(`CODEYE_DATABASE_URL`)만 구현. 관리형(번들) PostgreSQL은 구현하지 않았다 |
| 저장 영역 상한 | snapshot·workspace·cache·logs·artifacts. 새 작업만 거절하고 지우지 않는다(§4) |
| 백업·복원 | DB와 확정 산출물을 같은 논리 시점으로 묶는다. 빈 대상에만 복원하고 이전 lease를 살리지 않는다(§5) |
| `codeye doctor --config` | 저장 영역 사용량·상한, 최신 백업 나이, start/stop 상태 일관성(§6) |

요구사항 연결: REQ-018(자원 제한), REQ-021(drain), REQ-022(lease·fencing·복구), NFR-003(재시작 뒤 복구), NFR-006(loopback 개인 모드, 민감값), NFR-007(산출물 hash). 수용 시험은 TC-124~127과 TC-32다(§7). 결정은 [A32](19-decisions-and-open-questions.md)에 있다.

## 1. 지원 범위

- **Linux.** 프로세스 관리는 `pidfd_open`(Linux 5.3 이상), `flock`, `setsid`, `SIGUSR1`(drain)·`SIGTERM`(취소)을 쓴다. 이 저장소의 검증은 WSL2 Linux 커널 6.18에서 했다.
- **RHEL 8.10.** 커널(4.18 기반)이 `pidfd_open`을 제공하는지 확인하지 않았다. 제공하지 않으면 `stop`은 신호를 보내지 않는다. 동작은 `action=not_signalled_unsupported`, 결과는 `recovery_required`다. 같은 pid를 쓰는 다른 프로세스에 신호가 가지 않게 하려는 선택이다. RHEL 8에서는 실행하지 않았다.
- **Windows.** `GOOS=windows go vet ./...`가 통과하고, 변경한 패키지의 Windows 시험 바이너리가 통과한다(§10). `start`·`stop`·`status`·`backup`·`restore`는 `codeye start/stop/status manage processes on Linux only (W25)…` 오류와 종료 코드 1로 끝난다. `doctor --config`는 동작하며 `processes.status=unsupported`를 낸다.
- **macOS.** 빌드와 실행을 확인하지 않았다.

## 2. 설정 파일

기본 경로는 `$XDG_CONFIG_HOME/codeye/personal.json`(Go `os.UserConfigDir`)이다. 모든 명령은 `--config PATH`를 받는다. 형식은 JSON 하나다. 알 수 없는 필드, 뒤에 붙은 값, 상대 경로, 정리되지 않은 경로는 거부한다. 오류 메시지에는 설정 경로나 거부된 값을 넣지 않는다. 예시는 [examples/personal.json](../examples/personal.json)이고, 이 빌드의 도구 이름으로 읽힌다는 시험이 있다.

| 필드 | 기본값 | 검증 |
|---|---|---|
| `schema_version` | (필수) | `"0.1"` |
| `state_dir` | (필수) | 절대 경로. 0700으로 만들고, 소유자가 다르거나 symlink면 거부한다. source root와 서로 포함하면 안 된다 |
| `listen` | `127.0.0.1:7331` | loopback 주소와 0이 아닌 포트만 |
| `web_dir` | 빈 값(API만) | 빌드된 `web/dist`의 절대 경로 |
| `database.mode` | (필수) | `external`만 구현. `managed`는 `not implemented` 오류 |
| `database.postgres_bin_dir` | 빈 값 | `pg_dump`·`pg_restore`가 있는 절대 경로. PATH에서 찾지 않는다. 백업·복원에 필요하다 |
| `source_roots[]` | (1~32개) | `label`, 절대 `path`. 선택 `repository_code`(저장소 코드 실행 허가, [docs/39 §2.7](39-r1-analyzer-packs.md)), `model_review`(모델 전송 승인, `agent.model` 필요) |
| `agent.max_workers` | 1 | 1~16. 에이전트는 `--autoscale --max-workers N`으로 시작한다 |
| `agent.toolchain_path` | `/usr/bin:/bin`(Linux) | 절대 경로 목록 |
| `agent.go`, `agent.go_mod_cache` | 빈 값 | 절대 경로 |
| `agent.tools` | 빈 값 | 이 빌드의 분석기·toolchain 플래그 이름(`staticcheck`, `node`, `ruff` 등)만 |
| `agent.python_both_type_checkers` | false | mypy·pyright를 모두 실행 |
| `agent.model` | 없음 | `opencode`(절대 경로), `name`(`PROVIDER/MODEL`), 선택 `relay_file`, `review_pack`(`review-v1`·`review-r1`) |
| `quota.*_bytes` | 0(상한 없음) | `snapshot`·`workspace`·`cache`·`logs`·`artifacts`, 음수 거부 |
| `backup_dir` | 빈 값 | 절대 경로. `state_dir` 안이면 거부 |
| `stop.drain_timeout_seconds` | 300 | 1~86400 |
| `stop.cancel_timeout_seconds` | 90 | 45~3600 |

설정과 상태 디렉터리에는 DB URL이나 토큰을 쓰지 않는다. DB 연결은 실행할 때의 환경변수 `CODEYE_DATABASE_URL`만 쓴다. `codeye` 프로세스 환경에 `PG*` 변수가 있으면 DB 열기를 거부한다(fail closed).

## 3. start·stop·status

```text
codeye start  [--config PATH]
codeye stop   [--config PATH] [--format text|json]
codeye status [--config PATH] [--format text|json]
```

상태 디렉터리 배치(모두 0700, 파일 0600):

| 경로 | 내용 |
|---|---|
| `run/controller.json`, `run/agent.json` | pid, 프로세스 시작 시각(`/proc/PID/stat` starttime), 실행 파일, 기록 시각 |
| `run/enroll-token` | 일회용 등록 토큰. 등록·heartbeat 확인 직후, 그리고 실패한 start의 끝에 지운다 |
| `personal.json` | 개인 사용자 ID |
| `login-link` | 일회용 로그인 링크(만료 시각 포함) |
| `personal.lock` | start·stop·backup·restore의 상호 배제 |
| `logs/controller.log`, `logs/agent.log` | 자식 프로세스 출력(O_APPEND, 0600) |
| `agent/` | 에이전트 상태 디렉터리(journal, node.json, 작업 사본) |
| `artifacts/` | controller 산출물 저장소 |

**start.** 개인 lock을 잡고 상태를 검사한다(§3.1). 둘 다 실행 중이면 그대로 종료 코드 0이다. 하나만 실행 중이거나 기록 없는 프로세스가 lock·포트를 쥐고 있으면 거부한다. 기록의 프로세스가 끝났거나 pid가 다른 프로세스로 재사용됐으면 기록만 지운다(신호 없음). 그 뒤 순서는 다음과 같다.

1. schema가 이 바이너리의 migration(현재 9)과 같은지 확인한다. 다르면 `run "codeye migrate" explicitly` 오류로 끝난다. start는 migration을 하지 않는다.
2. 개인 사용자를 만들거나 다시 쓴다. 설정의 source root를 label로 등록하고 사용자에게 허가한다. 같은 label이 다른 경로로 등록돼 있으면 거부한다. `quota.artifacts_bytes`를 `scheduler_limits.artifact_quota_bytes`에 반영한다(0이면 제거).
3. 등록이 필요한지 정한다. 처음 시작했거나, 노드가 철회됐거나, DB가 노드를 모르거나(복원 뒤), source root 집합이 바뀌었으면 다시 등록한다. 다시 등록하기 전에 에이전트 journal에 끝나지 않은 작업이 없는지 확인한다. 이전 노드는 철회하고, 이전 `agent/`는 `agent.retired-<ns>`로 이름만 바꾼다(삭제하지 않는다). 등록 토큰은 10분짜리 일회용이며 0600 파일로만 전달한다.
4. `codeye serve`를 setsid로 띄운다. 환경은 PATH·HOME·LANG·TZ와 `CODEYE_DATABASE_URL`뿐이다. `/api/v1/ready`를 30초까지 기다린다.
5. `codeye agent`를 띄운다. 환경은 PATH·LANG뿐이며 DB 정보가 없다. argv는 설정에서 만든다(source root 허가, 도구, 모델, `--storage-quota`, `--storage-log-dir`). `node.json`이 생기고 DB에 신선한 heartbeat가 올 때까지 120초를 기다린다.
6. 실패하면 이미 띄운 프로세스를 역순으로 SIGTERM하고 45초 기다린다. 끝나지 않은 프로세스의 기록은 지우지 않는다.

**stop.** 신호는 pidfd로 보낸다. pidfd를 연 뒤 그 프로세스의 시작 시각이 기록과 같은지 확인하고 `pidfd_send_signal`을 호출한다. 그래서 재사용된 pid의 다른 프로세스에는 신호가 가지 않는다. 강제 종료(SIGKILL)는 하지 않는다.

1. 에이전트에 SIGUSR1(drain)을 보낸다. 에이전트는 새 claim을 멈추고 유휴 워커를 정리한다. 실행 중인 attempt는 끝까지 수행해 결과를 보고한 뒤 스스로 종료한다(exit 0).
2. `stop.drain_timeout_seconds` 안에 끝나지 않으면 SIGTERM을 보낸다. 에이전트는 실행 중인 작업을 취소하고 하위 프로세스 정리를 확인한다. 그 attempt는 `failed`(`agent_shutdown`)로 끝나고 run도 `failed`다. 성공으로 바꾸지 않는다. 다시 분석하려면 새 run을 요청한다.
3. 에이전트가 끝나면 에이전트 journal을 읽어 정리를 판정한다(`agent_cleanup`: `confirmed`·`uncertain`). 그다음 controller에 SIGTERM을 보낸다.
4. 결과는 `stopped`, `not_running`, `recovery_required`, `failed` 중 하나다. 다음이면 `recovery_required`다: 에이전트가 `cancel_timeout_seconds` 뒤에도 실행 중(이때 controller는 남겨 정리 보고를 받을 수 있게 한다), journal 판정 `uncertain`, 이 노드의 attempt가 DB에서 아직 `leased`·`recovery_required`. controller가 멈추지 않으면 `failed`다. 종료 코드 0은 `stopped`·`not_running`일 때뿐이다.

JSON 출력은 `outcome`, `processes[]`(`state_before`, `action`, `state_after`), `in_flight_at_stop`, `attempts_after_stop`, `agent_cleanup`, `notes`를 담는다. DB가 없으면 진행 중 작업을 읽지 못했다고 `notes`에 적는다.

**status.** 프로세스별 상태, controller `/ready`, DB 연결, 노드 ID와 heartbeat 신선도, 워커 상태별 수, 이 노드의 진행 중 attempt, 저장 영역(§4)을 보여 준다. 상태는 `running`·`stopped`·`degraded`이며 `degraded`면 종료 코드 1이다. 다른 start·stop이 lock을 쥐고 있으면 `operation_in_progress`를 보고하고 에이전트 lock은 확인하지 않는다.

### 3.1 상태 일관성

`Inspect`는 기록, 살아 있는 프로세스, 에이전트 상태 lock(`agent/agent.lock`), listen 주소를 대조한다.

| 문제 | 뜻 |
|---|---|
| `stale_record` | 기록의 프로세스가 끝났거나 pid가 재사용됐다(`exited`·`pid_reused`) |
| `agent_running_without_record` | 기록 없는 프로세스가 에이전트 lock을 쥐고 있다 |
| `listen_in_use_without_record` | 기록 없는 프로세스가 listen 주소를 쓰고 있다 |
| `partially_running` | controller와 에이전트 중 하나만 실행 중이다 |
| `record_unreadable` | 기록이나 lock을 읽지 못했다 |

기록되지 않은 프로세스에는 어떤 명령도 신호를 보내지 않는다.

## 4. 저장 영역 상한

| 영역 | 측정 대상 | 적용 근거 |
|---|---|---|
| `snapshot` | `agent/data/acquire-*`(그 안의 `workspace` 제외) | 에이전트가 직접 잰다. heartbeat `storage_quota_exceeded` |
| `workspace` | `agent/data/acquire-*/workspace`, `agent/data/ws-*` | 같음 |
| `cache` | `agent/workers`(워커 home·scratch), `agent.go_mod_cache` | 같음 |
| `logs` | `state_dir/logs` | 같음(`--storage-log-dir`) |
| `artifacts` | controller 산출물 | DB 집계. `artifacts`에서 sha256마다 한 번 센 크기 합 |

- 측정은 symlink를 따라가지 않고 파일 자체 크기를 센다. 읽지 못한 항목이 있으면 `incomplete`로 표시한다.
- 에이전트는 30초마다 잰다. 사용량이 상한 이상인 영역을 heartbeat에 보낸다. 상한이 없으면 필드를 보내지 않아 heartbeat 바이트가 바뀌지 않는다.
- **거절 지점.** `ClaimAttempt` 트랜잭션 안에서 판정한다. 노드가 보고한 영역이 하나라도 있으면 그 노드는 새 claim을 받지 않는다(204). `artifact_quota_bytes`가 있고 확정 산출물 합이 그 이상이면 어떤 노드도 새 claim을 받지 않는다. 대기 중인 run의 `progress.waiting_reason`은 `storage_quota_exceeded`다. 기존 검사(`request_unavailable`부터 `project_limit`까지) 뒤의 순서는 `pool_paused` → 전역 상한 `capacity_full` → 산출물 상한 또는 "여유 있는 호환 노드가 모두 저장 상한"이면 `storage_quota_exceeded` → 그 밖의 여유 부족 `capacity_full`이다.
- **보호.** 실행 중인 attempt는 중단하지 않는다. 그래서 그 attempt의 업로드로 상한을 넘을 수 있다. 확정된 산출물과 보고서는 지우지 않는다. 오래된 파일부터 지우는 정리도, 정리 후보 목록(dry run)도 구현하지 않았다. 공간을 비우는 일은 운영자가 한다.
- **측정과 적용의 구분.** `status`는 영역별 `basis`(`node_heartbeat`·`database_accounting`)를 보여 준다. `doctor`는 디렉터리를 지금 직접 재므로 적용 값과 다를 수 있다. 특히 artifacts의 디렉터리 크기와 DB 집계는 다르다.
- 상한 조정: 설정을 바꾸고 `codeye stop`·`codeye start`. 운영자 CLI `codeye scheduler set --artifact-quota-bytes N`(0이면 제거)도 있다.

[migration 0009](../migrations/sql/0009_storage_quota.sql)는 `nodes.storage_quota_exceeded`(영역 배열, 기본 빈 배열)와 `scheduler_limits.artifact_quota_bytes`(NULL 또는 양수)만 더한다. 기존 행과 0001~0008 checksum은 바뀌지 않는다.

## 5. 백업과 복원

```text
codeye backup  [--config PATH] [--output DIR] [--format text|json]
codeye restore [--config PATH] --from DIR [--format text|json]
```

### 5.1 백업

출력 디렉터리는 없거나 비어 있어야 한다. `--output`이 없으면 `<backup_dir>/codeye-<UTC 시각>`이다. 실행 중에도(온라인) 정지 뒤에도 만들 수 있다. 개인 lock을 잡으므로 start·stop·restore와 겹치지 않는다.

1. `REPEATABLE READ READ ONLY` 트랜잭션을 열고 `pg_export_snapshot()`으로 snapshot ID를 얻는다. 같은 snapshot에서 migration 버전, 서버 버전, 확정 산출물 목록(`finalized`이고 저장 키가 있는 행의 sha256·크기), 표별 행 수를 읽는다.
2. 트랜잭션을 연 채 `pg_dump --format=custom --no-owner --no-privileges --snapshot=ID --schema=<현재 schema> --file=database.dump`를 실행한다. 파일은 미리 0600으로 만든다. `pg_dump` 주 버전이 서버보다 낮으면 거부한다.
3. 목록의 각 내용을 산출물 저장소에서 복사하며 다시 hash한다. sha256과 크기가 DB와 다르면 실패한다.
4. `artifacts.json`(정렬된 sha256·크기 목록)을 쓰고, 마지막에 `manifest.json`을 쓴다. manifest가 없으면 완성되지 않은 세트다. 실패하면 출력 디렉터리 내용을 지운다.

**같은 논리 시점.** dump와 산출물 목록은 같은 DB snapshot에서 나온다. 산출물은 내용 주소(sha256)이고 확정 뒤 바뀌지 않는다. 그래서 snapshot 뒤에 복사해도 같은 바이트이며, 복사할 때 hash로 확인한다. snapshot 뒤에 확정된 산출물은 세트에 들어가지 않는다. 그 산출물을 가리키는 행도 dump에 없다. 확정되지 않은 업로드는 백업하지 않는다. 시험은 snapshot 직후 산출물을 하나 더 확정해 이것을 확인한다(TC-32).

manifest 필드: `format`(`codeye-backup/0.1`), `created_at`, `consistency`(`exported_snapshot`), `migration_version`, `codeye_binary_migrations`, `postgres_server_version`, `pg_dump_version`, `database`(파일·형식·sha256·크기), `artifacts`(목록 파일 hash, 수, 바이트, 내용 없는 행 수), `rows`, `personal_user_id`. 호스트·사용자·DB 이름·비밀번호·URL은 넣지 않는다. 모든 파일은 0600, 디렉터리는 0700이다.

**인증정보.** `pg_dump`·`pg_restore`는 `database.postgres_bin_dir`에서만 찾는다. argv에는 플래그와 파일 경로만 들어간다. 연결 정보는 자식 환경의 `PGHOST`·`PGPORT`·`PGUSER`·`PGPASSWORD`·`PGDATABASE`·`PGSSLMODE`·`PGSSLROOTCERT`로만 전달한다. `PGPASSFILE=/dev/null`로 사용자 `.pgpass`를 읽지 않게 하고, cwd는 임시 디렉터리다. 오류 문구의 비밀번호는 `[REDACTED]`로 바꾼다. URL 규칙은 controller와 같다(loopback만 `sslmode=disable`, 그 외 `verify-full`).

### 5.2 복원 전 검증

- 대상 설치가 실행 중이거나 불일치하면 거부한다. 대상 `state_dir`에 `agent/node.json`이나 `personal.json`이 있으면 거부한다. 새 `state_dir`가 필요하다.
- 세트 검증: manifest를 엄격하게 읽고 형식을 확인한다. dump와 목록의 hash·크기, 목록 정렬, 각 내용의 hash·크기, 총계를 확인한다. 하나라도 다르면 대상에 아무것도 쓰지 않는다.
- manifest의 migration 버전이 이 바이너리보다 높으면 거부한다. 낮으면 복원 뒤 start가 `codeye migrate`를 요구한다.
- 대상 DB의 현재 schema에 표가 하나라도 있으면 거부한다(`not empty`). 대상 산출물 디렉터리에 파일이 있으면 거부한다.

### 5.3 복원과 fencing

1. 내용을 산출물 저장소의 쓰기 경로(`artifactstore.Put`)로 넣는다.
2. `pg_restore --list` 결과에서 `SCHEMA public`·`COMMENT ON SCHEMA public` 항목을 뺀다. 그 목록으로 `pg_restore --use-list --exit-on-error --single-transaction --no-owner --no-privileges`를 실행한다.
3. 복원된 행 수가 manifest와 같은지 비교한다. 확정 산출물 행마다 내용이 있는지 다시 hash해 확인한다.
4. **fence.** 한 트랜잭션에서 모든 노드를 철회하고 그 워커를 `stopped`로 둔다. 모든 세션을 철회하고, 쓰지 않은 로그인 토큰을 사용 처리하고, 쓰지 않은 등록 토큰을 철회한다. `leased` attempt는 lease를 지금으로 끝낸 뒤 기존 만료 경로(`ExpireAttempt`)로 `recovery_required`로 둔다. 예약은 유지한다. 대기 중인 run, 예약, 이미 만든 예약 회차는 그대로라서 같은 회차가 다시 만들어지지 않는다.
5. `personal.json`에 manifest의 사용자 ID를 쓴다.

실패하면 대상 산출물 디렉터리를 비운다. `pg_restore`가 성공한 뒤의 실패라면 DB를 지우고 다시 만들라고 출력한다(복원 명령은 DB를 지우지 않는다).

복원 뒤 `codeye start`는 새 노드로 등록한다. 이전 노드 credential과 세션은 거부된다. 이전 epoch의 lease 갱신은 `ErrStaleAttempt`다. 복원 시점에 실행 중이던 run은 `recovery_required`로 남는다. 철회된 노드만 정리를 확인할 수 있어서 이 run의 예약을 푸는 운영자 명령은 아직 없다(§9). 정지 뒤에 백업하면 이 경우가 생기지 않는다.

## 6. doctor의 operations 절

`codeye doctor --config PATH`는 기존 13개 check에 선택 필드 `operations`를 더한다([schema](../schemas/doctor.schema.json), [예시](../examples/doctor.json)). `--config`가 없으면 출력은 W01과 같다. 종료 코드 규칙도 같다.

- `storage[]`: 다섯 영역마다 `status`(`within_limit`·`over_limit`·`no_limit`·`unmeasured`), `bytes`, `limit_bytes`, `filesystem_available_bytes`(statfs, Linux 외 null), `incomplete`, `enforcement`(`claim_refusal_node_heartbeat`·`claim_refusal_database_accounting`).
- `backup`: `backup_dir`의 가장 최근 완성 세트(manifest 형식이 맞는 것). `status`는 `not_configured`·`none`·`unreadable`·`recent`·`stale`(7일 초과)이고 `latest`, `created_at`, `age_seconds`가 붙는다. 세트 검증(hash)은 하지 않는다. 검증은 restore가 한다.
- `processes`: §3.1의 검사. `status`는 `consistent`·`inconsistent`·`unsupported`, 역할별 상태, `issues`다. start·stop이 진행 중이면 `operation_in_progress`이고 에이전트 lock은 확인하지 않는다.

schema는 선택 속성 하나만 더했다. 상태와 필드의 조합(`unmeasured`면 `bytes`가 null, `consistent`면 문제 없음 등)은 `if/then`으로 묶었다. 웹의 Ajv strict 검증기도 이 schema로 생성된다. 화면에는 아직 operations를 표시하지 않는다.

## 7. 수용 시험

| ID | 시험 | 위치 |
|---|---|---|
| TC-124 | 영역 측정(`TestMeasureAreas`, `TestParseLimit`), heartbeat 보고와 상한 없을 때 생략(`TestStorageQuotaReportedInHeartbeat`, `TestHeartbeatOmitsQuotaWithoutLimits`), 새 claim만 거절·대기 사유·실행 중 attempt 유지(`TestW25StorageQuotaRefusesOnlyNewClaims`), 실제 바이너리의 artifacts·logs 상한 거절 | `internal/quota`, `internal/agent/w25_test.go`, `internal/storage/postgres/w25_test.go`, `scripts/personal_smoke.py` C |
| TC-125 | drain 중 실행 작업 완료·추가 claim 없음·exit 0(`TestDrainFinishesRunningWorkThenExits`), 검증된 신호(`TestSignalVerifiedSingleProcess`), 설정 엄격성(`TestParseDefaultsAndStrictness`), 0600·0700·symlink 거부(`TestRecordsAndIdentityArePrivate`), stale·재사용 pid·기록 없는 에이전트·lock 배타(`TestInspectFindsStaleAndUnrecordedProcesses`), agent argv(`TestPersonalAgentArgv`), drain 시간 초과 취소·남은 프로세스 없음·재사용 pid 기록 무신호 | `internal/agent`, `internal/agent/procid`, `internal/personal`, `cmd/codeye`, smoke C |
| TC-126 | doctor operations(`TestOperationsStorageBackupAndProcesses`), schema 음성 사례 9개(`check_w25_doctor`), 복원 설치의 실제 doctor 출력 schema 검증 | `internal/doctor`, `scripts/validate_bundle.py`, smoke A |
| TC-127 | 개인 사용 경로 8단계 자동 실행과 수동 walkthrough(§8). 안내서의 명령은 모두 실행한 것이다 | smoke A, §8, [docs/48](48-user-guide.md) |
| TC-32 | 실행 중 백업, 늦게 확정된 산출물 제외, 0600·manifest 비밀값 없음, 변조 거부, 비어 있지 않은 대상 거절(DB·산출물 미변경), 복원 뒤 보고서·근거 hash·ACL·source grant·예약 회차 중복 없음·`recovery_required`·이전 lease/노드/세션 거부·새 노드 claim(`TestTC32BackupRestoreRoundTrip`), fence 단위(`TestW25FenceRestoredNeverRevivesLeases`), TOC 필터(`TestSchemaEntryFilter`), 불완전 세트(`TestVerifyRejectsIncompleteSets`), URL·redaction(`TestParseURLEnvironmentOnly`, `TestRunRedactsPassword`) | `internal/backup`, `internal/storage/postgres`, `internal/pgtool`, smoke A |

`scripts/test_postgres.py --personal`이 [personal_smoke.py](../scripts/personal_smoke.py)를 실행한다. 임시 PostgreSQL에 DB 셋을 만든다. A는 실제 `go vet` 분석, 온라인·정지 백업, 거절과 복원, 복원본 재시작·새 분석까지다. C는 시험용 handler(`-tags codeye_fixture`) sleep 작업으로 drain 시간 초과와 저장 상한을 본다. 실패해도 띄운 설치는 `finally`에서 멈춘다. 이 저장소 CI의 Linux 작업은 `--nodes --web --scheduler --personal`로 실행한다. `--smokes-only`는 Go 시험 묶음 없이 smoke만 다시 돌리며, 이때 마지막 줄은 저장 통합 시험을 실행했다고 쓰지 않는다.

CI에서 건너뛰는 시험: Windows 작업에는 PostgreSQL이 없다. 그래서 `TestTC32BackupRestoreRoundTrip`, `TestW25*`, `TestDatabaseIntegration*`는 건너뛴다. 프로세스 관리 시험(`TestRecordsAndIdentityArePrivate`, `TestInspectFindsStaleAndUnrecordedProcesses`), `TestRunRedactsPassword`, `TestPersonal*`도 Windows에서 건너뛴다. `internal/agent/w25_test.go`는 Linux 빌드 태그다. Linux 작업에서 TC-32는 `pg_dump`가 PATH에 있어야 실행된다(CI는 `pg_config --bindir`를 PATH에 넣는다).

## 8. 수동 walkthrough

2026년 11월 19일, Linux(WSL2), PostgreSQL 17.10(운영자 준비 클러스터, loopback), Go 1.27.4. 바이너리는 이 브랜치의 최종 코드에서 빌드했다. 웹 자산은 `npm run build`로 만든 `web/dist`를 썼다. 화면은 Playwright로 띄운 Chromium에서 사람이 하듯 조작했다. 아래 경로는 임시 디렉터리이며 DB URL과 로그인 토큰은 적지 않는다. 설정은 두 개를 썼다. `w1.json`(state `s1`, `127.0.0.1:47341`)과 `w2.json`(state `s2`, `127.0.0.1:47342`)이다. 둘 다 같은 source root `work`, `agent.go`, 상한(workspace 2 GB, logs 500 MB, artifacts 1 GB), `backup_dir`를 쓴다. DB는 빈 `walk1`·`walk2`다.

| 단계 | 명령·조작 | 결과 |
|---|---|---|
| 1 시작 | `codeye status` → `codeye start` → `codeye migrate` → `codeye start` → `codeye status` | 처음 status는 `stopped`. migration 전 start는 `database schema is outdated (applied 0, this binary expects 9)…`로 종료 코드 1. migration 뒤 start는 0.5초에 개인 사용자 생성, source root 등록, 로그인 링크(0600) 기록. status는 `running`, `ready=true`, `heartbeat_fresh=true` |
| 2 프로젝트 | 로그인 링크를 브라우저로 열고 프로젝트 등록(로컬 폴더 `work` / `hello`) | 로그인됨, "프로젝트를 등록했습니다" |
| 3 Go 분석 | "분석 요청 등록" | 약 7초 뒤 `partial`, 보고서 리비전 1. 도구 진단 1, 발견 1(`go vet printf: fmt.Printf format %s reads arg #2, but call has 1 arg`, `main.go:10`). 정적 분석은 경로를 지정하지 않은 도구 4개가 미지원, 모델 검토는 정책 제외로 표시. 발견에 사람 판정 "확인함"과 사유를 기록 |
| 4 정지 | `codeye stop` | 진행 중 0, `drain_requested` → `exited`, controller `terminated`, `outcome: stopped`, 0.5초. 기록 파일 삭제, status `stopped` |
| 5 백업 | `codeye backup --config w1.json` | 0.11초. `codeye-20261118T191428Z`: migration 9, PostgreSQL 17.10, dump 175,706바이트, 산출물 내용 14개(19,418바이트), 행 projects 1·runs 1·reports 1·finding_reviews 1·artifacts 19. 파일 0600, 세트 0700, manifest에 연결 정보 없음 |
| 6 복원 | 비어 있지 않은 `walk1`로 `restore --config w2.json` → 빈 `walk2`로 다시 → 같은 대상에 한 번 더 | 첫 번째는 `the target database is not empty…`, 종료 코드 1, `s2/artifacts` 비어 있음. 두 번째는 0.19초에 dump와 내용 14개 hash 확인, 노드 1 철회, 세션 1 철회, leased attempt 0. 세 번째는 `already has personal.json`으로 거부 |
| 7 복원본 시작 | `codeye start --config w2.json` | 새 노드로 등록(이전 노드 `revoked`), `running` |
| 8 보고서·근거 | 새 로그인 링크로 브라우저에서 프로젝트 → 마지막 실행 → 보고서 열기 → 발견·원시 진단 | 같은 run·리비전 1의 보고서, 같은 발견과 근거(진단 위치 `main.go:10–10`, go vet stdout 산출물), 사람 판정 "확인함"과 사유·기록자·시각 유지. API로 보고서가 가리키는 산출물 19개(내용 14개)가 새 산출물 디렉터리에서 sha256·크기 일치, Markdown·HTML 내보내기에 보고서 sha256 포함. 복원본에서 새 분석을 요청해 새 노드가 실행·게시(`partial`, 리비전 1) |
| 정리 | `codeye doctor --config w2.json`, `codeye stop --config w2.json --format json` | operations: 영역 5개 측정, 백업 `recent`(104초), processes `consistent`. stop `stopped`, `agent_cleanup: confirmed` |

같은 흐름의 cancel 경로도 직접 확인했다(시험용 handler sleep 작업, drain 2초). `draining … (1 attempt(s) in flight)` → `drain timeout: cancelling running work` → 에이전트 `cancelled_after_drain_timeout`, attempt `released`·cleanup `confirmed`, run `failed`(`agent_shutdown`), `outcome: stopped`, 2.5초. 상태 디렉터리를 cwd로 둔 프로세스는 남지 않았다.

## 9. 제한과 하지 않은 것

- 관리형 PostgreSQL(설치·초기화·시작을 CODEYE가 하는 모드)은 구현하지 않았다. 설정에서 `managed`를 고르면 거부한다.
- Windows·macOS의 start/stop, systemd·Windows Service 등록, 로그아웃 뒤 유지는 없다. 개인 모드 프로세스는 setsid로 터미널과 분리될 뿐이다.
- RHEL 8에서는 실행하지 않았다. pidfd가 없는 커널이면 stop이 신호를 보내지 않는다(§1).
- 복원 시점에 실행 중이던 attempt는 `recovery_required`로 남고 예약을 유지한다. 운영자가 해제하는 명령이 없다. 전역 상한이 막히면 `codeye scheduler set --global-max-running N`으로 늘린다.
- 저장 상한은 새 작업 거절뿐이다. 보존 기간·참조 기반 정리, 정리 후보 목록, 참조 없는 임시 업로드 정리는 없다. 노드 영역 측정은 30초 주기라 그 사이 사용량은 반영되지 않는다.
- 백업은 전체 세트만 만든다. 증분, 압축, 암호화, 보존 개수, 자동 예약 백업은 없다. 백업 디렉터리 보호(권한·위치·외부 보관)는 운영자 책임이다. dump에는 프로젝트 이름·경로·보고서 같은 운영 데이터가 들어 있다. 인증정보 값은 없고, 세션·토큰은 hash로만 저장된다.
- 업그레이드는 새 바이너리로 `codeye stop` → `codeye backup` → 바이너리 교체 → `codeye migrate` → `codeye start` 순서다. 이 순서 전체를 이전 버전에서 시험하지는 않았다(W25 이전 버전에는 start가 없다).
- 웹 화면은 doctor operations와 저장 상한 상태를 아직 보여 주지 않는다. 대기 사유 문구만 추가했다.
- 모델 설정은 설정 검증과 argv 연결까지만 시험했다. W25에서는 모델을 호출하지 않았다.

## 10. 검증 결과

최종 커밋에서 실행한 명령과 결과는 아래에 적는다.

(최종 검증 뒤 기록)
