# 10. 데이터 모델과 무결성

## 1. 저장 원칙

관계·상태·권한·작업 소유권은 PostgreSQL, 대형 소스·도구 출력·로그·내보내기는 artifact store에 저장한다. JSONB는 버전 있는 분석 데이터에 사용하고 상태·필터·유일성에 필요한 필드는 별도 컬럼으로 둔다. 모든 ID는 의미 없는 식별자이며 API 예시의 사람이 읽기 쉬운 ID는 fixture용이다.

모든 시각은 UTC timestamptz, 예약만 IANA timezone을 함께 가진다. 상태 변경용 version은 낙관적 동시성에 사용한다. schema_version은 데이터 해석 버전이다. 미측정 수치를 0으로 저장하지 않고 null+reason을 사용한다.

## 2. 엔티티

| 엔티티 | 주요 필드/관계 |
|---|---|
| users, project_members | identity, project_id, role, status |
| providers, credentials | host/API URL, kind, credential reference; secret 값은 별도 암호화 저장 |
| projects | owner, source kind, provider/repo 또는 root/path, default branch, current_revision |
| project_revisions | immutable source policy, profile, execution policy, config digest |
| attachments | project/revision, kind, artifact, trust, uploader |
| schedules | project revision policy, cron, timezone, overlap/misfire, next occurrence |
| runs | project, trigger, request key, requested_at, revision, snapshot nullable, status, verdict, version |
| attempts | run, number, node, worker, epoch, lease expiry, cleanup state, start/end |
| snapshots | kind, SHA 또는 manifest digest, consistency, artifact, source_fixed_at |
| plans, tasks | run, revision, DAG, purpose/scope, state, required flag, budgets |
| task_executions | attempt/task, ordinal, tool/model manifest, timestamps, result artifacts |
| observations | task execution, 원시 진단, tool/rule, source location, normalized fields |
| evidence | snapshot/artifact, type, path/range/hash, provenance, collection execution |
| findings, finding_occurrences | 지속 논점과 run별 occurrence, validation/severity/conditions/claims |
| finding_evidence | occurrence, evidence ID, supports/refutes/context 관계 |
| finding_reviews | user disposition, reason, scope, expiry, audit trail |
| reports | run, revision, schema version, report artifact, summary statistics, status |
| nodes, worker_pools, workers | identity, capabilities, policy, desired/observed lifecycle |
| capacity_reservations | node/pool/attempt, resources, state, expiry |
| events | run sequence, entity version, type, redacted payload |
| artifacts | scope, SHA-256, size, MIME, storage key, upload/finalize, retention |
| webhook_deliveries, outbox | provider delivery ID, payload digest, durable request, publication retry |
| audit_log | actor/action/target/version, redacted before/after, time |

## 3. finding과 observation

observation은 도구나 모델이 낸 원시 관찰이다. finding은 같은 원인과 영향의 관찰을 통합한 논점이다. 도구의 원래 severity를 보존하고 CODEYE severity는 이유와 정책 버전을 함께 기록한다. 원문을 LLM이 덮어쓰지 않는다.

발견 사항 필수 구조는 [Schema](../schemas/finding.schema.json)를 따른다. title, category, severity, validation, disposition, 발생 조건, impact, locations, claims, evidence, counterevidence, recommendation, verification, limitations가 핵심이다.

`claims`는 assertion, kind(observed/inferred), evidence_ids로 구성한다. 모델의 비공개 사고 과정 대신 검증 가능한 주장 단위를 저장한다. `supported`는 코드 근거가 주장을 지지한다는 뜻이지 형식적 증명이나 재현과 같지 않다. reproduced는 실행 근거가 있어야 한다.

## 4. 증거 주소

코드 증거는 snapshot_id, path, blob_sha256, start_line/end_line을 갖는다. 행은 1-based inclusive이며 bytes/columns는 별도 단위를 명시한다. 문서와 코드의 encoding을 보존하고 행 범위는 디코딩된 snapshot 텍스트로 검증한다. 생성 파일은 snapshot 경로를 가장하지 않고 artifact_id를 사용한다.

도구·실행 증거는 task_execution_id, artifact_id, offset 또는 JSON pointer, 명령 manifest, exit state를 연결한다. 사람 검토는 actor·시각·이유가 근거다. evidence ID는 수집 계층이 발급한다. LLM이 임의 ID와 원문을 만들어 등록할 수 없다.

raw artifact가 삭제돼도 남아 있는 요약이 재현 가능한 것처럼 보이지 않게 evidence_availability를 계산한다. 민감한 코드 조각도 프로젝트 ACL과 retention을 따른다.

## 5. 식별과 이전 결과 비교

