# 11. API와 이벤트 계약

R0에서 세션·프로젝트·실행 요청과 조회·취소·노드 프로토콜·보고서 조회와 내보내기를 제공한다. [OpenAPI](../api/openapi.yaml)의 `x-codeye-status`와 [실제 라우트 검사](../internal/httpapi/contract_test.go)로 구현된 경로를 확인한다. W22에서 예약과 실행 SSE를 추가했다. 설계 경로는 아래에 구분했다.

## 1. 공통 규칙

기본 prefix는 `/api/v1`이다. JSON은 UTF-8, 시각은 ISO 8601 UTC, 용량은 bytes, 시간 제한은 seconds를 쓴다. list는 cursor/limit, mutation은 인증·프로젝트 ACL·필요 시 If-Match version을 적용한다. run 생성과 외부 작업 명령은 Idempotency-Key를 지원한다.

개인 loopback 모드에도 임의 로컬 웹사이트가 API를 호출하지 못하도록 launch secret/session, Origin 검증, CSRF 정책을 둔다. LAN 배포는 인증 필수이며 기본 CORS는 same-origin이다. 노드 API는 사용자 세션과 별도의 node-scoped token을 사용한다. 토큰을 query parameter에 넣지 않는다.

## 2. 사용자 API

### R0 구현 경로

아래 경로는 같은 origin의 개인 모드에서 사용한다. session 생성과 상태 변경 요청의 인증·CSRF 조건은 [인증 절차](24-w03-api.md)를 따른다.

| 메서드·경로 | 반환 또는 동작 |
|---|---|
| GET /health, GET /ready | 프로세스 응답과 DB/schema 준비 상태 |
| POST, GET, DELETE /session | 일회용 로그인, 현재 세션 조회, 로그아웃 |
| GET /source-roots, GET /providers | 등록할 수 있는 소스 루트와 Git 공급자 |
| POST /projects, GET /projects, GET /projects/{id} | 소스 위치 선언, 허가된 프로젝트 목록·상세와 마지막 실행 참조 |
| POST /projects/{id}/runs | 실행을 영속 큐에 등록하고 202 반환 |
| GET /runs/{id}, POST /runs/{id}/cancel | 실행 상태·진행 요약·게시 보고서 참조 조회, 실행 취소 요청 |
| GET /runs/{id}/report, GET /runs/{id}/reports | 선택 리비전의 JSON·Markdown·HTML과 리비전 목록. 기본은 코드 제외 |

### 후속 설계

W22에서 프로젝트 예약(`/projects/{id}/schedules`)과 실행 SSE(`GET /runs/{id}/events`)를 구현했다. 프로젝트 설정 변경·연결 진단·브랜치 조회, 같은 입력 retry, 개별 task·log 조회, finding 이력·사람 판정, 풀·노드 관리 웹 API는 후속 작업이다. R0의 풀·노드 관리는 운영자 CLI를 사용한다. 보고서 내보내기는 위의 게시 리비전 GET 경로로 수행한다. [근거와 게시 계약](31-w09-report.md)

## 3. 작업 요청

```json
{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}
```

클라이언트는 raw shell 명령이나 arbitrary credential를 run 요청에 넣지 못한다. 프로젝트 관리자가 저장한 승인 policy revision을 서버가 해석한다. POST 응답은 run ID·status·version·URL을 반환한다. 같은 Idempotency-Key와 body는 같은 run, 같은 key 다른 body는 409다.

수동 새로 분석과 같은 입력 재시도는 분리한다. cancel 재호출은 멱등적이며 이미 terminal인 경우 현재 상태를 반환한다. cancel 응답이 202라고 실제 프로세스가 끝난 것은 아니다.

## 4. 노드 프로토콜

W06 구현 경로(`/api/v1` 아래, 개인 모드 loopback controller). 노드 경로는 `Authorization: Bearer <node_secret>`만 받으며 cookie를 읽지 않고 CSRF를 쓰지 않는다. 브라우저가 보내는 `Origin` header가 있으면 403이다. 세부 필드는 [OpenAPI](../api/openapi.yaml), 결정과 시험은 [W06 기록](28-w06-node-protocol.md)을 따른다.

| 경로 | 계약 |
|---|---|
| POST /nodes/enroll | 운영자 CLI가 발급한 만료·1회용·철회 가능 등록 토큰(pool·승인 source root에 묶임)→node_secret 1회 반환. 서버는 hash만 저장 |
| POST /nodes/{id}/heartbeat | observed workers/capabilities/capacity(bytes)/`last_command_sequence`→cursor 이후 명령. 보고 안 된 워커는 stopped. cleanup_reports의 confirmed만 정리 확인으로 반영 |
| POST /nodes/{id}/claims | 마지막 heartbeat에서 idle인 worker ID와 `Idempotency-Key`로 요청. 서버가 전역 상한·heartbeat 신선도·능력·노드 root 허가·요청자 권한/grant·snapshot 위치·예약 여유를 한 트랜잭션에서 검증하고 attempt·예약·epoch·이벤트를 함께 기록. 응답은 `{lease, job}` |
| POST /attempts/{id}/heartbeat | node·worker·epoch와 DB 시각으로 갱신, `cancel_requested` 반환 |
| POST /attempts/{id}/events | `client_event_id` 멱등. snapshot_fixed(업로드된 source_manifest 필요)·task_created(key 재사용)·task_status·run_advance. 응답에 run event sequence·run_version |
| PUT /attempts/{id}/artifacts/{client_artifact_id} | 크기·SHA-256 header와 bytes 대조 후 controller 저장소에 내구 저장, finalized 행 기록. 동일 재전송은 같은 201 |
| POST /attempts/{id}/complete | epoch·정리 상태·result_manifest artifact 확인 후 결과 접수. 접수된 성공은 finalizing을 거쳐 W09 게시 트랜잭션에서 종결 |
| POST /attempts/{id}/cleanup | recovery_required attempt의 실제 정리 확인→failed, 예약 해제 |

