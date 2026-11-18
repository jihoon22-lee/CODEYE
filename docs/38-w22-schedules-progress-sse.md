# W22 예약·계획 revision·가중 진행률·SSE

> 구현 기록. 아래 계약을 구현하고 시험했다. 필드 형식의 기준은 [OpenAPI](../api/openapi.yaml)의 `x-codeye-status: implemented-w22` 경로와 스키마다.

## 1. 목적과 요구

| 항목 | 내용 |
|---|---|
| 목적 | 시간대 예약으로 일반 분석 요청을 만들고, W15 단계 집계와 같은 저장 기록으로 계획 revision과 가중 진행률을 계산하며, 실행 상세를 SSE로 갱신하고 끊김·누락 뒤 같은 상태로 복원한다. |
| 요구 | REQ-017, REQ-023, REQ-013/016/022(TC-99 확장), NFR-003/004 |
| 수용 시험 | TC-25, TC-99의 W22 추가분(SSE 순번 누락·계획 분모 변경) ([시나리오](18-acceptance-and-traceability.md)) |
| 선행 | W07(자동 증감 reconcile 루프), W13(보고서 게시), W15(단계 집계) |
| 범위 밖 | 잔여 시간 범위 추정(REQ-017의 "충분한 이력 후"), 예약 수정 화면, SSE 이벤트 보관 기간·삭제, 제품 handler의 task 가중치 선언 |

## 2. 예약

### 2.1 API

| 경로 | 권한 | 요청 조건 | 응답 |
|---|---|---|---|
| `GET /projects/{id}/schedules` | 구성원(viewer 포함) | — | `{items, can_manage}`. 비구성원 404 |
| `POST /projects/{id}/schedules` | manager | `Idempotency-Key` 필수 | 201, `Location`, `ETag: "1"`. 같은 키·같은 정규화 정의는 같은 예약, 다른 정의는 409 `IDEMPOTENCY_CONFLICT` |
| `PATCH /projects/{id}/schedules/{sid}` | manager | `If-Match: "N"` 필수, 필드 1개 이상 | 200, 새 `ETag`. 버전 불일치 409 `CONFLICT` |
| `DELETE /projects/{id}/schedules/{sid}` | manager | `If-Match: "N"` 필수 | 204. 소프트 삭제이며 대기 occurrence는 `cancelled(schedule_deleted)` |

본문의 `run`은 수동 요청과 같은 계약(`profile`, `source_policy: latest_at_acquire`, `scope`)이다. cron·시간대·정책 오류와 9년 안에 발생하지 않는 표현은 422 `VALIDATION_FAILED`, 프로젝트당 살아 있는 예약 50개 초과는 409다. 첫 occurrence는 생성 시각 이후이며 과거를 소급 실행하지 않는다. 예약을 다시 켜거나 cron·시간대를 바꾸면 그 시각 이후부터 다시 센다. 예시: [예약 목록](../examples/schedules.json).

### 2.2 cron과 시간대

- 5필드(분 시 일 월 요일), 목록·범위·간격, 월·요일 영문 약어, 요일 7=일요일, `@hourly`·`@daily`·`@weekly`·`@monthly`·`@yearly`를 받는다. 초 필드·`L`·`W`·`#`·`?`는 받지 않는다.
- 일과 요일이 모두 제한되면 OR, 하나가 `*`로 시작하면 AND다(`*/2`도 `*`로 시작하는 것으로 본다).
- 시간대는 IANA 이름만 받는다. 빈 값과 `Local`은 거부한다. 시스템 zone DB가 없어도 같은 결과가 나오도록 `time/tzdata`를 내장했다.
- 저장은 표현과 시간대 문자열이며, 각 occurrence는 UTC 시각(`scheduled_for`)으로 남긴다.

### 2.3 일광 절약 시간(DST) 정책

| 표현 | 없는 시각(봄, 시계가 앞으로) | 반복 시각(가을, 시계가 뒤로) |
|---|---|---|
| 시 필드가 모든 시간(`0 * * * *`, `*/15 * * * *`) | 경과 시간 기준: 없는 시간대의 occurrence는 없다 | 두 시각 모두 실행한다(UTC로 1시간 간격 유지) |
| 특정 시(`30 2 * * *`) | 하루 한 번 보장: 전환 시각에 실행한다 | 앞선 시각(전환 전 offset)에 한 번만 실행한다 |

