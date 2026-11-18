# W23 이력·비교·사람 검토

> 구현 기록. 아래 계약을 구현하고 시험했다. 필드 형식의 기준은 [OpenAPI](../api/openapi.yaml)의 `x-codeye-status: implemented-w23` 경로와 스키마, [발견 스키마](../schemas/finding.schema.json), [보고서 스키마](../schemas/report.schema.json)다.

## 1. 목적과 요구

| 항목 | 내용 |
|---|---|
| 목적 | 같은 프로젝트의 게시 보고서를 run 사이에 비교하고, 검사 누락을 해결로 바꾸지 않으며, 사람의 판정(disposition)을 이유·기간·권한·감사 기록과 함께 남긴다. |
| 요구 | REQ-028(이전 결과 비교, 검사 누락을 해결로 처리하지 않음), REQ-029(사람의 처리 상태와 이유), NFR-005(키보드·색상 외 상태 구분), NFR-006 일부(판정 감사 기록) |
| 수용 시험 | TC-14(이전 finding이 이번 도구 실패로 안 보이면 resolved가 아니라 absent_unverified), TC-31 일부(code-free export에 발견 식별 없음) ([시나리오](18-acceptance-and-traceability.md)) |
| 선행 | W13(검증된 보고서 게시), W15(발견·환경 산출물), W22(OpenAPI·예시 계약 방식) |
| 범위 밖 | 첫 발견 시각(first seen) 이력, 판정 일괄 적용, 판정 화면의 감사 이력 목록, 코드 문맥 해시, 프로젝트 키 기반 내보내기 식별자 |

## 2. 발견 식별(fingerprint) `fp2`

### 2.1 정의

식별 규칙은 `internal/report/fingerprint.go` 한 곳에만 있다. 분석기 decoder는 `ToolFingerprint` 또는 `setModelFingerprint`를 부르고 자체 해시를 만들지 않는다.

```
fingerprint = "fp2:" + sha256( NUL로 이은 [
  "codeye-fingerprint", "fp2", producer, analyzer, rule,
  paths(정렬·중복 제거, 줄바꿈으로 이음), symbols(같음), context, condition ])
move_key    = 같은 식에서 첫 값을 "codeye-move-key"로, paths를 빈 값으로 바꾼 것
```

| 입력 | 도구 발견 | 모델 후보 |
|---|---|---|
| producer / analyzer | `tool` / 분석기 ID(예: `go.vet`) | `model` / `review` |
| rule | 도구 규칙 ID | 원인 분류 `candidate:<검증기 category>`(판정이 있을 때), 없으면 `candidate:unclassified` |
| paths | 진단 위치의 snapshot 상대 경로 | 후보가 인용한 code·test 발췌의 경로 집합(없으면 문서 경로) |
| symbols | 둘러싼 심볼(분석기가 줄 때만, go vet은 없음) | 인용 발췌의 심볼 집합 |
| context | `unavailable`(아래 제한 참고) | `unavailable` |
| condition | `NormalizeCondition(도구 메시지)`: 파일 위치(`a.go:12:3`)→`<pos>`, `line N`→`line <n>`, 공백 압축 | 연결된 도구 관찰의 (도구, 규칙, 경로, 정규화 메시지) 집합 |

- 줄·열 번호, snapshot·run·attempt ID, 모델이 쓴 제목·서술, 근거 인용 순서는 입력이 아니다. 줄 이동이나 제목 변경은 같은 식별을 유지한다.
- 프로젝트와 source variant는 해시 입력이 아니라 비교 범위(namespace)다. 판정은 프로젝트 단위, 기준 보고서는 source variant 단위로 찾는다.
- 한 보고서 안에서 식별은 유일하지 않다(같은 파일·규칙·메시지의 진단 두 개). 비교와 판정은 식별을 묶음 키로 다루고 하나씩 짝짓지 않는다.
- 보고서에는 `fingerprint`와 `fingerprint_basis`(scheme, producer, analyzer, rule, paths, symbols, context, move_key)를 저장한다. 정규화한 condition은 도구 원문이므로 저장하지 않고 해시에만 쓴다.

### 2.2 버전과 이전 보고서

