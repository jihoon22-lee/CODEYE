# HTTP API와 이벤트

관리 서버(`codeye serve`, 개인 모드 `codeye up`·`codeye start`의 controller)가 제공하는 HTTP API와 실행 이벤트 스트림의 규칙이다. 필드와 응답 형식의 기계 판독 기준은 [`api/openapi.yaml`](../api/openapi.yaml)이다. 이 문서는 필드를 되풀이하지 않고 OpenAPI가 표현하지 못하는 규칙을 적는다. 보고서 내용·비교 상태·판정 적용 규칙과 화면은 [보고서와 화면](REPORT.md), 노드 프로토콜의 lease·fencing·완료 절차는 [실행](EXECUTION.md), 위협 모델과 비밀정보 정책은 [보안](SECURITY.md)에 있다.

## 구현 범위

- OpenAPI operation의 `x-codeye-status`가 `implemented`이면 서버가 실제로 제공한다. 그 밖의 operation은 설계 계약(미구현)이다. 현재 미구현 operation은 풀 관리(`/api/v1/admin/pools/{pool_id}`)와 Git webhook 수신(`/api/v1/providers/{provider_id}/webhook`)이다.
- 서버가 등록한 라우트와 OpenAPI의 구현 operation이 같은지 [`internal/httpapi/contract_test.go`](../internal/httpapi/contract_test.go)가 확인한다. 라우트 표는 [`internal/httpapi/api.go`](../internal/httpapi/api.go)와 [`node.go`](../internal/httpapi/node.go)에 있다.
- 사용자 인증은 세션 cookie뿐이며, 세션은 일회용 로그인 링크나 사용자 이름·비밀번호로 만든다. 사용자 Bearer 토큰(`UserBearer`)은 구현하지 않았다. loopback이 아닌 주소는 TLS로만 받는다([보안](SECURITY.md#네트워크와-브라우저)). 계정·구성원·감사 기록은 웹 화면과 CLI에서 다룬다.
- 설계 계약(미구현) 사용자 API: 프로젝트 설정 변경(분석 선택 `PUT /projects/{id}/analysis`만 구현), 연결 진단·브랜치 조회, 개별 task·로그 조회(로그는 상태 이벤트와 별도의 byte/record cursor로 가져오는 설계), 첫 발견 시각 이력, 판정 일괄 적용, 풀·노드 관리 웹 API. 풀·노드 관리는 운영자 CLI를 쓴다([운영](OPERATIONS.md)).

## 공통 규칙

| 항목 | 규칙 |
|---|---|
| 기본 경로 | `/api/v1`. 그 밖의 `/api` 경로는 404 |
| 본문 | JSON, UTF-8. 요청 `Content-Type`은 `application/json`(charset는 생략 또는 utf-8)만 받는다. 다르면 415 |
| 엄격한 해석 | 알 수 없는 필드, 중복 key, 형식이 틀린 JSON은 400 `INVALID_REQUEST` |
| 크기 | 사용자 요청 본문 최대 64 KiB, 넘으면 413 |
| 경로 ID | `{id}` 자리는 정규형 UUID여야 한다. 아니면 404(존재 여부와 구분하지 않음) |
| 시각 | ISO 8601 UTC. 판단에 쓰는 시각(세션 만료, 판정 만료, 예약 시각, 단계 시작·종료)은 DB 시각이다. |
| 단위 | 용량은 bytes, 제한시간은 seconds |
| 메서드 | 경로는 있으나 메서드가 다르면 405와 `Allow` 헤더 |
| 응답 헤더 | 모든 응답에 `X-Request-ID`(오류 envelope의 `request_id`와 같음), CSP `default-src 'self'` 계열, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy`·`Cross-Origin-Resource-Policy: same-origin`. API 응답은 `Cache-Control: no-store` |
| 정적 파일 | 같은 origin에서 빌드된 웹 앱을 제공한다. SPA fallback이 있고 symlink·`..` 탈출은 404, 디렉터리 목록은 없다. 웹 디렉터리가 없으면 API만 동작하고 다른 경로는 503이다. |

## 인증

클라이언트가 인증하는 절차와 헤더를 적는다. 이 보호가 막는 위협, 쿠키·링크 파일의 운영 경계, 로그인 제한과 비밀번호 저장 방식은 [보안](SECURITY.md)에 있다. listen 주소는 IP와 0이 아닌 포트여야 하며, loopback이 아니면 TLS 설정(`--tls-cert`, `--tls-key`, `--public-origin`)이 모두 있어야 서버가 시작한다.

### 일회용 로그인 링크

- `codeye start`, `codeye serve --user`, `codeye login-link`가 일회용 launch token(32 byte 난수, base64url 43자, 10분, 한 번만 교환)을 발급해 `<origin>/#launch=<token>` 링크를 0600 파일에 쓴다. 개인 모드의 `codeye up`·`codeye open`은 같은 링크를 터미널에 출력하고 브라우저로 연다(A38). DB에는 SHA-256만 저장한다. CLI 사용법은 [사용 안내](GUIDE.md)에 있다.
- `codeye up PATH`의 링크는 토큰 뒤에 깊은 링크 `&project=<ID>&run=<ID>`를 붙인다. 웹 앱은 토큰을 지울 때 이 두 키(canonical UUID)만 fragment에 남기고 그 프로젝트와 실행을 연다.
- 관리자 API의 사용자 생성과 비밀번호 재설정도 같은 링크를 응답의 `login_link`로 한 번 준다([계정과 관리자](#계정과-관리자)). origin은 TLS 서버면 public origin, 아니면 요청의 loopback origin이다.
- 토큰은 URL fragment에 있으므로 HTTP 요청·서버 로그에 실리지 않는다. 웹 앱은 fragment를 읽은 즉시 주소창에서 지우고 `POST /session`으로 교환한다.
- 세션 수명은 12시간이다. 개인 모드 controller(`codeye serve --personal`, `up`·`start`가 띄운다)는 7일이다(저장 계층의 상한과 같다).

### 세션

- `POST /session`은 `{launch_token}` 또는 `{username, password}` 중 하나만 받는다. 섞거나 일부만 보내면 400이다.
  - launch token: 토큰을 소비하고 같은 트랜잭션에서 세션을 만든다. 잘못된·사용한·만료된 토큰은 모두 401 `launch token is invalid, used or expired`다. Host·Origin 검사에 걸린 요청은 토큰을 소비하지 않는다. 교환의 잠금 순서는 user(FOR SHARE)→token→session이고, 사용자 비활성화(user FOR UPDATE→세션 폐기)와 직렬화되므로 둘이 경합해도 유효한 세션이 남지 않는다.
  - 비밀번호: 사용자 이름은 대소문자를 구분하지 않는다. 없는 사용자·틀린 비밀번호·비활성·비밀번호 없는 사용자는 모두 같은 401 `username or password is incorrect`다. 실패가 한도를 넘으면 429 `RATE_LIMITED`이고 `Retry-After` 헤더와 `details.retry_after_seconds`가 같은 초다. 429 동안은 맞는 비밀번호도 거절한다. 서버의 KDF 계산 자리가 3초 안에 나지 않으면 503 `UNAVAILABLE`이고 시도로 세지 않는다.
- 응답은 201 [`SessionView`](../api/openapi.yaml)(`user_id`, `csrf_token`, `expires_at`, `username`, `display_name`, `is_admin`, `must_change_password`, `has_password`, `password_reset`)와 세션 cookie다. `password_reset`은 관리자가 한 가장 최근의 비밀번호 재설정(재설정 뒤 30일 동안, 아니면 null)이며 `reset_at`, `reset_by_user_id`, `reset_by_username`을 담는다. 로그인 횟수와 무관하게 30일 동안 나오고 지우는 경로는 없다.
- cookie는 평문 loopback에서 `codeye_session`, TLS에서 `__Host-codeye_session`(Secure)이다. HttpOnly, SameSite=Strict, Path=/, 절대 만료 12시간이며 sliding 연장은 없다. 만료는 DB 시각으로 판단한다. TLS 서버는 평문 이름의 cookie를 읽지 않는다.
- `GET /session`은 같은 형태의 현재 세션을 준다. `DELETE /session`은 세션을 폐기하고 cookie를 지운다(204, CSRF 필요). 사용자 비활성화(`codeye user disable`, `POST /admin/users/{id}/disable`)와 비밀번호 재설정은 그 사용자의 모든 세션을 폐기한다.

### 비밀번호 변경

- `POST /session/password` `{current_password?, new_password}`(CSRF 필요). 비밀번호가 있는 사용자(`has_password: true`)는 `current_password`가 필수다(없으면 400). 비밀번호가 없는 사용자는 세션만으로 처음 정한다.
- 새 비밀번호가 규칙(12~256자, 사용자 이름·표시 이름과 다름, 한 문자 반복 아님)을 어기면 422 `VALIDATION_FAILED`이고 시도로 세지 않는다. 현재 비밀번호가 틀리면 403 `PASSWORD_INCORRECT`이고 로그인 실패 한도에 센다(넘으면 429). 사용자 이름이 없는 사용자, 확인 뒤 비밀번호가 바뀐 경우는 409다.
- 성공하면 200 `SessionView`(`must_change_password: false`, `has_password: true`)이고 그 사용자의 다른 세션과 쓰지 않은 로그인 링크는 모두 끝난다. 이 세션과 cookie는 그대로다.
- `must_change_password`가 true인 세션은 `GET/DELETE /session`과 `POST /session/password`만 쓸 수 있다. 다른 사용자 경로(이벤트 스트림 포함)는 403 `PASSWORD_CHANGE_REQUIRED`다.

### CSRF

`X-CODEYE-CSRF` 값은 base64url(SHA-256(`"codeye-csrf\0"` + 세션 토큰))이다. 세션 토큰을 가진 서버만 계산할 수 있고 `/session` 응답의 `csrf_token`으로 전달한다. 웹 앱은 이 값을 메모리에만 둔다. `POST /session`을 제외한 모든 상태 변경 요청(POST·PATCH·PUT·DELETE)에 필수이며 없거나 틀리면 403 `CSRF_REJECTED`다.

### Host와 Origin

- 평문 loopback: `Host`는 `127.0.0.1:<port>`, `localhost:<port>`, `[::1]:<port>`만 허용한다. 상태 변경 요청은 `Origin`이 `http://` + Host와 정확히 같아야 한다.
- TLS: `Host`는 public origin의 host(기본 포트 443이면 `host`와 `host:443`)만 허용하고, 상태 변경 요청의 `Origin`은 public origin과 정확히 같아야 한다. 모든 응답에 `Strict-Transport-Security: max-age=31536000`을 보낸다.
- Host 위반은 정적 파일을 포함한 모든 요청에서 403 `HOST_REJECTED`, Origin 위반과 `same-origin`이 아닌 `Sec-Fetch-Site`는 403 `ORIGIN_REJECTED`다. `POST /session`도 Origin 검사를 받는다.
- CORS 허용 헤더를 보내지 않는다(same-origin만).

### 관리자와 비밀번호 변경 대기

- 관리자 경로(`/admin/…`, `GET /audit`)는 세션 사용자가 활성 관리자여야 한다. 아니면 403 `FORBIDDEN`이다. 저장 계층이 같은 트랜잭션에서 다시 확인한다.
- 관리자 역할은 프로젝트 권한을 주지 않는다. 관리자도 구성원이 아닌 프로젝트는 404다.
- 검사 순서는 Host → Origin(상태 변경) → 세션 cookie → CSRF(상태 변경) → 비밀번호 변경 대기 → 관리자 → 본문 형식 → 처리다.

### 노드 경로

노드 경로는 `Authorization: Bearer <node_secret>`만 받는다. cookie를 읽지 않고 CSRF를 쓰지 않는다. `Origin` 헤더가 있으면 403 `ORIGIN_REJECTED`다. 토큰을 query parameter에 넣지 않는다. 사용자 경로는 Bearer를 받지 않는다. 자세한 규칙은 [노드 API 요약](#노드-api-요약)에 있다.

## 오류

오류 응답은 `{"error":{"code","message","retryable","request_id","details"}}`다. `message`는 고정 문구이며 SQL·DSN·경로·토큰·stack을 넣지 않는다. 다른 사용자의 경로·저장소 존재는 오류로 드러내지 않는다.

| 상태 | code |
|---|---|
| 400 | `INVALID_REQUEST`: JSON 형식, 알 수 없는 필드, 중복 key, `Idempotency-Key`·`If-Match`·cursor·query 형식 오류, 알 수 없는 query parameter |
| 401 | `UNAUTHENTICATED`. 노드 경로는 `NODE_UNAUTHENTICATED` |
| 403 | `FORBIDDEN`(구성원이지만 역할 부족, 관리자 아님), `HOST_REJECTED`, `ORIGIN_REJECTED`, `CSRF_REJECTED`, `PASSWORD_CHANGE_REQUIRED`, `PASSWORD_INCORRECT` |
| 404 | `NOT_FOUND`: 없음·비구성원·허가 없는 source root·없는 provider·UUID 아님을 구분하지 않는다. |
| 405 | `METHOD_NOT_ALLOWED` |
| 409 | `IDEMPOTENCY_CONFLICT`(같은 키, 다른 요청), `CONFLICT`(version 불일치 등 현재 상태와 충돌), `STALE_ATTEMPT`(노드 fencing) |
| 411 | `LENGTH_REQUIRED`(노드 산출물 업로드) |
| 413 | `PAYLOAD_TOO_LARGE` |
| 415 | `UNSUPPORTED_MEDIA_TYPE` |
| 429 | `RATE_LIMITED`(`retryable=true`, `Retry-After`, `details.retry_after_seconds`): 로그인과 비밀번호 변경의 실패 한도 |
| 422 | `VALIDATION_FAILED`(의미 제약), `SOURCE_UNSUPPORTED`(ssh·scp·`git://`), `ATTACHMENTS_UNSUPPORTED`, 노드 `INTEGRITY_MISMATCH`·`PROTOCOL_UNSUPPORTED` |
| 500 | `INTERNAL`, `REPORT_INVALID`(저장된 보고서가 검증에 실패) |
| 503 | `UNAVAILABLE`(`retryable=true`). DB 오류 뒤에는 202를 반환하지 않는다. 이벤트 스트림 상한 초과도 503이다. |

- 프로젝트 구성원이 아니면 403이 아니라 404다(존재 비노출). 관리자도 같다. 구성원의 역할 부족만 403이다. 권한이 철회되면 그 뒤 요청은 404다.
- 상태를 바꾼 요청의 감사 기록을 쓰지 못하면 변경은 커밋되지 않고 503이다.

## 멱등성과 동시 변경

| 대상 | 헤더 | 규칙 |
|---|---|---|
| 실행 요청 `POST /projects/{id}/runs` | `Idempotency-Key` 필수 | 1..200자의 보이는 ASCII. 같은 사용자·프로젝트에서 같은 키와 같은 본문은 같은 run, 다른 본문은 409 `IDEMPOTENCY_CONFLICT`. 서버·DB 재시작 뒤에도 유지된다. |
| 예약 생성 | `Idempotency-Key` 필수 | 같은 키와 같은 정규화 정의는 같은 예약, 다른 정의는 409 `IDEMPOTENCY_CONFLICT` |
| 예약 변경·삭제 | `If-Match: "N"` 필수 | N은 예약 version(`ETag`). 불일치 409 `CONFLICT`, 헤더 없음·형식 오류 400 |
| 판정 기록 | `Idempotency-Key` 필수, 그리고 `If-Match: "N"`(현재 판정 version) 또는 `If-None-Match: *`(첫 판정) 중 하나만 | 같은 키·같은 요청 재전송은 같은 행 200, 다른 요청은 409 `IDEMPOTENCY_CONFLICT`, version 불일치 409 `CONFLICT`, 전제 조건 헤더 없음 400 |
| 노드 요청 | 경로별 | claim의 `Idempotency-Key`, attempt 이벤트의 `client_event_id`, 산출물의 `client_artifact_id`([노드 API 요약](#노드-api-요약)) |

`If-Match`는 `"N"`(강한 ETag)과 따옴표 없는 N을 모두 받는다. 웹 앱은 응답을 잃은 뒤 다시 보낼 때 같은 키를 다시 쓴다.

## 목록과 pagination

- `GET /projects`: `?cursor&limit`(1..100, 기본 20), 최신순 keyset. 응답은 `{items, next_cursor}`이고 마지막 페이지의 `next_cursor`는 null이다. 형식이 틀린 cursor는 400이다.
- `GET /projects/{id}/finding-reviews`: `?cursor&limit`(1..200, 기본 50). cursor는 fp2 fingerprint이며 `fingerprint`와 함께 쓸 수 없다.
- `GET /admin/users`: `?cursor&limit`(1..200, 기본 50), 생성순 keyset. cursor는 앞 페이지의 `next_cursor`(사용자 ID)다.
- `GET /audit`, `GET /projects/{id}/audit`: `?since&until&actor&action&cursor&limit`(`/audit`은 `project`도). 최신순(id 내림차순) keyset이고 cursor는 앞 페이지의 `next_cursor`(행 id 문자열)다. `since`는 포함, `until`은 제외하는 RFC 3339 시각이다. 모르는 action·형식 오류는 400이다.
- `GET /source-roots`, `GET /providers`, `GET /projects/{id}/schedules`, `GET /runs/{id}/reports`, `GET /projects/{id}/members`, `GET /admin/source-roots`, `GET /admin/providers`는 `{items, …}` 객체를 준다. 맨 배열을 반환하는 경로는 없다.

## 자원 개요

권한 열의 "구성원"은 프로젝트 소유자와 manager·runner·viewer 역할이다. 소유자는 모든 프로젝트 권한을 갖는다. 관리자는 관리자 경로만 더 쓸 수 있고 프로젝트 권한은 없다.

| 메서드·경로 | 권한 | 결과 |
|---|---|---|
| `GET /health` | 없음 | 프로세스 생존. DB readiness와 다르다. |
| `GET /ready` | 없음 | 200 `{status:"ready", schema_version}` 또는 503 `{status:"not_ready", reason}`. reason: `database_unavailable`, `schema_outdated`, `schema_newer`, `schema_inconsistent`. 읽기 전용이며 migration을 바꾸지 않는다. |
| `POST/GET/DELETE /session` | Origin / 세션 / 세션+CSRF | [세션](#세션) |
| `POST /session/password` | 세션+CSRF(변경 대기 세션 포함) | [비밀번호 변경](#비밀번호-변경) |
| `GET /source-roots` | 세션 | 이 사용자에게 허가된 root의 `{id, label}`만. 서버 경로는 보이지 않는다. |
| `GET /providers` | 세션 | `{id, kind(github·ghes), host}` |
| `POST /projects`, `GET /projects`, `GET /projects/{id}` | 세션(+CSRF) | 등록 201+`Location`(개인 모드에서 같은 폴더·저장소면 200과 그 프로젝트), 허가된 목록·상세. 항목마다 호출자의 `role`(`owner`·`manager`·`runner`·`viewer`), `can_run`, `can_manage`, `analysis` |
| `PUT /projects/{id}/analysis` | 소유자·manager(+CSRF) | 분석 선택을 바꾼 새 설정 revision. 200과 프로젝트([프로젝트 등록](#프로젝트-등록)) |
| `GET /setup` | 세션 | 첫 화면의 설치 정보([설치 정보](#설치-정보)) |
| `GET /projects/{id}/members`, `PUT/DELETE /projects/{id}/members/{uid}` | 소유자·manager | [구성원](#구성원) |
| `GET /projects/{id}/audit` | 소유자·manager | 그 프로젝트의 감사 기록([계정과 관리자](#계정과-관리자)) |
| `/admin/…`, `GET /audit` | 관리자 | [계정과 관리자](#계정과-관리자) |
| `POST /projects/{id}/runs` | 소유자·manager·runner | 202 `{id, status, version, url}`+`Location` |
| `GET /runs/{id}`, `GET /runs/{id}/events` | 구성원 | [실행 조회와 진행](#실행-조회와-진행), [실행 이벤트 스트림](#실행-이벤트-스트림) |
| `POST /runs/{id}/cancel` | 소유자·manager·runner | 상태를 바꿨으면 202, 이미 종료·취소 중이면 200(현재 상태) |
| `POST /runs/{id}/retry` | 소유자·manager·runner(+CSRF) | 202 `{id, status, version, url, retry}`+`Location`([다시 시도](#다시-시도)) |
| `GET /runs/{id}/report`, `GET /runs/{id}/reports` | 구성원 | [보고서 경로](#보고서-경로) |
| `GET /runs/{id}/comparison` | 구성원 | [비교와 판정 경로](#비교와-판정-경로) |
| `GET/POST /projects/{id}/finding-reviews` | 구성원 / 소유자·manager·runner | [비교와 판정 경로](#비교와-판정-경로) |
| `GET/POST /projects/{id}/schedules`, `PATCH/DELETE /projects/{id}/schedules/{sid}` | 구성원 / 소유자·manager | [예약](#예약) |
| `/nodes/…`, `/attempts/…` | 노드 Bearer | [노드 API 요약](#노드-api-요약) |

## 프로젝트 등록

- 본문은 `{name?, source | local_path, analysis?, description?, attachment_ids?}`다. `source`와 `local_path` 중 정확히 하나를 준다(둘 다·둘 다 없음은 400). 첨부는 아직 받지 않는다(`attachment_ids`가 비어 있지 않으면 422 `ATTACHMENTS_UNSUPPORTED`). 빈 이름은 422다.
- **local 소스**: 이 사용자에게 허가된 source root와 상대 경로. 경로는 `.`이거나, 절대 경로·드라이브·역슬래시·`..`·빈 구간이 없어야 한다. 허가 없는 root는 404다.
- **local_path(개인 모드)**: 절대 경로의 폴더. 서버가 symlink를 풀고 허가된 root 중 가장 긴 root 아래의 상대 경로로 바꾼다. 개인 모드는 홈 디렉터리를 기본 root(`home`)로 둔다. 없는 폴더는 422 `FOLDER_NOT_FOUND`, root 밖은 422 `FOLDER_NOT_READABLE`(터미널의 `codeye up <경로>`가 root를 더한다), 상대 경로로 쓸 수 없는 이름(`:` 등)은 422 `FOLDER_NAME_UNSUPPORTED`다. 서버 모드는 422 `LOCAL_PATH_UNSUPPORTED`다.
- **CODEYE 데이터 폴더(개인 모드)**: 프로젝트 폴더가 상태 디렉터리나 백업 디렉터리를 품거나 그 안에 있으면, `local_path`든 root와 상대 경로든 symlink를 푼 뒤 422 `FOLDER_HOLDS_CODEYE_DATA`다. `message`는 문제의 두 폴더를 적고 프로젝트가 든 하위 폴더를 고르라고 안내한다. 노드도 소스를 확보하기 전에 같은 검사를 하고 실패 사유는 `source_holds_codeye_data`다.
- **같은 소스(개인 모드)**: 같은 root와 경로, 또는 같은 provider와 저장소 주소(대소문자, 끝의 `/`·`.git` 무시)의 프로젝트가 이미 있으면 새로 만들지 않고 200과 그 프로젝트를 준다.
- **git 소스**: 승인된 provider와 `https://<provider host>/<owner>/<repo>[.git]`. 브랜치는 선택이며 `git check-ref-format`의 부분집합을 만족해야 한다. 비우면 소스를 확보할 때마다 저장소의 기본 브랜치(`git ls-remote --symref … HEAD`)를 쓰고 보고서 입력에 그 브랜치와 커밋을 남긴다. ssh·scp·`git://`는 422 `SOURCE_UNSUPPORTED`다. 비공개 Git 인증은 아직 없다. 인증을 요구하는 저장소(비공개이거나 없는 저장소)는 실행이 `source_auth_required`, 호스트에 닿지 못하면 `source_unreachable`로 실패한다.
- **analysis**: `{send_source_to_model, run_build_scripts, fetch_dependencies?}`. 앞의 둘은 있어야 한다(아니면 422 `VALIDATION_FAILED`). 없으면 소스 종류의 기본값이다. 로컬 폴더는 둘 다 `true`, Git 저장소는 `send_source_to_model: true`, `run_build_scripts: false`다. `fetch_dependencies`는 선택이고 없으면 `true`다(두 종류 모두). 설정에는 `false`일 때만 쓴다. 이 선택은 노드 운영자 스위치(`--dependency-fetch`)와 함께 모든 노드에서 의존성 받기를 정한다([의존성 준비](ARCHITECTURE.md#의존성-준비)). 설정의 `analysis`에 저장하고 응답의 `analysis`로 보인다. `--project-analysis-policy`로 띄운 노드(개인 모드)는 로컬·Git 모두 이 선택을 따른다. 그 밖의 노드는 운영자의 root별 허가(`--model-source-root`·`--repo-code-source-root`)를 따른다. `PUT /projects/{id}/analysis`가 바꾸고(설정의 나머지는 그대로, 새 revision, `project.revised` 감사), 그 뒤의 run이 새 선택을 따른다.
- 등록은 소스 위치의 선언이며 형식과 권한만 검사한다. 서버는 등록 시 파일 시스템·네트워크에 접근하지 않는다. 경로 정규화와 symlink·junction 경계는 워커가 소스를 확보할 때 검사한다([실행](EXECUTION.md)). 이름이 없으면 경로 마지막 구간, root label, 저장소 이름 순으로 정한다. 저장되는 설정 revision 1은 `{"config_version":1,"source":…,"description"?}`이다.
- 프로젝트 응답의 `source_status`는 등록만 확인한 소스이므로 `declared_unverified`다. `capability_gaps`는 `source_access_unverified`(항상), `execution_unavailable`(철회되지 않은 등록 노드가 없음), `report_unavailable`(게시된 보고서가 없음)이다. 실행별 배치 가능 여부는 대기 사유로 따로 보고한다.

## 실행 요청과 취소

요청 본문은 다음 형태다.

```json
{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}
```

- `profile`은 `deep-review`, `with-tests`, `focused`다. `scope.kind`는 `full` 또는 `paths`다. 형식은 OpenAPI가 정한다.
- 클라이언트는 raw shell 명령이나 임의 자격증명을 run 요청에 넣지 못한다. 프로젝트에 저장된 승인 설정을 서버가 해석한다.
- 요청은 영속 큐에 들어가고 202를 반환한다. 202는 실행이 시작됐다는 뜻이 아니다. 등록된 노드가 없으면 run은 `queued`에 머물고 `waiting_reason`이 `execution_unavailable`이다.
- 배정 시점(claim)에 서버가 요청자의 권한을 다시 확인한다.
- 수동 새로 분석과 다시 시도는 별도 동작이다([다시 시도](#다시-시도)).
- 취소 재호출은 멱등이며 이미 종료된 run은 현재 상태를 반환한다. 취소 응답이 202여도 실제 프로세스가 끝났다는 뜻이 아니다. 정리 확인 절차는 [실행](EXECUTION.md)에 있다.

### 다시 시도

`POST /runs/{id}/retry`는 끝난 run(`failed`·`cancelled`·`partial`·`completed`)을 다시 실행한다.

- `retry: "same_run"`: `failed`·`cancelled` run의 스냅샷을 철회되지 않은 노드가 갖고 있고, 모든 attempt의 정리가 확인됐고, 그 뒤 프로젝트 설정이 바뀌지 않았으면 그 run 자체를 같은 스냅샷으로 다시 큐에 넣는다. 응답의 `id`는 같은 run이다.
- `retry: "new_run"`: 그 밖에는 저장된 같은 요청으로 새 run을 만든다. 소스를 다시 확보한다(소스 확보 실패, 도구를 설치하거나 분석 선택을 바꾼 뒤의 부분 완료). 같은 run version에 대한 반복 요청은 같은 새 run을 준다. 응답과 `Location`은 새 run이다.
- 아직 끝나지 않았거나 `recovery_required`인 run은 409 `RUN_NOT_FINISHED`, viewer는 403이다. 어느 경우든 성공하면 `run.requested` 감사(`trigger: "retry"`)를 남긴다. 클라이언트는 응답의 run을 따라간다.

### 설치 정보

`GET /setup`은 첫 화면이 쓰는 `{mode, model, git, readable_folders, analysis_defaults, tools}`다([schema](../api/openapi.yaml)의 `Setup`).

- 개인 모드(`mode: "personal"`): `model`은 `{configured, id, probe}`(키 없음, probe는 `verified`·`failed`·`not_probed`), `git`은 `{available, hosts}`, `readable_folders`는 이 사용자에게 허가된 root의 절대 경로, `tools`는 `codeye up`이 찾은 도구(`<state_dir>/tools.json`, [schema](../schemas/tools.schema.json))다. 기록이 아직 없으면 `git`·`tools`가 `null`이다.
- 서버 모드(`mode: "server"`): controller는 노드마다 다른 이 정보를 모르므로 `model`·`git`·`tools`가 `null`이고 `readable_folders`가 비어 있다.
- `analysis_defaults`는 `{local, git}`의 기본 분석 선택이다.

## 실행 조회와 진행

`GET /runs/{id}`의 문서는 SSE `run.view` 이벤트와 같다. 주요 필드의 의미는 다음과 같다. 형식은 OpenAPI의 `RunView`와 [`examples/run-progress.json`](../examples/run-progress.json)을 따른다.

| 필드 | 규칙 |
|---|---|
| `status`, `version` | run 상태와 낙관적 version |
| `event_sequence` | 문서를 읽은 시점의 run 이벤트 순번. 이벤트 스트림의 cursor다. |
| `trigger` | `{kind:"manual"}` 또는 `{kind:"schedule", schedule_id, scheduled_for}` |
| `snapshot_id` | 고정된 snapshot이 없으면 null |
| `quality_verdict`, `report` | 최신 게시 리비전의 판정과 `{revision, status, sha256, url}`. 판정은 저장된 보고서에서만 읽으며 run 상태로 추측하지 않는다. |
| `progress.stage` | run 상태와 같다. 활성 분석 역할을 뜻하지 않는다. 세부 단계는 `progress.stages`다. |
| `progress.completed_tasks`, `total_tasks` | 종료 task 수(성공·실패·제외 포함)와 등록된 task 수. task가 등록되기 전에는 `total_tasks`가 null이다. |
| `progress.detail`, `stages`, `last_event_at`, `checked_at` | [단계 집계](#단계-집계). `checked_at`은 서버가 집계한 시각이다. |
| `progress.plan_revision`, `planned_weight`, `finished_weight`, `weight_basis`, `estimated_percent`, `plan_changes` | [계획 revision과 가중 진행률](#계획-revision과-가중-진행률) |
| `progress.waiting_reason` | [대기 사유](#대기-사유). `queued`일 때만 값이 있다. |

### 단계 집계

`progress.stages`와 프로젝트 `activity.stages`는 같은 함수([`internal/progress`](../internal/progress/progress.go))가 저장된 기록에서 계산한다. 단계 순서, 단계 상태(`plan_pending`, `no_tasks`, `waiting`, `running`, `succeeded`, `skipped`, `failed`, `cancelled`, `partial`)의 판정 조건, `source`·`report` 단계, 단계 기록과 재시도 규칙은 [아키텍처](ARCHITECTURE.md)에 있다. 응답에서 지켜야 할 것은 다음과 같다.

- `detail`은 `recorded` 또는 `not_recorded`다. 단계 키 없이 기록된 task가 있는 실행은 `not_recorded`이고 `stages`는 빈 배열이다. 클라이언트는 단계를 추정하지 않는다.
- 단계 항목은 `key`, `state`, `planned`, `finished`, `succeeded`, `counts`, `reasons`, `started_at`, `finished_at`이다.
  - `planned`는 계획이 확정된 뒤에만 숫자이고 `source`·`report`는 항상 null이다.
  - `finished`는 종료 task 수(성공·실패·제외 포함), `succeeded`는 그중 성공 수다.
  - `counts`는 0이 아닌 비성공 종료 상태별 건수(`failed`, `timed_out`, `cancelled`, `unsupported`, `skipped_policy`, `budget_exhausted`)다.
  - `reasons`는 종료 task의 사유 코드를 중복 제거·정렬한 목록이다.
  - 시각은 서버가 task 전이를 수용한 DB 시각이다.
- `key`와 `state`는 열린 문자열로 다룬다. 모르는 값을 성공으로 읽지 않는다.
- 노드 이벤트 `task_created`의 선택 필드 `stage`가 알 수 없는 값이면 422다.

### 대표 실행과 프로젝트 activity

`GET /projects`, `GET /projects/{id}`, `POST /projects` 응답의 각 프로젝트는 `activity`를 갖는다. 목록 응답에 요약이 들어 있어 프로젝트마다 상세를 따로 요청할 필요가 없다. 형식은 [`examples/project-activity.json`](../examples/project-activity.json)이고, 대표 run을 고르는 우선순위는 [아키텍처](ARCHITECTURE.md)에 있다.

- `run`은 대표 run의 `{id, status, requested_at}`이고 실행 기록이 없으면 null이다. 그때 `detail`도 null이고 `stages`는 빈 배열이다.
- `other_active_runs`는 대표가 아닌 활성 run 수다. `waiting_reason`은 대표 run이 `queued`일 때만 값이 있다.
- `last_report`는 프로젝트의 마지막 게시 보고서 `{run_id, revision, published_at}`이다. 화면은 이 run·리비전에 링크를 고정한다.
- `last_event_at`은 대표 run의 마지막 이벤트 시각, `checked_at`은 서버가 집계한 시각이다.
- 기존 `last_run`(가장 최근에 요청한 run의 ID·상태)은 의미를 바꾸지 않고 유지한다.
- 권한 없는 프로젝트의 run ID·단계·사유는 반환하지 않는다.

### 계획 revision과 가중 진행률

- 계획 확정은 현재 attempt가 run을 `running`으로 옮긴 이벤트다. 그 이벤트 순번 이전에 만든 task가 revision 1의 계획이다. 확정 전(`queued`, `preparing`, `running`에 도달하지 못한 종료)에는 `plan_revision`·`planned_weight`·`finished_weight`·`weight_basis`·`estimated_percent`가 null이고 `plan_changes`는 빈 배열이다.
- 확정 뒤 만든 task(생성 이벤트 순번이 더 큼)마다 revision이 1 오르고 `plan_changes`에 `revision`, `reason: task_added`, `stage`, `weight`, `planned_weight`(변경 뒤 분모), `recorded_at`(DB 시각)을 남긴다. 화면은 분모가 바뀐 이유를 이 목록으로 보여준다.
- `estimated_percent = 종료된 task 가중치 / 현재 계획 가중치 × 100`(소수 첫째 자리 반올림)이다. 종료는 성공·실패·제외·취소 등 모든 종료 상태다. 분모가 0이면 null이다.
- 가중치는 워커가 task를 만들 때 선택 필드 `weight`(1..1000)로 선언한 값이며 없으면 1이다. `weight_basis`는 `task_count`(선언 없음, 작업 수 비율), `declared`(모두 선언), `mixed`(일부 선언)다. 같은 key 재사용에서 다른 가중치는 409, 범위 밖은 422다. 저장값이 없을 때 처음 선언하면 `task.weight_recorded` 이벤트를 남긴다. 현재 제품 handler는 가중치를 선언하지 않으므로 실제 run의 `weight_basis`는 `task_count`다.
- heartbeat, 모델 토큰, 경과 시간은 근거가 아니다. 보고서 게시(`report` 단계)는 퍼센트에 들지 않는다.
- 재시도한 run은 새 attempt가 `running`에 도달하면 revision 1부터 다시 센다.
- 잔여 시간 추정은 제공하지 않는다.

### 대기 사유

`progress.waiting_reason`은 `queued`일 때만 값이 있고, 저장된 노드·능력·허가·snapshot 위치·한도에서 읽을 때마다 계산한다. 예측하지 않는다. 값은 `request_unavailable`, `execution_unavailable`, `snapshot_unavailable`, `node_stale`, `no_capable_node`, `capacity_full`, `project_limit`, `pool_paused`, `storage_quota_exceeded`이고, 호환 노드에 여유가 있으면 null이다. 검사 순서와 각 조건은 [실행](EXECUTION.md)에 있다. 대기 사유는 이벤트를 만들지 않으므로 클라이언트가 다시 읽어야 바뀐 값을 본다.

## 실행 이벤트 스트림

`GET /runs/{id}/events`는 그 run의 저장된 이벤트를 `text/event-stream`으로 보낸다. 상태의 원천은 `GET /runs/{id}`와 `run.view` 문서이고, 이벤트 행은 무효화 신호다. 예시는 [`examples/run-events.txt`](../examples/run-events.txt)다.

### 메시지

```text
retry: 3000

id: 22
data: {"run_id":"…","sequence":22,"version":14,"type":"task.created","at":"…","task_id":"…","key":"review.investigate","required":true,"stage":"code_review","weight":3}

event: run.view
data: {…GET /runs/{id}와 같은 문서…}

event: heartbeat
data: {"run_id":"…"}
```

- **이벤트 행**은 이름 없는 메시지다. `id`는 그 run 안의 이벤트 순번이고 run 안에서 단조 증가한다. 이벤트와 DB 상태는 같은 트랜잭션에서 기록한다. 순번을 DB 행 ID와 섞어 쓰지 않는다.
- `type`은 저장된 이벤트 종류다: `run.queued`, `attempt.leased`, `snapshot.fixed`, `snapshot.confirmed`, `task.created`, `task.status`, `task.stage_recorded`, `task.weight_recorded`, `run.status`, `run.cancel_requested`, `run.retried`, `run.failed`, `attempt.finished`, `attempt.expired`, `attempt.cleanup_confirmed`, `artifact.finalized`, `report.published`, `report.rejected` 등. 클라이언트는 모르는 종류를 무시할 수 있어야 한다.
- `data`는 `run_id`, `sequence`, `version`(그 이벤트 뒤 run version), `type`, `at`(DB 시각)과 payload의 허용 필드만 담는다. 허용 목록은 OpenAPI `RunEventData`의 `x-codeye-payload-fields`이며, 서버 코드와 같은지 [`scripts/validate_bundle.py`](../scripts/validate_bundle.py)가 확인한다. 문자열은 100자 이하만 싣는다. 노드·워커 식별자, digest, 경로, 로그, 자격증명은 보내지 않는다. 거대한 로그를 상태 이벤트에 넣지 않는다.
- **제어 메시지**(`run.view`, `heartbeat`, `state.resync`, `closed`)는 `id`가 없어 cursor를 움직이지 않는다. heartbeat는 SSE 주석이 아니라 이름 있는 이벤트다(브라우저 EventSource는 주석을 전달하지 않는다). 기본 15초 동안 아무것도 보내지 않았을 때 보내며 진행률을 바꾸지 않는다.

### 행 묶음과 run.view

- 행 묶음(최대 200개)을 보낸 뒤 `run.view`를 읽어 보낸다.
- 행을 보낸 뒤와 view를 읽기 전 사이에 커밋된 이벤트는 view에 이미 반영되어 있다. 그래서 view의 `event_sequence`까지의 행을 먼저 보내고 view를 보낸다. `run.view`의 `event_sequence`는 그 연결에서 보낸 마지막 행 순번과 같고, view가 보낸 행보다 앞서지 않는다.
- 그 행을 읽지 못하거나 순번이 끊기면 view를 보내지 않고 `state.resync`(`view_unavailable` 또는 `gap`)로 끝낸다.

### cursor와 재접속

- 시작 위치는 `?cursor=N`(0 이상 정규형 정수) 또는 `Last-Event-ID` 헤더다. 둘 다 있으면 재접속한 브라우저가 보낸 `Last-Event-ID`가 우선한다. 둘 다 없거나 형식이 틀리면 400이다.
- 서버는 N 다음 순번부터 보낸다. 다음 경우에는 `event: state.resync`와 `reason`을 보내고 연결을 닫는다.

| reason | 조건 | 함께 오는 값 |
|---|---|---|
| `gap` | 순번이 빠졌다. | `head`, `expected` |
| `cursor_ahead` | cursor가 run의 최신 순번보다 크다. | `head` |
| `cursor_expired` | cursor가 최신보다 1000개 넘게 뒤처졌다. | `head` |
| `view_unavailable` | 상태 문서를 읽지 못했다. | — |

- 클라이언트는 resync를 받으면 `GET /runs/{id}`로 다시 읽고 그 `event_sequence`로 다시 연결한다.
- 이벤트는 지우지 않으므로 지금 `cursor_expired`는 보관 만료가 아니라 재전송 상한이다. 이벤트 보관·삭제 정책은 아직 없다.

### 권한·세션·자원

- 첫 읽기에서 프로젝트 ACL을 확인한다. 읽을 수 없는 run은 스트림을 열기 전에 일반 404다. 세션이 없으면 401이다.
- 매 poll(기본 1초)마다 짧은 트랜잭션으로 세션과 ACL을 다시 확인한다. poll 사이에 DB 연결을 잡고 있지 않는다. 로그아웃·세션 만료·사용자 비활성화는 `event: closed`(`reason: unauthenticated`), 구성원 제거·프로젝트 삭제는 `closed`(`access_revoked`)로 끝난다.
- 저장소 오류가 30초 넘게 이어지면 `closed`(`unavailable`)다.
- 동시 스트림은 세션 사용자별 4개, 전체 32개다. 사용자별 상한을 먼저 확인하므로 한 사용자가 전체 상한을 모두 차지할 수 없다. 넘으면 그 사용자만 503 `UNAVAILABLE`(재시도 가능)을 받는다. 모든 종료 경로에서 슬롯을 반환한다.
- 스트림은 30분 뒤 닫힌다. 클라이언트는 다시 읽고 새 연결에서 다시 인증받는다. 서버의 읽기 제한시간은 스트림마다 해제하고 쓰기 제한시간은 쓸 때마다 연장한다. 서버 종료(shutdown)를 시작하면 열린 스트림을 먼저 닫는다.
- 이 값들은 `httpapi.StreamConfig`의 기본값이며 지금 CLI 플래그로 바꿀 수 없다.

### 클라이언트 규칙

웹 클라이언트([`web/src/runStream.ts`](../web/src/runStream.ts))는 다음을 지킨다. 다른 클라이언트도 같은 규칙을 따라야 같은 상태로 복원된다.

- 첫 `GET /runs/{id}`의 `event_sequence`에서 연결한다. 응답에 `event_sequence`가 없는 서버면 스트림 없이 polling한다.
- 이벤트 행으로는 cursor만 옮긴다. 순번이 이어지지 않으면 연결을 닫고 다시 읽은 뒤 연결한다. 건너뛰지 않는다.
- 받은 `run.view`와 GET 문서는 `event_sequence`가, 그다음 `version`이 작아지면 적용하지 않는다.
- 재연결은 클라이언트가 맡는다. resync·closed·오류·종료 상태·탭 숨김·화면 이탈 때 EventSource를 닫아 브라우저가 오래된 cursor로 스스로 재시도하지 않게 한다.
- 열렸던 연결이 끝나면(30분 상한, 서버 재시작) 바로 다시 읽고 연결한다. heartbeat나 `run.view` 없이 연속 3번을 넘으면 실패로 본다.
- 열리지 않은 연결(503 상한, 401, 404 포함)이나 45초 무응답은 5초 polling으로 바꾸고, 30초·60초·120초 뒤 최대 3번 다시 연결을 시도한다. `closed`를 받으면 GET 한 번으로 401·404를 오류로 보인다.
- 대기 사유는 이벤트가 없으므로, 연결 중에도 `queued`이면 5초마다 GET한다.
- EventSource가 없는 환경은 polling만 쓴다. polling도 같은 version 기반 API를 쓴다.

## 예약

예약은 시간대와 cron으로 일반 분석 요청을 만든다. 실행 요청은 수동 요청과 같은 enqueue 경로를 쓴다. 예시는 [`examples/schedules.json`](../examples/schedules.json)이다.

### 경로

| 경로 | 권한 | 요청 조건 | 응답 |
|---|---|---|---|
| `GET /projects/{id}/schedules` | 구성원 | — | `{items, can_manage}`. 비구성원 404 |
| `POST /projects/{id}/schedules` | 소유자·manager | `Idempotency-Key` 필수 | 201, `Location`, `ETag: "1"` |
| `PATCH /projects/{id}/schedules/{sid}` | 소유자·manager | `If-Match: "N"` 필수, 바꿀 필드 1개 이상 | 200, 새 `ETag`. 불일치 409 `CONFLICT` |
| `DELETE /projects/{id}/schedules/{sid}` | 소유자·manager | `If-Match: "N"` 필수 | 204. 소프트 삭제이며 대기 occurrence는 `cancelled`(`schedule_deleted`) |

- 본문은 `cron`, `timezone`, 선택 `overlap`(기본 `coalesce_latest`), 선택 `misfire`(기본 `latest_once`), 선택 `enabled`, `run`이다. `run`은 수동 요청과 같은 계약(`profile`, `source_policy: latest_at_acquire`, `scope`)이다. 정책을 생략하면 기본값이고 빈 문자열은 오류다.
- cron·시간대·정책 오류와 9년 안에 한 번도 발생하지 않는 표현은 422 `VALIDATION_FAILED`다. 프로젝트당 살아 있는 예약이 50개를 넘으면 409다.
- 첫 occurrence는 생성 시각 이후이며 과거를 소급 실행하지 않는다. 예약을 다시 켜거나 cron·시간대를 바꾸면 그 시각 이후부터 다시 센다.
- 예약 수정은 API만 있고 웹 화면은 등록·삭제만 제공한다.

### 예약의 의미

cron 문법과 시간대, 일광 절약 시간 처리, 겹침 정책(`coalesce_latest`·`skip`·`queue`), 놓친 예약 정책(`latest_once`·`skip`), occurrence 생성과 다중 controller 안전성, occurrence 상태·사유는 [실행](EXECUTION.md)에 있다. API에서 보이는 결과는 다음과 같다.

- 목록의 `next_run_at`, `last_occurrence`(`scheduled_for`, `state`, `reason`, `missed_from`, `decided_at`, `run_id`), `pending_occurrence`는 서버가 DB 시각으로 결정한 값이다.
- 예약이 만든 run은 `GET /runs/{id}`의 `trigger`가 `{kind:"schedule", schedule_id, scheduled_for}`이고 `run.queued` 이벤트도 같은 출처를 싣는다.
- occurrence마다 예약의 actor 권한을 다시 검사하므로, 권한을 잃은 actor의 예약은 run을 만들지 않고 `skipped` occurrence로 남는다.

## 보고서 경로

보고서 내용과 projection 규칙은 [보고서와 화면](REPORT.md)에 있다.

### GET /runs/{id}/report

| query | 값 | 기본 |
|---|---|---|
| `revision` | 1 이상 정수 | 최신 리비전 |
| `format` | `json`, `markdown`, `html` | `json` |
| `include_code` | `true`, `false` | `false`(코드 미포함) |
| `detail` | `summary`, `full` | `summary`(요약 보기) |

- `detail`은 Markdown·HTML의 상세도다([내보내기 상세도](REPORT.md#내보내기-상세도)). `summary`는 값이 큰 절부터 놓고 긴 목록을 묶어 앞의 몇 개만 보인다. `full`은 모든 행을 싣는다. JSON은 언제나 전체이므로 `detail`이 바뀌어도 같은 바이트다. 다른 값은 400이다.
- 다른 query parameter는 400이다. 프로젝트 구성원이 아니면 404이고, 권한은 요청마다 확인한다.
- 성공한 요청마다 본문을 보내기 전에 `report.exported` 감사 행(형식, 코드 포함 여부, run·revision)을 쓰고, 쓰지 못하면 503이다. 웹 화면의 보고서 조회도 이 경로(JSON, 코드 포함)를 쓰므로 볼 때마다 행이 남는다([결정](DECISIONS.md) A35).
- 저장된 본문을 엄격하게 다시 해석·검증하고 저장된 SHA-256과 대조한다. 실패하면 500 `REPORT_INVALID`이며 본문을 보내지 않는다.

| 응답 헤더 | 값 |
|---|---|
| `Cache-Control` | `private, no-store` |
| `X-CODEYE-Report-Revision` | 리비전 번호 |
| `X-CODEYE-Report-SHA256` | 요청한 projection의 JSON 문서 SHA-256. Markdown·HTML 본문에도 같은 digest가 찍혀 JSON 내보내기와 대조할 수 있다. |
| `X-CODEYE-Include-Code` | `true` 또는 `false` |
| `ETag` | 실제로 보낸 응답 바이트의 SHA-256 |
| `Content-Type` | `application/json`, `text/markdown`, `text/html`(모두 `charset=utf-8`) |
| `Content-Disposition` | Markdown·HTML은 첨부 파일 `codeye-report-<run>-r<N>[-nocode][-full].md|.html` |
| `Content-Security-Policy` | HTML은 스크립트·외부 로딩을 막는 보고서 전용 정책 |

관리 서버에는 snapshot 파일 본문이 없으므로 `include_code=true`도 코드 발췌를 주지 않는다. 저장된 도구 메시지와 검토 문구를 준다.

### GET /runs/{id}/reports

게시된 리비전 목록이다. 항목마다 `id`, `revision`, `status`, `quality_verdict`, `sha256`(저장 본문 해시), `size_bytes`, `created_at`을 주고, 함께 `latest_revision`, `formats`, `default_include_code: false`, `default_baseline`(`{run_id, revision, url}` 또는 null)을 준다. 비구성원은 404다.

## 비교와 판정 경로

비교 상태·이유·판정 적용 규칙은 [보고서와 화면](REPORT.md)에 있다. 응답 형식은 OpenAPI `RunComparison`과 [`examples/comparison.json`](../examples/comparison.json), [`examples/finding-reviews.json`](../examples/finding-reviews.json)이다.

### GET /runs/{id}/comparison

- query는 `revision`(생략 시 최신), `baseline`(run ID), `baseline_revision`(생략 시 기준 run의 최신)이다. 모르는 parameter·반복 parameter는 400, `baseline` 없이 `baseline_revision`만 있으면 400이다.
- 권한은 프로젝트 읽기(viewer 포함)다. 비구성원 404, 게시 보고서가 없는 run 404, 기준 run이 다른 프로젝트이거나 없으면 404, 자기 자신을 기준으로 주면 422다.
- 비교는 요청마다 저장된 게시 보고서에서 다시 계산한다. 응답은 현재 리비전의 `run_id`·`project_id`·`revision`·`report_created_at`, `baseline`(`run_id`, `revision`, `report_created_at`, `selection`) 또는 null, 비교 결과(`comparable`, `not_comparable_reason`, `differences`, `counts`, `entries`, `unidentified`), 판정 오버레이(`findings`, `summary.at_publication`, `summary.current`), `can_review`, `checked_at`(DB 시각)을 담는다.

### GET /projects/{id}/finding-reviews

- `?fingerprint=<fp2>`: 그 식별의 모든 판정 version(최신 먼저).
- `?cursor=<fp2>&limit=N`: fingerprint별 최신 version을 cursor pagination으로. `limit`은 1..200(기본 50).
- 응답은 `{items, next_cursor, can_review}`다. 권한은 구성원(viewer 포함)이고 비구성원은 404다.

### POST /projects/{id}/finding-reviews

- 본문은 `fingerprint`, `disposition`, `reason`, 선택 `expires_at`, 선택 `path_prefix`, `run_id`, `revision`, `finding_id`다.
- 권한은 소유자·manager·runner다. viewer 403, 비구성원 404.
- 사유 없음, 2000자 초과, 과거 만료, `open`에 만료·경로 지정, 그 리비전 본문에 같은 fingerprint의 발견이 없음은 422 `VALIDATION_FAILED`다.
- 새 version이면 201, 같은 키·같은 요청의 재전송이면 기존 행과 200이다. 둘 다 `ETag: "<version>"`을 준다. 헤더 규칙은 [멱등성과 동시 변경](#멱등성과-동시-변경)에 있다.

## 구성원

형식은 OpenAPI `Member`와 [`examples/accounts.json`](../examples/accounts.json)이다.

| 경로 | 본문 | 결과 |
|---|---|---|
| `GET /projects/{id}/members` | — | `{items}`. 소유자, manager, runner, viewer 순, 그 안에서 사용자 이름 순 |
| `POST /projects/{id}/members` | `{username, role}` | 200 `Member`. 사용자 이름(대소문자 무시, 정확히 일치)으로 구성원을 추가하거나 역할을 바꾼다 |
| `PUT /projects/{id}/members/{uid}` | `{role}` | 200 `Member`. 사용자 ID로 구성원을 추가하거나 역할을 바꾼다 |
| `DELETE /projects/{id}/members/{uid}` | — | 204. 구성원이 아니었으면 아무것도 바꾸지 않고 204 |

- 권한은 소유자와 manager다. viewer·runner 403, 비구성원(관리자 포함) 404.
- `role`은 `manager`, `runner`, `viewer`이고 그 밖은 422다. 소유자는 바꾸거나 제거할 수 없다(409). 없거나 비활성인 사용자는 404다.
- 사용자 이름으로 추가할 때 역할 확인이 먼저라 viewer·runner는 이름의 존재와 무관하게 403이다. manager에게는 없는·비활성·형식이 틀린 이름이 모두 같은 404다. 검색이나 부분 일치는 없다([결정](DECISIONS.md) A36).
- 변경과 감사 기록(`project.member_set`, `project.member_removed`, detail에 이전 역할)은 같은 트랜잭션이다. 제거된 사용자의 다음 요청은 404이고 열린 이벤트 스트림은 다음 poll에서 `closed`(`access_revoked`)로 끝난다.

## 계정과 관리자

관리자 경로는 활성 관리자의 세션이 필요하다. 관리자 역할은 어떤 프로젝트도 열지 않는다. 형식은 OpenAPI의 `UserView`, `UserWithLink`, `AuditPage`와 [`examples/accounts.json`](../examples/accounts.json)이다.

| 경로 | 본문 | 결과 |
|---|---|---|
| `GET /admin/users` | — | `{items: UserView[], next_cursor}`. hash·salt는 없다 |
| `POST /admin/users` | `{username, display_name?, is_admin?}` | 201 `{user, login_link}`. 비밀번호 없는 계정(`must_change_password: true`)과 일회용 로그인 링크. 쓰인 이름 409 |
| `POST /admin/users/{id}/disable` | — | 200 `UserView`. 모든 세션 폐기. 마지막 활성 관리자 409. 이미 비활성이면 변경 없이 200 |
| `PUT /admin/users/{id}/admin` | `{is_admin}` | 200 `UserView`. 마지막 활성 관리자 해제·비활성 사용자 지정 409 |
| `POST /admin/users/{id}/password-reset` | — | 200 `{user, login_link}`. 비밀번호 삭제, 모든 세션과 이전 로그인 링크 폐기, 새 로그인 링크. 자기 자신 409, 사용자 이름이 없는 사용자 409(문구가 다르다), 비활성 404. 링크로 시작한 세션은 비밀번호 변경에 묶이고 그 세션의 감사 행에 `link_issued_by`가 붙는다 |
| `GET /admin/source-roots` | — | `{items: [{id, label, created_at, grants: [{user_id, username, granted_at}]}]}`. 서버 경로는 없다 |
| `PUT/DELETE /admin/source-roots/{rid}/grants/{uid}` | — | 204. 이미 그 상태면 변경 없이 204 |
| `GET /admin/providers` | — | `{items: [{id, kind, host, api_url}]}` |
| `GET /audit` | — | 전체 감사 기록 `AuditPage` |

- 로그인 링크(`login_link.url`, `expires_at`)는 응답에 한 번만 나온다. 관리자는 링크를 사용자에게 안전한 경로로 전달하고, 사용자는 링크로 로그인한 뒤 `POST /session/password`로 비밀번호를 정한다. 그 전에는 다른 경로가 403 `PASSWORD_CHANGE_REQUIRED`다.
- source root와 provider의 등록은 운영자 CLI(`codeye source-root add`, `codeye provider add`)가 한다. 관리자 API는 허가만 바꾼다.
- 첫 관리자와 비밀번호 직접 지정은 운영자 CLI(`codeye user create --admin`, `codeye user passwd`)다([운영](OPERATIONS.md#계정과-관리자-cli)).
- 감사 기록 항목은 `id`(문자열), `occurred_at`, `actor_kind`, `actor_user_id`, `actor_username`, `actor_node_id`, `action`, `target_type`, `target_id`, `project_id`, `request_id`, `detail`이다. action 목록과 detail 규칙은 [보안](SECURITY.md#감사)에 있다.

## 노드 API 요약

노드 에이전트가 관리 서버에서 작업을 받는 경로다. SSH 원격 실행은 없다. 상태 전이·lease·fencing·정리 확인의 규칙은 [실행](EXECUTION.md)에 있고 필드는 OpenAPI를 따른다.

| 경로 | 요약 |
|---|---|
| `POST /nodes/enroll` | 운영자 CLI가 발급한 만료·1회용·철회 가능 등록 토큰(pool·승인 source root에 묶임)으로 등록하고 `node_secret`을 한 번 받는다. 서버는 hash만 저장한다. `protocol_version`은 서버가 지원하는 값(현재 `0.1`)과 정확히 같아야 하며, 다르면 422 `PROTOCOL_UNSUPPORTED`이고 작업을 주지 않는다. |
| `POST /nodes/{id}/heartbeat` | 관측한 워커·능력·용량과 `last_command_sequence`를 보고하고 그 cursor 이후 명령을 받는다. |
| `POST /nodes/{id}/claims` | idle 워커 ID와 `Idempotency-Key`로 작업을 요청한다. 응답은 `{lease, job}`이다. |
| `POST /attempts/{id}/heartbeat` | node·worker·epoch로 lease를 연장하고 취소 요청 여부를 받는다. |
| `POST /attempts/{id}/events` | `client_event_id`로 멱등. `snapshot_fixed`, `task_created`(선택 `stage`·`weight`), `task_status`, `run_advance`. 응답에 run 이벤트 순번과 version이 있다. |
| `PUT /attempts/{id}/artifacts/{client_artifact_id}` | 크기·SHA-256 헤더와 바이트를 대조해 저장한다. `Content-Length`가 없으면 411, 불일치는 422 `INTEGRITY_MISMATCH`. 같은 재전송은 같은 201이다. |
| `POST /attempts/{id}/complete` | epoch·정리 상태·결과 manifest를 확인하고 결과를 접수한다. 접수된 성공은 `finalizing`을 거쳐 보고서 게시에서 종결된다. |
| `POST /attempts/{id}/cleanup` | `recovery_required` attempt의 실제 정리 확인 |

- 인증 실패와 철회된 노드는 401 `NODE_UNAUTHENTICATED`다. 경로의 node ID가 인증한 노드와 다르거나 다른 노드의 attempt면 404다. 다른 worker·이전 epoch·만료·종료된 attempt는 409 `STALE_ATTEMPT`다.
- 워커에게 node secret을 주지 않고 에이전트가 중계한다.
- 에이전트가 워커에 넘기는 job의 허가 두 개(`allow_model_review`, `allow_repository_code`)는 노드 운영자 설정에서 나온다. 관리 서버와 run 요청은 정하지 않는다([분석기](ANALYZERS.md), [보안](SECURITY.md)). `allow_dependency_fetch`는 운영자 스위치와 프로젝트 선택 `fetch_dependencies`가 둘 다 켜졌을 때만 참이다([의존성 준비](ARCHITECTURE.md#의존성-준비)).

## 호환과 버전

- 응답 필드는 추가만 한다. 웹은 `activity`, `event_sequence`, `trigger`, 계획 필드가 없는 서버에서도 동작한다(polling, 표시 생략).
- enum 값을 추가하면 구버전 UI가 모르는 값을 "알 수 없음"과 원문으로 안전하게 표시해야 한다. 필수 필드 추가나 의미 변경은 API·스키마 버전 정책을 따른다.
- 모호한 버전을 자동 추정해 실제 API 호출을 반복하지 않는다.
- 노드 프로토콜은 등록 때 정확한 버전 일치를 요구한다. 범위 협상은 없다.
- 구버전 컨트롤러는 `task_created`의 `stage`·`weight`를 거부하므로 업그레이드할 때 컨트롤러를 워커보다 먼저 올린다.
- 서버는 migration 이력이 바이너리의 최신과 정확히 같을 때만 시작한다. 더 새 schema의 DB에서 이전 바이너리는 시작하지 않는다(`schema_newer`). 운영 절차는 [운영](OPERATIONS.md)에 있다.

관련 요구사항·수용 시험: REQ-013, REQ-016, REQ-017, REQ-022, REQ-023, NFR-003, NFR-004, TC-25(예약), TC-46~TC-52(loopback 보호, 일회용 토큰과 세션, 선언 등록, ACL 404/403, 멱등 202와 DB 실패 503, readiness, 정적 파일 경계), TC-99(단계 집계, SSE 순번 누락, 계획 분모 변경), TC-140~TC-157(경로별 격리와 감사, 비밀번호 로그인과 제한, 관리자, TLS, 재설정 기록, 이름으로 구성원 추가). 요구사항 정의는 [요구사항](REQUIREMENTS.md), 수용 시험 정의는 [시험](TESTING.md)에 있다.
