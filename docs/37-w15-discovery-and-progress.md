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

## 6. 역할 지침 후보 팩

`prompts/review-r1-draft`를 후보 팩으로 묶어 활성 팩 `review-v1`과 나란히 둔다. 기본값은 `review-v1`이다. 후보 팩 선택은 run 프로필로 하고, 호출마다 팩 ID·버전·역할별 digest를 기록한다. 보고서의 필수 역할은 고정 목록이 아니라 계획된 task의 `required_for_verdict`로 판정한다. 필수 검토 task가 하나도 없으면 `not_performed`이고, R0 기록은 작성 당시의 세 역할로 해석한다.

후보 팩 평가(TC-100)는 같은 스냅샷·모델·역할별 예산에서 활성 팩과 후보 팩을 실행하고 팩별 호출 수·usage를 따로 보고한다. W15 입력과 R0 사례로 조정한 결과는 회귀 근거일 뿐 독립 평가가 아니다. 조정에 쓰지 않은 사례가 없으면 후보 팩을 승격하지 않고 미측정 지표를 기록한다.

## 7. 첫 입력

`fixtures/w15-modules/input/`은 루트 안내가 있는 저장소에 하위 모듈 둘을 둔다. 한 모듈은 문서가 없다. 다른 모듈은 루트와 충돌하는 안내와 노드보다 높은 Go 버전 요구를 갖는다. 읽기 예산을 넘는 설계 문서와 권한 변경을 요구하는 에이전트 지침도 포함한다. 정답 `fixtures/w15-modules/expected.json`은 입력 디렉터리 밖에 둔다. 단위 시험은 노드 관측을 주입해 CI의 실제 Go 버전에 기대지 않는다.

## 8. 실패 시나리오와 호환

- 오래된 epoch·다른 attempt의 단계 이벤트는 기존 fencing으로 409를 받고 화면을 되돌리지 않는다. 같은 `client_event_id` 재전송은 한 번만 반영한다.
- 탐색 task가 실패해도 정적 분석과 코드 검토는 계속하고, 해당 단계는 실패 사유와 함께 표시한다.
- migration `0006`은 forward 전용이다. 새 열은 nullable이고 기존 행을 바꾸지 않는다. 구버전 워커가 `stage` 없이 보내면 NULL로 저장한다.
- 노드 관측은 다른 모듈로 옮기지 않는다. 선언 버전이 노드보다 높으면 Go vet은 `GOTOOLCHAIN=local`로 toolchain을 내려받지 않는다. 그 실패는 탐색의 `mismatch` 판정과 일치해야 한다.