- 보고서 `builder_version` 3부터 `fp2`를 쓴다(`FingerprintSince = 3`). `schema_version`은 0.1 그대로이며 새 필드는 모두 선택 필드다.
- builder 2 이하 보고서의 식별(`go-vet-fp-v1:…` 등)은 저장된 그대로 읽는다. 다시 계산하거나 변환하지 않는다. 한쪽이라도 `fp2`가 아니면 비교는 `comparable=false`, `not_comparable_reason=fingerprint_scheme_differs`이며 run 차이(`builder_version` 등)만 보여준다.

### 2.3 코드 제외 내보내기

`include_code=false` 내보내기(기본)에는 `fingerprint`, `fingerprint_basis`, `disposition_version`을 싣지 않는다. 키 없는 digest는 짧은 코드 줄을 추측해 맞춰 볼 수 있는 확인 수단이 되기 때문이다. 프로젝트별 키로 감싼 식별자는 키 수명 관리가 필요해 이번 범위에서 제외했다. 보고서 스키마의 code-free 규칙과 `validate_bundle.py` 부정 사례가 이를 강제한다.

## 3. run 비교

### 3.1 API

| 경로 | 권한 | 응답 |
|---|---|---|
| `GET /runs/{id}/comparison?revision=N&baseline=<run_id>&baseline_revision=M` | 프로젝트 읽기(viewer 포함). 비구성원 404 | `RunComparison`. 기준 run이 다른 프로젝트·없음 404, 자기 자신 422, `baseline_revision`만 있으면 400 |
| `GET /runs/{id}/reports` | 같음 | 기존 응답에 `default_baseline`(`{run_id, revision, url}` 또는 null) 추가 |

비교는 저장 테이블이 아니라 요청마다 불변 보고서 본문과 run 사실로 다시 계산한다(`internal/history`, 순수 함수). 예시: [비교](../examples/comparison.json).

### 3.2 기본 기준

같은 프로젝트에서 이 run보다 먼저 만든 run 중 다음을 모두 만족하는 가장 최근 run의 가장 최근 그런 revision이다.

- source variant가 같다: 그 run의 project revision `config.source`(Git 저장소+브랜치 또는 source root+경로)가 같다.
- 요청의 `profile`과 `scope`가 같다.
- 보고서가 `completed` 또는 `partial`이고 builder 3 이상이다. `failed`·`cancelled` 보고서는 기본 기준이 아니다(명시하면 비교할 수 있다).

### 3.3 상태