America/New_York 2026년 기준: `30 2 * * *`는 3월 8일 07:00Z(03:00 EDT), `30 1 * * *`는 11월 1일 05:30Z(01:30 EDT) 한 번이다. 매시 예약은 11월 1일 05:00Z와 06:00Z를 모두 만든다. Asia/Seoul은 DST가 없으므로 벽시계 그대로다.

### 2.4 겹침(overlap)

| 정책 | 이전 예약 실행이 끝나지 않았을 때 |
|---|---|
| `coalesce_latest`(기본) | 최신 occurrence 1개만 `pending`으로 남기고 더 이전 대기분은 `coalesced(superseded)`. 실행이 끝나면 다음 reconcile에서 일반 요청으로 넣는다. |
| `skip` | `skipped(overlap_active)` |
| `queue` | 모두 넣는다. 종료되지 않은 예약 run이 10개면 `skipped(queue_full)` |

"끝나지 않음"은 같은 예약이 만든 run 중 종료 상태(`completed`·`partial`·`failed`·`cancelled`)가 아닌 것이 있다는 뜻이다. 예약마다 `pending`은 최대 하나(부분 유일 색인)다.

### 2.5 놓친 예약(misfire)

- `latest_once`(기본): 컨트롤러가 멈춘 사이 지난 occurrence 중 최신 하나만 평가하고, 이전 구간은 그 occurrence의 `missed_from`(첫 놓친 시각)으로만 남긴다. 1년 정지 뒤에도 행은 하나다(무제한 catch-up 없음).
- `skip`: 그 최신 occurrence도 예정 시각보다 2분 넘게 늦게 발견되면 `skipped(misfire)`다.
- 탐색은 정해진 창(1시간·26시간·32일·367일·9년)에서 최신 시각을 찾으므로 정지 기간에 비례해 계산하지 않는다.

### 2.6 materialize와 다중 컨트롤러 안전성

- `codeye serve`의 reconcile 루프(`--reconcile-interval`, 기본 5초, 음수면 끔)가 자동 증감 전에 예약을 평가한다. 프로세스 전역 예약 상태는 없다.
- 시각은 DB의 `clock_timestamp()`다. 컨트롤러 시계가 어긋나도 판단이 바뀌지 않는다.
- 예약마다 짧은 트랜잭션에서 프로젝트 행, 예약 행 순서로 `FOR UPDATE`(API와 같은 순서)한 뒤 `next_at`을 다시 확인한다. 두 번째 컨트롤러는 기다린 뒤 이미 처리된 예약을 본다.
- occurrence 키 `(schedule_id, scheduled_for)`는 기본 키이고 삽입은 `ON CONFLICT DO NOTHING`이다. run은 `(schedule_id, scheduled_for)`로 occurrence를 참조하며 그 쌍은 유일하다.
- 실행 요청은 수동 요청과 같은 enqueue 경로를 쓴다. 요청자는 예약의 actor(마지막으로 정의를 바꾼 manager)이며 occurrence마다 권한을 다시 검사한다. 권한이 없으면 `skipped(actor_forbidden)`, 비활성 사용자는 `skipped(actor_disabled)`, 삭제된 프로젝트는 `skipped(project_deleted)`다. 멱등성 키는 `schedule <id> <unix 초>`다.
- `run.queued` 이벤트와 `GET /runs/{id}`의 `trigger`가 출처(`manual` 또는 `schedule`+`schedule_id`+`scheduled_for`)를 보여준다.

### 2.7 데이터(migration 0007)

`schedules`(정의·버전·`next_at`·소프트 삭제), `schedule_occurrences`(`pending`·`enqueued`·`skipped`·`coalesced`·`cancelled`, 사유, `missed_from`, 결정 시각; 결정 뒤 불변, 삭제 금지), `runs.schedule_id`·`scheduled_for`(쌍 CHECK, occurrence FK, 불변), `tasks.weight`(1..1000, 한 번 기록 뒤 불변)·`tasks.created_sequence`(생성 이벤트 순번, 불변). 기존 행은 바꾸지 않는다(NULL은 수동 run, 가중치 미선언, 생성 순번 미기록).

## 3. 계획 revision과 가중 진행률

