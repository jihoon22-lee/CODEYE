# 11. API와 이벤트 계약

W01 doctor/W02 migrate는 로컬 CLI다. W03에서 세션·프로젝트·run 요청/조회/취소·readiness와 정적 웹 호스팅을 구현했다. [OpenAPI](../api/openapi.yaml)의 `x-codeye-status: implemented-w03` 표시와 실제 route를 자동 대조한다. 나머지 endpoint는 설계 계약이며 [W03 구현 기록](24-w03-api.md)을 따른다.

## 1. 공통 규칙

기본 prefix는 `/api/v1`이다. JSON은 UTF-8, 시각은 ISO 8601 UTC, 용량은 bytes, 시간 제한은 seconds를 쓴다. list는 cursor/limit, mutation은 인증·프로젝트 ACL·필요 시 If-Match version을 적용한다. run 생성과 외부 작업 명령은 Idempotency-Key를 지원한다.

개인 loopback 모드에도 임의 로컬 웹사이트가 API를 호출하지 못하도록 launch secret/session, Origin 검증, CSRF 정책을 둔다. LAN 배포는 인증 필수이며 기본 CORS는 same-origin이다. 노드 API는 사용자 세션과 별도의 node-scoped token을 사용한다. 토큰을 query parameter에 넣지 않는다.

## 2. 사용자 API

| Method / Path | 의미 |
|---|---|
| GET /health, /ready | liveness와 DB/필수 설정 readiness 분리 |
| POST /projects | 최소 입력으로 등록·접근 진단 |
| GET /projects | 권한 있는 프로젝트와 현재/마지막 실행 상태 |
| GET/PATCH /projects/{id} | 설정 조회·revision 생성 |
| POST /projects/{id}/diagnostics | 소스·환경·모델 연결 진단. 무단 빌드 없음 |
| GET /projects/{id}/branches | provider에서 브랜치 조회 |
| POST /projects/{id}/runs | durable enqueue 후 202 |
| GET /runs/{id} | 실행·attempt·scope·progress·version |
| POST /runs/{id}/cancel | 취소 요청 후 202, 실제 종료는 상태로 확인 |
| POST /runs/{id}/retry | 같은 입력 새 attempt 또는 거부 사유 |
| GET /runs/{id}/tasks | task별 상태·도구·소요·reason |
| GET /runs/{id}/events | SSE, Last-Event-ID 복원 |
| GET /runs/{id}/logs | task/stream별 cursor 조회·민감값 마스킹 |
| GET /runs/{id}/report | 선택 revision 구조화 보고서 |
| GET /runs/{id}/findings | 필터·정렬·페이지 |
| GET /findings/{id} | 발생 이력·근거·사람 검토 |
| PATCH /findings/{id}/disposition | version 검사 후 검토 기록 추가 |
| POST /runs/{id}/exports | 형식·코드 포함 여부를 검사해 export 생성 |
| GET/POST/PATCH /projects/{id}/schedules | 시간대·overlap·misfire 정책 |
| GET/PATCH /admin/pools/{id} | min/max·동시 한도·revision·degraded 이유 |
| GET /admin/nodes | 능력·health·워커·자원·강제 한도 수준 |

직접 API에 필드를 추가하는 CRUD는 구현 시 OpenAPI를 함께 확장한다. [핵심 OpenAPI](../api/openapi.yaml)는 프로젝트·실행·취소·보고서·이벤트·노드 heartbeat/claim/완료의 수직 경로를 기계 판독 가능하게 정의한다.

## 3. 작업 요청

```json
{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}
```

클라이언트는 raw shell 명령이나 arbitrary credential를 run 요청에 넣지 못한다. 프로젝트 관리자가 저장한 승인 policy revision을 서버가 해석한다. POST 응답은 run ID·status·version·URL을 반환한다. 같은 Idempotency-Key와 body는 같은 run, 같은 key 다른 body는 409다.

수동 새로 분석과 같은 입력 재시도는 분리한다. cancel 재호출은 멱등적이며 이미 terminal인 경우 현재 상태를 반환한다. cancel 응답이 202라고 실제 프로세스가 끝난 것은 아니다.

## 4. 노드 프로토콜

| 경로 | 계약 |
|---|---|
| POST /nodes/enroll | 만료·1회용 enrollment token→node credential |
| POST /nodes/{id}/heartbeat | observed workers/capabilities/capacity/last command cursor→desired commands |
| POST /nodes/{id}/claims | agent가 ready worker ID로 작업 요청; 서버가 권한·능력·자원 검증 |
| POST /attempts/{id}/heartbeat | lease epoch로 갱신, cancel desired state 반환 |
| POST /attempts/{id}/events | client_event_id로 중복 제거, 서버 sequence 발급 |
| POST /attempts/{id}/snapshot | 최초 snapshot 확정, 동일 digest 재전송 허용 |
| POST /attempts/{id}/complete | epoch·프로세스 정리·artifact 검증 후 결과 접수 |
| POST /artifacts/uploads | scope·예상 hash/size/MIME에 묶인 임시 업로드 생성 |
| PUT /artifacts/uploads/{id} | 제한된 bytes/재전송 |
| POST /artifacts/uploads/{id}/finalize | size/hash 검사, immutable artifact 전환 |

worker에게 직접 무기한 전체 노드 토큰을 주지 않는다. agent가 중계하거나 attempt-scoped token을 발급한다. node identity·attempt의 owner·epoch를 모두 검증한다. 서버 재시작 뒤 command_id 재전송은 새 워커 중복 생성으로 이어지지 않아야 한다.

## 5. 이벤트와 화면 복원

```text
id: 142
 event: task.updated
 data: {"run_id":"run_demo","sequence":142,"version":19,"task_id":"task_typecheck","status":"running"}
```

실제 SSE에서는 event/data 줄 앞의 공백 없이 직렬화한다. sequence는 run 안에서 단조 증가하며 event와 DB 상태는 같은 트랜잭션으로 기록한다. UI는 첫 조회로 상태를 받고 cursor 이후 이벤트를 적용한다. sequence gap 또는 보관 기간 만료 시 전체 상태를 다시 조회한다. event_id를 DB 행 ID와 임의 혼용하지 않는다.

heartbeat 이벤트는 연결 유지용이며 분석 진행률을 올리지 않는다. 로그는 byte/record cursor로 별도 가져오고 거대한 로그를 상태 이벤트에 넣지 않는다. 권한 변경·로그아웃 시 SSE 연결을 해제한다. polling fallback은 같은 version 기반 API를 쓴다.

## 6. 오류 모델

```json
{"error":{"code":"STALE_ATTEMPT","message":"이 실행 시도의 소유권이 만료되었습니다.","retryable":false,"request_id":"req_demo","details":{}}}
```

400 입력, 401 인증, 403 권한, 404 비노출 자원, 409 revision/멱등성/fencing 충돌, 413 크기, 422 의미 제약, 429 한도, 503 일시 unavailable을 구분한다. 다른 사용자의 경로·저장소 존재를 오류로 유출하지 않는다. 공개 오류에는 내부 stack·토큰·절대 경로를 넣지 않는다.

## 7. 버전과 호환

enum을 추가하면 구버전 UI가 unknown 상태를 안전하게 표시해야 한다. 필수 필드나 의미 변경은 API/schema 버전 정책을 따른다. 노드 enrollment 시 protocol range를 협상하고 호환하지 않으면 작업을 주지 않는다. 모호한 버전 자동 추정으로 실제 API 호출을 반복하지 않는다.