| 상태 | 조건 |
|---|---|
| `new` | 이번에만 있다. 기준 run이 이 위치를 같은 방식으로 볼 수 없었다면 `reason`에 그 이유를 단다. |
| `persisting` | 양쪽에 있고 개수·심각도·검증 상태·영역이 같다. 줄 위치만 다르면 `changes: ["lines"]`인 persisting이다. |
| `changed` | 양쪽에 있고 `count`·`severity`·`validation`·`category` 중 하나가 다르다. |
| `resolved_observed` | 기준에만 있고, 이번 run이 같은 source variant·scope에서 같은 분석기를 성공적으로 실행했고(성공 종료, 부분 파싱 아님, 산출물 거부 아님) 그 분석기의 `processed_paths`가 발견의 모든 경로를 포함하며, 도구 버전과 규칙 문맥(W19, [docs/39 §5.1](39-r1-analyzer-packs.md#51-규칙-문맥과-소스-안-억제-builder-4-tc-104))이 양쪽에서 알려져 있고 같으며, 이번 run의 도구가 그 진단을 소스 안 억제로 숨겨졌다고 보고하지 않았고 억제를 보고하지 않는 방식(`source_suppressions=honored`)도 아닐 때만. 도구 발견만 가능하다. |
| `absent_unverified` | 기준에만 있고 위 조건 중 하나라도 확인되지 않는다. `reason`이 필수다. |

`reason` 값: `source_variant_differs`, `scope_reduced`, `check_not_run`, `check_failed`, `rules_changed`, `rules_unknown`, `location_not_inspected`, `model_review_not_performed`, `model_review_partial`, `model_absence_not_observation`, 그리고 W19의 `rule_context_unknown`(한쪽 규칙 문맥을 모름; builder 3 이하의 go vet 외 분석기 행 포함), `rule_context_changed`(규칙 팩·버전·선택·저장소 설정 모드·설정 파일 digest·억제 처리 중 하나가 다름), `suppressed_in_source`(관찰한 쪽 도구가 같은 fp2의 진단을 억제됨으로 보고함; new에서는 기준 쪽이 억제했음), `source_suppressions_hidden`(현재 쪽 도구가 억제를 적용하고 숨긴 진단을 보고하지 않음: `source_suppressions=honored`이거나 그 규칙이 `unreported_suppression_rules`에 있음, 예 staticcheck U1000). 모델이 후보를 다시 내지 않은 것은 관찰이 아니므로 모델 후보는 절대 `resolved_observed`가 되지 않는다.

도구 발견의 이유 우선순위는 source variant → scope → 미실행·실패 → 위치 미검사 → `suppressed_in_source` → 도구 버전(`rules_unknown`, `rules_changed`) → 규칙 문맥(`rule_context_unknown`, `rule_context_changed`) → `source_suppressions_hidden`이다. go vet 문서에는 rule pack이 없다. builder 4 decoder는 `codeye-go-vet-default` v1을 합성해 행에 쓰고, 문맥이 없는 builder 3 go vet 행에는 비교가 같은 문맥을 합성한다. 그래서 builder 3과 4의 go vet 보고서는 서로 비교되고 W23 세 run smoke의 수정 → `resolved_observed`가 유지된다.

- `ambiguous`: 한쪽에 같은 식별의 발견이 여럿이면 true이며, 상태는 묶음 전체로 정하고 하나씩 짝짓지 않는다.
- `move_candidates`: `move_key`가 같고 경로만 다른 new↔사라진 항목. 이동·이름 변경 후보로만 보여주고 자동으로 합치거나 판정을 옮기지 않는다.
- `differences`: `source_variant`, `source_revision`, `profile`, `scope`, `builder_version`, `tool:<analyzer>:version|rule_context|result|processed_files`, `ai_review`, `review:<role>`(모델·지침 digest). `rule_context` 값은 `pack@version [선택] config=<모드>[:설정 digest 앞 12자|:unrecorded] suppressions=<모드>[ unreported=<규칙,…>]`이고 문맥이 없는 행은 `unknown`이다. 설정 digest 앞부분은 코드 제외 내보내기에서 빼는 값이지만, 비교 응답은 `include_code=true` 보고서를 받을 수 있는 같은 프로젝트 읽기 권한자에게만 가고 이미 fp2 식별을 싣는다. 같은 값은 생략한다.
- `counts`는 entry(식별) 단위 상태 수이고, `unidentified`는 `fp2` 식별이 없어 비교하지 않은 발견 수다.

## 4. 사람 판정

### 4.1 모델

판정은 `open`, `acknowledged`, `accepted_risk`, `false_positive`, `fixed_by_user`, `suppressed` 중 하나다. 한 판정 기록은 프로젝트·fingerprint의 다음 version이며 다음을 갖는다: 사유(공백 제외 1..2000자, 필수), 선택 만료 시각(미래), 선택 경로 범위(snapshot 상대 경로, 그 경로 또는 아래에만 적용), 기록자, 기록 시각, 사람이 판단한 게시 보고서의 발견(`run_id`, `revision`, `finding_id`, 그 본문에 같은 fingerprint로 있어야 함). `open`은 만료·경로 범위를 갖지 않는다.

### 4.2 API와 권한

| 경로 | 권한 | 요청 조건 | 응답 |
|---|---|---|---|
| `GET /projects/{id}/finding-reviews?fingerprint=…` | 구성원(viewer 포함), 비구성원 404 | — | 그 fingerprint의 모든 version(최신 먼저) |
| `GET /projects/{id}/finding-reviews?cursor=…&limit=N` | 같음 | `limit` 1..200 | fingerprint별 최신 version, cursor pagination, `can_review` |
| `POST /projects/{id}/finding-reviews` | owner·manager·runner. viewer 403, 비구성원 404 | `Idempotency-Key` 필수. 현재 version이면 `If-Match: "N"`, 첫 판정이면 `If-None-Match: *` | 201과 `ETag: "version"`. 같은 키·같은 요청 재전송은 같은 행 200, 다른 요청은 409 `IDEMPOTENCY_CONFLICT`, version 불일치 409 `CONFLICT`, 사유 없음·과거 만료·없는 발견 422 `VALIDATION_FAILED`, 전제 조건 헤더 없음 400 |

예시: [판정 목록](../examples/finding-reviews.json). 판정 행은 수정·삭제·TRUNCATE할 수 없고(trigger), 이 행들이 감사 기록이다.

### 4.3 적용 규칙

| `review_match` | 뜻 | 현재 판정 |
|---|---|---|
| `none` | 판정 없음 | `open` |
| `applied` | 적용 중 | 판정 값 |
| `candidate` | 같은 식별이 그 revision에서 여럿이거나(`ambiguous`) 식별이 약하다(모델 후보에 심볼도 연결 관찰도 없음). 사람이 그 발견에서 다시 판정해야 적용된다. | `open` |
| `expired` | 만료 시각이 지났다(DB 시각 기준) | `open` |
| `out_of_scope` | 경로 범위가 발견 경로를 덮지 않는다 | `open` |
| `reverted` | 최신 판정이 `open`이다 | `open` |

적용하는 판정은 fingerprint마다 최신 version 하나다. 그 판정이 직접 가리킨 발견(같은 run·revision·finding_id)에는 식별이 약하거나 모호해도 적용한다. 그 밖에는 `DispositionCarries(basis, matches) = matches == 1 && !WeakIdentity(basis)`일 때만 자동 적용한다.

### 4.4 게시 시점과 현재

- 게시 시점 판정: 보고서를 만들 때 프로젝트의 fingerprint별 최신 판정을 위 규칙으로 발견의 `disposition`·`disposition_version`에 고정한다. 판정 집합은 `input_digest`에 들어가며, 이후 판정이 바뀌어도 그 revision 본문은 바뀌지 않는다.
- 현재 판정: 비교 응답의 `findings[]`가 각 발견의 `publication_disposition`과 `current_disposition`, `review_match`, 최신 `review`를 따로 준다. 과거 revision을 열어도 그 revision의 게시 시점 판정이 보인다.
- 통계: `accepted_risk`·`false_positive`·`suppressed`는 조치 집계에서 빠지고 `summary.hidden_findings`로 센다. `total_findings`·원시 진단·비교에는 그대로 남는다. 비교 응답의 `summary.at_publication`과 `summary.current`는 같은 규칙으로 각각 계산한다.

## 5. 데이터(migration 0008)

[0008](../migrations/sql/0008_finding_reviews.sql)은 `finding_reviews` 테이블 하나만 추가하고 기존 행을 바꾸지 않는다. fingerprint 형식·판정 enum·사유 길이·경로·만료 CHECK, `(project_id, fingerprint, version)`와 `(project_id, actor_id, idempotency_key)` 유일 제약, run과 게시 보고서 FK, 불변·TRUNCATE 금지 trigger, version이 1부터 빈틈없이 증가하는 trigger(`CY003`)를 둔다. 비교 결과 테이블은 만들지 않았다. 비교는 불변 보고서에서 결정적으로 계산되므로 파생 테이블은 원천과 어긋날 위험만 더한다. 0007 바이너리는 0008 DB에서 기동하지 않는다(`ErrUnknownVersion`).

## 6. 웹

보고서 화면은 표시 중인 revision의 비교를 따로 불러온다(실패해도 보고서는 그대로). 기준 run·revision·선택 방식, run 차이, 상태별 수(기호와 글자, 색만 쓰지 않음), 기준에만 있던 발견과 이유, 판정 반영 집계(게시 시점/현재)를 보여준다. 각 발견 제목 옆에 비교 상태와 현재 판정을 글자로 붙이고, 펼치면 게시 시점·현재 판정과 적용 여부 설명, 판정 사유·기록자·만료·경로를 보여준다. 숨김 판정 발견은 목록에서만 빼고(체크박스로 표시) 집계에는 남긴다. 판정 양식(판정 선택, 필수 사유, 선택 만료일, 선택 경로)은 `can_review`일 때만 나오며 native 양식 요소로 키보드 조작이 된다. 같은 요청에는 같은 Idempotency-Key를 다시 쓰고, 409 `CONFLICT`면 최신 비교를 다시 읽는다. 문자열은 `web/src/locales/ko.ts`의 `history`에 있다.

## 7. 보안 검토 반영

| 지적 | 조치 | 시험 |
|---|---|---|
| 모델 식별에 원인 구분이 없어 같은 심볼·경로의 다른 후보가 충돌하고, 숨김 판정 하나가 둘 다 숨길 수 있다 | 검증기 category를 원인 분류(`candidate:<category>`/`candidate:unclassified`)로 식별에 넣었다. 모호한 일치(같은 revision에 같은 식별 2개 이상)와 약한 식별에는 판정을 자동 적용하지 않고 `candidate`로 열어 둔다(게시 시점과 현재 모두). | `TestModelFingerprintStableAcrossTitles`, `TestDispositionNeverCarriesToAmbiguousOrWeakMatch`, `TestBuildStampsPublicationDisposition`, `TestOverlayKeepsPublicationAndCurrentApart`, `TestAmbiguousGroupHasOneConfirmedFinding` |
| 코드 제외 내보내기의 `fingerprint_basis`(경로·문맥)와 `move_key`가 추측으로 코드 내용을 확인하게 한다 | 코드 제외 내보내기에서 `fingerprint`·`fingerprint_basis`·`disposition_version`을 모두 뺐다(2.3). | `TestCodeFreeExportHasNoIdentity`, 보고서 스키마 code-free 규칙, `validate_bundle.py` 부정 사례, HTTP·smoke의 코드 제외 확인 |

## 8. 결과

| 시험 | 확인한 결과 | 근거 |
|---|---|---|
| 식별 안정성 | 메시지 속 위치·줄 번호가 달라도 같고, 규칙·경로·메시지가 다르면 다르다. 모델 후보는 제목·서술·인용 순서가 달라도 같고 검증 전후·영역이 다르면 다르다. | `TestToolFingerprintIgnoresPositions`, `TestModelFingerprintStableAcrossTitles` |
| TC-14 | 도구 실패·산출물 거부·부분 파싱·미계획·범위 축소·파일 미분석·도구 버전 변경·버전 미상·source variant 차이에서 사라진 발견은 `absent_unverified`와 이유이며 `resolved_observed`가 아니다. | `TestTC14MissingCheckIsNeverResolved`, `TestW23DefaultBaselineAndTC14` |
| 상태 | 줄 이동 persisting, 심각도·개수 변화 changed, 새 발견 new, 모호한 이동은 확정하지 않음, 모델 부재는 해결 아님. | `TestPersistingChangedNew`, `TestAmbiguousMoveIsNotConfirmed`, `TestModelAbsenceIsNotResolution` |
| 기본 기준 | 같은 source variant·profile·scope의 직전 fp2 보고서, failed 보고서 제외, 다른 프로젝트 404, 자기 자신 422. | `TestW23DefaultBaselineAndTC14`, `TestW23ComparisonAndReviewsOverHTTP` |
| 판정 | viewer 403·비구성원 404, Idempotency 재전송·충돌, If-Match 불일치 409, 과거 만료·빈 사유·없는 발견 거부, 행 불변, version 빈틈 거부, 이력 목록, 만료 뒤 open, 게시 시점 고정과 되돌림 뒤 현재 open. | `TestW23FindingReviews`, `TestW23ComparisonAndReviewsOverHTTP`, `TestBuildStampsPublicationDisposition` |
| migration | 0007 데이터에 0008 적용 시 기존 행 불변, CHECK·FK·version trigger, 재적용, 0007 바이너리 거부. | `TestW23MigrationAddsReviewsOnly` |
| 실제 worker | 실제 go vet 세 run: 줄 이동 persisting(`changes: ["lines"]`, 줄 +2), 새 printf 진단 new, run 1에 기록한 판정이 run 2 게시 시점에 고정(`hidden_findings` 1), 수정 뒤 `resolved_observed`(이유 없음), 명시 기준 비교, 코드 제외 내보내기에 식별 없음. 응답은 OpenAPI `RunComparison`으로 검증. | `scripts/node_smoke.py`의 `w23_history` |
| 웹 | 예시 파싱·불일치 거부, 기준·상태 글자 표시, 숨김 발견 목록 제외와 표시, 후보 일치는 열린 채 표시, viewer 양식 없음, 필수 사유·과거 만료 차단, If-None-Match/If-Match, 재시도 키 재사용, 409 뒤 재조회, 식별 체계 차이 시 run 차이 표시. | `web/src/HistoryView.test.tsx` |
| 계약 | OpenAPI·0008·발견 스키마·Go의 판정/상태/이유/일치 enum 일치, 예시의 counts·summary 재계산, 해결 항목은 도구만, 부정 사례 10개(W19의 규칙 문맥·도구 버전 차이가 있는데 해결로 표시한 항목 2개 포함). | `scripts/validate_bundle.py`의 `check_w23_contracts` |

### 실행한 검증

2026년 11월 18일 Linux(WSL2), 작업 브랜치 `feat/w23-history`의 커밋 `41ea370`(이 문단을 넣기 직전 커밋)에서 실행했다. `gofmt -l .`(출력 없음), `go vet ./...`, `GOOS=windows go vet ./...`, `go test -race ./...`(DB 없이), `python3 scripts/validate_bundle.py`, `cd web && npm ci && npm test && npm run build`(17개 파일, 187개 시험), PostgreSQL 클라이언트를 `PATH`에 둔 `python3 scripts/test_postgres.py --nodes --web --scheduler`가 모두 통과했다. 마지막 명령은 임시 PostgreSQL 17에서 `go test -race ./...`(PostgreSQL 시험 포함, 27개 패키지 통과), Chromium e2e 2개, 두 노드·실제 Go vet smoke(W23 세 run 비교 포함), scheduler smoke를 실행한다. Windows는 vet만 했고 Windows에서 시험을 실행하지 않았다. 실제 모델 요청은 보내지 않았으므로 모델 후보의 비교·판정은 단위·저장 시험의 구성한 보고서로만 확인했다.

## 9. 남은 제한

- 코드 문맥 해시가 없다(`context: unavailable`). 컨트롤러는 snapshot 원문을 갖지 않고 현재 산출물(go vet 관찰, 검토 기록)이 정규화 문맥을 싣지 않는다. 그래서 같은 파일·규칙·메시지의 진단은 같은 식별을 공유하고 `ambiguous`로 묶이며, 그 묶음에는 판정이 자동 적용되지 않는다.
- 판정은 fingerprint마다 최신 하나만 적용된다. 모호한 묶음에서 사람이 한 발견을 확인한 뒤 같은 묶음의 다른 발견을 확인하면 앞의 확인은 후보(열림)로 돌아가고, 이후 run의 게시 시점에도 그 묶음에는 고정되지 않는다(`TestAmbiguousGroupHasOneConfirmedFinding`).
- 보안 검토 1의 위험은 줄였지만 없애지 않았다. 이후 run에서 원인 분류·경로·심볼이 모두 같은 다른 모델 후보가 그 revision에 하나만 있으면 강한 단일 일치로 보아 판정을 이어받는다. 원인 분류는 검증기 category 수준이라 같은 심볼의 서로 다른 결함을 구분하지 못한다.
- 모델 후보는 다시 나오지 않아도 해결로 보지 않는다. 모델 후보의 해결은 사람 판정(`fixed_by_user`)이나 도구 관찰로만 남는다.
- 첫 발견 시각과 run 사이 연속 이력(몇 번째 run부터 있었는지)은 계산하지 않는다. 비교는 두 revision 사이만 본다.
- 코드 제외 내보내기에는 식별과 현재 판정이 없다. Markdown·HTML 내보내기는 게시 시점 판정만 보여준다.
- 새 분석기 decoder는 `ToolFingerprint`로 식별을 만들고 성공한 작업의 `processed_paths`를 채워야 `resolved_observed`가 가능하다. 채우지 않으면 그 분석기의 사라진 발견은 항상 `location_not_inspected`다. 도구 행에 규칙 문맥(`rule_context`)이 없거나 저장소 설정을 읽으면서 파일을 기록하지 못하면 `rule_context_unknown`, 억제를 보고하지 않으면 `source_suppressions_hidden`이다(W19).
- 판정 화면은 최신 판정만 보여주며 감사 이력 목록 화면, 일괄 판정, 이동 후보의 판정 이전 기능은 없다(API로 이력을 읽을 수 있다).
- 경로 범위는 snapshot 상대 경로 접두사만 받는다. glob은 지원하지 않는다.
- [데이터 모델](10-data-model.md)의 migration 표, [API 문서](11-api-and-events.md), 구현 순서·수용 시험·색인의 W23 상태는 이 브랜치에서 고치지 않았다(통합 검토에서 갱신).
