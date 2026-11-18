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
| `succeeded` | 모든 task가 `succeeded`이다. |
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
- **관측**: 운영자가 지정한 go 실행 파일(go-vet과 같은 `--go`)만 attempt scratch에서 `go version`으로 확인한다. 이때 `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOENV=off`를 쓴다. 다른 도구는 설정 경로가 없어 `not_probed`다. probe가 깨끗하게 끝나지 않으면 task는 `failed`/`environment_probe_incomplete`로 남고 산출물은 올린다. timeout, 취소, 시작 거부, 정리 불확실, 잘린 출력이 이 경우다. 워커는 그런 실행이 붙은 성공을 허용하지 않는다.
- **판정**: 사유는 `not_declared`, `unsupported_constraint`, `not_observed` 순으로 정한다. 선언 파일을 읽지 못해 선언 여부를 모르면 확장 사유 `declaration_unreadable`을 쓴다. Go `toolchain`은 `preferred_minimum`이다. go1.27.4에서 `go 1.22`+`toolchain go1.31.0`은 `GOTOOLCHAIN=local`로 vet에 성공했다. 따라서 이 불일치는 vet을 막지 않는다. go vet과 같은 결과를 내는 것은 `go` 지시문 판정이다.
- **문서**: 기본 예산은 문서당 32 KiB, 합계 256 KiB, 최대 1,000개이고 산출물의 `budget`에 기록한다. 읽는 순서는 에이전트 지침, README, CONTRIBUTING, 설계, 기타 순이다. 같은 분류에서는 얕은 경로가 먼저다. 사유 코드는 `document_too_large`, `read_budget_exhausted`, `out_of_scope`, `credential_like_content`, `not_utf8`, `contains_nul`과 manifest 제외 사유다. `applies_to`는 문서 디렉터리다. `other` 분류는 목록에만 남기고 모듈 연결과 겹침 묶음에는 넣지 않는다. 겹침 묶음은 문서 디렉터리마다 만든다.

## 6. 역할 지침 후보 팩

`prompts/review-r1-draft`를 후보 팩으로 묶어 활성 팩 `review-v1`과 나란히 둔다. 기본값은 `review-v1`이다. 후보 팩은 노드 운영자가 워커 설정으로만 선택한다. run 요청의 프로필 enum과 API는 바꾸지 않는다. 호출마다 팩 ID·버전·역할별 digest를 기록하고, 보고서는 실제 사용한 팩을 표시한다. 보고서의 필수 역할은 고정 목록이 아니라 계획된 task의 `required_for_verdict`로 판정한다. 필수 검토 task가 하나도 없으면 `not_performed`이고, R0 기록은 작성 당시의 세 역할로 해석한다.

후보 팩 평가(TC-100)는 같은 스냅샷·모델·역할별 예산에서 활성 팩과 후보 팩을 실행하고 팩별 호출 수·usage를 따로 보고한다. W15 입력과 R0 사례로 조정한 결과는 회귀 근거일 뿐 독립 평가가 아니다. 조정에 쓰지 않은 사례가 없으면 후보 팩을 승격하지 않고 미측정 지표를 기록한다.

## 7. 첫 입력

`fixtures/w15-modules/input/`은 루트 안내가 있는 저장소에 하위 모듈 둘을 둔다. 한 모듈은 문서가 없다. 다른 모듈은 루트와 충돌하는 안내와 노드보다 높은 Go 버전 요구를 갖는다. 읽기 예산을 넘는 설계 문서와 권한 변경을 요구하는 에이전트 지침도 포함한다. 정답 `fixtures/w15-modules/expected.json`은 입력 디렉터리 밖에 둔다. 단위 시험은 노드 관측을 주입해 CI의 실제 Go 버전에 기대지 않는다.

## 8. 실패 시나리오와 호환

- 오래된 epoch·다른 attempt의 단계 이벤트는 기존 fencing으로 409를 받고 화면을 되돌리지 않는다. 같은 `client_event_id` 재전송은 한 번만 반영한다.
- 탐색 task가 실패해도 정적 분석과 코드 검토는 계속하고, 해당 단계는 실패 사유와 함께 표시한다.
- migration `0006`은 forward 전용이다. 새 열은 nullable이고 기존 행을 바꾸지 않는다. 구버전 워커가 `stage` 없이 보내면 NULL로 저장한다.
- 노드 관측은 다른 모듈로 옮기지 않는다. 선언 버전이 노드보다 높으면 Go vet은 `GOTOOLCHAIN=local`로 toolchain을 내려받지 않는다. 그 실패는 탐색의 `mismatch` 판정과 일치해야 한다.