fingerprint의 입력은 project/source variant, rule family 또는 원인 분류, canonical path, symbol, normalized code context, 발생 조건이다. 줄 번호만으로 식별하지 않는다. 코드 이동이나 rename은 후보 매칭으로 다루고 애매하면 자동 확정하지 않는다.

비교 상태는 new, persisting, changed, absent_unverified, resolved_observed다. 이전 검사와 현재 검사가 동일/대체 가능한 규칙·환경·범위이고 해당 위치 검사에 성공한 경우만 resolved_observed를 사용할 수 있다. 필수 도구 실패·scope 축소·unknown parser는 absent_unverified다. fixed_by_user는 사용자의 주장이고 이 비교 상태와 별개다.

branch·build variant가 다른 결과를 기본 비교 대상으로 고르지 않는다. suppressed 항목도 원시 통계와 변경 추적에서 완전히 삭제하지 않는다.

## 6. DB 제약과 트랜잭션

- `(project_id, actor_id, idempotency_key)` 유일성과 요청 body digest. 같은 키 다른 body는 409.
- `(schedule_id, scheduled_for)` 및 `(provider_id, delivery_id)` 유일성.
- `(run_id, attempt_number)` 유일성. 한 run의 활성 소유 attempt는 한 개.
- `(run_id, event_sequence)`, `(attempt_id, task_id, ordinal)`, `(run_id, report_revision)` 유일성.
- snapshot 연결은 null→확정 ID 한 번만 허용. 변경하려면 새 run.
- 상태 변경+event+필요 outbox는 한 트랜잭션. artifact bytes 업로드 자체는 DB 트랜잭션 밖.
- lease epoch를 만족하지 않는 완료·finding·report finalize 요청 거부.
- soft deletion 직후 신규 요청을 막고 진행 중 작업 취소/정리 후 retention에 따라 bytes 삭제.

외래키 CASCADE로 감사·보고서 증거를 예상치 않게 삭제하지 않도록 명시적 GC 작업을 사용한다. DB 오류 시 orphan artifact는 격리 후 유예 기간 뒤 수거한다.

## 7. 인덱스와 조회

queue는 상태·우선순위·available_at, lease는 expires_at, finding은 project/severity/validation/category, event는 run/sequence로 인덱싱한다. 큰 목록은 cursor pagination, source와 raw log는 전체 DB 결과에 포함하지 않는다. JSONB 전문 검색을 초기 필수로 넣지 않고 제목·메시지·규칙·path의 기본 검색부터 구현한다.

## 8. 보고서 revision

서술 재생성은 원래 run 결과를 변조하지 않고 새 report revision을 만든다. 다른 모델·지침으로 재검토하면 새 run 또는 명시된 review revision과 그 근거를 남긴다. 사용자의 disposition은 보고서 출판 당시 값과 현재 값을 분리해 볼 수 있게 한다. 감사 가능한 과거 보고서를 현재 상태로 조용히 덮어쓰지 않는다.


## 9. 적용한 저장 계약

| Migration | 책임 | 근거 |
|---|---|---|
| 0001 | 프로젝트 ACL, 설정 revision, 실행·시도·작업 이력, 입력·근거 참조, 이벤트 | [저장 계약](23-w02-storage.md) |
| 0002 | 사용자·일회용 로그인·세션, 승인 source root와 Git provider | [개인 모드 인증](24-w03-api.md) |
| 0003 | 노드·자격증명·lease·claim·자원 예약, 스냅샷 위치와 산출물 | [노드 프로토콜](28-w06-node-protocol.md) |
| 0004 | 워커 풀 정책·자동 증감·공정 배정 | [워커 풀](30-w07-scheduler.md) |
| 0005 | attempt와 입력 digest에 묶인 불변 보고서 리비전·게시 | [보고서 게시](31-w09-report.md) |
| 0006 | task 표시 단계(한 번만 기록), 서버가 수용한 task 시작·종료 DB 시각, 실행 수명 이벤트 부분 색인. 기존 행은 backfill하지 않음 | [W15 단계 기록](37-w15-discovery-and-progress.md) |
| 0007 | 예약·occurrence(유일 키·결정 뒤 불변), run의 예약 출처, task 가중치·생성 순번(한 번 기록). 기존 행은 변경 없음 | [W22](38-w22-schedules-progress-sse.md) |
| 0008 | `finding_reviews`: fingerprint별 사람 판정 version(이유 필수·만료·경로 범위·멱등 키, 수정·삭제·truncate 거부). 기존 행은 변경 없음 | [W23](40-w23-history-comparison.md) |

W15의 단계 요약은 서버가 수용한 실행 기록에서 파생한다. 같은 표시 단계를 여러 task가 공유할 수 있으며, 예전 attempt의 결과는 그 시도의 이력에 속한다. 모델 호출 여부와 실행 단계의 종료는 별도 사실로 보존한다. [단계 집계 계약](11-api-and-events.md)
