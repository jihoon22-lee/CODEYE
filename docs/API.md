# HTTP API와 이벤트

관리 서버(`codeye serve`, `codeye up`·`codeye start`가 띄우는 controller)가 제공하는 HTTP API와 실행 이벤트 스트림의 규칙이다. 필드와 응답 형식의 기계 판독 기준은 [`api/openapi.yaml`](../api/openapi.yaml)이다. 이 문서는 필드를 되풀이하지 않고 OpenAPI가 표현하지 못하는 규칙을 적는다. 보고서 내용·비교 상태·판정 적용 규칙과 화면은 [보고서와 화면](REPORT.md), 노드 프로토콜의 lease·fencing·완료 절차는 [실행](EXECUTION.md), 위협 모델과 비밀정보 정책은 [보안](SECURITY.md)에 있다.

## 구현 범위

| 필요한 정보 | 어디서 확인하나 |
|---|---|
| 사용 가능한 경로 | [자원 개요](#자원-개요)와 OpenAPI에서 `x-codeye-status: implemented`로 표시한 operation. [계약 시험](../internal/httpapi/contract_test.go)이 서버 라우트와 대조한다. |
| 사용자 로그인과 권한 | [인증](#인증). 이메일·비밀번호 또는 일회용 링크로 세션을 만든다. 관리자도 프로젝트 구성원 권한으로 보고서를 읽는다. |
| 관리자·노드 운영 | [계정과 관리자](#계정과-관리자), [노드 API](#노드-api-요약), [운영 CLI](OPERATIONS.md). 관리자는 Git 호스트와 서비스 설정을 관리한다. |
| GitHub 웹훅 | [프로젝트별 수신 주소와 이벤트](#github-저장소-웹훅) |

프로젝트의 분석 선택은 `PUT /projects/{id}/analysis`로 변경한다. 일반 설정 변경, 연결 진단·브랜치 조회, 개별 task·로그 조회, 첫 발견 시각의 이력, 판정 일괄 적용, 풀·노드 관리 웹 API와 provider 단위 웹훅은 설계 단계다.

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

클라이언트가 인증하는 절차와 헤더를 적는다. 이 보호가 막는 위협, 쿠키·링크 파일의 운영 경계, 로그인 제한과 비밀번호 저장 방식은 [보안](SECURITY.md)에 있다. listen 주소는 IP와 0이 아닌 포트여야 하며, loopback이 아니면 TLS 설정(`--tls-cert`, `--tls-key`, 하나 이상의 `--public-origin`)이 모두 있거나 `--plain-http`가 있어야 서버가 시작한다. `--local-listen`은 같은 머신의 에이전트·명령용 loopback 수신 주소를 하나 더 연다.

### 일회용 로그인 링크

- `codeye start`, `codeye serve --user`, `codeye login-link`가 일회용 launch token(32 byte 난수, base64url 43자, 10분, 한 번만 교환)을 발급해 `<origin>/#launch=<token>` 링크를 0600 파일에 쓴다. `codeye up`·`codeye open`은 같은 링크를 터미널에 출력하고 브라우저로 연다(A38). DB에는 SHA-256만 저장한다. CLI 사용법은 [사용 안내](GUIDE.md)에 있다.
- `codeye up PATH`의 링크는 토큰 뒤에 깊은 링크 `&project=<ID>&run=<ID>`를 붙인다. 웹 앱은 토큰을 지울 때 이 두 키(canonical UUID)만 fragment에 남기고 그 프로젝트와 실행을 연다.
- 관리자 API의 사용자 생성과 비밀번호 재설정도 같은 링크를 응답의 `login_link`로 한 번 준다([계정과 관리자](#계정과-관리자)). origin은 관리자가 지금 연 주소(그 요청의 scheme + Host)다. 공개 서비스를 loopback 주소로 열었으면 알려 줄 주소(`service_origin`)다.
- 토큰은 URL fragment에 있으므로 HTTP 요청·서버 로그에 실리지 않는다. 웹 앱은 fragment를 읽은 즉시 주소창에서 지우고 `POST /session`으로 교환한다.
- 세션 수명은 12시간이다. `codeye up`·`start`가 띄우는 controller(`codeye serve --session-ttl 168h`)는 7일이다(저장 계층의 상한과 같다).

### 회원가입

`POST /account/register`에 `{email, password}`를 보낸다. 이메일은 앞뒤 공백을 제거하고 소문자로 저장한다. 이메일은 ASCII 주소와 점으로 구분된 DNS 도메인을 사용하며 최대 254자다. 비밀번호는 12~256자이며 이메일과 같거나 한 문자만 반복하면 거절한다. Origin을 검사한다.

성공하면 일반 사용자와 첫 세션을 함께 저장하고 201 `SessionView`와 세션 cookie를 준다. 같은 주소로 가입한 계정이 있으면 동시 요청에서도 409 `EMAIL_TAKEN`을 응답한다. 잘못된 형식은 400 `INVALID_REQUEST`, 주소나 비밀번호 규칙 위반은 422 `VALIDATION_FAILED`다. 이메일 확인 절차는 없다.

### 세션

- `POST /session`은 `{launch_token}`, `{email, password}`, `{username, password}` 중 하나를 받는다. 섞거나 일부만 보내면 400이다.
  - launch token: 토큰을 소비하고 같은 트랜잭션에서 세션을 만든다. 잘못된·사용한·만료된 토큰은 모두 401 `launch token is invalid, used or expired`다. Host·Origin 검사에 걸린 요청은 토큰을 소비하지 않는다. 교환의 잠금 순서는 user(FOR SHARE)→token→session이고, 사용자 비활성화(user FOR UPDATE→세션 폐기)와 직렬화되므로 둘이 경합해도 유효한 세션이 남지 않는다.
  - 비밀번호: 이메일의 앞뒤 공백과 대소문자, 기존 사용자 이름의 대소문자는 무시한다. 없는 사용자·틀린 비밀번호·비활성·비밀번호 없는 사용자는 모두 같은 401 `login identifier or password is incorrect`다. 실패가 한도를 넘으면 429 `RATE_LIMITED`이고 `Retry-After` 헤더와 `details.retry_after_seconds`가 같은 초다. 429 동안은 맞는 비밀번호도 거절한다. 서버의 KDF 계산 자리가 3초 안에 나지 않으면 503 `UNAVAILABLE`이고 시도로 세지 않는다.
- 응답은 201 [`SessionView`](../api/openapi.yaml)와 세션 cookie다. 이메일로 가입한 계정은 `email`을 포함한다. `password_reset`은 관리자가 한 가장 최근의 비밀번호 재설정(재설정 뒤 30일 동안, 아니면 null)이며 `reset_at`, `reset_by_user_id`, `reset_by_username`을 담는다. 로그인 횟수와 무관하게 30일 동안 나오고 지우는 경로는 없다.
- cookie는 평문(loopback과 공개 평문 HTTP)에서 `codeye_session`, TLS에서 `__Host-codeye_session`(Secure)이다. HttpOnly, SameSite=Strict, Path=/, 절대 만료 12시간이며 sliding 연장은 없다. 만료는 DB 시각으로 판단한다. TLS 서버는 평문 이름의 cookie를 읽지 않는다.
- `GET /session`은 같은 형태의 현재 세션을 준다. `DELETE /session`은 세션을 폐기하고 cookie를 지운다(204, CSRF 필요). 사용자 비활성화(`codeye user disable`, `POST /admin/users/{id}/disable`)와 비밀번호 재설정은 그 사용자의 모든 세션을 폐기한다.

### 비밀번호 변경

- `POST /session/password` `{current_password?, new_password}`(CSRF 필요). 비밀번호가 있는 사용자(`has_password: true`)는 `current_password`가 필수다(없으면 400). 비밀번호가 없는 사용자는 세션만으로 처음 정한다.
- 새 비밀번호가 규칙(12~256자, 이메일·사용자 이름·표시 이름과 다름, 한 문자 반복 아님)을 어기면 422 `VALIDATION_FAILED`이고 시도로 세지 않는다. 현재 비밀번호가 틀리면 403 `PASSWORD_INCORRECT`이고 로그인 실패 한도에 센다(넘으면 429). 로그인 링크 전용 계정, 확인 뒤 비밀번호가 바뀐 경우는 409다.
- 성공하면 200 `SessionView`(`must_change_password: false`, `has_password: true`)이고 그 사용자의 다른 세션과 쓰지 않은 로그인 링크는 모두 끝난다. 이 세션과 cookie는 그대로다.
- `must_change_password`가 true인 세션은 `GET/DELETE /session`과 `POST /session/password`만 쓸 수 있다. 다른 사용자 경로(이벤트 스트림 포함)는 403 `PASSWORD_CHANGE_REQUIRED`다.

### CSRF

`X-CODEYE-CSRF` 값은 base64url(SHA-256(`"codeye-csrf\0"` + 세션 토큰))이다. 세션 토큰을 가진 서버만 계산할 수 있고 `SessionView`의 `csrf_token`으로 전달한다. 웹 앱은 이 값을 메모리에만 둔다. 세션이 필요한 상태 변경 요청(POST·PATCH·PUT·DELETE)에 필수이며 없거나 틀리면 403 `CSRF_REJECTED`다.

### Host와 Origin

- `Host`는 loopback이 아닌 주소로 들으면 이 머신의 이름과 주소에 listen 포트(`localhost`, loopback 주소, 호스트 이름, 인터페이스 주소. [받는 주소](OPERATIONS.md#받는-주소))와 `--public-origin`의 host다. loopback으로만 들으면 loopback 이름(`127.0.0.1`, `localhost`, `[::1]`, 듣는 주소)과 `--public-origin`의 host만이다. 기본 포트(평문 80, TLS 443)면 포트 없는 Host도 받는다.
- 상태 변경 요청의 `Origin`은 그 요청의 scheme + Host(`http://` 또는 `https://`)와 정확히 같아야 한다.
- TLS는 모든 응답에 `Strict-Transport-Security: max-age=31536000`을 보낸다.
- `--local-listen`의 loopback 수신 주소는 평문 loopback 규칙을 따른다. 규칙은 요청이 들어온 수신 주소가 정한다.
- Host 위반은 정적 파일을 포함한 모든 요청에서 403 `HOST_REJECTED`다. `message`는 쓴 주소, 받는 주소, 그 주소를 더하는 `codeye up --public-origin …` 명령을 한국어로 말하고 `details`에 `host`(`host[:port]` 모양일 때만), `accepted_origins`, `add_command`가 있다. 브라우저가 연 화면(Accept에 `text/html`, `/api` 밖)이면 같은 내용의 HTML이다. Origin 위반과 `same-origin`이 아닌 `Sec-Fetch-Site`는 403 `ORIGIN_REJECTED`다. `POST /session`도 Origin 검사를 받는다.
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
| 422 | `VALIDATION_FAILED`(의미 제약), `SOURCE_UNSUPPORTED`(ssh·scp·`git://` 및 오프라인 모드에서 허용되지 않은 Git 호스트의 새 소스 확보), `ATTACHMENTS_UNSUPPORTED`, 폴더 경로의 `FOLDER_NOT_READABLE`·`FOLDER_NOT_FOUND`·`FOLDER_HOLDS_CODEYE_DATA`([프로젝트 등록](#프로젝트-등록)), 노드 `INTEGRITY_MISMATCH`·`PROTOCOL_UNSUPPORTED`, pull request run의 `PULL_REQUEST_UNSUPPORTED`·`PULL_REQUEST_NOT_FOUND`·`GITHUB_TOKEN_REFUSED`·`GITHUB_FORBIDDEN`([pull request run](#pull-request-run)) |
| 500 | `INTERNAL`, `REPORT_INVALID`(저장된 보고서가 검증에 실패) |
| 502 | `GITHUB_UNAVAILABLE`(`retryable=true`): pull request를 확인할 GitHub API에 닿지 못함 |
| 503 | `UNAVAILABLE`(`retryable=true`). DB 오류 뒤에는 202를 반환하지 않는다. 이벤트 스트림 상한 초과도 503이다. `GITHUB_RATE_LIMITED`(`retryable=true`)는 GitHub API rate limit 소진이다. |

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
| `POST /projects`, `GET /projects`, `GET /projects/{id}` | 세션(+CSRF) | 등록 201+`Location`(`open_existing`이고 같은 폴더·저장소면 200과 그 프로젝트), 허가된 목록·상세. 항목마다 호출자의 `role`(`owner`·`manager`·`runner`·`viewer`), `can_run`, `can_manage`, `analysis` |
| `PUT /projects/{id}/analysis` | 소유자·manager(+CSRF) | 분석 선택을 바꾼 새 설정 revision. 200과 프로젝트([프로젝트 등록](#프로젝트-등록)) |
| `GET /setup` | 세션 | 이 사용자에게 맞춘 첫 화면 정보([설치 정보](#설치-정보)) |
| `GET /projects/{id}/members`, `PUT/DELETE /projects/{id}/members/{uid}` | 소유자·manager | [구성원](#구성원) |
| `GET /projects/{id}/audit` | 소유자·manager | 그 프로젝트의 감사 기록([계정과 관리자](#계정과-관리자)) |
| `/admin/…`, `GET /audit` | 관리자 | [계정과 관리자](#계정과-관리자) |
| `POST /projects/{id}/runs` | 소유자·manager·runner | 202 `{id, status, version, url}`+`Location` |
| `GET /runs/{id}`, `GET /runs/{id}/events` | 구성원 | [실행 조회와 진행](#실행-조회와-진행), [실행 이벤트 스트림](#실행-이벤트-스트림) |
| `POST /runs/{id}/cancel` | 소유자·manager·runner | 상태를 바꿨으면 202, 이미 종료·취소 중이면 200(현재 상태) |
| `POST /runs/{id}/retry` | 소유자·manager·runner(+CSRF) | 202 `{id, status, version, url, retry}`+`Location`([다시 시도](#다시-시도)) |
| `GET /runs/{id}/report`, `GET /runs/{id}/reports` | 구성원 | [보고서 경로](#보고서-경로) |
| `GET /runs/{id}/comparison` | 구성원 | [비교와 판정 경로](#비교와-판정-경로) |
| `GET /runs/{id}/pull-request-comment` | 구성원 | pull request run의 결과 댓글을 게시하지 않고 렌더링한 미리 보기([pull request run](#pull-request-run)) |
| `GET/POST /projects/{id}/finding-reviews` | 구성원 / 소유자·manager·runner | [비교와 판정 경로](#비교와-판정-경로) |
| `GET/POST /projects/{id}/schedules`, `PATCH/DELETE /projects/{id}/schedules/{sid}` | 구성원 / 소유자·manager | [예약](#예약) |
| `/nodes/…`, `/attempts/…` | 노드 Bearer | [노드 API 요약](#노드-api-요약) |

## 프로젝트 등록

- 본문은 `{name?, source | local_path, analysis?, description?, attachment_ids?}`다. `source`와 `local_path` 중 정확히 하나를 준다(둘 다·둘 다 없음은 400). 첨부는 아직 받지 않는다(`attachment_ids`가 비어 있지 않으면 422 `ATTACHMENTS_UNSUPPORTED`). 빈 이름은 422다.
- **local 소스**: 기존 source root ID와 그 아래의 상대 경로를 받는다. 경로는 `.`이거나, 절대 경로·드라이브·역슬래시·`..`·빈 구간이 없어야 한다. 등록되지 않은 root는 404다.
- **서버 폴더**는 `local_path`로 서비스 계정이 읽을 수 있는 서버 절대 경로를 보낸다. 상대 경로로 등록한 기존 `source`도 읽는다. 대상이 없으면 `FOLDER_NOT_FOUND`, 읽을 권한이 없으면 `FOLDER_NOT_READABLE`, 보호된 데이터 폴더와 겹치면 `FOLDER_HOLDS_CODEYE_DATA`다.
- **CODEYE 데이터 폴더**(state 디렉터리가 있는 controller): 프로젝트 폴더가 상태 디렉터리나 백업 디렉터리를 품거나 그 안에 있으면, `local_path`든 root와 상대 경로든 symlink를 푼 뒤 422 `FOLDER_HOLDS_CODEYE_DATA`다. `message`는 문제의 두 폴더를 적고 프로젝트가 든 하위 폴더를 고르라고 안내한다. 노드도 소스를 확보하기 전에 같은 검사를 하고 실패 사유는 `source_holds_codeye_data`다.
- `open_existing`(기본 `local_path`는 true, `source`는 false)이 true이고, 요청자가 같은 root와 경로 또는 같은 provider와 저장소 주소(대소문자, 끝의 `/`·`.git` 무시)의 프로젝트를 소유하면 200과 그 프로젝트를 준다. 웹의 첫 화면은 둘 다 true로 보낸다. 두 사용자가 같은 소스로 등록하면 각자 자기 프로젝트를 소유한다.
- **git 소스**: 승인된 provider와 `https://<provider host>/<owner>/<repo>[.git]`. 브랜치는 선택이며 `git check-ref-format`의 부분집합을 만족해야 한다. 비우면 소스를 확보할 때마다 저장소의 기본 브랜치(`git ls-remote --symref … HEAD`)를 쓰고 보고서 입력에 그 브랜치와 커밋을 남긴다. ssh·scp·`git://`는 422 `SOURCE_UNSUPPORTED`다. 등록된 호스트의 플랫폼 GitHub 계정으로 비공개 저장소를 읽는다. 인증 실패는 `source_auth_required`, 연결 실패는 `source_unreachable`이다.
- **analysis**: `{send_source_to_model, run_build_scripts, fetch_dependencies?}`. 앞의 둘은 있어야 한다(아니면 422 `VALIDATION_FAILED`). 없으면 소스 종류의 기본값이다. 로컬 폴더는 둘 다 `true`, Git 저장소는 `send_source_to_model: true`, `run_build_scripts: false`다. `fetch_dependencies`는 선택이고 없으면 `true`다(두 종류 모두). 설정에는 `false`일 때만 쓴다. 이 선택은 노드 운영자 스위치(`--dependency-fetch`)와 함께 모든 노드에서 의존성 받기를 정한다([의존성 준비](ARCHITECTURE.md#의존성-준비)). 설정의 `analysis`에 저장하고 응답의 `analysis`로 보인다. `--project-analysis-policy`로 띄운 노드(`codeye up`이 띄운다)는 로컬·Git 모두 이 선택과, 배정의 `offered`(관리자가 아닌 사람이 시작한 run에 관리자 스위치가 허용한 것)를 함께 따른다. 그 밖의 노드는 운영자의 root별 허가(`--model-source-root`·`--repo-code-source-root`)를 상한으로 이 선택을 따른다. `PUT /projects/{id}/analysis`가 바꾸고(설정의 나머지는 그대로, 새 revision, `project.revised` 감사), 그 뒤의 run이 새 선택을 따른다.
- 등록은 소스 위치의 선언이다. Git 서버에는 분석 시 연결한다. 로컬 절대 경로는 등록 시 존재와 읽기 권한을 확인하고, 워커는 확보할 때 symlink·junction 경계를 다시 검사한다([실행](EXECUTION.md)). 이름이 없으면 경로 마지막 구간, root label, 저장소 이름 순으로 정한다. 저장되는 설정 revision 1은 `{"config_version":1,"source":…,"description"?}`이다.
- 프로젝트 응답의 `source_status`는 등록만 확인한 소스이므로 `declared_unverified`다. `capability_gaps`는 `source_access_unverified`(항상), `execution_unavailable`(철회되지 않은 등록 노드가 없음), `report_unavailable`(게시된 보고서가 없음)이다. 실행별 배치 가능 여부는 대기 사유로 따로 보고한다.

## 실행 요청과 취소

요청 본문은 다음 형태다.

```json
{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}
```

- `profile`은 `deep-review`, `with-tests`, `focused`다. `scope.kind`는 `full` 또는 `paths`다. 형식은 OpenAPI가 정한다.
- `scope.kind=paths`에는 다음 실행이 확보할 스냅샷의 파일이나 폴더를 `scope.paths`에 최대 100개까지 적는다. 경로는 프로젝트 기준이며 고정한 시점의 로컬 사본 또는 등록 브랜치의 Git 커밋에서 찾는다. 기존 보고서 리비전은 이전 실행의 스냅샷과 범위를 보존한다.
- `scope.review_coverage: "extended"`는 선택한 범위의 확장 코드 검토를 요청한다. 생략하면 일반 검토다. 이 선택은 고정하며, 실제 호출 상한은 실행하는 노드의 설정이 정한다. 기존 프로필과 경로 범위를 사용할 수 있다.
- `with-tests`는 코드 실행 권한과 도구를 확인한 뒤 작업 사본에서 지원하는 기존 테스트를 실행한다. 게시 결과의 `coverage.test_status`와 `test.*` 작업, `test.run` 실행 행에서 확인한 범위와 종료 결과를 읽을 수 있다. [보고서 판정](REPORT.md#상태와-판정)
- 클라이언트는 raw shell 명령이나 임의 자격증명을 run 요청에 넣지 못한다. 프로젝트에 저장된 승인 설정을 서버가 해석한다.
- 요청은 영속 큐에 들어가고 202를 반환한다. 202는 실행이 시작됐다는 뜻이 아니다. 등록된 노드가 없으면 run은 `queued`에 머물고 `waiting_reason`이 `execution_unavailable`이다.
- 배정 시점(claim)에 서버가 요청자의 권한을 다시 확인한다.
- 수동 새로 분석과 다시 시도는 별도 동작이다([다시 시도](#다시-시도)).
- `pull_request: {number, comment?}`를 더하면 pull request run이다([pull request run](#pull-request-run)).
- 취소 재호출은 멱등이며 이미 종료된 run은 현재 상태를 반환한다. 취소 응답이 202여도 실제 프로세스가 끝났다는 뜻이 아니다. 정리 확인 절차는 [실행](EXECUTION.md)에 있다.

### 다시 시도

`POST /runs/{id}/retry`는 끝난 run(`failed`·`cancelled`·`partial`·`completed`)을 다시 실행한다.

- `retry: "same_run"`: `failed`·`cancelled` run의 스냅샷을 철회되지 않고 heartbeat가 신선한 노드가 갖고 있고, 모든 attempt의 정리가 확인됐고, 그 뒤 프로젝트 설정이 바뀌지 않았으면 그 run 자체를 같은 스냅샷으로 다시 큐에 넣는다. 응답의 `id`는 같은 run이다.
- `retry: "new_run"`: 그 밖에는 저장된 같은 요청으로 새 run을 만든다. 소스를 다시 확보한다(소스 확보 실패, 도구를 설치하거나 분석 선택을 바꾼 뒤의 부분 완료). 같은 run version에 대한 반복 요청은 같은 새 run을 준다. 응답과 `Location`은 새 run이다.
- 아직 끝나지 않았거나 `recovery_required`인 run은 409 `RUN_NOT_FINISHED`, viewer는 403이다. 어느 경우든 성공하면 `run.requested` 감사(`trigger: "retry"`)를 남긴다. 클라이언트는 응답의 run을 따라간다. `recovery_required`는 [정리 확인](#정리-확인)을 쓴다.

### 정리 확인

`POST /runs/{id}/recover`는 `recovery_required` run에 대해 "그 attempt의 프로세스가 노드에 남지 않았다"는 사람의 확인을 받는다(A48).

- 본문은 정확히 `{"cleanup_confirmed": true}`다. 본문이 없으면 415, `false`·`{}`·다른 필드는 400 `INVALID_REQUEST`다. 권한은 [다시 시도](#다시-시도)와 같다(프로젝트 실행 권한, viewer는 403).
- 한 트랜잭션에서 attempt를 `released`·`confirmed`로 두고 예약을 풀고 남은 task를 닫고 run을 `failed`로 바꾼다(이벤트 `attempt.cleanup_confirmed`, 사유 `operator_confirmed`). `run.recovery_confirmed` 감사(`detail`: `epoch`, `status`)를 남긴다.
- 그다음 다시 시도와 같은 규칙으로 다시 넣는다. 202와 `Location`, 본문은 다시 실행할 run의 ack에 `retry`(`same_run`·`new_run`), 확인한 `attempt_id`·`epoch`를 더한 것이다. 확인은 기록됐지만 다시 넣지 못했으면 200과 `retry: "not_queued"`(본문의 run은 확인한 `failed` run)다.
- `recovery_required`가 아닌 run은 409 `RUN_NOT_RECOVERY_REQUIRED`다. 같은 확인을 두 번 보내면 두 번째가 409다.
- 운영자 CLI는 `codeye run recover --id RUN_ID --confirm-cleanup`이다([실행](EXECUTION.md#등록-토큰과-운영자-명령)).

### pull request run

github.com 저장소 프로젝트는 누가 등록했든 `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"},"pull_request":{"number":7,"comment":true}}`처럼 pull request 하나를 분석한다. 동작과 근거는 [GitHub 연결](GITHUB.md#pull-request-분석)에 있다.

- controller가 대상 호스트의 플랫폼 GitHub 인증정보(없으면 익명)으로 pull request의 base/head SHA, head 저장소(fork), 변경 파일과 변경 줄을 읽어 run에 한 번 고정한다(`runs.pull_request`, 바꾸지 않는다). 노드는 `refs/pull/N/head`를 받아 고정한 head SHA를 분석한다. 202 뒤에 head가 움직여도 이 run은 고정한 head다.
- `comment`는 이 run이 결과 댓글을 남길지다. 없으면 프로젝트 `analysis.comment_on_pull_request`(기본 `false`)를 따른다. 화면과 `codeye up --comment`는 값을 보낸 뒤 `PUT /projects/{id}/analysis`로 기억시킨다.
- idempotency 비교는 보낸 body로만 한다. 같은 키의 반복은 GitHub에 다시 묻지 않고 같은 run을 준다. 새 push 뒤의 새 분석은 새 키(새 run)다.
- 거절: 승인된 GitHub API를 사용할 수 없는 프로젝트는 422 `PULL_REQUEST_UNSUPPORTED`, GitHub이 플랫폼 계정에 보여 주지 않는 번호는 422 `PULL_REQUEST_NOT_FOUND`, 거부된 token은 422 `GITHUB_TOKEN_REFUSED`, 권한 부족은 422 `GITHUB_FORBIDDEN`, rate limit은 503 `GITHUB_RATE_LIMITED`, 연결 실패는 502 `GITHUB_UNAVAILABLE`이다. 예약은 `pull_request`를 받지 않는다(422 `VALIDATION_FAILED`).
- `GET /runs/{id}`의 `pull_request`가 고정한 값과 댓글 상태(`comment.state`: `not_requested`·`waiting_for_report`·`pending`·`posted`·`superseded`·`failed`, `reason`, `url`, `attempts`, `next_attempt_at`)다. pull request run이 아니면 필드가 없다.
- `GET /runs/{id}/pull-request-comment[?revision=N]`은 게시할(또는 게시한) 댓글 본문을 렌더링해 `{run_id, report_revision, format:"markdown", body, head_sha, current_head_sha, head_moved, comment}`로 준다. GitHub에 쓰지 않고, 현재 head와 결과 댓글은 대상 호스트의 플랫폼 계정으로 확인하고 게시한다. 게시에 필요한 토큰이 없으면 `failed`, `github_token_missing`이 기록된다. pull request run이 아니면 404 `NOT_A_PULL_REQUEST_RUN`이다.
- 보고서에는 `pull_request` 절이 생긴다([보고서](REPORT.md#pull-request-보고서)). 결과 댓글 게시는 보고서 게시와 별개라 실패해도 run 상태와 보고서를 바꾸지 않는다.

### GitHub 저장소 웹훅

`POST /github/webhooks/{project_id}`는 `application/json` 본문을 최대 2 MiB 받는다. `X-Hub-Signature-256`으로 원본 바이트의 HMAC을 확인하고, `X-GitHub-Event`와 `X-GitHub-Delivery`를 저장한다. 세션 cookie 대신 서명으로 인증하며 브라우저 Origin은 403이다.

202는 저장된 전달 또는 기존 전달과 같은 재전송을 뜻한다. 같은 ID에 다른 이벤트나 본문은 409, 설정되지 않은 프로젝트는 404, 틀린 서명은 401이다. 프로젝트·PR·댓글 작성자 권한을 후속 처리에서 확인한 뒤 분석을 요청한다. 처리 정책과 파일 설정은 [저장소 웹훅](GITHUB.md#저장소-웹훅)에 있다.

### 설치 정보

`GET /setup`은 로그인한 사용자의 분석 기본값, Git 호스트 목록, 모델 상태와 도구 정보를 준다. 도구 경로와 힌트는 관리자에게만 표시한다.

- 관리형 설치의 모델 상태는 저장된 OpenCode 설정을 읽는다. 일반 계정은 모델 검토의 사용 가능 여부를 보고, 관리자는 모델 ID와 최근 연결 확인 결과도 본다.
- `available`은 `git_url`, `local_path`, `pull_requests`, `model_review`, `build_scripts`, `fetch_dependencies`별 가용 여부와 사유를 담는다. 절대 경로 등록은 해당 경로를 서비스 계정이 실제로 읽을 수 있는지 확인한다.
- `github_connections`는 호스트별 `enabled`, `host`, `token`(설정 여부), 선택 `account` 및 `reason`이다. 서비스 시작 시 확인한 상태로 토큰 값은 포함하지 않는다. [플랫폼 GitHub 계정](GITHUB.md#플랫폼-github-계정)
- 오프라인에서는 설정한 사내 GHES가 `git.hosts`에 표시된다. `readable_folders`는 이전 클라이언트용 필드이며 빈 배열이다.

## 실행 조회와 진행

`GET /runs/{id}`의 문서는 SSE `run.view` 이벤트와 같다. 주요 필드의 의미는 다음과 같다. 형식은 OpenAPI의 `RunView`와 [`examples/run-progress.json`](../examples/run-progress.json)을 따른다.

| 필드 | 규칙 |
|---|---|
| `status`, `version` | run 상태와 낙관적 version |
| `event_sequence` | 문서를 읽은 시점의 run 이벤트 순번. 이벤트 스트림의 cursor다. |
| `trigger` | 직접 요청 `{kind:"manual"}`, 예약 `{kind:"schedule", schedule_id, scheduled_for}`, GitHub 웹훅 `{kind:"github_webhook"}` |
| `snapshot_id` | 고정된 snapshot이 없으면 null |
| `quality_verdict`, `report` | 최신 게시 리비전의 판정과 `{revision, status, sha256, url}`. 판정은 저장된 보고서에서만 읽으며 run 상태로 추측하지 않는다. |
| `progress.stage` | run 상태와 같다. 활성 분석 역할을 뜻하지 않는다. 세부 단계는 `progress.stages`다. |
| `progress.completed_tasks`, `total_tasks` | 종료 task 수(성공·실패·제외 포함)와 등록된 task 수. task가 등록되기 전에는 `total_tasks`가 null이다. |
| `progress.detail`, `stages`, `last_event_at`, `checked_at` | [단계 집계](#단계-집계). `checked_at`은 서버가 집계한 시각이다. |
| `progress.plan_revision`, `planned_weight`, `finished_weight`, `weight_basis`, `estimated_percent`, `plan_changes` | [계획 revision과 가중 진행률](#계획-revision과-가중-진행률) |
| `progress.waiting_reason` | [대기 사유](#대기-사유). `queued`일 때만 값이 있다. |
| `raw_evidence_removed_at` | [보존 정리](OPERATIONS.md#보존-정리)가 이 run의 원시 산출물 내용을 지운 마지막 시각. 지운 적이 없으면 null. 보고서는 그대로다. |
| `pull_request` | pull request run에만 있다. 고정한 pull request와 결과 댓글 상태([pull request run](#pull-request-run)) |

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

프로젝트 구성원은 실행의 보고서를 조회하거나 게시된 리비전을 선택해 내려받을 수 있다. 보고서의 분석 항목과 화면 구성은 [보고서와 화면](REPORT.md)에서 설명한다.

### GET /runs/{id}/report

| query | 값 | 기본 |
|---|---|---|
| `revision` | 1 이상 정수 | 최신 리비전 |
| `format` | `json`, `markdown`, `html` | `json` |
| `include_code` | `true`, `false` | `false`(코드 미포함) |
| `detail` | `summary`, `full` | `summary`(요약 보기) |

- `detail`은 Markdown·HTML의 상세도다([내보내기 상세도](REPORT.md#내보내기-상세도)). `summary`는 긴 목록을 묶어 대표 항목을 보여 준다. `full`은 개별 진단·발견·작업의 전체 행을 싣는다. JSON은 언제나 전체이므로 `detail`이 바뀌어도 같은 바이트다. 다른 값은 400이다.
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
| `Content-Security-Policy` | HTML은 외부 자산을 차단하고 절 목차 스크립트의 SHA-256 해시를 허용하는 보고서 전용 정책 |

`include_code=true`는 게시된 발췌, 도구 메시지, 검토 문구를 반환한다. 조회 시에는 스냅샷이나 Git에서 소스 파일을 다시 읽지 않는다.

### GET /runs/{id}/reports

게시된 리비전 목록이다. 항목마다 `id`, `revision`, `status`, `quality_verdict`, `sha256`(저장 본문 해시), `size_bytes`, `created_at`을 주고, 함께 `latest_revision`, `formats`, `default_include_code: false`, `default_baseline`(`{run_id, revision, url}` 또는 null)을 준다. 비구성원은 404다.

### 분석 근거 필드

이 필드는 근거를 확보한 경우에만 반환한다. 예전 리비전에는 항목이 없을 수 있다. 전체 구조는 [보고서 스키마](../schemas/report.schema.json)에 있다.

| 필드 | 해석 |
|---|---|
| `observations[].metric.counterpart` | 중복된 코드의 두 번째 블록. 첫 블록은 같은 관찰의 `location`이다. |
| `observations[].related_locations` | C/C++ 도구가 원래 진단과 연결한 보조 위치·가정. 코드 미포함 응답은 위치와 유형을 남긴다. |
| `metrics.go_package_dependencies` | Go 모듈의 import 관계와 줄 위치. 목록 조사가 끝나면 `unlisted_files`로 목록에 없던 후보 파일 수를 표시한다. |
| `documents.quality`, `documents.comparisons`, `code_review.strengths` | 수락된 모델 해석과 근거 ID. 문서 비교의 `code_evidence_ids`를 따라가면 근거가 코드인지 테스트인지 구별할 수 있다. |
| `documents.documentation_gaps` | 공개 CLI 선언의 `source`와 이름을 검색한 안내 문서 `searched`를 함께 제공한다. 코드 미포함 응답은 `symbol`과 `default`를 생략하고 선언 위치와 검색 범위를 보존한다. |
| `documents.evidence[].directive_path`, `package_reference` | Python docstring을 가리킨 원래 문서, 또는 사용 예제와 연결된 로컬 npm 패키지의 manifest와 진입 파일. |
| `executions[].go_test_failures` | 실패한 Go 테스트의 선언 경로와 줄. 코드 포함 응답에는 확인한 테스트 이름도 들어간다. 배열이 없으면 실행 상태·종료 코드와 `coverage.test_status`를 읽는다. [예시](../examples/go-test-failures.json) |

**테스트 케이스.** 생성기 19부터 `executions[].test_cases`는 이번 `test.run`에 연결된 `runner`, `counts.{passed,failed,skipped}`를 제공한다. 코드 포함 응답의 선택 필드 `cases`는 최대 128개이며 검증한 위치만 경로·줄을 갖는다. 전체 개수는 표본 길이와 다를 수 있다. 코드 미포함 응답에서는 `cases`를 생략한다. [보고서 스키마](../schemas/report.schema.json)

**문서와 구현 대조.** 기존 미확인 계약의 판정이 바뀌면 비교에 `followup: true`와 새 근거를 싣는다. 결론을 유지하며 추가로 읽은 구현은 `additional_observation`에 둔다. `related_workflow`는 같은 동작의 조회 코드·변경 코드·테스트를 각각 근거 ID로 연결한다. 테스트 실행 여부는 `coverage.test_status`에서 확인한다. 조사 범위를 벗어나 생략한 항목은 `gaps[]`의 `document_scope_items_omitted`에 기록한다. 형식 복구 뒤에도 검사를 통과하지 못해 뺀 대조는 `gaps[]`의 `contract_comparison_omitted`에 기록하고 그 계약은 미확인으로 둔다. [연결된 근거 예시](../examples/review/investigate-workflow-reading.json)

**발췌와 내보내기.** 발췌는 고정 스냅샷에 저장한 원문이다. 도구 진단 문맥은 최대 9줄, 새 모델의 코드 발췌는 최대 50줄·JSON 인코딩 1,800바이트, 생성기 18의 문서 인용은 최대 40줄·2,000바이트다. `include_code=false`는 발췌를 생략하고 경로·줄·근거 참조를 보존한다. [코드 미포함 응답](REPORT.md#코드-미포함-내보내기)

## 비교와 판정 경로

비교 상태·이유·판정 적용 규칙은 [보고서와 화면](REPORT.md)에 있다. 응답 형식은 OpenAPI `RunComparison`과 [`examples/comparison.json`](../examples/comparison.json), [`examples/finding-reviews.json`](../examples/finding-reviews.json)이다.

### GET /runs/{id}/comparison

- query는 `revision`(생략 시 최신), `baseline`(run ID), `baseline_revision`(생략 시 기준 run의 최신)이다. 모르는 parameter·반복 parameter는 400, `baseline` 없이 `baseline_revision`만 있으면 400이다.
- 권한은 프로젝트 읽기(viewer 포함)다. 비구성원 404, 게시 보고서가 없는 run 404, 기준 run이 다른 프로젝트이거나 없으면 404, 자기 자신을 기준으로 주면 422다.
- 비교는 요청마다 저장된 게시 보고서에서 다시 계산한다. 응답은 현재 리비전의 `run_id`·`project_id`·`revision`·`report_created_at`, `baseline`(`run_id`, `revision`, `report_created_at`, `selection`) 또는 null, 비교 결과(`comparable`, `not_comparable_reason`, `differences`, `counts`, `entries`, `unidentified`), 판정 오버레이(`findings`, `summary.at_publication`, `summary.current`), `can_review`, `checked_at`(DB 시각)을 담는다.
- 발견 목록에서 사라진 도구 진단이 현재 실행의 원시 관찰에 남아 있으면 `{"state":"absent_unverified","reason":"active_observation_remains"}`로 표시한다.

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
| `POST /projects/{id}/members` | `{email, role}` 또는 `{username, role}` | 200 `Member`. 이메일이나 기존 사용자 이름으로 구성원을 추가하거나 역할을 바꾼다 |
| `PUT /projects/{id}/members/{uid}` | `{role}` | 200 `Member`. 사용자 ID로 구성원을 추가하거나 역할을 바꾼다 |
| `DELETE /projects/{id}/members/{uid}` | — | 204. 구성원이 아니었으면 아무것도 바꾸지 않고 204 |

- 권한은 소유자와 manager다. viewer·runner 403, 비구성원(관리자 포함) 404.
- `role`은 `manager`, `runner`, `viewer`이고 그 밖은 422다. 소유자는 바꾸거나 제거할 수 없다(409). 없거나 비활성인 사용자는 404다.
- 역할 확인이 먼저라 viewer·runner는 계정 존재와 무관하게 403이다. manager에게는 없는·비활성·형식이 틀린 식별자가 모두 같은 404다. 이메일의 앞뒤 공백과 대소문자, 기존 사용자 이름의 대소문자는 무시하되 완전히 일치해야 한다. [결정 기록](DECISIONS.md)
- 변경과 감사 기록(`project.member_set`, `project.member_removed`, detail에 이전 역할)은 같은 트랜잭션이다. 제거된 사용자의 다음 요청은 404이고 열린 이벤트 스트림은 다음 poll에서 `closed`(`access_revoked`)로 끝난다.

## 계정과 관리자

관리자 경로는 활성 관리자의 세션이 필요하다. 관리자 역할은 어떤 프로젝트도 열지 않는다. 형식은 OpenAPI의 `UserView`, `UserWithLink`, `AuditPage`와 [`examples/accounts.json`](../examples/accounts.json)이다.

| 경로 | 본문 | 결과 |
|---|---|---|
| `GET /admin/users` | — | `{items: UserView[], next_cursor}`. hash·salt는 없다 |
| `POST /admin/users` | `{username, display_name?, is_admin?}` | 201 `{user, login_link}`. 비밀번호 없는 계정(`must_change_password: true`)과 일회용 로그인 링크. 쓰인 이름 409 |
| `POST /admin/users/{id}/disable` | — | 200 `UserView`. 모든 세션 폐기. 마지막 활성 관리자 409. 이미 비활성이면 변경 없이 200 |
| `PUT /admin/users/{id}/admin` | `{is_admin}` | 200 `UserView`. 마지막 활성 관리자 해제·비활성 사용자 지정 409 |
| `POST /admin/users/{id}/password-reset` | — | 200 `{user, login_link}`. 비밀번호 삭제, 모든 세션과 이전 로그인 링크 폐기, 새 로그인 링크. 자기 자신과 로그인 링크 전용 사용자는 각각 409, 비활성 사용자는 404. 링크로 시작한 세션은 비밀번호 변경에 묶이고 그 세션의 감사 행에 `link_issued_by`가 붙는다 |
| `GET /admin/source-roots` | — | `{items: AdminSourceRoot[]}`: `{id, label, path, everyone, created_at, grants: [{user_id, username, granted_at}]}`. 서버 경로는 관리자에게만 보인다 |
| `POST /admin/source-roots` | `{path, label?, everyone?}` | 201 `AdminSourceRoot`. 절대 경로의 있는 폴더여야 한다(422 `FOLDER_NOT_FOUND`). CODEYE 데이터 디렉터리 안은 422 `FOLDER_HOLDS_CODEYE_DATA`, 같은 경로·이름 409. label을 비우면 폴더 이름. 감사 `source_root.added`(와 `source_root.everyone_set`) |
| `PUT /admin/source-roots/{rid}/everyone` | `{everyone}` | 204. 모두에게 열거나 닫는다(사용자별 grant는 그대로). 감사 `source_root.everyone_set` |
| `PUT/DELETE /admin/source-roots/{rid}/grants/{uid}` | — | 204. 이미 그 상태면 변경 없이 204 |
| `GET /admin/providers` | — | `{items: [{id, kind, host, api_url}]}` |
| `POST /admin/providers` | `{host}` | Git host 승인. 새로 더하면 201, 이미 있으면 200(`AdminProvider`). `github.com`은 `github`, 그 밖은 `ghes`. 형식 오류 422. 감사 `provider.added` |
| `GET /admin/runtime-paths` | — | 관리자에게 로컬 도구의 수동 지정 값(`paths`), 시작 시 찾은 기본값(`automatic_paths`), `auto_detect`와 `editable`을 반환한다. |
| `PATCH /admin/runtime-paths` | `{"paths":{"java":"/opt/jdk-21","go":""}}` | Python·JDK·Go·Cargo·Node 중 보낸 키만 한 번에 저장한다. 빈 문자열은 수동 지정을 해제한다. 일반 사용자 또는 CSRF 오류는 403, 별도 controller는 409, 알 수 없는 키는 400, 실행 파일 확인 실패는 422와 해당 키를 반환한다. [사용 안내서](GUIDE.md#언어-도구-경로) |
| `GET /admin/opencode` | — | 관리자가 저장한 경로와 모델, 수정 가능 여부, 현재 선택된 모델과 연결 확인 결과. 인증 파일 내용은 전달하지 않는다. |
| `PUT /admin/opencode` | `{executable, config_file, credential_file, model}` | 관리형 서비스의 로컬 OpenCode 설정을 교체한다. 경로는 절대 경로 또는 서비스 계정의 `~/`로 입력한다. 빈 값은 설치 기본값을 쓴다. 저장은 모델을 호출하지 않으며 새 분석부터 적용된다. |
| `POST /admin/opencode/probe` | — | 저장된 설정으로 합성 입력을 한 번 보낸다. 응답의 `probe`와 `reason`으로 결과를 확인한다. [관리자 설정](GUIDE.md#관리자-화면에서-연결하기) |
| `GET /admin/settings` | — | `ServiceSettings`: `{others_model_review, others_build_scripts, updated_at, service_origin}` |
| `PUT /admin/settings` | `{others_model_review?, others_build_scripts?}`(하나 이상) | 200 `ServiceSettings`. 관리자가 아닌 사람이 시작한 run에 다음 배정부터 적용한다. 바뀐 값이 있을 때만 감사 `service.settings_set` |
| `GET /audit` | — | 전체 감사 기록 `AuditPage` |

- 로그인 링크(`login_link.url`, `expires_at`)는 응답에 한 번만 나온다. 관리자는 링크를 사용자에게 안전한 경로로 전달하고, 사용자는 링크로 로그인한 뒤 `POST /session/password`로 비밀번호를 정한다. 그 전에는 다른 경로가 403 `PASSWORD_CHANGE_REQUIRED`다.
- source root와 provider는 웹(위 경로)이나 운영자 CLI(`codeye source-root add`, `codeye provider add`)로 더한다. 웹에서 더한 root는 `codeye up`이 띄운 에이전트(능력 `source:controller`)가 배정에서 경로를 받아 바로 읽는다. 따로 띄운 노드는 자기 등록에 든 root만 읽는다.
- `codeye up`은 첫 관리자와 로그인 링크를 만든다. controller를 따로 띄운 운영자는 `codeye user create --admin`, 비밀번호 직접 지정은 `codeye user passwd`를 쓴다([운영](OPERATIONS.md#계정과-관리자-cli)).
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

보고서의 선택 필드 `project`는 해당 실행의 `name`, `source`(Git 주소 또는 서버 경로), `started_at`, `finished_at`을 담는다. 시간은 워커 배정과 반납의 UTC 시각이다. 이전 리비전에서 누락된 값은 조회 시 추정하지 않는다.