- 계획 확정은 현재 attempt가 `running`에 도달한 run 이벤트다. 그 이벤트 순번 이전에 만든 task가 revision 1의 계획이다.
- 확정 뒤 만든 task(`created_sequence`가 더 큼)마다 revision이 1 오르고 `plan_changes`에 `revision`·`reason: task_added`·`stage`·`weight`·`planned_weight`(변경 뒤 분모)·`recorded_at`(DB 시각)을 남긴다. 분모가 바뀐 이유를 화면이 그대로 보여준다.
- `estimated_percent = 종료된 task 가중치 / 현재 계획 가중치 × 100`(소수 첫째 자리 반올림). 종료는 성공·실패·제외·취소 등 W15의 종료 상태다. 확정 전이나 분모 0이면 null.
- 가중치는 워커가 `task_created`의 선택 필드 `weight`(1..1000)로 선언한 값이며, 없으면 1이다. `weight_basis`는 `task_count`(선언 없음, 작업 수 비율), `declared`, `mixed`다. 같은 key 재사용에서 다른 가중치는 409, 저장값이 없을 때 처음 선언하면 `task.weight_recorded` 이벤트를 남긴다.
- heartbeat, 모델 토큰, 경과 시간은 근거가 아니다. 보고서 게시(report 단계)는 퍼센트에 포함하지 않는다.
- 재시도한 run은 새 attempt가 `running`에 도달하면 revision 1부터 다시 센다.
- 예시: [계획 revision 2](../examples/run-progress.json).

## 4. SSE: `GET /runs/{id}/events`

### 4.1 메시지

```text
retry: 3000

id: 22
data: {"run_id":"…","sequence":22,"version":14,"type":"task.created","at":"…","task_id":"…","key":"review.security","required":true,"stage":"code_review","weight":3}

event: run.view
data: {…GET /runs/{id}와 같은 문서…}

event: heartbeat
data: {"run_id":"…"}
```

- 이벤트 행은 이름 없는 메시지이며 `id`는 그 run의 이벤트 순번이다. `type`은 저장된 이벤트 종류(`run.queued`, `attempt.leased`, `snapshot.fixed`, `task.created`, `task.status`, `run.status`, `report.published` 등)다. [API 문서](11-api-and-events.md)의 설계 예시 `task.updated`라는 이름은 쓰지 않는다.
- `data`는 `run_id`·`sequence`·`version`(그 이벤트 뒤 run version)·`type`·`at`(DB 시각)과 payload의 허용 필드만 담는다. 허용 목록은 OpenAPI `RunEventData.x-codeye-payload-fields`이며 서버 코드와 같은지 검증 스크립트가 확인한다. 노드·워커 식별자, digest, 경로, 로그, 자격증명은 보내지 않는다.
- 한 묶음의 행 뒤에 `run.view`를 보낸다. 상태의 원천은 이 문서와 GET이며 이벤트 행은 무효화 신호다.
- 제어 메시지(`run.view`, `heartbeat`, `state.resync`, `closed`)에는 `id`가 없어 cursor를 움직이지 않는다. heartbeat는 SSE 주석이 아닌 이름 있는 이벤트다(브라우저 EventSource는 주석을 전달하지 않는다). 진행률을 바꾸지 않는다.
- 예시: [재접속 wire 예시](../examples/run-events.txt).

### 4.2 cursor·재접속·누락

- `?cursor=N`(필수, 0 이상 정수) 또는 `Last-Event-ID`. 둘 다 있으면 재접속한 브라우저가 보낸 `Last-Event-ID`가 우선한다. 형식 오류는 400.
- 서버는 N 다음 순번부터 보낸다. 다음 경우에는 `event: state.resync`(`reason`)를 보내고 연결을 닫는다: 순번 누락 `gap`(`expected`), cursor가 run보다 앞섬 `cursor_ahead`, cursor가 최신보다 1000개 넘게 뒤처짐 `cursor_expired`, 상태 문서를 읽지 못함 `view_unavailable`. 클라이언트는 GET으로 다시 읽고 그 `event_sequence`로 다시 연결한다.
- 이벤트는 삭제하지 않으므로 현재 `cursor_expired`는 보관 만료가 아니라 재전송 상한이다.

### 4.3 권한·세션·자원

