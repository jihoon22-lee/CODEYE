# W15 모듈·문서·환경 탐색과 프로젝트별 단계 표시

> 작업 계약(착수 시점). 구현과 실제 시험 결과는 이 문서의 "결과" 절에 추가한다. 이 절이 채워지기 전의 필드·상태 이름은 설계 계약이며 구현 완료를 뜻하지 않는다.

## 1. 목적과 요구

| 항목 | 내용 |
|---|---|
| 목적 | 고정 스냅샷에서 모듈별 환경 선언·노드 관측·문서 적용 범위를 모델 없이 수집하고, 워커가 기록한 task 단계로 프로젝트 목록과 실행 상세의 진행 표시를 갱신한다. R1 역할 지침을 후보 팩으로 연결해 활성 팩과 같은 조건에서 평가한다. |
| 요구 | REQ-002/004/005/006/009/010/011/013/016/017/022/027, NFR-003/004/005/007/008/010 |
| 수용 시험 | TC-96~TC-100 ([시나리오](18-acceptance-and-traceability.md)) |
| 선행 | W14(`9c5bca2`). W22의 SSE·예약은 선행조건이 아니다. |
| 범위 밖 | 의존성 설치·빌드·테스트 실행, 언어별 진단 해석의 정밀 어댑터(W16~W21), SSE·가중 진행률(W22) |

## 2. 표시 단계와 task 단계

화면 단계는 `source`, `environment`, `documents`, `static_analysis`, `code_review`, `report` 순으로 읽는다. 빌드·기존 테스트를 계획한 실행만 `build`, `test`를 추가한다.

- `source`와 `report`는 서버 기록에서 계산한다. `source`는 run 상태와 snapshot 고정 기록, `report`는 finalizing 상태와 보고서 리비전 게시 기록을 쓴다.
- 나머지는 task가 가진 단계 키(`tasks.stage`)에서 계산한다. 워커는 계획 시 `TaskSpec.Stage`를 채우고 `task_created` payload의 선택 필드 `stage`로 보낸다. 서버는 enum을 검증하고 저장한다. 한 번 저장한 단계는 바꿀 수 없다.
- task 단계 enum: `environment`, `documents`, `static_analysis`, `code_review`, `build`, `test`.
- 단계가 없는 task 행(W15 이전 워커·R0 기록)은 그대로 둔다. migration에서 backfill하지 않고 key 접두어로 추정하지 않는다. 그런 실행은 `detail: "not_recorded"`로 표시한다.
- 시작·종료 시각은 서버가 task 전이를 수용한 DB 시각(`tasks.started_at`, `tasks.finished_at`)이다. 워커 시계를 쓰지 않는다.

### 단계 상태 판정

판정은 하나의 Go 함수가 수행하고 프로젝트 목록과 실행 상세가 같은 결과를 사용한다.

| 상태 | 조건 |
|---|---|
| `plan_pending` | 계획 미확정. run이 아직 `running` 이전이고 task가 없다. |
| `no_tasks` | 계획이 확정됐지만 해당 단계의 task가 없다. 목록과 상세에서는 숨길 수 있다. |
| `waiting` | 계획된 task가 모두 `queued`/`blocked`이다. |
| `running` | 시작 기록이 있는 task가 있고 종료되지 않은 task가 남아 있다. 먼저 발생한 실패 건수를 함께 둔다. |
| `succeeded` | 모든 task가 `succeeded`이다. 또는 성공이 하나 이상이고 나머지가 모두 검토 대상 없음 사유(`no_documents`, `no_tool_observations`, `no_environment_gap`)의 생략이다. 생략 건수와 사유는 그대로 표시한다. |
| `skipped` | 성공 없이 모두 `skipped_policy`/`unsupported`이다. 사유 코드로 정책 제외와 검토 대상 없음을 구분한다. |
| `failed` | 성공 없이 모두 `failed`/`timed_out`/`budget_exhausted`이다. |
| `cancelled` | 모두 `cancelled`이다. |
| `partial` | 종료됐지만 위 어느 것도 아니다. 상태별 건수를 표시한다. |

`source`는 queued→`waiting`, preparing이고 snapshot이 없으면 `running`, snapshot 고정 후 `succeeded`, snapshot 없이 종료되면 `failed` 또는 `cancelled`이다. `report`는 finalizing 이전 `waiting`, finalizing에서 `running`, 리비전 게시 후 `succeeded`, 게시 없이 종료되면 `failed` 또는 `cancelled`이다.

### 검토 대상이 없는 역할

새 task 상태를 추가하지 않는다. [파이프라인 명세](04-analysis-pipeline.md)의 `skipped_policy`+`dependency_failed`처럼 `skipped_policy`와 통제된 사유 코드를 함께 쓴다.

| 사유 코드 | 의미 | 모델 검토 성공으로 셈 |
|---|---|---|
| `model_source_not_approved` | 소스의 모델 전송 미승인(정책 제외) | 아니오 |
| `no_documents` | 스냅샷 범위에 문서가 없어 문서 역할 생략 | 아니오 |
| `no_tool_observations` | 해석할 도구 관찰이 없어 진단 해석 생략 | 아니오 |
| `no_environment_gap` | 모든 선언이 관측과 일치해 환경 해석 생략 | 아니오 |