초기 설계의 `POST /attempts/{id}/snapshot`과 3단계 artifact upload(`/artifacts/uploads...`)는 W06에서 각각 `snapshot_fixed` 이벤트와 attempt 범위 단일 PUT으로 대체했다(결정 A18). worker에게 node secret을 주지 않고 agent가 중계한다. node identity·attempt의 owner·epoch를 모두 검증한다. 명령은 노드별 sequence와 command_id를 가지며 cursor 이하 재전송은 확인 처리되어 중복 실행으로 이어지지 않는다.

## 5. 프로젝트 진행 조회와 이벤트 복원

R0의 `GET /projects`와 `GET /projects/{id}`는 `last_run`(ID·상태 또는 null)을 제공한다. `GET /runs/{id}`는 실행 상태, task 종료 수, 가용한 전체 수와 대기 사유를 제공한다. R0의 `progress.stage`는 run 상태이며 활성 분석 역할을 식별하지 않는다.

W15 설계에서는 서버가 목록의 각 프로젝트에 대표 run 참조와 단계 요약을, 실행 상세에 동일 집계의 단계별 상태·종료 수·계획 수·제외/실패 사유·마지막 확인 시각을 추가한다. 기존 `last_run`의 의미와 `progress.stage`는 유지하고 세부 단계는 선택 필드로 확장한다. 프로젝트 목록 응답에 요약을 담아 프로젝트마다 상세를 따로 요청할 필요가 없게 한다. 권한 없는 프로젝트의 run ID·단계·사유도 반환하지 않는다.

근거는 현재 attempt의 수용된 task 전이, 소스 취득·snapshot 기록, 서버의 보고서 게시 기록이다. 단계 시작·종료 기록은 run ID, attempt와 epoch, 적용 계획(있을 때), 단계 키, 순번에 묶는다. 동일 이벤트 재전송은 한 번만 반영하고 오래된 epoch는 제외한다. 보고서 생성 완료는 해당 리비전의 게시가 확정됐을 때 표시한다. 구버전 데이터는 세부 정보 부재를 허용한다. [표시 규칙과 예시](12-report-and-ui.md)

**W15 구현 필드.** 프로젝트 목록·상세·생성 응답에 선택 객체 `activity`를 둔다. 필드는 `run`(`id`, `status`, `requested_at` 또는 null), `other_active_runs`, `detail`(`recorded`·`not_recorded`, 실행이 없으면 null), `stages`, `waiting_reason`(대표 run이 대기 중일 때만), `last_report`(`run_id`, `revision`, `published_at` 또는 null), `last_event_at`, `checked_at`이다. `GET /runs/{id}`의 `progress`에는 같은 의미의 `detail`, `stages`, `last_event_at`, `checked_at`을 더한다. 단계 항목은 `key`, `state`, `planned`(계획 확정 시에만 숫자), `finished`, `succeeded`, `counts`, `reasons`, `started_at`, `finished_at`이다. 노드 이벤트 `task_created`는 선택 필드 `stage`를 받고 알 수 없는 값은 422로 거부한다. 재시도에서 NULL 단계를 처음 기록하면 run 이벤트 `task.stage_recorded`를 남긴다. 형식은 [OpenAPI](../api/openapi.yaml)와 [예시](../examples/project-activity.json), 판정 규칙은 [W15 기록](37-w15-discovery-and-progress.md)을 따른다.

### W22 SSE

이벤트 이름은 저장된 run 이벤트 type(`task.status`, `task.created`, `run.status` 등)이고 `id`는 run 안의 sequence다. 연결 유지는 이름 있는 `heartbeat` 이벤트로 보낸다. 브라우저가 SSE 주석을 전달하지 않기 때문이다. 형식은 [예시](../examples/run-events.txt)와 [W22 기록](38-w22-schedules-progress-sse.md)을 따른다. sequence는 run 안에서 단조 증가하며 event와 DB 상태는 같은 트랜잭션으로 기록한다. UI는 첫 조회로 상태를 받고 cursor 이후 이벤트를 적용한다. sequence gap 또는 보관 기간 만료 시 전체 상태를 다시 조회한다. event_id를 DB 행 ID와 임의 혼용하지 않는다.

heartbeat 이벤트는 연결 유지용이며 분석 진행률을 올리지 않는다. 로그는 byte/record cursor로 별도 가져오고 거대한 로그를 상태 이벤트에 넣지 않는다. 권한 변경·로그아웃 시 SSE 연결을 해제한다. polling fallback은 같은 version 기반 API를 쓴다.

## 6. 오류 모델

```json
{"error":{"code":"STALE_ATTEMPT","message":"이 실행 시도의 소유권이 만료되었습니다.","retryable":false,"request_id":"req_demo","details":{}}}
```

400 입력, 401 인증, 403 권한, 404 비노출 자원, 409 revision/멱등성/fencing 충돌, 413 크기, 422 의미 제약, 429 한도, 503 일시 unavailable을 구분한다. 다른 사용자의 경로·저장소 존재를 오류로 유출하지 않는다. 공개 오류에는 내부 stack·토큰·절대 경로를 넣지 않는다.

## 7. 버전과 호환

enum을 추가하면 구버전 UI가 unknown 상태를 안전하게 표시해야 한다. 필수 필드나 의미 변경은 API/schema 버전 정책을 따른다. 노드 enrollment 시 protocol range를 협상하고 호환하지 않으면 작업을 주지 않는다. 모호한 버전 자동 추정으로 실제 API 호출을 반복하지 않는다.