- 첫 읽기에서 프로젝트 ACL을 확인하며 권한이 없으면 스트림 전에 일반 404다. 세션 없음 401.
- 매 poll(기본 1초)마다 짧은 트랜잭션으로 세션과 ACL을 다시 확인한다. 로그아웃·세션 만료·사용자 비활성은 `closed(unauthenticated)`, 구성원 제거·프로젝트 삭제는 `closed(access_revoked)`로 끝난다. poll 사이에 DB 연결을 잡지 않는다.
- 동시 스트림은 세션 사용자별 4개, 전체 32개다. 사용자별 상한을 먼저 확인하므로 한 사용자가 전체 상한을 모두 차지할 수 없다. 넘으면 그 사용자만 503 `UNAVAILABLE`(재시도 가능)이다. 모든 종료 경로에서 슬롯을 반환한다.
- 스트림은 30분 뒤 닫힌다. 웹 클라이언트는 GET으로 다시 읽고 새 연결에서 다시 인증받는다(§4.4). 서버의 Read/WriteTimeout은 스트림마다 해제·연장하며, 종료(shutdown) 때는 열린 스트림을 먼저 닫는다.
- 저장소 오류가 30초 넘게 이어지면 `closed(unavailable)`.
- 값은 `httpapi.StreamConfig`의 기본값이며 현재 CLI 플래그는 없다.

### 4.4 웹 클라이언트

- 첫 GET의 `event_sequence`에서 연결한다. 이벤트 행으로 cursor만 옮기고 순번이 이어지지 않으면 연결을 닫고 GET 뒤 다시 연결한다. 받은 `run.view`와 GET은 `event_sequence`, 그다음 `version`이 작아지면 적용하지 않는다.
- 재연결은 클라이언트가 맡는다. resync·closed·오류·종료 상태·탭 숨김·화면 이탈 때 EventSource를 닫아 브라우저가 오래된 cursor로 재시도하지 않게 한다.
- 열렸던 연결이 끝나면(서버 30분 상한, 재시작) 바로 GET 뒤 다시 연결한다. heartbeat나 `run.view` 없이 연속 3번을 넘으면 실패로 본다.
- 열리지 않은 연결(503 상한, 401, 404 포함)·45초 무응답은 5초 polling으로 바꾸고 30초·60초·120초 뒤 최대 3번 다시 연결을 시도한다. `closed`는 GET 한 번으로 오류(401·404)를 표시한다.
- 대기 중 run의 대기 사유는 읽을 때 계산되고 이벤트가 없으므로, 실시간 연결 중에도 `queued`이면 5초마다 GET한다.
- 화면은 연결 방식(실시간·연결 중·polling 대체·숨김), 마지막 확인 시각, 계획 revision과 분모 변경 사유, 요청 경로(사용자·예약)를 보여준다. EventSource가 없는 환경은 W22 이전과 같은 polling이다.
- 예약 화면은 구성원에게 목록을, manager에게 등록(같은 본문 재시도는 같은 `Idempotency-Key`)·삭제(`If-Match`)를 제공한다. 문자열은 `web/src/locales/ko.ts`에 있다.

## 5. 호환과 운영 순서

- 모든 W22 응답 필드는 추가다. 웹은 `event_sequence`·`trigger`·계획 필드가 없는 서버에서도 동작한다(polling, 계획 표시 생략).
- 구버전 컨트롤러는 `task_created`의 `weight`를 거부하므로 컨트롤러를 워커보다 먼저 올린다. 현재 제품 handler는 가중치를 선언하지 않으므로 `weight_basis`는 `task_count`다.
- 0007을 적용한 DB에 0006 바이너리는 기동하지 않는다(`ErrUnknownVersion`).

## 6. 결과

2026년 11월 18일 Linux(WSL2), 실제 PostgreSQL 17, 제품 워커와 go vet, Chromium에서 확인했다.