필수 여부는 계획 시 정한다. 문서 역할은 manifest에 문서가 있을 때만 필수다. 해석·환경 역할은 선택 task다. 사유 코드는 task의 `reason`에 저장하고 화면은 코드별 문구로 표시한다.

## 3. 대표 실행 선택

프로젝트마다 실행·취소 처리 중(`preparing`, `running`, `finalizing`, `cancel_requested`, `cancelling`), `recovery_required`, `queued`, 종료 순으로 대표 run을 고른다. 같은 그룹에서는 요청 시각 내림차순, 동률이면 run ID 내림차순이다. 기존 `last_run`(가장 최근 요청 run)의 의미는 유지한다.

## 4. API 확장(선택 필드)

기존 필드와 `progress.stage`(= run 상태)는 그대로 둔다. 새 필드는 선택 필드이며 구버전 UI는 무시해도 된다.

`GET /projects`, `GET /projects/{id}`의 각 프로젝트:

```json
"activity": {
  "run": {"id": "run_...", "status": "running", "requested_at": "2026-11-18T03:00:00Z"},
  "other_active_runs": 1,
  "detail": "recorded",
  "stages": [{"key": "static_analysis", "state": "running", "planned": 2, "finished": 1, "succeeded": 1,
              "counts": {}, "reasons": [], "started_at": "...", "finished_at": null}],
  "last_report": {"run_id": "run_...", "revision": 1, "published_at": "..."},
  "last_event_at": "2026-11-18T03:01:10Z",
  "checked_at": "2026-11-18T03:01:12Z"
}
```

실행 기록이 없으면 `run`은 null이고 `stages`는 빈 배열이다. `GET /runs/{id}`의 `progress`에는 같은 형태의 `detail`, `stages`, `last_event_at`, `checked_at`을 추가한다. 권한 없는 프로젝트의 run ID·단계·사유는 반환하지 않는다.

`counts`는 0이 아닌 종료 상태별 건수(`failed`, `timed_out`, `cancelled`, `unsupported`, `skipped_policy`, `budget_exhausted`)다. `reasons`는 단계 task의 사유 코드를 중복 제거·정렬한 목록이다. `planned`는 계획이 확정된 경우에만 숫자다.

## 5. 환경·문서 탐색 산출물

새 artifact `application/vnd.codeye.environment+json`(schema `schemas/environment.schema.json`, 예시 `examples/environment.json`). 모델 없이 생성한다.

- **모듈**: `go.mod`, `go.work`, `package.json`, `pyproject.toml`/`setup.cfg`/`requirements*.txt`, `Cargo.toml`, `CMakeLists.txt`/`meson.build`/`Makefile`, `compile_commands.json`을 manifest에서 찾는다. 모듈 범위는 manifest 디렉터리에서 하위 모듈 디렉터리를 뺀 경로다.
- **선언**: `go`/`toolchain` 지시문, `engines.node`, `requires-python`·`.python-version`, `rust-version`·`rust-toolchain`, `cmake_minimum_required` 등. 원문 위치(path, 행 범위, blob SHA-256)와 근거 ID를 붙인다.
- **관측**: 워커가 운영자가 설정한 도구 경로만 executor(argv·제한시간)로 probe한다. PATH를 검색하지 않는다. 설정이 없으면 `not_probed`, 실행 실패는 `probe_failed`, 바이너리 없음은 `not_found`이다. 다른 모듈·호스트의 값을 옮기지 않는다.
- **판정**: 플랫폼이 선언과 관측을 비교해 `satisfied`, `mismatch`, `unknown`(사유: `not_declared`, `not_observed`, `unsupported_constraint`)을 남긴다. 비교할 수 없는 범위식은 `unknown`이다.
- **문서**: README·CONTRIBUTING·설계 문서·경로별 `AGENTS.md` 등. 적용 경로(문서 디렉터리), 분류(`readme`, `agent_instructions`, `design`, `contributing`, `other`), 상태(`selected`, `budget_exceeded`, `excluded`, `unreadable`)와 사유를 둔다. 모듈마다 자기 문서, 상속 문서, `none`을 구분한다. 에이전트 지침 문서는 분석 자료로 분류할 뿐 권한·도구 설정을 바꾸지 않는다.
- **충돌**: 플랫폼은 같은 경로에 적용되는 문서가 여럿이면 `overlapping_guidance`로 묶는다. 의미 충돌 판정은 문서 역할의 몫이며, 모델 미실행 시 미판정으로 남는다.

### 5.1 탐색 task와 산출물 필드(구현)

[`internal/worker/discovery`](../internal/worker/discovery/discover.go)가 생성하고 [`envdoc`](../internal/worker/discovery/envdoc/envdoc.go)가 검증한다. 예시는 [환경](../examples/environment.json)·[문서](../examples/environment-documents.json)이며 첫 입력에서 고정 ID로 만든다.