| 시험 | 확인한 결과 | 근거 |
|---|---|---|
| TC-25 중복 | 두 pool·네 goroutine이 같은 예약을 동시에 다섯 번 평가해도 occurrence와 run이 하나씩이다. | `TestTC25DuplicateOccurrenceIsUnique` |
| TC-25 겹침 | 실행 중 여러 occurrence → 최신 1개 `pending`, 이전 `coalesced(superseded)`, 종료 뒤 enqueue. `skip`·`queue`(상한 10) 사유. | `TestTC25CoalesceLatestWhileRunning`, `TestTC25SkipAndQueueOverlap` |
| TC-25 정지 | 정지 뒤 `latest_once`는 최신 1개와 `missed_from`, 1년 매분 예약도 행 하나. `misfire: skip`은 2분 초과 시 `skipped(misfire)`. | `TestTC25ControllerStoppedLatestOnce` |
| TC-25 DST | New York 봄·가을 전환과 매시 예약, Seoul. | `TestTC25DaylightSavingOccurrences`, `internal/schedule` 시험 |
| TC-25 serve 경로 | `codeye serve`의 reconcile 루프가 due occurrence를 일반 queued run으로 만들고 GET·목록·SSE가 같은 출처를 보여준다. | `TestServeLoopMaterializesSchedule` |
| 예약 권한·API | actor 권한 재검사, manager 전용 변경, viewer 목록, 비구성원 404, Idempotency-Key·If-Match. | `TestScheduleOccurrenceRechecksActor`, `TestScheduleManagement`, `TestScheduleAPI` |
| TC-99 계획 분모 | 확정 뒤 task 추가로 revision 2와 사유, 가중치 충돌 409·범위 밖 422, heartbeat 뒤 불변, 재시도 뒤 revision 1. | `TestTC99PlanRevisionAndWeightedProgress`, `TestPlanRevisionAndWeightedPercent` |
| TC-99 SSE | 순번 연속·허용 필드·`run.view`=GET, `Last-Event-ID` 우선, heartbeat 무 id, `cursor_ahead`·`cursor_expired`·`gap`, 401·404·400, 로그아웃·권한 상실 종료, 사용자별 상한(사용자 A 초과 503, 사용자 B 연결), 실제 `http.Server` timeout 생존, shutdown 시 종료. | `TestSSE*` (`internal/httpapi/w22_test.go`) |
| TC-99 실제 run | 실제 Go run의 SSE가 저장된 이벤트를 순서대로 전달하고 마지막 `run.view`가 GET과 같다. | `scripts/node_smoke.py` |
| 웹 | stream 적용·gap 재조회·오래된 view 무시·polling 대체·제한된 재연결·숨김 탭·queued 재조회, 계획 표시, 예약 화면. Chromium에서 실시간 연결 표시. | `runStream.test.ts`, `W22View.test.tsx`, `apiW22.test.ts`, `web/e2e/control.spec.ts` |
| migration | 0006 데이터에 0007 적용 시 기존 행 불변, 제약·불변·삭제 금지, 재적용, 0006 바이너리 거부. | `TestW22MigrationKeepsLegacyRows` |
| 계약 | OpenAPI·0007·Go 상수의 enum·범위 일치, 예시의 파생 진행률 재계산, SSE 예시의 id 연속·허용 필드, 부정 사례 8개. | `scripts/validate_bundle.py`의 `check_w22_contracts` |

### 실행한 검증

작업 브랜치에서 `gofmt -l .`(출력 없음), `go vet ./...`, `GOOS=windows go vet ./...`, `python3 scripts/validate_bundle.py`, `cd web && npm ci && npm test && npm run build`(16개 파일, 177개 시험), PostgreSQL 클라이언트를 `PATH`에 둔 `python3 scripts/test_postgres.py --nodes --web --scheduler`가 모두 통과했다. 마지막 명령은 임시 PostgreSQL에서 `go test -race ./...`(PostgreSQL 시험 포함, 건너뜀 없음), Chromium e2e, 두 노드·실제 Go vet smoke(실제 run SSE 40개 이벤트), scheduler smoke를 실행한다. Windows는 vet만 했고 Windows에서 시험을 실행하지 않았다. 실제 모델 요청은 보내지 않았다.

## 7. 남은 제한

- 제품 handler가 가중치를 선언하지 않아 실제 run의 퍼센트는 작업 수 비율이다. 가중치 선언은 handler별 작업량 근거가 생길 때 추가한다.
- 잔여 시간 범위는 제공하지 않는다.
- 예약 수정(PATCH)은 API만 있고 화면은 등록·삭제만 있다.
- SSE 이벤트 보관·삭제 정책이 없어 `cursor_expired`는 재전송 상한(1000)만 뜻한다. 스트림 설정은 CLI로 바꿀 수 없다.
- 한 reconcile 패스는 예약 100개까지 평가한다. 더 많으면 다음 패스로 넘어간다.
- 배치 중간·상태 문서 조회 중 권한 상실(`closed(access_revoked)`) 분기와 세션 만료(로그아웃 경로만 시험)는 별도 시험이 없다. 실제 서버의 예약 응답을 OpenAPI `Schedule`로 검증하는 스크립트 시험은 없다(예시와 Go 시험만).
- [API 문서](11-api-and-events.md)의 SSE·예약 "후속 작업" 문구와 설계 예시, [워커·예약 문서](09-workers-and-scheduling.md)의 "후속 설계" 문구, [데이터 모델](10-data-model.md)의 migration 표, 구현 순서·수용 시험·색인의 W22 상태는 이 브랜치에서 고치지 않았다(통합 검토에서 갱신).