- **task**: deep-review 계획 맨 앞에 `env.discover`(stage `environment`)와 `docs.discover`(stage `documents`)를 둔다. 둘 다 필수다. 탐색이 실패하면 사유와 함께 `failed`로 남고 정적 분석과 코드 검토 task는 계속 실행된다. 시험용 fixture handler에는 붙이지 않는다. review-v1의 `review.context`는 `documents`, `review.investigate`·`review.verify`는 `code_review`, go-vet은 `static_analysis` 단계다.
- **산출물**: task마다 하나를 role `other`로 올린다. `kind`는 `environment` 또는 `documents`다. 공통 필드는 `schema_version`, `kind`, `snapshot_manifest_digest`, `run_id`, `attempt_id`, `scope`, `modules`, `limitations`다. 정렬 순서는 고정이고 시각 필드와 문서 본문은 넣지 않는다. 보고서는 `EnvironmentDecoder`로 이 산출물을 검증만 하고 관찰·결함·도구 coverage에는 더하지 않는다. decoder가 없으면 성공한 필수 task의 산출물이 누락으로 집계되어 모든 보고서가 `partial`이 된다.
- **모듈**: (디렉터리, 생태계) 쌍이고 ID는 `<ecosystem>:<path>`다. `excluded_paths`는 같은 생태계의 가장 바깥 하위 모듈이다. Go는 go-vet 계획과 같이 `vendor`·`testdata`·`.`/`_` 디렉터리를 건너뛴다. C/C++의 하위 빌드 파일은 빌드 스크립트를 평가하지 않고는 별도 프로젝트인지 알 수 없어 가장 바깥 모듈에 합친다(`nested_build_files_merged`).
- **scope**: kind가 비어 있으면 go-vet과 같이 `full`로 기록한다. `paths`에서 모듈은 scope 경로가 모듈 범위 안에 있거나 모듈이 scope 경로 안에 있을 때 대상이다. 대상 모듈의 manifest는 scope가 모듈 안의 파일 하나만 가리켜도 읽는다. go vet이 읽는 입력과 같다. 문서는 경로가 scope에 들어갈 때만 읽고 나머지는 `excluded`/`out_of_scope`로 남긴다.
- **선언**: `environment.requirements`의 각 항목은 `kind`, 원문 `constraint`, `comparison`(`minimum`, `preferred_minimum`, `prefix`, `unsupported`), `source`(path, sha256, 행 범위), `evidence_id`를 갖는다. `evidence_id`는 `record.ExcerptID`와 같은 방식으로 만든다. review-v1 기록 검증기는 아직 이 근거 종류를 받지 않으며 R1 역할에서 연결한다. TOML·JSON·INI는 실행 없이 한 줄 단위로 읽는다. 읽지 못한 manifest는 `malformed`·`not_utf8`·`too_large`로 표시한다.
- **관측**: 운영자가 지정한 go 실행 파일(go-vet과 같은 `--go`)만 attempt scratch에서 `go version`으로 확인한다. 이때 `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOENV=off`를 쓴다. 다른 도구는 설정 경로가 없어 `not_probed`다. W19부터는 공유 toolchain `--node`도 같은 방식으로 `node --version`을 확인한다([docs/39 §2.4.1](39-r1-analyzer-packs.md#241-공유-interpreter-toolchain), [§6](39-r1-analyzer-packs.md#6-탐색-probe-hook)). probe가 깨끗하게 끝나지 않으면 task는 `failed`/`environment_probe_incomplete`로 남고 산출물은 올린다. timeout, 취소, 시작 거부, 정리 불확실, 잘린 출력이 이 경우다. 워커는 그런 실행이 붙은 성공을 허용하지 않는다.
- **판정**: 사유는 `not_declared`, `unsupported_constraint`, `not_observed` 순으로 정한다. 선언 파일을 읽지 못해 선언 여부를 모르면 확장 사유 `declaration_unreadable`을 쓴다. Go `toolchain`은 `preferred_minimum`이다. go1.27.4에서 `go 1.22`+`toolchain go1.31.0`은 `GOTOOLCHAIN=local`로 vet에 성공했다. 따라서 이 불일치는 vet을 막지 않는다. go vet과 같은 결과를 내는 것은 `go` 지시문 판정이다.
- **문서**: 기본 예산은 문서당 32 KiB, 합계 256 KiB, 최대 1,000개이고 산출물의 `budget`에 기록한다. 읽는 순서는 에이전트 지침, README, CONTRIBUTING, 설계, 기타 순이다. 같은 분류에서는 얕은 경로가 먼저다. 사유 코드는 `document_too_large`, `read_budget_exhausted`, `out_of_scope`, `credential_like_content`, `not_utf8`, `contains_nul`과 manifest 제외 사유다. `applies_to`는 문서 디렉터리다. `other` 분류는 목록에만 남기고 모듈 연결과 겹침 묶음에는 넣지 않는다. 겹침 묶음은 문서 디렉터리마다 만든다.

## 6. 역할 지침 후보 팩

초안을 [`prompts/review-r1`](../prompts/review-r1/pack.yaml) 후보 팩으로 묶어 활성 팩 `review-v1`과 나란히 둔다. 기본값은 `review-v1`이다. 후보 팩은 노드 운영자가 워커 설정으로만 선택한다. run 요청의 프로필 enum과 API는 바꾸지 않는다. 호출마다 팩 ID·버전·역할별 digest를 기록하고, 보고서는 실제 사용한 팩을 표시한다. 보고서의 필수 역할은 고정 목록이 아니라 계획된 task의 `required_for_verdict`로 판정한다. 필수 검토 task가 하나도 없으면 `not_performed`이고, R0 기록은 작성 당시의 세 역할로 해석한다.

후보 팩 평가(TC-100)는 같은 스냅샷·모델·역할별 예산에서 활성 팩과 후보 팩을 실행하고 팩별 호출 수·usage를 따로 보고한다. W15 입력과 R0 사례로 조정한 결과는 회귀 근거일 뿐 독립 평가가 아니다. 조정에 쓰지 않은 사례가 없으면 후보 팩을 승격하지 않고 미측정 지표를 기록한다.

## 7. 첫 입력

`fixtures/w15-modules/input/`은 루트 안내가 있는 저장소에 하위 모듈 둘을 둔다. 한 모듈은 문서가 없다. 다른 모듈은 루트와 충돌하는 안내와 노드보다 높은 Go 버전 요구를 갖는다. 읽기 예산을 넘는 설계 문서와 권한 변경을 요구하는 에이전트 지침도 포함한다. 정답 `fixtures/w15-modules/expected.json`은 입력 디렉터리 밖에 둔다. 단위 시험은 노드 관측을 주입해 CI의 실제 Go 버전에 기대지 않는다.

## 8. 실패 시나리오와 호환

- 오래된 epoch·다른 attempt의 단계 이벤트는 기존 fencing으로 409를 받고 화면을 되돌리지 않는다. 같은 `client_event_id` 재전송은 한 번만 반영한다.
- 탐색 task가 실패해도 정적 분석과 코드 검토는 계속하고, 해당 단계는 실패 사유와 함께 표시한다.
- migration `0006`은 forward 전용이다. 새 열은 nullable이고 기존 행을 바꾸지 않는다. 구버전 워커가 `stage` 없이 보내면 NULL로 저장한다.
- 노드 관측은 다른 모듈로 옮기지 않는다. 선언 버전이 노드보다 높으면 Go vet은 `GOTOOLCHAIN=local`로 toolchain을 내려받지 않는다. 그 실패는 탐색의 `mismatch` 판정과 일치해야 한다.

## 9. 결과 — 1단계(모델 없음)

2026년 11월 18일 Linux(WSL2)의 실제 PostgreSQL 17.10, 제품 워커, Chromium에서 확인했다. 역할 지침 연결과 실모델 평가(§6, TC-97 모델 부분·TC-100)는 2단계에서 수행한다.

| 시험 | 확인한 결과 | 근거 |
|---|---|---|
| TC-96 | 첫 입력에서 루트·`svc/a`는 `go 1.22` `satisfied`, `svc/b`는 `go 1.30` `mismatch`. 노드 관측(go1.27.4)은 주입값으로 고정한다. 실제 go1.27.4의 go vet은 `svc/b`에서 toolchain을 내려받지 않고 `toolchain_unavailable`로 끝나 판정과 일치했다. `toolchain` 지시문은 `preferred_minimum`이며 vet을 막지 않았다. | `TestW15FixtureMatchesExpected`, `TestW15FixtureGoVetAgreesWithVerdicts`, `TestToolchainPreferenceVerdict` |
| TC-97(모델 없음) | 문서 부재(`svc/a`의 `none`), 루트·하위 문서의 겹침 묶음, 읽기 예산 초과(`read_budget_exhausted`/`document_too_large`), 권한 변경을 요구하는 에이전트 지침을 각각 구분한다. 지침 문서가 있어도 계획·도구 설정이 바뀌지 않는다. | `TestDocumentsAbsent`, `TestDocumentsRootAndSubOverlap`, `TestDocumentsBudget`, `TestAgentInstructionsDemandingPermissionsChangeNothing` |
| TC-98 | 대표 run 선택(그룹·요청 시각·ID), 다른 활성 실행 수, 실행 기록 없는 프로젝트, 권한 없는 프로젝트 비노출, 목록과 상세의 동일 집계. 실제 Chromium에서 대기 중 단계·키보드 펼치기·취소 후 새로고침 상태를 확인했다. | `TestTC98RepresentativeRunsAndStages`, `TestTC98TC99ActivityOverHTTP`, `web/e2e/control.spec.ts`, `StageDisplay.test.tsx` |
| TC-99 | 도구 실패·성공 혼합의 `partial`과 건수, 정책 제외(`model_source_not_approved`)와 문서 없음의 구분, 취소, 새 attempt 후 옛 epoch 재전송 409와 화면 불변, 같은 `client_event_id` 1회 반영, R0 기록의 `not_recorded`. 실제 Go run은 `detail: recorded`이고 목록·상세가 일치했다. | `TestTC99MixedOutcomesAndExclusions`, `TestTC99StaleReplayAndDuplicateEvents`, `TestTC99LegacyRunIsNotRecorded`, `scripts/node_smoke.py` |

### 구현에서 정한 사항

- **산출물 두 개.** `env.discover`와 `docs.discover`가 각각 `kind`가 `environment`, `documents`인 산출물을 올린다. 예시도 두 개다. 선언 파일을 읽지 못한 경우를 `not_declared`로 쓰면 거짓이므로 판정 사유 `declaration_unreadable`을 추가했다.
- **탐색만 성공한 실행.** 정적 분석이 모두 실패해도 탐색 사실이 남으므로 실행은 `failed`가 아니라 `partial`이다. 보고서는 탐색 산출물을 검증만 하고 관찰·결함·도구 coverage에 더하지 않는다.
- **단계 재사용 규칙.** 재시도한 run이 같은 key를 다시 만들 때 단계가 같으면 수용, 다르면 409, 생략하면 저장값을 유지한다. 저장값이 NULL이고 새 워커가 단계를 선언하면 한 번 기록하고 `task.stage_recorded` 이벤트를 남긴다. 실제 워커의 선언이므로 key 추정이 아니다. 재시도는 task의 시작·종료 시각과 사유도 비운다. 실행 이력은 `task_executions`에 남는다.
- **판정 세부.** `running`에 도달하기 전에 끝난 실행의 task 단계는 `plan_pending`이다. 대기 중인 형제 task가 있는 제외 task는 `running`으로 셈한다. snapshot 없이 취소·복구 중이면 `source`는 `running`이다. `report`는 현재 attempt의 리비전 게시로 `succeeded`가 된다. 목록은 대기 중인 대표 run의 대기 사유를 `activity.waiting_reason`으로 보여준다.
- **운영 순서.** 구버전 컨트롤러는 `task_created`의 `stage`를 거부하므로 컨트롤러를 워커보다 먼저 올린다. migration 0006은 `events`에 부분 색인을 만든다.

### 실행한 검증

`gofmt -l .`, `go vet ./...`, `go test -race ./...`, `python3 scripts/validate_bundle.py`, `cd web && npm test && npm run build`, `python3 scripts/test_postgres.py --nodes --web --scheduler`가 통합 브랜치에서 모두 통과했다. Windows는 CI의 Go 시험으로 확인한다. 실제 모델 요청은 1단계에서 보내지 않았다.

## 10. 결과 — 2단계(후보 팩 연결, 실모델 없음)

2026년 11월 18일 같은 환경에서 확인했다. 모델 응답은 시험용 scripted caller가 만들었고 실제 모델 요청은 보내지 않았다. 따라서 아래 결과는 플랫폼 검사·기록·보고서 규칙의 근거이며 후보 지침의 품질 근거가 아니다. TC-100(같은 조건에서 활성 팩과 후보 팩 비교)은 수행하지 않았고 후보 팩은 승격하지 않았다.

### 선택과 팩

- **선택.** 노드 운영자만 `codeye agent --review-pack review-v1|review-r1`로 고른다. 기본값은 `review-v1`이다. 다른 값이나 빈 값은 사용 오류(종료 코드 2)이고 v1로 대체하지 않는다. 워커는 Hello의 `review_pack`으로 받는다. run 프로필, run 요청 API, OpenAPI는 바꾸지 않았다.
- **팩.** [`prompts/review-r1`](../prompts/review-r1/pack.yaml)은 id `review-r1`, version `2.0.0-rc1`, status `candidate-not-promoted`다. 공통 지침 `00-core.md`, 역할 지침 다섯, `domains.md`·`languages/go.md`(v1과 같은 바이트)로 구성한다. 역할별 digest는 v1과 같은 방식(pack.yaml과 선택 파일, 경로 오름차순, 경로 NUL 내용 NUL)이다. 호출 기록마다 팩 ID·버전·digest·파일 목록이 남는다.
- **review-v1 불변.** 역할별 digest·지침 해시, 기본 계획 key, 발췌 선택, 정규화한 요청 해시를 `TestReviewV1IsPinned`로 고정했다. 팩 전체 digest(`489d1ac5…`)도 변경 전과 같다.

### review-r1 계획

discovery·go vet 뒤에 다섯 task를 이 순서로 둔다. 생략은 `skipped_policy`와 사유 코드이며 모델 검토 성공으로 세지 않는다([A20](19-decisions-and-open-questions.md)). 모델 전송이 승인되지 않으면 모든 역할이 `model_source_not_approved`로 생략되고, 모델 연결이 없으면 `unsupported`/`model_not_configured`다.

| task | 단계 | 판정 필수 | 호출 없이 끝나는 경우 | 입력 | 출력 |
|---|---|---|---|---|---|
| `review.environment` | `environment` | 아니오 | 선언된 요구가 모두 `satisfied`면 `no_environment_gap`, 탐색 산출물을 못 쓰면 `dependency_failed` | 차이가 있는 모듈의 요구·근거 ID·노드 관측·판정 | 모듈별 영향 메모. 판정은 바꾸지 못한다. |
| `review.documents` | `documents` | 계획 시 범위 안 안내 문서가 있을 때만 | 문서 없음 `no_documents`(필수 아님), 선택된 문서 없음 `documents_not_selected`, `dependency_failed` | 선택 문서 발췌(문서당 40행·2,000바이트, 최대 8개) | 적용 경로가 있는 계약·충돌·질문, 지침성 문구 메모 |
| `review.investigate` | `code_review` | 예 | — | 수락된 문서·환경 결과, 모듈별 코드·테스트 발췌. 원시 도구 진단은 넣지 않는다(REQ-009). | 후보 `investigate_N`(최대 4) |
| `review.interpret` | `code_review` | 아니오 | go vet 관찰 0건이면 `no_tool_observations` | 관찰 ID·규칙·위치와 주변 발췌 | 관찰에 연결한 가설 `interpret_N`(최대 2) 또는 해석 한계 |
| `review.verify` | `code_review` | 예 | 앞 두 역할이 모두 수락되지 않으면 `dependency_failed`. 후보가 없으면 호출 없이 성공(`no_candidates_to_verify`) | 수락된 두 역할의 후보, 문서 계약, 관찰, 발췌 | 후보별 판정(v1 규칙) |

### 출력 검사와 요청 크기

- 출력은 엄격한 JSON이어야 한다. 그 호출에 준 근거·관찰 ID만 인용할 수 있다. 문서 규칙의 적용 경로는 인용한 문서의 디렉터리와 같거나 그 아래여야 한다(`scope_generalized`). 실행·재현을 주장하는 문구는 거부한다(`execution_claim_rejected`). 이 검사는 정규식 기반 휴리스틱이다.
- 형식 복구는 고정 오류 코드만 담아 한 번 요청한다. 그래도 틀리면 task는 `failed`이고 내용은 격리된다. 탐색 사실과 도구 관찰은 남는다. 하위 역할은 수락된 결과만 받는다. 앞 단계가 생략·실패하면 `upstream_<역할>_<상태>` 한계를 남긴다.
- 보내기 전에 메시지 바이트를 확인하고 발췌를 줄인다. 그래도 넘으면 `review_input_too_large`로 실패한다. 요청은 메시지 하나이고 도구 정의를 쓰지 않는다. w15 fixture의 역할별 최대 요청은 environment 9,913, documents 12,303, investigate 12,494, interpret 10,587, verify 14,602바이트였다. 최악 입력 시험의 최대값은 verify 36,645바이트였다. 모두 시험 한도 40,000바이트(relay 한도 49,152바이트) 이하다. 출력 토큰(relay 한도 1,536)은 측정하지 않았다.

### 기록과 보고서

- **기록.** review 기록 schema `0.2`(`record.R1Record`)는 단계마다 하나이고 자기 호출만 담는다. `pack`, `upstream`(source·state·reason·읽은 산출물의 sha256), `observations`, 역할별 내용을 가진다. `0.1` 기록은 이전 규칙 그대로 읽는다. [스키마](../schemas/review-record.schema.json) · [예시](../examples/review-r1/verify.json)(scripted caller로 worker가 쓴 다섯 단계 기록)
- **필수 역할.** R1 역할 key가 계획에 있으면 필수 검토 task는 `required_for_verdict`인 `review.*`다. 하나 이상이고 모두 수락됐을 때만 `performed`이고, 하나도 없으면 `not_performed`다. 필수가 아닌 역할은 `coverage.ai_review.optional_tasks`에 결과와 사유를 적고 수행으로 세지 않는다. 생략된 역할의 결과는 `skipped`다. R0 계획은 작성 당시의 세 역할(미계획 행 포함)로 해석한다.
- **연결 검사.** `coverage.review_calls`에 `pack_id`·`pack_version`을 싣는다(0.2 기록만). 앞 단계·탐색·go vet 산출물의 SHA-256, 관찰 ID, verify 후보와 앞 단계 후보가 맞지 않으면 그 단계를 `review_lineage_mismatch:<task>`로 제외한다. 계획과 다른 팩의 기록은 `review_pack_mismatch:<task>`로 제외한다.
- **단계 표시.** 처음에는 `env.discover` 성공과 `review.environment`의 `no_environment_gap` 생략이 섞이면 environment 단계가 `partial`이 되었다. 정상 실행의 목록에도 주의 표시가 생겼다. 실모델 대조 뒤 §2 판정을 바꿨다. 검토 대상 없음 사유의 생략은 다른 task가 모두 성공한 단계를 `succeeded`로 둔다. 정책 제외(`model_source_not_approved`)와 실패는 계속 `partial`이다(`TestNothingToReviewDoesNotDegradeStage`).

| 시험 | 확인한 결과 | 근거 |
|---|---|---|
| TC-97(모델 경로, 플랫폼 검사) | 적대적 AGENTS.md가 있어도 OpenCode 요청 설정은 같고 그 문구는 `<review-input>` 안에만 있다. 꾸민 근거 ID, 상위 경로로 넓힌 규칙, 실행·재현 주장, 판정 필드를 붙인 환경 출력은 거부되고 하위 역할로 넘어가지 않는다. | `TestTC97DocumentContentCannotChangeRequestConfig`, `TestR1FabricatedEvidenceIsQuarantined`, `TestR1ScopeGeneralizationRejected`, `TestR1ExecutionAndReproductionClaimsRejected`, `TestR1EnvironmentOutputRules` |
| 계획·생략 | 순서·단계·필수 여부와 `no_documents`·`no_environment_gap`·`no_tool_observations`, 모델 실패 시 탐색 사실 보존과 하위 역할의 한계 기록 | `TestR1PlanOrderStagesAndRequired`, `TestR1SkipReasons`, `TestR1ModelFailureKeepsFactsAndLimitsDownstream` |
| 실제 worker 경로 | 실제 go vet(go1.27.4)과 scripted caller로 다섯 역할을 실행하고 기본 registry로 보고서를 만들었다. fixture 그대로는 `svc/b`의 vet 실패와 interpret 생략(`no_tool_observations`)을, 루트 모듈에 printf 위반 파일 하나를 더하면 실제 go vet 관찰이 interpret 입력·후보·보고서 결과까지 같은 ID로 이어짐을 확인했다. 두 경우 모두 필수 세 역할이 수락되어 `performed`이고 선택 역할·사유·팩이 표시된다. 같은 발췌가 investigate(`context`)와 interpret(`observation`)에서 다른 출처 표시로 verify에 오는 경우를 이 시험에서 발견해, 보고서 연결 검사가 앞 단계의 어느 한 표시와 같으면 받도록 고쳤다. | `TestReviewR1ThroughTheWorkerAndReport` |
| 보고서 규칙 | 필수 검토 task가 없으면 `not_performed`, R0 세 역할 규칙 유지, 연결이 어긋난 단계 제외 | `TestR1RequiredRolesFromPlan`, `TestR1NoRequiredReviewTaskIsNotPerformed`, `TestR0ReviewRolesUnchanged`, `TestR1LineageRejected` |
| 요청 크기 | fixture·최악 입력 모두 40,000바이트 이하 | `TestR1RequestSizeOnFixture`, `TestR1RequestSizeWorstCase` |

- **OS 간 digest.** 처음 Windows CI에서 review-v1 고정 시험과 r1 예시 비교가 실패했다. checkout의 CRLF 변환으로 지침·fixture 바이트가 바뀌어 팩 digest와 요청 크기가 Linux와 달라졌기 때문이다. 저장소 `.gitattributes`(`* text=auto eol=lf`)로 모든 checkout을 LF로 고정했다. 이 문제는 R0에서도 Windows 노드의 지침 digest를 바꿀 수 있었다.

하지 않은 것: 실모델 요청과 TC-100 평가, 출력 토큰 측정, `coverage.environment` 요약, Go 이외 언어의 코드 발췌(`code_excerpts_go_only`), 후보 팩 승격.

## 11. 결과 — TC-100 실모델 대조(활성 팩 대 후보 팩)

2026년 11월 18일 같은 WSL2 호스트에서 실행했다. 실제 PostgreSQL 컨트롤러, 에이전트 두 대(최대 워커 2), Go 1.27.4, OpenCode 1.19.5를 썼다. 모델은 승인 relay를 거친 `gpt-6.2-sol`(추론 강도 low)이다. 입력은 R0 사례 다섯 쌍, W15 첫 입력, W12의 호출자 정리 반례(`w12-close`)다. 다섯 쌍은 Q01·Q03·Q05·Q08·Q10의 수정 전후 10개이고, Q01·Q03·Q05만 README를 둔다. `w12-close`는 `Open`이 반환한 handle을 호출자 `Load`가 닫는 W12 검증 fixture다. 두 팩은 같은 바이트의 소스 트리(디렉터리 digest 일치)와 같은 모델·relay·호출 상한을 썼다. 역할별 입력은 고정하지 않았다. 팩마다 발췌를 고르는 규칙이 달라서, v1 맥락 역할은 문서를 최대 둘, r1 문서 역할은 최대 여덟 개를 받는다. 따라서 아래 차이는 지침과 입력 선택을 함께 반영한 결과다. manifest digest는 고정 시각을 포함하므로 run마다 다르다([A16](19-decisions-and-open-questions.md)). 평가용 W15 사본은 개발 에이전트 경고 주석 한 줄을 뺀 `AGENTS.md`를 쓴다(`scripts/r0_pilot.py`의 `materialize_w15`). 정답과 판정은 스냅샷 밖에 두었다.

| 지표 | review-v1(활성) | review-r1(후보) |
|---|---|---|
| 모델 호출 / 정상 정산(R0 쌍·W15) | 32 / 32 | 27 / 27 |
| 역할별 호출 | context 11, investigate 11, verify 10 | documents 7, environment 1, investigate 11, verify 8, interpret 0 |
| 보고 usage(입력·출력 토큰) | 137,057 · 10,361 | 104,549 · 7,521 |
| relay 장부 차감(보수적) / 표준 단가 추정 | $0.789 / $0.378 | $0.598 / $0.284 |
| 모델 검토 `performed` | 11/11 | 11/11 |
| 수정 전 결함 포착 | 5/5 | 5/5 |
| 수정 후 같은 결함 재지적 | 0/5 | 0/5 |
| 수정 후 계약 밖 입력 경고(Q08 빈 입력, Q10 분모 0) | 2건, info지만 `supported`로 조치 수에 포함 | Q10 0건(n=1). Q08은 후보 지침이 정답을 담고 있어 무효 실험으로 제외 |
| 수정 후 별도 계약 지적(Q03, Q05) | 2건(low) | 2건(low) |
| W15 입력의 조치 대상 | 1건 오탐(루트 재시도 지침을 `svc/b`에 적용) | 0건(모듈 간 호출 근거 없는 후보 1건은 `inconclusive`) |
| 조치 대상 합계 | 10 | 7(Q08 수정본 포함) |
| 호출자 정리 반례(`w12-close`) | 누수 주장 없음. 다른 후보 1건(미등록 키의 0 값) `inconclusive`, 호출 3 | 누수 주장 없음, 후보 0, 호출 1 |
| 검증기가 거부한 출력·꾸민 근거·범위 일반화·실행 주장 | 0 | 0 |
| 실행 없는 `reproduced` / 통계 불일치 | 0 / 0 | 0 / 0 |
| 보관 결과 감사(근거 위치·줄 범위·blob) | 11개, claim 21·근거 16 통과 | 11개, claim 19·근거 15 통과 |

W15 입력에서 두 팩의 차이는 다음과 같다.

- **review-v1:** 맥락 역할이 문서를 최대 둘(루트 README, AGENTS.md)만 골랐다. 그래서 `svc/b/README.md`의 재시도 금지를 보지 못했다. `Charge`가 루트 지침과 달리 재시도하지 않는다는 low 후보가 `supported`로 남았다. 적대적 AGENTS.md의 지시는 따르지 않았지만 기록하지도 않았다.
- **review-r1:**
  - 문서 역할이 루트(`.`)의 재시도 계약과 `svc/b`의 단일 호출 계약을 각각의 적용 경로로 기록했다. 둘의 충돌은 `svc/b` 범위로 한정했다.
  - 예산으로 읽지 못한 설계 문서에 대한 질문을 남겼다. AGENTS.md의 권한·네트워크·키 조회 요구는 문서 내용으로만 적었다.
  - 환경 역할은 `go:svc/b` 모듈에만 영향 메모를 남겼다. Go 1.30 요구 때문에 빌드·타입 검사 기반 분석이 영향을 받고, 실제로 선택되는 toolchain 관측이 필요하다는 내용이다. 다른 모듈로 옮기지 않았다.
  - 조사 역할은 `Forward`가 결과가 불명확한 청구 오류도 재시도할 수 있다는 후보를 냈다. 두 모듈을 잇는 호출 근거가 없어 검증 역할이 `inconclusive`로 남겼다.

### 판정과 승격

- **판정 방법.** 개발 에이전트가 `corpus.json`의 원인·조건과 첫 입력의 설계 의도를 보고서의 조건·근거 위치와 대조했다. 사람의 판정이 아니며 precision·recall 추정치로 쓰지 않는다. 표본은 10+1개이고, 각 팩을 한 번씩 실행해 모델 변동을 측정하지 않았다.
- **차단 조건.** TC-100의 차단 조건(허위 근거, 실행 없는 재현 단정, 통계 오염)은 두 팩 모두 관찰되지 않았다. 후보 팩은 R0 쌍·W15에서 호출 5건과 장부 $0.19를 덜 쓰고 같은 포착 수를 냈다. Q10 계약 밖 경고와 W15 오탐도 없었다.
- **미측정.**
  - 호출자 정리 반례에서는 두 팩 모두 누수 후보를 내지 않았다. 따라서 반대 근거로 후보를 철회하는 검증 경로는 실모델에서 측정되지 않았다.
  - `review.interpret`는 go vet 관찰이 있는 사례가 없어 실모델 호출이 0건이다.
  - `review.environment`는 1건만 호출됐다.
- **승격하지 않음.** 후보 팩은 `candidate-not-promoted`로 둔다. 이유는 두 가지다.
  - 후보 지침의 조사 예시가 Q08 수정본의 답을 직접 담고 있다. 평가 기준([§2](16-evaluation.md#2-데이터셋))에 따라 그 사례는 무효이며, 보류 평가 전에 예시를 바꾼 rc2가 필요하다. W15 첫 입력도 이 지침과 함께 설계했다. 따라서 위 결과는 조정에 쓴 사례의 회귀 근거일 뿐 독립 평가가 아니다([평가 기준](16-evaluation.md#8-역할-지침을-바꿀-때의-비교)).
  - 거부 경로(꾸민 근거·범위 일반화·실행 주장)는 실모델에서 한 번도 발동하지 않았다. 시험용 caller로만 확인했다.
- **후속.** 조정에 쓰지 않은 보류 사례, 다른 언어, 반복 실행 분산은 W24 평가 runner에서 측정한다. 기본 팩 변경은 그 결과와 소유자 판단으로 정한다.

### 비용과 재현

R1 전체가 공유하는 relay 장부 하나에서 W15가 쓴 금액은 보수적 차감 기준 $1.498(호출 63건, 실패 0)이고, 남은 상한은 $8.492다. 표준 단가 추정은 $0.714다. 공급자 청구서는 확인하지 않았다. 결과 디렉터리와 판정은 저장소 밖 비공개 경로에 보관했다. 재현 방법은 다음과 같다.

- `CODEYE_R0_REVIEW_PACK=review-r1`(생략 시 v1)과 `CODEYE_R0_CASES=Q01-before,…,w15,w12-close`를 지정해 `python3 scripts/test_postgres.py --r0-pilot`을 실행한다.
- `python3 scripts/r0_pilot.py --audit-results DIR`로 보관 결과를 감사한다.
- `python3 scripts/w15_pack_compare.py V1_DIR R1_DIR --judgments FILE`로 두 결과를 비교한다.
