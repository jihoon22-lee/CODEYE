# 시험과 요구사항 추적

수용 시험(TC)의 정의, 요구사항과 시험의 연결, 시험을 실행하는 방법을 정한다. TC 번호와 뜻은 바꾸지 않는다. 요구사항 ID의 뜻은 [요구사항](REQUIREMENTS.md)에 있다.

## 시험을 읽는 방법

- 각 TC는 제품 경로에서 확인할 조건과 결과다. 보조 검사(문서 validator, fixture 검사)의 통과를 TC 통과로 보고하지 않는다([보조 검증과 제품 시험의 차이](#보조-검증과-제품-시험의-차이)).
- **설계(미구현)** 표시는 해당 기능이 없어 아직 실행할 수 없는 시험이다. **일부** 표시는 `codeye up`의 한 서비스에서 일부 조건만 실행할 수 있다는 뜻이며 괄호 안에 남은 부분을 적는다.
- **실모델** 표시는 승인된 모델 연결이 있어야 하는 시험이다. CI는 모델을 호출하지 않는다.
- 표시가 없다는 것은 다른 문서에서 미구현으로 확인되지 않았다는 뜻일 뿐이며, 자동 시험이 있거나 모든 조건을 실행했다는 보장이 아니다. 실기 검증은 WSL2 호스트에서 했고 Windows는 CI의 Go 단위 시험까지 확인했다. RHEL 8.10 호스트 커널과 여러 물리 머신은 시험하지 않았다.
- 요구 열은 주된 연결이다. 전체 연결은 [요구사항 추적표](#요구사항-추적표)에 있다.

## 입력·환경

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-01 | 설명 없는 Git/local 프로젝트 등록 | 필수 추가 문서 없이 요청 가능, 자동 탐색 및 추정 표시 | REQ-001,004,005 |
| TC-05 | monorepo·문서 없음·문서 충돌·버전 불일치 | 모듈별 사실/추론·제약 기록, 가능한 분석 계속 | REQ-001,004,005 |
| TC-36 | Native doctor 실행, 도구 없음·실패·출력 초과·timeout | OS/arch와 실제 버전만 기록, 실패/미검증 구분, bounded probe, secret 상속·원시 출력 노출 없음. 도구는 에이전트와 같은 탐색으로 찾고 JSON 보고서에 경로가 없다 | REQ-004,030; NFR-001,006 |
| TC-37 | DB 미설정·정상·잘못된 인증·없는 DB·허용되지 않은 TLS 설정 | 인증 query 성공만 available, 고정 실패 reason, credential argv/결과 비노출, 설정 없는 네트워크 호출 없음 | REQ-030; NFR-006 |
| TC-39 | 웹에서 doctor JSON 읽기, 손상·미지원 schema·중복 ID·파일 교체 | 실제 결과만 표시, 오류와 미검증을 성공으로 바꾸지 않음, 파일 서버 전송 없음, 최신 선택 유지 | REQ-013,030; NFR-006 |
| TC-96 | 루트와 중첩 모듈의 버전 요구가 다르고 노드 관측이 한 모듈에만 맞음 | 모듈별 요구·관측·적용 범위·근거가 분리된다. 모델이 없어도 사실과 미확인 값이 남는다. | REQ-004,005,006 |

## 저장

| TC | 검증 | 요구 |
|---|---|---|
| TC-40 | 빈 DB 적용·재적용·동시 migration, checksum/future/gap 거부, 실패 rollback | NFR-003 |
| TC-41 | 프로젝트 ACL·권한 철회·immutable revision·원자적 생성 | REQ-002,003 |
| TC-42 | 동시 멱등 enqueue·body 충돌·상태/event rollback·cursor 순서 | REQ-022; NFR-002,003 |
| TC-43 | 동시 소유권 경쟁·node/epoch 불일치·만료 fencing·불확실 cleanup·명시적 재시도 | REQ-022,035 |
| TC-44 | snapshot 한 번 고정·동일 입력 재시도·task 실행 이력·다른 run/project 근거 차단 | REQ-003,013; NFR-007 |
| TC-45 | 성공을 finalizing에 유지·미실행/실패 구분·오류 credential 비노출 | REQ-013; NFR-006 |

저장 계층 시험은 agent·NativeExecutor·artifact bytes·보고서 종단 통과를 뜻하지 않는다.

## API·인증

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-02 | 웹 계정이 플랫폼 소스로 각각의 프로젝트를 등록하고 타인 프로젝트·보고서·SSE 접근을 시도 | 서비스 계정이 읽을 수 있는 소스는 등록. 프로젝트 접근은 구성원 ACL에 따라 제한 | REQ-002,015; NFR-006,007 |
| TC-31 | **일부**(회사 SSO·GitHub App 권한 철회는 미실행). 권한 철회/삭제·code-free export | source/report/SSE 차단, export 내용과 metadata 민감값 제거. 구성원 제거와 사용자 비활성화가 열린 SSE를 `access_revoked`·`unauthenticated`로 닫고 다음 요청이 404·401인 것, 노드 철회가 실행 중 에이전트를 종료 코드 4로 끝내는 것은 TC-255 D8이 실행한다 | REQ-028,029,031; NFR-006,007 |
| TC-46 | loopback 강제, Host/Origin/CSRF 거부 | 요청 거부 | NFR-006 |
| TC-47 | 일회용 launch token, 동시 교환·비활성화, 세션 만료/폐기 | 한 번만 교환되고 만료·폐기 뒤 거부 | REQ-002; NFR-006 |
| TC-48 | 설명 없는 local/Git 선언 등록, root grant | 등록 성공, 소스 미검증 표시 | REQ-001,002,005 |
| TC-49 | 비구성원·viewer·철회된 구성원의 접근 | 비구성원 404, viewer 403, 철회 후 접근 거부 | REQ-002 |
| TC-50 | 멱등 요청·충돌·DB 실패·서버 재접속 | 멱등 202, 충돌 409, DB 실패 503, 재접속 후 상태 보존 | REQ-022; NFR-002,003 |
| TC-51 | readiness 조회, serve의 구/신/변조 schema | readiness는 읽기 전용, 맞지 않는 schema 거부 | NFR-003 |
| TC-52 | 정적 파일 경계·CSP·비밀값 비노출·실브라우저 요청/취소 | 경계 밖 파일 없음, CSP 적용, 비밀값 없음, 브라우저에서 요청·취소 동작 | NFR-005,006 |

TC-02·TC-31은 소유자와 프로젝트 구성원 ACL, 플랫폼 소스 접근을 분리해 시험한다(TC-258·TC-259·TC-261).

## 계정·권한·감사

여러 사용자 사이의 격리는 아래 시험이 실제 HTTP와 PostgreSQL로 확인한다. 두 사용자의 웹 화면은 TC-263이 확인한다.

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-140 | 라우터의 모든 경로(`Server.routes`에서 목록을 만든다)를 관리자(비구성원), 외부인, viewer, runner, manager, 소유자로 요청한다. 노드 경로는 사용자 세션으로 요청한다 | 사례가 없는 경로가 있으면 실패한다. 비구성원은 관리자도 404, 역할 부족 403, 관리자 경로는 관리자만. 거절된 요청은 감사 행이 없다. 받아들인 상태 변경(과 내보내기)은 그 요청 ID의 감사 행이 경로가 선언한 action 중 하나다. 상태를 바꾸는 사용자 경로는 모두 감사 action을 선언한다. 노드 경로는 사용자 세션으로 401. 같은 경로 목록으로: 세션 없음 401(인증 경로 전체), 다른 Origin·CSRF 없음 403이고 감사 행 없음(상태 변경 경로 전체), 비밀번호 변경 대기 세션 403(허용 경로 밖 전체) | REQ-002,031; NFR-006 |
| TC-141 | 비밀번호 로그인: 맞는 비밀번호, 틀린 비밀번호, 없는 사용자, 빈 이름, 섞인 본문, 다른 Origin, 연속 실패, 잠긴 뒤 맞는 비밀번호, 없는 이름의 연속 실패 | 201과 계정 필드. 틀림·없음·빈 이름은 같은 401과 같은 문구. 섞인 본문 400, 다른 Origin 403. 한도를 넘으면 429와 `Retry-After`(= `details.retry_after_seconds`), 맞는 비밀번호도 429. 없는 이름도 같은 방식으로 잠긴다. 로그에 비밀번호가 없다 | REQ-002; NFR-006 |
| TC-142 | 관리자가 만든 계정의 첫 로그인과 비밀번호 설정, 변경 대기 세션의 다른 경로, 현재 비밀번호 없음·틀림·CSRF 없음, 다른 세션, 관리자 재설정·자기 재설정·비관리자 재설정, 비활성화, 마지막 관리자 | 변경 대기 세션은 403 `PASSWORD_CHANGE_REQUIRED`. 첫 비밀번호는 현재 비밀번호 없이 정한다. 변경은 다른 세션을 끝내고 이 세션은 유지한다. 약한 새 비밀번호는 현재 비밀번호보다 먼저 422. 재설정은 모든 세션과 이전 링크를 끝내고 새 링크만 남긴다. 비활성화는 세션을 끝낸다. 마지막 관리자는 내릴 수 없다(409). 감사 detail·로그에 비밀번호가 없다 | REQ-002; NFR-006 |
| TC-143 | 프로젝트 목록·상세의 역할 필드, 비구성원의 목록, 프로젝트 감사 기록 | `role`·`can_run`·`can_manage`가 역할과 맞다. 관리자·외부인의 목록에 그 프로젝트와 run이 없다. 프로젝트 감사 기록은 그 프로젝트 행만 담고 `project` 필터를 받지 않는다 | REQ-002 |
| TC-144 | JSON·Markdown·HTML 내보내기(코드 포함·제외)를 관리자·외부인·viewer로, 다른 프로젝트의 기준 run, 감사 행을 쓸 수 없는 DB | 비구성원은 여섯 경우 모두 404이고 본문·감사 행이 없다. viewer의 내보내기마다 형식과 코드 포함 여부가 감사 detail에 남고 발견 문구는 없다. 다른 프로젝트의 기준 run은 두 프로젝트의 소유자에게도 404. 감사 행을 쓰지 못하면 내보내기 본문을 보내지 않고 run·구성원 변경도 커밋되지 않는다(503) | REQ-028,031; NFR-006 |
| TC-145 | 열린 이벤트 스트림 중 구성원 제거(API), 사용자 비활성화(관리자 API), 비밀번호 재설정, 변경 대기 세션의 스트림 | 다음 poll에서 `closed`(`access_revoked`, `unauthenticated`, `unauthenticated`)로 끝난다. 변경 대기 세션은 스트림을 열지 못한다(403) | REQ-002,016; NFR-006 |
| TC-146 | 수신 규칙: listen 형식, TLS 없는 비 loopback 주소, TLS 일부만, http·path·사용자 정보가 있는 public origin, group이 읽을 수 있는 키, Serve에 넘긴 평문 wildcard listener, 자체 서명 인증서의 TLS 서버 | 비 loopback은 TLS나 `--plain-http` 없이 시작하지 않고 그 선택 없는 평문 listener도 거부한다. TLS 서버는 HSTS를 보내고 public origin 밖의 Host를 403, http Origin을 403으로 거부한다. cookie는 `__Host-codeye_session`(Secure, HttpOnly, Domain 없음)이고 평문 이름 cookie는 401. 로그인 링크는 public origin을 쓴다. 같은 포트의 평문 요청은 API를 받지 못한다 | NFR-006 |
| TC-147 | 사용자 이름 정규화, 비밀번호 규칙, KDF 왕복·salt·변조, 기본 비용 | 대소문자 무시, 형식 밖 거부. 12자 미만·사용자 이름과 같음·한 문자 반복 거부. 같은 비밀번호도 salt가 달라 hash가 다르다. 기본 반복 횟수의 계산 시간을 기록한다 | NFR-006 |
| TC-148 | 저장 계층 비밀번호 로그인: 없는 사용자, 비밀번호 없는 사용자, 비활성 사용자, 틀린 비밀번호 | 모두 같은 오류이고 감사 행의 형태가 같다. 대상 사용자는 있는 경우에만 감사 행에 남는다(관리자만 읽는다) | NFR-006 |
| TC-149 | (이름, 주소) 쌍·주소 지연, 지연 감사 한 번, 동시 추측, 공유 주소, 지연이 끝난 뒤 성공, 행 정리, IPv6 /64, KDF 계산 자리 | 쌍 5회(같은 주소의 같은 이름만, 없는 이름도 같게), 주소 500회에서 지연되고 지연마다 `auth.login_throttled` 한 행. 같은 이름의 다른 주소와 같은 주소의 다른 이름은 로그인한다. 한 주소에서 다른 이름 499회 실패 뒤에도 다른 사람은 그 주소에서 로그인한다. 거절된 요청은 행을 만들거나 바꾸지 않는다. 지연이 끝난 뒤 성공하면 다음 로그인이 429가 아니다. 15분 지난 행은 다음 실패 때 지워진다. 행이 없는 키에 병렬 첫 시도 32개가 몰려도 KDF에 닿는 것은 5개이고 두 카운터가 모두 5다. 같은 /64의 두 주소는 쌍 지연을 같이 쓰고 다음 /64는 따로다. KDF 계산 자리가 없으면 로그인·변경은 기다린 뒤 503이고 아무것도 세거나 기록하지 않는다 | NFR-006 |
| TC-150 | 비밀번호 변경·재설정·운영자 설정과 반복 횟수 상향 | 다른 세션 폐기, 재설정 뒤 링크만 유효, 반복 횟수가 오르면 다음 로그인에서 다시 계산해 저장한다 | NFR-006 |
| TC-151 | 마지막 관리자: 비활성화·해제, 두 관리자의 동시 상호 해제 | 마지막 활성 관리자는 남는다. 동시 해제에서도 한 명이 남는다 | REQ-002 |
| TC-152 | 구성원 추가·변경·제거와 소유자 대상, 감사 읽기 필터·pagination·권한, 감사 detail 허용 목록 | 소유자는 409. 감사 읽기는 관리자(전체)·manager(그 프로젝트)만이고 구성원이 아닌 관리자는 프로젝트 감사를 읽지 못한다. 허용 목록 밖 key나 비밀번호 같은 값은 쓰지 못한다 | REQ-002; NFR-006 |
| TC-153 | migration 0010을 로그인 링크 사용자가 있는 0009 DB에 적용, 감사 행 수정·삭제·truncate, 0010 중간 실패 | 기존 사용자는 이름·비밀번호 없는 비관리자로 남아 로그인 링크로 들어간다. 제한 키는 하나이고 바꿀 수 없다. 감사 행은 수정·삭제·truncate할 수 없다. 실패하면 전체가 rollback된다 | NFR-003 |
| TC-154 | `codeye user create/passwd/list/disable/admin`, `--password` 인자, 접근 가능한 비밀번호 파일, 약한 비밀번호, 중복 이름, 마지막 관리자 | 비밀번호는 표준 입력·0600 파일에서만 읽고 출력·감사 detail에 없다. 인자는 거부(2), 넓은 mode 파일 거부, 약한 비밀번호 2, 중복 1, 마지막 관리자 1. 변경마다 `operator_cli` 감사 행 | REQ-002; NFR-006 |
| TC-156 | 관리자 재설정의 기록과 표시: 이름 없는 사용자 재설정, 자기 재설정, 재설정 링크 세션(계정 표시를 지워도), 링크 로그인과 그 세션의 이후 행, 운영자 링크, 사용자의 세션 조회(여러 번 로그인, 지우기 시도, 30일 뒤), 경로 표 전체를 재설정 링크 세션으로 요청 | 이름 없는 사용자와 자기 재설정은 각기 다른 문구의 409. 재설정 링크 세션은 계정 표시와 무관하게 비밀번호 변경 전까지 403 `PASSWORD_CHANGE_REQUIRED`. 로그인 행에 `link_issued_by`(관리자 ID)·`link_purpose`, 그 세션의 이후 모든 행에 `link_issued_by`. 운영자 링크는 `operator_cli`. 세션 조회의 `password_reset`이 관리자·시각을 로그인마다 주고, 지우는 경로는 없으며(404), 30일 뒤에는 null이다. 격리 표의 manager 세션이 재설정 링크로 시작해 그 세션이 쓴 모든 행이 관리자를 가리킨다 | REQ-002; NFR-006 |
| TC-157 | 사용자 이름으로 구성원 추가: 대소문자가 다른 이름, 같은 이름으로 역할 변경, 없는·비활성·형식이 틀린 이름, owner 이름, viewer의 요청(있는 이름과 없는 이름) | 추가·변경은 200 `Member`와 `project.member_set` 한 행씩. 없는·비활성·형식이 틀린 이름은 404, owner는 409. viewer는 두 이름 모두 403이라 존재를 알 수 없다. 격리 표에서도 manager 이상만 200이다 | REQ-002; NFR-006 |
| TC-266 | 기존 DB migration, 이메일 대소문자와 공백을 달리한 동시 가입, 틀린 입력·Origin, 비밀번호 로그인과 변경, 같은 Git 주소를 등록한 두 계정. Chromium 가입·중복·재로그인 | 기존 사용자 ID·세션·프로젝트 소유권 보존. DB는 정규형만 받고 동시 가입 중 한 계정·세션만 저장. 다른 사용자의 프로젝트·실행·보고서는 ACL로 차단되고 같은 주소로 각자 실행 가능. 기존 사용자 이름·로그인 링크와 신규 이메일 로그인이 동작(`TestEmailAccountsMigrationKeepsLegacyOwnership`, `TestEmailRegistrationAndLogin`, `TestSignedUpUsersOwnIndependentProjects`, `web/e2e/password.spec.ts`) | REQ-001,002,031; NFR-006 |
| TC-155 | 계약 검사: 감사 action·행위자·대상 목록이 migration(0010·0011·0013·0014)·저장 계층·OpenAPI에서 같음, `examples/accounts.json`, 허용 detail key 이름 | `scripts/validate_bundle.py`가 대조하고 목록 차이·비밀번호 key·모르는 action·hash 필드 같은 부정 사례를 거부한다. 보조 검사이며 TC-140~TC-154를 대신하지 않는다 | NFR-006 |

TC-02·TC-31의 "여러 사용자" 부분은 TC-140·TC-144·TC-145가 실행한다. 웹 화면에서 역할에 따라 버튼을 숨기는 시험은 화면이 생길 때 더한다.

## 소스 확보

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-03 | 큐 대기 중 브랜치 변경, 확보 후 다시 변경 | 확보 시 SHA를 사용하고 후속 변경 미반영; 재시도도 동일 SHA | REQ-003 |
| TC-04 | local 복사 중 파일 변경·symlink root 탈출 | 변경 재시도/불안정 실패, 외부 링크 미추적, 원본 미수정 | REQ-002,003,015 |
| TC-53 | 승인 root 원자적 경계·symlink/FIFO 교체·credential 제외·원본 아래 data root | 경계 유지, credential 제외, 원본 아래 data root 거부 | REQ-002,015; TC-04 |
| TC-54 | 변경 감지 재시도·불안정 실패·manifest 무결성·독립 workspace·retained retry | 안정 사본 또는 `source_unstable`, manifest 무결, retained retry는 원본 재확보 없음 | REQ-003,015; TC-03,04 |
| TC-55 | 실제 Git 확보 | SHA 고정, filter 미실행, LFS/submodule 공백 표시, host/전송 제한, soft fetch quota | REQ-003,024,030 |

TC-01의 등록과 TC-04의 정상 소스 확보 경로를 읽기 전용 NFS에서 수동으로 확인했다. 외부망이 차단된 내부 브리지에 서버의 `secure` export를 두고 WSL2 호스트 커널에서 `ro,vers=4.1,sec=sys`로 마운트했다. Red Hat UBI 8.10 사용자 공간에서 UID 1000으로 실제 `codeye up --offline`을 실행했고 PostgreSQL 17은 같은 환경의 로컬 DB로 띄웠다. 탐색 산출물에 기록된 README SHA-256이 원본과 일치했다. 모델 미설정 상태에서 run이 `partial`로 끝났으며 로그인한 웹 화면에서 보고서를 열고 HTML을 내보냈다.

이 확인의 호스트 커널은 WSL2였다. RHEL 8.10 호스트 커널, 회사 NFS의 마운트·ACL, SSO·GHES, 여러 호스트·부하·장애 전환은 시험하지 않았다.

## 실행기

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-17 | Native에서 정적 도구와 기존 테스트 실행(빌드 전용 작업은 후속) | 원본 hash 불변, 작업별 환경·자원·로그 독립. `with-tests`는 Go 선택 패키지와 npm의 단위·모듈·E2E 스크립트를 Linux 전용 loopback에서 실행한다. 파일 범위 npm, 코드 실행 허가 거부, 네트워크 격리 실패에서는 테스트 명령이 실행되지 않는다. Python unittest와 Cargo의 실제 실행 결과 및 통과한 테스트가 없는 출력을 구분한다(`internal/worker/testexec`). | REQ-002,014,015; NFR-001,009 |
| TC-275 | 격리된 Go 테스트에서 최상위 테스트 하나가 실패하는 사본과 정상 사본, 개별 테스트를 알 수 없는 명령 실패 | 실패한 작업에서 실행 기록과 스냅샷 소스가 일치하는 테스트 이름과 선언 줄만 보여 준다. 정상 사본에는 실패 항목이 없다. 코드 미포함 응답에는 이름과 원문을 넣지 않는다. 위치가 불분명하거나 출력이 잘리거나 연결된 기록이 맞지 않으면 세부 근거를 생략하고 작업 상태를 보존한다. | REQ-002,012,013,014,015; TC-17 |
| TC-276 | 한 문서의 인용 범위 뒤쪽에 있는 계약 문장이 일반 코드 발췌 바이트 한도를 넘지만 모델에 제공된 문서 범위에는 포함되는 경우 | 저장된 문서 발췌에서 해당 문장의 실제 줄이 보인다. 다른 코드·도구 발췌 한도, 스냅샷·비밀값 검증, 코드 미포함 투영을 유지한다. 이전 리비전에서 인용 범위와 저장 발췌가 다르면 상세에서 구별한다. | REQ-005,011,012,013; TC-268 |
| TC-18 | 손자 프로세스를 만든 작업 취소/timeout | 그룹 종료 확인, 불확실하면 recovery_required | REQ-014,022,035; NFR-001,009 |
| TC-56 | literal argv·최소 env·원본/snapshot/cwd 경계 | 경계 유지, 비밀값과 잘린 출력 마스킹 | REQ-014,015; NFR-006 |
| TC-57 | timeout/cancel 후 자식·손자 | OS 종료 확인, 잔류/탈출·관측 실패는 uncertain | REQ-022,035; TC-18 |
| TC-58 | Windows suspended 생성→Job 연결→resume, breakaway | breakaway 거부, 미지원과 enforced limit 구분 | NFR-001,009 |
| TC-189 | 한 attempt의 작업이 작업 사본의 manifest 파일을 고쳐 쓰고 새 파일·디렉터리를 남김, 뒤 작업이 사본 전체를 검사, 자기 입력을 고쳐 쓰는 작업, 링크·같은 크기로 바꾼 내용·바뀐 권한, 취소 | 작업이 끝날 때마다 작업 사본을 스냅샷 내용으로 되돌린다. 남긴 작업에는 `workspace_restored_after_task`·`workspace_leftovers_removed`가 남고 뒤 작업은 drift 없이 성공한다. 자기 실행 중에 manifest 파일이 바뀐 작업은 여전히 `workspace_drift`로 실패한다. 도구 데이터 디렉터리는 지우지 않는다. 되돌리기에 실패하면 `workspace_restore_failed`를 남긴다(`TestWorkspaceRestoredBetweenTasks`, `TestWorkspaceGuard*`) | REQ-002,014,015; TC-70,116 |

프로세스 그룹/Job은 OS 권한 샌드박스가 아니다.

## 노드·워커·스케줄러

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-19 | min0/max2, 작업 유입 후 유휴 | 0→최대2→0, agent/controller는 살아 있음 | REQ-020,021 |
| TC-20 | busy3 상태에서 max1로 축소 | 작업 강제 종료 없이 drain, 새 작업 제한 | REQ-020,021 |
| TC-21 | **한 호스트**(여러 물리 머신은 미실행). 두 agent가 같은 run을 동시에 claim | 한 활성 epoch만 소유, 노드/전역 예산 중복 차감 없음. 동시 claim은 TC-60, 한 호스트의 노드 신원 8개 배정·대기·사망·분할·drain·갱신은 TC-250~254가 실행한다 | REQ-018,019,022,035; NFR-002,003 |
| TC-22 | **한 호스트**(여러 물리 머신의 실제 network partition은 미실행). node partition 후 오래된 complete 도착 | lease fence 409, 최신 보고서 미덮어쓰기. 로컬 TCP 프록시 차단과 SIGSTOP·SIGKILL로 TC-251·TC-252이 실행한다(늦은 complete 409, 새 attempt 결과 유지, 중복 실행 없음) | REQ-022,035; NFR-006,007 |
| TC-23 | controller/agent 재시작·PID 재사용 | journal/DB reconciliation, 중복 워커 방지, 다른 PID 오살 없음 | REQ-022,035; NFR-001,002,003,009 |
| TC-24 | **설계(미구현)**: endpoint별 모델 동시 요청 상한과 수요 기반 증감([실행](EXECUTION.md)). 모델 endpoint 포화·CPU 여유 | 모델 동시 제한 유지, 가능한 CPU task는 진행, 과도한 worker 증가 억제 | REQ-018,019; NFR-008,010 |
| TC-59 | 일회용 등록, 철회·만료 | 사용자와 노드 credential 분리 | REQ-019; NFR-006 |
| TC-60 | 두 노드의 동시 claim | 한 epoch·예약·이벤트, 재전송과 실패 rollback | REQ-018,019,022; TC-21 |
| TC-61 | 호환성·root 권한·자원 조건, 비호환 요청 뒤의 호환 작업 | 실제 대기 사유 표시, 호환 작업 선택 | REQ-002,018,019 |
| TC-62 | 갱신 중단 | stale 쓰기 409, cleanup 불확실 시 예약 유지 | REQ-022,035; TC-22 |
| TC-63 | 워커 환경·IPC, 취소, journal·PID 재사용 | 노드 비밀값 제외, 취소 후 자손 종료, 복구 | REQ-015,022; TC-18,23 |
| TC-64 | 고정 snapshot 재시도와 artifact 접수 | hash·크기·attempt 소유권에 묶인 artifact만 접수 | REQ-003,035; NFR-007 |
| TC-65 | 기존 0002 DB 이행, Linux 실제 두 에이전트에서 취소 후 다음 run, Windows Job 소유자 종료 | 이행 성공, 다음 run 접수, Job 종료 | NFR-001,003,009 |
| TC-71 | 0→2→0, 호환 능력 probe, 유휴 시간·상하 쿨다운·warm minimum | 정책대로 증감 | REQ-020,021 |
| TC-72 | busy 3개에서 max 1로 축소 | 완료까지 drain·새 claim 차단 | REQ-020,021,022 |
| TC-73 | 전역·프로젝트·pool·노드 자원 상한 | 공정 배정과 대기 사유 | REQ-016,018,019,020 |
| TC-74 | agent 강제 종료·재시작, controller 재시작 | 명령 중복 적용 방지 | REQ-021,022 |
| TC-75 | lease 만료 후 늦은 complete | 409, 정리 확인 후 예약 해제 | REQ-022,035 |
| TC-246 | `recovery_required` run의 정리 확인: 다른 상태의 run, 본문 없음·`false`·`{}`·다른 필드, 실행 권한 없는 사용자, 운영자 CLI(`--confirm-cleanup` 없음·있음), 확인 두 번, 웹 확인 상자 | 확인 없이는 400(본문 없음 415)·CLI 종료 코드 2이고 아무것도 바뀌지 않는다. 확인하면 한 트랜잭션에서 attempt `released`·`confirmed`, 예약 해제, run `failed`(사유 `operator_confirmed`), 감사 `run.recovery_confirmed`(사용자 또는 `operator_cli`)다. 신선한 노드가 snapshot을 가지면 같은 run, 아니면 새 run이 202로 대기열에 들어가고 운영자 경로의 재시도는 요청자 대신(`system`)이다. 두 번째 확인은 409 `RUN_NOT_RECOVERY_REQUIRED`다. 웹은 상자를 체크해야 단추가 켜진다(`TestConfirmRecoveryReleasesAndRequeues`, pull request run이 고정값·댓글 선택을 그대로 갖고 다시 들어가는 `TestRecoveredPullRequestRunKeepsItsPin`, DB URL·설정이 없으면 아무것도 만들지 않고 종료 코드 2인 `TestRunRecoverWithoutDatabaseCreatesNothing`, `TestRecoverRunNeedsTheStatedConfirmation`, `isolation_test.go`의 `runs.recover`, `web/src/StartScreen.test.tsx`) | REQ-021,022; NFR-003 |
| TC-247 | 고정 snapshot 재시도: snapshot을 가진 노드가 stale·철회됨, 노드가 `snapshot_unavailable`로 끝냄 | stale·철회 노드의 위치는 보유로 세지 않아 새 run이 된다. `snapshot_unavailable`은 그 노드의 위치 기록을 지운다(`TestConfirmRecoveryReleasesAndRequeues`, `TestSnapshotUnavailableDropsTheLocation`) | REQ-003,035 |
| TC-250 | 한 호스트의 노드 신원 여럿: go 있는 노드, go 없이 다른 기준 분석기만 있는 노드, 도구 없는 노드, root·Git host가 다른 노드, 아무도 못 하는 run | run은 root·Git host·analysis 능력이 맞는 노드만 받는다. go 없는 노드의 Go 작업은 `unsupported`(`tool_not_configured`)이고 run은 `completed`가 아니다. 도구 없는 노드는 root를 맡아도 능력을 알리지 않는다. 못 하는 run은 `no_capable_node`로 대기하다 능력 있는 노드가 등록되면 시작한다(`scripts/multinode_smoke.py` S1, 실제 go vet) | REQ-002,018,019 |
| TC-251 | 작업 중 노드의 에이전트·워커·작업 프로세스 SIGKILL | 20초 뒤 노드 stale, lease 만료 뒤 `recovery_required`(cleanup `uncertain`), 자동 재배정 없음. 죽은 노드 자격·옛 epoch의 complete는 409와 `attempt.stale_rejected`. 프로세스가 없음을 확인한 뒤 사용자가 정리를 확인하면 새 run이 다른 노드에서 끝나고, 그 뒤의 옛 complete도 409이며 새 결과가 바뀌지 않는다. 같은 상태로 다시 띄운 에이전트는 옛 attempt를 정리 보고하고 새 일을 받는다(S2) | REQ-021,022,035; NFR-003 |
| TC-252 | 한 노드만 지나는 로컬 TCP 프록시 차단(분할) 뒤 복구, lease 안의 8초 SIGSTOP | 분할 동안 같은 run을 다른 노드가 실행하지 않는다. 에이전트가 lease 여유에서 `lease_lost`로 작업을 멈추고 복구 뒤 정리 보고로 run `failed`, 늦은 complete 409, 같은 노드가 다시 일을 받는다. 짧은 정지는 상태를 바꾸지 않는다(같은 attempt·epoch 1로 끝)(S3) | REQ-022,035; NFR-003 |
| TC-253 | 자동 모드 노드의 drain(SIGUSR1) 중 새 run | 하던 일은 첫 attempt로 끝나고 새 run은 `no_capable_node`로 대기하며 그 노드에 `start_worker`가 가지 않는다. drain heartbeat는 static 증감·analysis 능력 없음이고, 에이전트는 종료 코드 0이다(S4, `TestDrainingAgentLeavesTheScalingPlan`) | REQ-020,021 |
| TC-254 | 에이전트 갱신: drain → 종료 → 다시 빌드한 바이너리(hash 다름)를 같은 상태 디렉터리로 시작 | 같은 node ID·비밀값, `node.enrolled` 감사 없음, protocol `0.1`, controller 증감으로 복귀해 대기 run을 끝낸다(S5) | REQ-019,021; NFR-003 |

노드 수용 시험(TC-59~65)은 전용 빌드의 시험용 handler로 프로토콜과 OS 동작을 검사한다. 한 머신에서 별도 노드 신원 둘로 실행하며 여러 물리 머신·NFS·network partition 보증을 대신하지 않는다. TC-250~254도 한 WSL2 호스트에서 실행한다. 측정값은 [실행](EXECUTION.md#여러-노드에서-확인한-것)에 있다.

## 분석기

Go 형식 비교는 TC-07·TC-08·TC-101의 판정과 범위를 따른다. `TestReadOnlyGoFmtCoverage`는 실제 도구의 서식 일치, 서식 차이, 구문 오류, 제외 경로, 입력 작업 사본 불변을 시험한다.

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-277 | Maven·Gradle 모듈, JDK 선언, PMD 소스 규칙, 같은 스냅샷에서 컴파일한 SpotBugs 클래스 | 선택 모듈의 파일·줄과 도구 관찰이 연결된다. 일치하는 외부 JAR만 클래스패스에 쓰며 누락·다른 버전·해시 불일치·컴파일 실패는 해당 행에 남긴다. | REQ-003,006,007,008,014 |
| TC-278 | 여섯 언어의 공개 기능이 다른 파일을 호출함. 별칭·같은 이름·빌드 조건·범위 밖 정의 대조 | 조사 및 후보 검증 입력에 확인한 호출과 구현이 들어간다. 모호한 대상에 확정 호출 관계를 붙이지 않는다. | REQ-009,010,011,027 |
| TC-279 | 기존 테스트가 모두 통과, 일부 실패, 모두 건너뜀, 실행 준비 실패 또는 결과 파일 없음 | Linux 작업 사본에서 확인한 케이스만 집계한다. 전체 수와 표본 길이를 구분한다. 코드 미포함 JSON·HTML에서는 케이스 이름을 생략한다. | REQ-011,012,013,014 |
| TC-280 | 실행한 테스트의 줄이 후보의 인용 범위 안·밖에 있거나, 실행 산출물의 SHA가 달라짐 | 접수한 실행의 해당 범위만 검증 역할 및 보고서에 연결한다. 다른 테스트의 결과나 변경된 산출물로 결함을 재현했다고 판정하지 않는다. | REQ-009,010,011,013,027 |
| TC-282 | 관리자가 웹에서 언어 실행 파일·설치 디렉터리를 지정하고 해제 | 다음 로컬 분석의 go vet이 새 실행 파일을 사용한다. 해제하면 자동 경로가 복원된다. 잘못된 경로를 함께 보내면 설정을 보존하며, 일반 사용자의 조회·저장과 CSRF 누락을 거부한다. | REQ-004, NFR-006 |
| TC-281 | Go 반환 오류, Rust 문서 링크, C 파일 디스크립터 경로, Python·JS/TS 모듈 의존 규칙의 정상·위반 사례 | 추가 검사 도구의 실제 결과에서 스냅샷 위치를 찾는다. 읽지 않은 모듈이나 설정 때문에 수행하지 못한 검사는 완료로 집계하지 않는다. | REQ-006,007,008,012 |
| TC-06 | C++ compile DB가 원본 절대 경로를 포함 | workspace 매핑·include 권한 확인, 분석 TU 범위 표시 | REQ-006,007,008 |
| TC-07 | 도구가 findings용 nonzero exit를 반환 | tool crash와 구분하고 정상 observations 생성 | REQ-006,007,008 |
| TC-08 | 출력 손상·일부 파싱·timeout | raw 보존, 실패 범위 표시, 0건 통과 금지 | REQ-006,007,008 |
| TC-09 | 비슷한 경고 두 개와 다른 원인 같은 줄 | 동일 원인만 묶고 서로 다른 조건은 유지 | REQ-009,010,011 |
| TC-66 | 실제 `go vet -json` exit 0 + 진단 | 관찰로 변환, snapshot 상대 위치·manifest hash·raw byte 범위, 순서 무관 ID, 노드 경로 미노출, 원본·snapshot 불변 | REQ-006,007,008,014; TC-07,17 |
| TC-67 | 타입 오류 부분 파싱, 잘림·손상·analyzer 오류 | 실패로 기록하고 0건으로 바꾸지 않음 | REQ-007,008; TC-08 |
| TC-68 | 오프라인 의존 모듈·toolchain 부족, snapshot 밖 `replace` | configuration_error, 다운로드 fallback 없음, `replace`는 실행 전 거부 | REQ-015; NFR-006 |
| TC-69 | timeout·취소·실행 거부·정리 불확실·비 Go 입력, probe 실패. 분석기 팩 composite에서 Go 소스가 범위에 없음, 분석기가 없는 언어의 소스, 지원 언어 소스 없음 | 각 상태 기록, probe 실패 시 capability 미광고. Go 소스가 없으면 go vet 작업을 계획하지 않는다. 분석기가 없는 언어의 소스는 필수가 아닌 공백 작업(`pack-gap.<생태계>`, `analyzer_pack_missing`)과 보고서 `static_analysis_gap`으로, 지원 언어 소스가 없으면 `static_analysis_not_planned`로 보이며 보고서는 `partial`이다 | REQ-012,022,035; TC-18 |
| TC-70 | build constraint·cgo로 빠진 패키지, 무시 파일, 분석 패키지 0개, 중첩 모듈·scope, workspace drift | 범위 표시, 0개는 미지원, drift 실패·위치 미연결, handler registry·result manifest 검증 | REQ-006,008; TC-05 |
| TC-101 | 각 분석기 팩. 도구 경로 없음, 잘못된 경로·버전, 필요한 toolchain(`--go`, 공유 `--node`) 없음·검증 실패 | 작업은 계획되고 `unsupported`(`tool_not_configured`·`tool_unavailable`·`toolchain_not_configured`)로 끝난다. 처리 파일 0이 알려지고 성공·0건 통과로 세지 않는다. 공유 toolchain은 시작 시 한 번 검증하고 그 핸들로만 도구 스크립트를 실행한다(`#!` 해석 없음). 탐색은 `not_probed`·`probe_failed`를 기록하고 PATH 검색·다운로드가 없다. | REQ-007,012,013,015 |
| TC-102 | 같은 스냅샷에서 두 분석 엔진이 같은 줄·같은 구성을 보고, 같은 행에 다른 메시지 | 중복 검사는 규칙 선택에서 제외하고 남은 관찰은 도구별로 분리한다. 같은 분석기·규칙·위치·메시지만 한 finding으로 묶는다. 처리 파일은 합집합으로 센다. | REQ-007,008,009,013; TC-09,13 |
| TC-103 | 각 분석기 팩. 저장소 코드(build script·설정 프로그램·plugin·proc macro)를 실행하는 분석 작업, 노드 허가 없음·있음, 도구 미설정 동시 발생, Git 소스 | 허가 없으면 `Composite.Run`이 dispatch 전에 `unsupported`(`repository_code_not_allowed`)로 끝내고 프로세스를 시작하지 않는다. not-run envelope는 처리 파일 0을 기록한다. 도구 사유는 제약으로 남는다. 허가는 노드 설정의 local source root에서만 오고 Git 소스·스냅샷·저장소 설정은 올릴 수 없다. | REQ-002,015 |
| TC-105 | C/C++. 다른 머신의 compile DB: 원래 루트·build 디렉터리 경로, 루트 include(`-I.`·`-iquote .`), 상대 경로, compiler wrapper, 같은 파일의 두 항목, 스냅샷 밖·헤더·MSVC·nvcc 항목, plugin·응답 파일·driver 설정 flag, 값에 넣은 `@file`, 옵션처럼 보이는 파일 이름(`-x.c`, `--help.cpp`), compile DB 없음 | 위치는 원래 루트를 기준으로 작업 사본의 절대 경로에 매핑하고 항목의 compiler는 실행하지 않는다. 루트를 쓰는 검색 디렉터리는 두 도구에서 번역 단위가 루트 헤더를 찾게 한다. 루트는 소스·강제 include 파일이 될 수 없다. 빈 값·제어 문자가 들어간 경로와 루트 밖 include는 버린다. 제외 항목과 버린 flag는 사유별 수로 남는다. 허용 목록 밖 flag, `@` 원소, 코드 적재 옵션은 CODEYE compile DB에 없다. compile DB가 없으면 clang-tidy는 `unsupported`(`compile_db_missing`)이고 cppcheck 축소 모드는 제약으로 표시되며 정밀 분석 완료가 아니다. | REQ-002,006,007,015 |
| TC-106 | C/C++. 생성 헤더(configure가 만드는 `config.h`)가 없음·있음, 한 batch의 두 단위 실패, 단위별 batch, 헤더만 있는 범위, 결함 없는 단위 | 찾지 못한 헤더는 결함이 아니며 그 단위는 부분·실패 범위다. 작업은 `missing_include_files`로 실패하고 다른 단위의 관찰은 남는다. clang-tidy의 누적 오류 수로 실패 단위를 가른다. 처리 파일은 성공한 작업의 번역 단위만이며 헤더를 주장하지 않는다. | REQ-006,007,008,013 |
| TC-285 | C/C++. compile DB가 없는 CMake 프로젝트(`configure_file`로 build 디렉터리에 생성 헤더를 쓰는 프로젝트, 중첩·데이터 디렉터리·범위 밖 프로젝트, 스냅샷 compile DB가 있는 경우): 저장소 코드 실행 허가 없음·있음·서비스가 제공하지 않음, toolchain 경로에 cmake 없음·make와 ninja 없음, 생성한 DB가 바뀜, C/C++ 분석기가 없는 노드 | 허가가 있으면 분석기 앞의 `compile-db` 작업이 작업 사본에서 configure만 실행하고(스냅샷·원본 경로 불변, 환경 `PATH`·`LANG`, build 산출물 없음) cppcheck·clang-tidy·GCC가 생성한 DB로 compile DB 모드 분석을 한다. 생성 헤더를 찾고 `compile_db_source=generated`, `compile_db_generated`, `generated_include_dirs_used`, `repository_code_executed`가 남으며 보고서에 노드 경로가 없다. 허가가 없으면 프로세스 없이 `unsupported`(`repository_code_not_allowed`·`repository_code_not_offered`)이고 분석기에는 `compile_db_generation_not_allowed`가 남는다. 도구가 없으면 `unsupported`(`build_tool_not_configured`)와 `compile_db_generation_unavailable`이다. 바뀐 DB는 `snapshot_integrity_failed`이며 축소 모드로 조용히 바뀌지 않는다. 스냅샷 DB가 있거나 C/C++ 분석기·CMake 프로젝트가 없으면 계획하지 않는다. 보고서에서 `compile-db`는 도구 행이 아니고, 작업 제약은 보고서 한계로, 성공하지 않은 작업은 분석 공백 `compile_db_not_generated`로 보이며 Markdown·웹에서 문장으로 읽힌다. `codeye tools`·`tools.json`의 `toolchain_path`와 `codeye doctor`의 `build_tools`가 cmake·make·ninja·컴파일러 유무를 보인다. | REQ-002,006,007,015 |
| TC-286 | Rust 허가 작업의 build script가 C 도구(`cc`·`ar`)를 이름으로 부름, rustdoc의 같은 이름 crate 문서 충돌, dependency-cruiser 출력의 core·미해석·`doNotFollow` 대상 항목과 그 대상을 금지한 규칙 | build script는 `--toolchain-path`의 도구를 찾아 성공하고 `build_scripts_used_toolchain_path`를 남긴다. 의존성 받기는 toolchain 디렉터리만 쓴다. 문서 충돌은 `failed`(`doc_output_collision`)다. dependency-cruiser는 대상 항목을 소스로 세지 않고(`dependency_targets_not_followed`) 금지 규칙 위반을 해당 import 줄의 관찰로 남긴다. 연결하지 못한 위반은 `dependency_violations_not_represented` 제약이며 대상 항목만 있으면 `unsupported`다. | REQ-006,007,015 |
| TC-292 | 카탈로그의 진단 분석기, 표의 규칙 계열·규칙 팩 버전, 표에 없는 규칙·원시 등급, 다른 규칙 팩 버전 | 진단 분석기마다 표가 있고 계열은 `info`~`high`이며 `critical`이 없다. 표에 없는 규칙과 다른 규칙 팩 버전은 `low`다. 원시 등급은 바뀌지 않고 보고서 도구 행에 `severity_table`이 남는다([기본 심각도](ANALYZERS.md#기본-심각도)). | REQ-007,013 |
| TC-107 | C/C++. cppcheck·clang-tidy 정규화: 종료 코드와 출력 불일치, 잘린·손상 출력, 버전 계열, 저장소 `.clang-tidy`·cwd의 cppcheck 설정·clang driver 설정 파일, NOLINT와 `cppcheck-suppress`, 두 엔진의 같은 줄 보고, 작업 사본 밖 파일을 include하는 소스 | 성공은 종료 코드·출력·단위 처리가 모두 맞을 때만이다. 저장소 도구 설정은 결과를 바꾸지 못한다. 규칙 문맥은 compile DB digest를 기록하고 cppcheck 억제는 `disabled`, clang-tidy NOLINT는 `honored`다. 기준 분석기와 같은 구성만 보고한 clang-tidy 검사는 선택하지 않고 남은 같은 줄 보고는 도구별 finding이다. 밖 파일의 내용은 저장 stream·envelope·보고서에 없다. | REQ-007,008,009,013,028; TC-102,104 |
| TC-108 | Python 스냅샷: ruff·mypy·pyright·bandit 설정·미설정, 하위 모듈, 선언된·없는 Python 버전, 구문 오류, 도구 실패·잘림·버전 미인식 | ruff는 필수 기준이고 나머지는 추가 깊이다. 모듈마다 task를 계획하고 `pack-gap.python`은 없다. 대상 버전과 출처(`requires_python`·`python_requires`·`python_version_file`·`codeye_default`)를 명시 전달·기록한다. 구문 분석하지 못한 파일은 처리 파일에서 빠지고(`syntax_error` 단위, `unparsable_files_not_analyzed`) 나머지 결과로 성공한다. 도구가 위치를 주는 구문 오류는 그 파일의 관찰이다. 처리 파일은 도구 자신의 기록으로 확인한 성공 task에만 센다. 두 타입 검사기 동시 실행은 `--python-both-type-checkers` 없이는 agent 사용법 오류다. | REQ-007,008,012,013,015; TC-101 |
| TC-109 | Python. 저장소 설정(ruff.toml·mypy plugins·pyrightconfig·.bandit)과 소스 안 억제(`# noqa` 계열·`# nosec`·`# type: ignore`·`# pyright: ignore`), 작업 공간 밖을 가리키는 설정 값, 옵션처럼 보이는 파일 이름 | 네 도구 모두 CODEYE 관리 설정으로 저장소 설정을 대신하고(`overridden`) 저장소 코드를 실행하지 않는다. ruff·bandit 억제는 두 실행 차이로 `reported`, mypy·pyright는 `honored`다. 밖을 가리키는 설정의 canary는 stream·envelope·보고서에 없고 밖에 아무것도 쓰지 않는다. 수집 stream에 소스 줄이 없다. 위치 인자는 `--`·`./`로 옵션이 되지 않는다. | REQ-002,015,028; TC-103,104 |
| TC-110 | Python. 설치되지 않은 의존성 import·stub 누락, 두 엔진이 같은 구성을 보고 | import·stub 공백은 관찰이 아니라 `import_unresolved`·`stub_missing`·`import_source_missing` 단위와 제약이다. 후속 오류를 결함으로 보이지 않는다. ruff와 타입 검사기·bandit의 같은 구성 검사는 한쪽에서 끈다(측정한 제외 목록). | REQ-007,008,009,013; TC-102 |
| TC-111 | JS/TS. 루트·TS·JS 모듈의 모노레포, tsconfig 체인(JSONC·상위 extends·plugin 항목·solution 참조), 실행되면 표식을 남기는 저장소 `eslint.config.js`, 쓰기·호스트 경로 tsconfig 옵션, option처럼 보이는 파일 이름 | 모듈마다 기준선 eslint(Required)·tsc·type-aware 작업이 계획된다. 저장소 설정·plugin은 실행되지 않고 원본·스냅샷·작업 사본이 바뀌지 않는다. 체인은 경로·SHA-256으로 기록된다. 쓰기 옵션과 스냅샷 밖 경로는 실행 전에 `skipped_policy`로 끝나고 옵션 이름만 남는다. 위치 인자는 option으로 해석되지 않는다. | REQ-007,008,012,015; TC-101,103 |
| TC-112 | JS/TS. node_modules 없는 스냅샷의 TS 모듈(해석되지 않는 import·그 결과의 implicit any·무관해 보이는 타입 오류) | 기준선은 성공한다. tsc·type-aware 작업은 `failed`(`dependency_unavailable`)이고 공백은 dependency tool error, 나머지 진단은 withheld로 남는다. 관찰·처리 파일로 세지 않는다. | REQ-007,008,013; TC-08 |
| TC-113 | JS/TS. eslint-disable 계열·inline 규칙 설정·`eslint-suppressions.json`·`@ts-expect-error` | eslint는 관리 실행에서 모든 계열이 꺼져 진단이 모두 관찰되고 `source_suppressions: disabled`다. tsc는 숨긴 진단을 보고할 수 없어 `honored`이고 `unreported_suppression_rules`는 비어 있다. | REQ-013; TC-104 |
| TC-114 | Rust. cargo check와 clippy 결과 판정: 경고만(exit 0), 컴파일 오류·deny 수준 lint·빌드 스크립트 실패(exit 101), `--all-targets`의 중복 진단, clippy가 함께 출력하는 rustc 진단, 비ASCII 줄의 열 | 종료 코드와 마지막 `build-finished`가 일치하고 레코드를 모두 읽었을 때만 성공이다. 경고는 `success_with_findings`, 컴파일 오류는 도구 오류(`package_errors`), deny lint는 관찰을 남기고 `lints_denied`로 실패한다(처리 파일 없음). clippy 작업은 `clippy::` 진단만 남기고 lint 수준을 warn으로 낮춰 같은 구성이 두 작업에 중복되지 않는다. 같은 진단은 하나로 합치고 열은 바이트 열이다. 잘림·손상·알 수 없는 레코드는 성공이 아니다. | REQ-007,008,012,013 |
| TC-115 | Rust. 모듈·workspace·범위·build script: 가상 workspace, 독립 crate, 멤버가 아닌 중첩 crate, 경로 범위, build script와 proc macro가 있는 workspace, Cargo.lock·registry 의존성 없음, 링커 없음 | 가장 바깥 Cargo.toml마다 한 작업(`--workspace`)이고 키·범위·필수 여부가 계획에서 정해진다. 멤버가 아닌 소스는 `files_unlisted`와 `rust_files_not_compiled`로 보인다. build script·proc macro 실행은 `build_script_executed`·`proc_macro_executed`로 남는다. 의존성·잠금 파일·링커 부족은 내려받거나 생성하지 않고 구성 오류 사유(`dependency_unavailable`·`lockfile_missing`·`linker_unavailable`)로 끝난다. | REQ-007,008,012,013; TC-68 |
| TC-116 | Rust. 저장소 코드 신뢰와 toolchain 무결성: 허가 없는 소스(도구 설정·미설정), 허가 있는 소스, rustup proxy와 `rust-toolchain.toml`, 저장소 `.cargo/config`의 target·rustc wrapper, 작업 사본을 고쳐 쓰는 build script, 소스 안 `#[allow]`, unsafe·panic review lint | 허가가 없으면 기준 분석기도 `unsupported`(`repository_code_not_allowed`)이고 version probe 말고는 프로세스가 시작되지 않으며 단계·보고서가 부분이다. 허가가 있어도 운영자가 지정한 실제 toolchain만 쓰고 proxy는 거부한다. target·wrapper는 환경으로 고정하고 CARGO_HOME·target은 scratch에 둔다. 작업 사본이 바뀌면 `workspace_drift`로 실패한다. `#[allow]`가 숨긴 진단은 보고되지 않으므로 소스 안 억제는 `honored`이고 사라진 finding은 해결되지 않는다. unsafe·panic review lint는 clippy 작업이 켠다. | REQ-002,015,028; TC-103,104 |
| TC-273 | Go 패키지 관계: A↔B import, 제거한 수정본, B의 외부 테스트에서만 A를 import, 패키지 A만 선택 | 운영 코드 순환의 양쪽 실제 import 줄, 수정본의 순환 없음, 테스트 소비자를 A에 표시, 선택 범위는 부분 결과. 패키지 목록에 나타나지 않은 대상 파일은 빌드 구성에서 제외된 목록과 구분한다. manifest와 줄 근거가 일치하며 판정을 바꾸지 않는다. | REQ-007,012,013 |
| TC-274 | C/C++ 다중 위치: cppcheck의 초기 대입과 덮어쓰기, clang-tidy의 이동·사용 및 할당·분기 가정 | 진단 수는 그대로이며 관련 위치를 순서대로 보존한다. 코드 미포함에서 원문과 발췌는 빠지고 검증된 위치는 남는다. | REQ-007,012,013 |
| TC-117 | 구조·중복 지표: 임계값을 넘는 함수 복잡도(CCN 17)와 10줄 이상 중복 블록, 임계값 아래 값, `vendor`·`testdata`·숨김 디렉터리의 같은 코드, 지표만 있는 성공, 위치를 확인하지 못한 지표, 지표를 인용한 finding | 지표는 `metric`을 가진 관찰로만 남고 finding·결함 수·조치 수·모델 입력이 되지 않는다. 지표만 있으면 `success_without_findings`이고 요약에는 `metric_observations`로 따로 센다. 제외 디렉터리는 `excluded_data_directory`로 센다. 위치를 확인하지 못한 지표는 미해결 관찰로 세어 판정을 `inconclusive`로 둔다. 지표를 인용한 finding이 있는 보고서는 무효다. 복잡도는 lizard, 중복은 jscpd만 잰다. 이름이 명시된 외부 코드 디렉터리는 측정 범위에서 빼고 수를 기록한다. 중복의 양쪽 줄 범위는 코드 미포함 보고서에서도 확인할 수 있다. | REQ-007,008,012,013; TC-09 |
| TC-118 | 비밀값 탐지: 생성한 자격증명을 소스·설정·이진 파일에 두고, 인라인 `gitleaks:allow`, 저장소 `.gitleaks.toml` allowlist, `.gitleaksignore`(정상·사본 밖 항목), 기본 allowlist 파일, 같은 파일의 같은 규칙 두 번, 모델 입력 선별의 세 경우 | 탐지는 고정 문구의 미검토 낮음 보안 finding이고 값·일부·hash는 어디에도 남지 않는다. entropy와 열은 원시 stdout에만 있고 관찰·보고서에는 없다. 식별은 규칙·경로·고정 문구다. 인라인 allow와 저장소 `.gitleaks.toml`은 효과가 없고 `.gitleaksignore`는 digest와 함께 따른다. 사본 밖을 가리키는 항목은 값을 기록하지 않고 작업을 거부한다. 비밀값 작업이 실행되지 않으면 모델 입력은 거르지 않되 제약을 남긴다. 실행했지만 쓸 수 없으면 실행되지 않은 경우처럼 거르지 않고 보내되, 읽을 수 있는 결과가 보고한 파일은 계속 뺀다. 성공하면 탐지된 파일을 모델 입력에서 뺀다. ruff S105·bandit B105와 합치지 않는다. | REQ-008,011,013,015,031; NFR-006 |
| TC-119 | 적대적 저장소와 비밀값 부재 종단: `--help`·`-x` 이름의 경로, 작업 사본 밖 canary를 가리키는 링크, 사본 밖 결과, 실제 세 도구를 쓰는 워커·agent·controller·PostgreSQL 실행 | 저장소 경로는 옵션으로 읽히지 않고 링크는 따라가지 않는다(`workspace_link_refused`). 사본 밖 결과는 버리고 작업은 실패한다. 생성 비밀값과 canary는 stream 산출물, envelope, 보고서, 코드 포함·제외 JSON·HTML·Markdown, 로그, DB 행, 노드 scratch 어디에도 없다. | REQ-008,015,031; NFR-006 |
| TC-289 | SARIF 입력 어댑터: Opengrep 2.0.0이 낸 실제 SARIF(흐름·억제·CWE 태그), 명세 기능을 모은 손으로 쓴 SARIF(relatedLocations, 여러 codeFlow·threadFlow, partialFingerprints, `%SRCROOT%`·퍼센트 인코딩 URI), 모르는 필드·중복 키·깊은 중첩·뒤 데이터·크기·결과 수·흐름 단계 상한·`kind≠fail`·절대·`file:`·`..`·역슬래시·드라이브 문자·`./` 뒤 절대 경로·모르는 `uriBaseId` 경로, 멤버와 대소문자만 다른 키(결과·위치·codeFlows·relatedLocations 안), 시작 줄 없는 위치, 허용 기준 ID를 정의한 `originalUriBaseIds`, 팩 값과 다른 `columnKind`, 제어 문자가 든 쓰지 않는 문구(snippet·알림·위치 메시지·help), `executionSuccessful=false`, `a:b.py`처럼 드라이브가 아닌 콜론 이름 | 결과마다 JSON pointer와 byte 범위, 흐름 단계의 순서·종류·byte 범위, 규칙의 CWE, 억제 종류가 남는다. 잘못된 입력은 모두 형태 오류이고 결과 0건이 되지 않는다. 작업 사본을 벗어난 경로는 벗어남으로 표시된다. 쓰지 않는 문구는 디코드를 막지 않고 실행 상태와 열 단위는 run에 남는다. `Maps`는 URI로 같은 경로를 돌려받는 이름만 참이다(`TestDecode*`, `TestMapURI`, `TestMaps`) | REQ-007,012,013 |
| TC-290 | Opengrep 패턴 taint 팩: 여섯 언어 fixture(`fixtures/taint/`, 규칙마다 양성과 `_safe` 음성), `nosemgrep`·`nosem`·다른 규칙 ID 주석, 두 fixture 디렉터리를 숨기는 저장소 `.semgrepignore`·`.gitignore`, 실도구와 재생. 재생으로 규칙 로드 실패(exit 4·5·7·8, 규칙 오류 알림, 다른 규칙 목록), 치명 오류, JSON 사본 없음·불일치, 넘기지 않은 파일의 결과, 일부만 해석한 파일, 한국어·이모지 뒤의 위험 호출 | 양성 122줄을 모두 흐름 단계와 함께 찾고 음성에서는 결과가 없다. 비ASCII 줄의 시작·끝 열은 위험 호출의 바이트 열이다. 규칙 없는 주석과 같은 규칙 ID 주석은 억제된 관찰, 다른 규칙 ID 주석은 활성 관찰이다. ignore 파일은 적용되지 않는다(`overridden`). 규칙 로드 실패는 `rule_pack_load_failed`, 그 밖은 `opengrep_failed`·`output_unparsed`·`result_outside_snapshot`이고 0건 성공이 아니다. 일부만 해석한 파일은 처리 파일이 아니다. 흐름 단계는 보고서 관련 위치로 남고 코드 제외 내보내기에서 문구가 빠진다. 규칙마다 CWE를 적은 한국어 설명이 있다. 실도구 세 번 실행의 관찰 순서·SARIF 바이트가 같다(`TestRealOpengrepFixtures`, `TestRealOpengrepResultOrder`, `TestReplayOpengrep*`, `TestOpengrep*`) | REQ-007,008,012,013 |
| TC-291 | Opengrep 네트워크 미사용: 팩이 만든 버전 probe와 scan의 argv·환경을 strace(`trace=network,process`)로 실행 | `socket`·`connect`·`sendto`·`bind` 호출이 없고 실행마다 `execve`가 하나다. 재생 시험은 기록된 argv·환경에 레지스트리·URL·`git+`·파일 사이 스위치와 공통값 밖 환경변수가 없음을 확인한다(`TestRealOpengrepNoNetwork`, `TestReplayOpengrepFixtures`) | REQ-008; NFR-006 |
| TC-304 | Opengrep 파일별 오류와 거부할 로그: 실제로 캡처한 오류 세 가지(`PartialParsing` 범위, `"Syntax error"` 문자열)와 모양을 모르는 유형 여섯 가지(null·빈 문자열·빈 목록·객체·중첩 목록·숫자), 제어 문자가 든 알림 메시지, `error` 수준 파일 오류, 파일의 규칙 제한시간, 파일 없는 실행 오류, `executionSuccessful=false`, `utf16CodeUnits` 열, 값을 준 `%SRCROOT%` 기준, `.//etc/passwd` 결과, 언어 일부만 있는 범위, Windows와 그 밖의 argv 한도, 공백이 든 짧은 이름 2만 개 | 파일 하나의 오류는 수준·유형과 관계없이 그 파일만 불완전(`not_fully_analyzed`)으로 세고 나머지 결과를 지키며 작업은 실패하지 않는다. 실행 오류와 거부할 로그는 `opengrep_failed`·`output_unparsed`·`result_outside_snapshot`이다. 선택은 불러온 규칙 파일만 적는다. 한도는 Windows 24 KiB, 그 밖 96 KiB이고 따옴표를 붙인 Windows 명령줄로 써도 한 scan의 경로 인자가 한도를 넘지 않는다(`TestReplayOpengrepFileErrors`, `TestReplayOpengrepRejectedLogs`, `TestOpengrepSelectionIsLoadedFiles`, `TestMaxArgBytes`) | REQ-007,012,013 |
| TC-305 | Opengrep 진단의 분류: 흐름 단계 두 개가 있는 경로 진단 두 건(요청 이름으로 읽기, 예제 파일 `examples/b.go`에서 허용 목록 검사 뒤 지우기), 가짜 상류의 분류(문제·무해)와 slot 검증(지지), 같은 줄의 bandit·SpotBugs·clang-tidy 보고와 다른 줄·SSRF | 분류 요청의 예에 흐름 단계가 묶음 자신의 맥락 발췌를 가리키는 관련 위치로 실리고 기본 심각도는 팩의 표(경로 `injection` 계열, 상한 `medium`)다. 배포 코드의 도구 finding은 medium, 예제 파일의 도구 finding은 경로 규칙으로 low이고 사유가 경로 규칙을 적는다. 도구 행의 표는 `codeye-taint/1+paths.1`이다. 지지된 발견은 검증이 정한 심각도다. SpotBugs 경로·XSS 검출기는 병합되지만 low다. 문제 묶음은 후보가 되어 지지된 발견이고, 무해 묶음은 이유와 함께 목록에 남으며 도구 finding을 지킨다. 도구 finding의 영향은 고정 규칙 설명이다. 주입 계열이 같은 줄의 다른 도구 보고와는 한 묶음이고 SSRF·다른 줄은 합치지 않는다(`TestOpengrepDiagnosticTriage`, `TestOpengrepMergesWithOtherToolsOnOneLine`, `TestSeverityCoversEveryRule`) | REQ-007,013 |
| TC-306 | Opengrep 규칙 측정(수동, 실도구): 레이블 벤치마크(OWASP BenchmarkJava·BenchmarkPython, NIST Juliet C/C++·Java)와 공개 웹 애플리케이션을 고정 커밋으로 받아 팩 그대로 실행(`TestRealOpengrepMeasureDirectory`, `CODEYE_OPENGREP_MEASURE_SOURCE`·`_OUT`) | 결과를 보기 전에 정한 개발·보고 전용 분할로 CWE별 TP·FN·FP·TN과 진단마다의 판정을 기록한다. 규칙은 개발 부분에서만 고치고, 고치는 동안 본 보고 전용 자료는 개발 숫자로 내린다. 결과는 [측정](ANALYZERS.md#opengrep-규칙-측정)에 집계로만 남기고 제3자 소스는 저장소에 넣지 않는다 | REQ-007,013 |
| TC-191 | Python: 구문 오류 파일(`def broken(:`)과 Python 2 `print` 파일 옆에 실제 결함이 있는 정상 파일(`fixtures/python/syntaxmix`), 네 도구 각각, 실도구와 재생 | ruff·mypy·pyright·bandit 모두 성공하고 정상 파일의 결함(E722·`return-value`·`reportReturnType`·B602)이 남는다. 두 파일은 `syntax_error` 단위이고 처리 파일에 없으며 제약 `unparsable_files_not_analyzed`가 남고 tool error가 없다. 위치를 주는 도구의 구문 오류는 파일마다 한 관찰이다. 파일 이름은 대상 속성 `unparsable_files`에 있다. mypy는 구문 오류 파일을 하나씩 빼고 다시 실행하며(`mypy.check.r01`…), 정상 파일이 그 파일을 import해도(`fixtures/python/syntaximport`) 재실행이 그 모듈을 건너뛰어 정상 파일의 결함이 남는다 | REQ-007,008,013; TC-108 |
| TC-192 | mypy 재실행이 작업 제한시간 안에서 시간이 남음·다 씀 | 첫 실행과 재실행이 제한시간 하나를 나눠 쓰고 재실행은 남은 시간만 받는다. 시간이 없으면 재실행을 시작하지 않고 찾은 구문 오류를 남긴 채 `timed_out`(`timeout`, `mypy_reruns_time_limit_reached`)이다 | REQ-007,013; TC-08 |
| TC-193 | ESLint: 해석하지 못하는 파일과 정상 파일, 해석하지 못하는 파일만 있음 | 정상 파일의 관찰로 성공하고 해석하지 못한 파일은 `parse-error` 관찰 하나, `parse_error` 단위, `unparsable_files_not_analyzed`다. 해석할 수 있는 파일이 없으면 `parse_errors`로 실패한다 | REQ-007,008,013; TC-108 |

TC-107은 C 구조체 멤버의 앞 대입이 읽히기 전에 덮어쓰이는 경우, 앞 대입을 제거한 수정본, 중간에 읽는 대조군을 포함한다. Cppcheck 2.22.0에서 첫 입력에만 `redundantAssignment`가 나오며 두 대입 줄을 기록한다. 다른 style만 보고된 경우 도구의 exit 2와 원시 XML을 보존하며 관찰은 0건이다(`TestRealCppcheckRedundantAssignment`, `TestReplayCppcheckRedundantAssignment`).

`TestRealESLintPromiseRejections`와 `TestReplayESLintPromiseRejections`는 TC-111의 관리 규칙 범위를 확인한다. JS의 문자열·숫자 거부 두 곳만 `warning`으로 잡히고 도구가 exit 0을 내도 발견이 남는다. `Error` 객체·인자 없는 거부·타입이 불명확한 값·프로젝트가 정의한 동명 함수는 경고하지 않는다. TS의 문자열 거부는 기준선에서 나오지 않고 type-aware 작업에서 한 번 잡힌다.

`TestRealESLintManagedJavaScriptRejections`와 `TestReplayESLintManagedJavaScriptRejections`는 격리된 보강 작업의 범위와 원시 진단을 확인한다(TC-111, TC-102). 응답 본문과 XHR 상태 문자열은 확인하고, 다른 모듈·범위 밖 선언·현 작업 사본에 설치된 ambient 타입·불명확한 인자·`Error`를 포함한 타입·인자 없는 호출은 보강 진단으로 내지 않는다. 한 줄에 있는 별도 두 호출도 열로 구분한다. 벤더 디렉터리만 경로로 지정하거나 입력 한도를 넘거나 스냅샷 경로가 링크이면 보강 도구를 실행하지 않는다.

`TestManagedJavaScriptRejectionsDeduplicateOnlyAcceptedBaseline`은 같은 호출의 기준선 진단을 수락했을 때만 보강 발견이 접히는지 확인한다. 기준선이 실패하거나 결과를 받지 못하면 보강 발견을 유지한다. 보강 발견이 접힌 경우에도 보강 작업의 관찰 수와 원시 근거가 보존되고 해석과 검증 역할이 해당 관찰을 참조할 수 있다.

Linux의 ESLint 10.15.0과 Node 24.18.0으로 PicoShare의 두 모듈을 세 번 검사했다. 기준선은 JS·TS 25개 파일을 처리하며 순차 합계 3.00~3.91초와 프로세스당 최대 RSS 약 233 MiB를 썼다. 보강은 JS 12개 파일에 순차 합계 3.94~4.47초와 프로세스당 최대 RSS 약 289 MiB를 썼다. 기준선의 거부 진단 8개와 보강의 19개는 서로 겹치는 8개 호출을 포함한다. 두 작업이 성공하면 최종 발견은 19개다. 모델 호출과 웹 서비스를 제외한 분석기 작업의 측정값이다.

분석기 계약과 도구별 규칙은 [분석기](ANALYZERS.md)에 있다. 실도구 시험은 도구 경로 환경변수가 있을 때만 실행하고, CI에서는 캡처한 실제 출력을 재생하는 시험이 같은 판정 경로를 검사한다([시험 실행 방법](#시험-실행-방법)).

## 의존성 준비

규칙은 [의존성 준비](ARCHITECTURE.md#의존성-준비)와 분석기 문서의 [의존성 준비](ANALYZERS.md#의존성-준비)(Go·Rust·JS/TS·Python) 장이다. 받기 시험은 가짜 runner로 argv·환경·cwd를 고정해 확인하고, 실제 proxy에서 받는 시험은 `CODEYE_TEST_DEPS_NETWORK=1`일 때만 돈다.

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-194 | 계획: 선택 꺼짐·스위치 꺼짐·캐시 root 없음, 루트와 하위 모듈, 내부 handler가 `deps.` key를 계획, 팩 이름 `deps`. job: 스위치·선택·선택 없는 job의 조합(로컬·Git, project 선택을 따르는 노드와 운영자 플래그만 따르는 노드). 저장 선택: 필드 없음, `false`, 잘못된 값, 이전 strict reader | 꺼져 있으면 계획이 내부 계획과 같다. 켜져 있으면 `deps.go`·`deps.go.m01`이 분석기 앞에 오고 접미사가 go vet key와 같다. `deps.` key를 계획하는 내부 handler는 거부된다. `allow_dependency_fetch`는 스위치와 선택이 모두 켜졌을 때만 참이고 선택 없는 job은 켜짐이다. 필드는 꺼짐일 때만 저장되고 이전 reader가 켜짐 기록을 읽는다 | REQ-004,014; NFR-006 |
| TC-195 | Go 받기(가짜 runner): argv·cwd·환경(`GOENV`·`GOWORK`·`GOTOOLCHAIN`·`GOFLAGS`·`GOVCS`·`GOPROXY`·`GOSUMDB`·`GOMODCACHE`), 운영자 캐시 seed, 운영자 `GOPROXY`(`direct`만이면 `off`)·`GOSUMDB`·`GOPRIVATE`, 운영자 캐시의 비공개 모듈, vendor·require 없음·snapshot 밖 `replace` | cwd는 실행 사본의 모듈 디렉터리이고 원본·snapshot이 아니다. `-modfile` 사본을 쓰고 원본·snapshot·실행 사본의 go.mod·go.sum은 그대로다. 기록에 절대 경로가 없다. 운영자 설정이 그대로 가고 비공개 모듈은 복사돼 `private_modules_seeded`다. 받을 것이 없으면 `skipped_policy`이고 환경 단계를 낮추지 않는다 | REQ-014,015; NFR-006 |
| TC-196 | 받기 실패와 예산: proxy 연결 실패, proxy에 없는 모듈(일부만 받음), checksum 불일치, go.sum 사본 변경, toolchain 부족, 비공개 모듈(GOVCS), timeout, 정리 불확실, attempt 취소, 합계 예산 소진·공유, 크기 상한, cache 저장 영역 가득 참, `--go` 없음 | 각각 `registry_unreachable`, `module_not_on_proxy`(`partial`), `checksum_mismatch`, `go_sum_incomplete`, `toolchain_unavailable`, `private_module_not_fetched`, `timed_out`, `cleanup_uncertain`, `cancelled`, `dependency_budget_exhausted`, `download_limit_exceeded`, `dependency_cache_full`, `unsupported`/`package_manager_not_configured`로 끝나고 기록은 늘 남는다. 실행이 깨끗하지 않은 성공은 없다 | REQ-014; NFR-006 |
| TC-197 | 분석기 연결: 실제 go vet이 기록 없음·`prepared`·`partial`·다른 run의 기록·`failed` 기록을 만난다. Staticcheck는 같은 함수를 쓴다 | 쓸 수 있는 기록(`prepared`·`partial`, 같은 snapshot·attempt·모듈)일 때만 `GOMODCACHE`가 받기 캐시이고 보호 경로가 되며 한계가 `dependencies_prepared`·`dependencies_partially_prepared`다. 그 밖에는 받기 이전과 같은 경로(`no_offline_module_cache`)다. `GOPROXY`는 늘 `off`다 | REQ-006,008,014 |
| TC-198 | 보고서: 받은·일부만 받은·받을 것 없는 기록, 다른 attempt·다른 snapshot·상태가 다른·role이 다른·snapshot에 없는 lockfile 기록, 의존성 task 없는 run, 분석기가 `dependency_unavailable`인데 그 생태계 task가 없음, 버전 고정 안 됨, 장부의 보내지 않은 호출(`model_not_configured`), 예시(`examples/report.json`, `examples/dependencies.json`), 웹 표와 안내 | 수락한 기록은 환경 절 한 행이고 상태·판정·요약·한계를 바꾸지 않는다. 거부는 `artifact_rejected:<task>`와 `result_rejected` 행이다. 의존성 task가 없으면 필드와 종합 수가 없다. 공백은 `dependencies_not_prepared`(받을 것 없음 제외), `dependencies_not_fetched`, `dependency_versions_not_pinned`다. 장부는 보낸 호출만 센다. builder는 7이다. 웹은 표와 "의존성 받기" 켜기 안내를 보인다 | REQ-012,013; NFR-006 |
| TC-199 | 실제 받기: `CODEYE_TEST_DEPS_NETWORK=1`에서 공개 proxy로 받고 실제 go vet이 그 캐시를 읽음. `codeye up` 종단(임시 설정·상태, 빈 Go 모듈 캐시, 모델 없음): 공개 Go 저장소를 받기 끔·켬으로, 의존성 있는 로컬 폴더, 닿지 않는 proxy, 받는 중 취소, 작은 크기 상한 | 받은 뒤 go vet이 성공하고 원본은 그대로이며 캐시는 일반 삭제로 지워진다. 종단 결과와 출력은 [변경 이력](../CHANGELOG.md)에 있다(수동 확인, CI 밖) | REQ-014,015 |
| TC-200 | Rust 받기(가짜 runner): 모듈 계획, argv·cwd·환경(`CARGO_HOME`, `CARGO_NET_OFFLINE` 없음), `Cargo.lock` 있음·없음(`generate-lockfile`), 허가 없음·`--cargo` 없음·cargo check 쓸 수 없음·vendor·경로 의존성만, 실패 출력(lockfile 불일치·manifest·레지스트리 없음·인증·연결 실패) | 접미사가 cargo check key와 같다. 허가가 없으면 아무것도 실행하지 않고 `dependency_consumers_not_allowed`다. lockfile이 없으면 만든 lockfile이 task 디렉터리에 남고 실행 사본에는 없으며 `dependency_versions_not_pinned`다. 실패는 `lockfile_outdated`·`manifest_invalid`·`dependency_not_found`·`registry_auth_required`·`registry_unreachable`이다(`TestRustFetch*`, `TestPlanRustModulesFollowCargoCheckKeys`) | REQ-014,015; NFR-006 |
| TC-201 | JS/TS 받기(가짜 runner): 설치 루트 계획(workspace 루트·중첩 lockfile·lockfile 없음), npm·pnpm·yarn·lockfile 없음의 argv·환경, 실패·timeout, tsconfig 없음·소비자 없음·의존성 없음·yarn 2·bun·관리자 없음·corepack shim, workspace 멤버 트리, 작업 사본 밖 링크, 스냅샷 밖 `file:`·`link:`·`portal:` 의존성(package.json·npm·pnpm·yarn lockfile) | 스크립트를 끄고 위치·레지스트리·proxy를 명령줄로 정한다. pnpm은 모든 명령에서 릴리스 전환이 꺼진다. 성공하면 `installed_dirs`가 루트와 멤버의 `node_modules`이고 원본에 `node_modules`가 없다. 실패·중단한 설치는 트리를 남기지 않는다. 밖을 가리키는 링크는 지워지고 `dropped_inputs`·`links_outside_workspace_removed`로 남으며 안쪽 링크는 그대로다. 스냅샷 밖 로컬 의존성은 아무것도 실행하지 않고 `local_dependency_outside_project`로 그 의존성을 적으며 안쪽 로컬 의존성은 막지 않는다(`TestJSLocalDependencyOutsideTheProject`, `TestJSFetch*`, `TestPlanJS*`, `TestInstallRootsFromTheManifest`) | REQ-014,015; NFR-006 |
| TC-202 | 기록: `installed_dirs`가 비었지만 있음·공유 캐시·다른 생태계·`node_modules` 아님·모듈 밖·중첩·정렬 안 됨·중복·스냅샷 탈출, 여러 JS 기록 중 쓸 수 있는 것·실패 기록·다른 attempt | 잘못된 기록은 쓰지 않고, 쓸 수 있는 JS 기록의 트리만 모인다(`TestInstalledDirsValidation`, `TestInstalledTreesReadsUsableRecords`) | REQ-012,014 |
| TC-203 | 분석기 연결: cargo check가 `prepared`·`partial`·기록 없음, lockfile 없는 모듈의 생성 lockfile 있음·없음. tsc와 type-aware ESLint가 기록된 `node_modules`·실패 기록·기록 없음·링크인 `node_modules`. gitleaks 링크 검사가 설치 트리·트리 밖 링크·링크인 트리 | 쓸 수 있는 기록일 때만 받은 `CARGO_HOME`이고 한계가 `dependencies_prepared`·`dependencies_partially_prepared`이며 생성 lockfile은 실행하는 동안만 사본에 있다. tsc·ESLint는 기록된 실제 디렉터리만 받아들이고 그 밖은 `node_modules_outside_snapshot`이다. gitleaks는 설치 트리만 건너뛴다(`TestCheckReadsTheFetchedCrates`, `TestCheckPlacesTheGeneratedLockfile`, `TestTscAcceptsTheRecordedNodeModules`, `TestTypedESLintAcceptsTheRecordedNodeModules`, `TestPreparedTreesAndAcceptedNodeModules`, `TestLinkFreeTreeExceptSkipsInstalledTrees`) | REQ-006,008,014 |
| TC-204 | 패키지 관리자 도구와 설정: npm·pnpm·yarn 탐지(node 설치·도구 접두사)와 검증(node 없음·다른 패키지·셸 shim·알 수 없는 버전), hello `package_managers`와 서비스 설정 `agent.tools.npm`·`pnpm`·`yarn`, npm 레지스트리(기본값·인증정보·http(s) 아님), `agent.dependencies.npm_registry`, 받기 전에 건너뛴 기록의 버전 고정 공백, 웹 안내(cargo, node 없음과 없는 JS 관리자, 빌드 스크립트와 crate) | 세 관리자는 분석기가 아닌 패키지 관리자로 `tools.json`에 있고 플래그가 되며 `dependencies.package_managers`로 워커에 간다. 인증정보가 든 레지스트리는 쓰지 않는다. 건너뛴 기록에는 `dependency_versions_not_pinned`가 없다. 안내는 빠진 것을 짚는다. node가 없으면 node를, 있으면 기록의 관리자 중 없는 것만 말한다(`TestValidateJSPackageManagers`, `TestInstanceAgentArgvPackageManagers`, `TestRegistryOf`, `TestNpmRegistrySetting`, `TestDependenciesNotFetchedGap`, `quickStart.test.ts`) | REQ-012,014; NFR-006 |
| TC-205 | 실제 받기: `CODEYE_TEST_DEPS_NETWORK=1`과 `CODEYE_TEST_CARGO`·`CODEYE_TEST_NPM`·`CODEYE_TEST_NODE`·`CODEYE_TEST_TSC`에서 crates.io의 crate(itoa)를 받고 실제 cargo check가, npm 레지스트리의 type-fest를 설치하고 실제 tsc가 읽음(`TestRealRustFetchThenCheck`, `TestRealJSFetchThenTsc`). `codeye up` 종단(임시 설정·상태, 모델 없음): Rust 저장소 빌드 스크립트 켬·받기 끔과 켬, pnpm 저장소와 lockfile 없는 npm 저장소 받기 끔·켬, 의존성 있는 로컬 폴더, 닿지 않는 레지스트리, 받는 중 취소, 작은 크기 상한 | 받은 뒤 cargo check가 성공하고 tsc가 실제 타입 오류(TS2322)를 내며 원본은 그대로다. 종단 결과와 출력은 [변경 이력](../CHANGELOG.md)에 있다(수동 확인, CI 밖) | REQ-014,015 |
| TC-210 | Python 계획과 건너뛰기: mypy·pyright 모듈 계획, 소비자 없음, 요구사항 없음, `poetry.lock`만 있음, `dynamic` dependencies, `--python` 없음, pip 없는 interpreter와 uv 없음 | `deps.python`·`deps.python.mNN` 접미사가 mypy key와 같다. 각각 `no_dependency_consumer`, `no_dependencies`(`skipped_policy`), `lockfile_format_unsupported`·`dynamic_dependencies_not_read`·`package_manager_not_configured`(`unsupported`)이고 설치를 실행하지 않는다 | REQ-014; TC-194 |
| TC-211 | Python 설치(가짜 runner): uv와 pip의 argv·환경·cwd, CODEYE 요구사항 파일, `-r`·`-c` 따라가기, 허용 목록(이름·extras·지정자·marker·hash·`@ https`)과 빼는 입력(editable·로컬·VCS·설치 옵션·이해 못 함), 일부만 hash, `pyproject.toml` 읽기(실제 interpreter: dependencies·optional·dependency-groups·include-group·poetry·두 번씩 포함하는 group 40단·순환) | cwd는 빈 task 작업 디렉터리이고 저장소 파일을 설치 도구에 넘기지 않는다. 빼는 입력은 사유와 함께 `dropped_inputs`다. 섞인 hash는 모두 떼고 `requirement_hashes_not_checked`다. group은 한 번씩 펼치고 10,000개를 넘으면 `too_many_requirements`로 그 모듈만 실패한다 | REQ-014,015; NFR-006 |
| TC-212 | Python 캐시: 모든 줄 `==`, `uv.lock`(uv 있음·없음), 고정 안 됨, 같은 key 두 번째 run | 고정이면 공유 트리 `python/trees/<key>`(`shared`)이고 두 번째는 설치 없이 `prepared_tree_reused`다. 고정이 아니면 attempt 트리(`attempt`)와 `dependency_versions_not_pinned`다. uv가 없으면 `uv.lock`은 `uv_not_configured`로 빠진다. `uv export` 뒤 실행 사본이 바뀌면 `workspace_drift`다 | REQ-014 |
| TC-213 | Python 실패: wheel 없는 직접·전이 요구사항(uv·pip), index에 없는 요구사항(uv), 해결 불가, 인증 요구, hash 불일치, 닿지 않는 index(uv·pip), 그 밖의 실패, 임시 디렉터리가 크기 상한을 넘김 | wheel이 없거나 index에 없는 요구사항은 빼고 다시 설치해 `partial`(`no_binary_distribution`·`no_matching_distribution`·`dependency_not_found`)이다. 나머지는 각 사유로 `failed`다. 임시 디렉터리 증가도 `download_limit_exceeded`다 | REQ-014; NFR-006 |
| TC-214 | 시작 hook: 받은 트리의 `*.pth`·`sitecustomize.py`, 그 트리를 읽는 실제 interpreter | 지운 뒤 interpreter가 hook을 실행하지 않고 `interpreter_startup_hooks_removed`가 남는다 | REQ-015; NFR-006 |
| TC-215 | 분석기 연결: mypy·pyright가 기록 없음·공유·attempt·`partial`·트리 없음·`failed` 기록을 만난다. 실제 mypy·pyright(`CODEYE_TEST_PYTHON`·`CODEYE_TEST_MYPY`·`CODEYE_TEST_PYRIGHT`) | 쓸 수 있는 기록이면 mypy는 `--python-executable`(가짜 venv), pyright는 `venvPath`/`venv`로 트리를 읽고 `third_party_packages_prepared`(`partially_prepared`)이며 트리 경로는 `$DEPS`다. 아니면 받기 이전과 같다(`--no-site-packages`, 빈 venv). Windows의 mypy는 `prepared_packages_not_used`다 | REQ-007,014 |
| TC-216 | 도구와 설정: uv·python 탐지(pip 있음·없음, uv 있음·없음, 시험한 계열 밖 버전), 에이전트 `--python`·`--uv`·`--pip-index-url`, 서비스 설정 `agent.tools.python`·`uv`·`agent.dependencies.pip_index_url`, 인증정보가 든 index URL·상대 경로, hello `package_managers`·`pip_index_url` | pip이 없는 interpreter는 uv가 있을 때만 `found`다. 인증정보 URL은 값을 되풀이하지 않는 사용법 오류다. 스위치가 꺼지면 hello에 싣지 않는다 | REQ-014,026; NFR-006 |
| TC-217 | 보고서와 안내: 받기 꺼짐에서 mypy가 `import_unresolved`와 `third_party_packages_not_installed`, 받은 트리를 읽은 pyright, Python 사유·한계 라벨, `deps.python`의 `package_manager_not_configured` | 앞의 것만 `dependencies_not_fetched`(`imports_unresolved`)이고 "의존성 받기 켜고 다시 시도"다. 받는 도구가 없으면 python 설치 안내다 | REQ-014,024 |
| TC-218 | 실제 받기: `CODEYE_TEST_DEPS_NETWORK=1`에서 PyPI로 uv와 pip이 받고 wheel 없는 패키지를 뺌. `codeye up` 종단(임시 설정·상태, 모델 없음): 공개 Python 저장소 받기 끔·켬, 고정된 `requirements.txt`의 로컬 폴더 두 번, 닿지 않는 index, 고정 안 된 요구사항 | 받은 뒤 해결되지 않은 import가 줄고 원본은 그대로이며 캐시는 일반 삭제로 지워진다. 종단 결과는 [변경 이력](../CHANGELOG.md)에 있다(수동 확인, CI 밖) | REQ-014,015 |

## 도구 탐지

도구를 찾는 규칙은 [도구 찾기](ANALYZERS.md#도구-찾기)다. 시험은 임시 디렉터리에 만든 가짜 도구 트리(버전 명령에 답하는 셸 스크립트)로 하므로 이 머신에 무엇이 설치되어 있는지와 무관하다. 실제 머신 시험은 `CODEYE_TEST_REAL_TOOLS=all`(또는 쉼표로 나눈 도구 이름)을 줄 때만 돈다.

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-176 | 모든 도구를 문서가 정한 위치에 둔 가짜 머신: PATH, `~/.local/bin`, `~/.opencode/bin`, CODEYE 도구 접두사(`bin`, `cpp/bin`, `go/bin`), `node_modules`의 진입 스크립트(도구 접두사와 node 설치 옆), rustup 기본 toolchain과 PATH의 rustup proxy, PATH의 npm shim | 23개 도구(npm·pnpm·yarn·uv·python 포함)가 각 위치에서 경로·버전·`source`와 함께 `found`다. cargo·clippy는 proxy가 아닌 toolchain 디렉터리, JS 도구는 shim이 아닌 진입 스크립트다. 빈 머신은 모든 도구가 `not_found`이고 추가 방법이 있으며 쓸 수 있다고 세지 않는다 | REQ-004,030 |
| TC-177 | 버전 처리: 시험한 계열 밖 버전, 알아볼 수 없는 출력, 앞 후보가 실패, 여러 후보 중 시험한 계열, proxy만 있는 cargo, node 없는 JS 도구, 명시 경로(쓸 수 없는 경우 포함) | 계열 밖은 쓰고 `tool_version_untested`를 기록한다. 알아볼 수 없는 후보는 건너뛰고 다음 후보를 쓰며, 남은 것이 없으면 `unusable`과 이유·`tried`다. 시험한 계열 후보가 우선한다. proxy와 node 없음은 `unusable`이다. 명시 경로는 유일한 후보이며 실패해도 다른 것으로 바꾸지 않는다. `agent.auto_detect=false`면 탐색하지 않는다 | REQ-004,030 |
| TC-178 | 탐색 결과와 명시 설정을 합친 에이전트 인자: mypy와 pyright 둘 다·하나·명시 설정만, 쓸 수 없는 탐색 결과, 명시 go와 모듈 캐시, 모듈 캐시가 없는 디렉터리·상대 경로·파일 | 명시 값이 항상 우선하고 쓸 수 없는 탐색 결과는 넘기지 않는다. `--python-both-type-checkers`는 두 검사기가 모두 있을 때만 붙어 에이전트의 일관성 검사를 통과한다. `go env GOMODCACHE`는 실제 절대 디렉터리일 때만 `--go-mod-cache`가 된다. `tools.json`은 0600으로 쓰고 읽으며 다른 schema 버전·모르는 필드는 거부한다 | REQ-004,030 |
| TC-179 | `codeye tools`(표·`--json`·설정 오류·없는 상태 디렉터리)와 `tools.json`·`examples/tools.json` schema, builtin의 `CheckTool`(모양 규칙·버전·시험한 계열) | 표는 찾은 것·없는 것·추가 방법을 보이고 `--json`은 schema를 통과한다. 상태 디렉터리가 없으면 아무것도 만들지 않는다. 도구 이름마다 시험한 계열이 있고, env shebang·proxy·node 없음은 분석기가 거부하는 이유 그대로 나온다 | REQ-004,030 |
| TC-180 | `codeye doctor` 종료 코드: 도구 하나도 없음, 기준 분석기만 있음, `external` 모드에서 DB 없음, 관리형 모드에서 DB 설정 없음, DB 주소가 있는데 질의 실패, 설정에 적은 도구가 쓸 수 없음, 작업 디스크 쓰기 불가, 탐색된 도구가 쓸 수 없음 | 중요한 항목(`checks[].blocking`)만 1이다. 없는 선택 도구·psql·DB 미설정은 상태만 표시하고 0이다. `model`·`process_control` 항목과 그 사유 값이 없고 JSON 보고서에 경로가 없다. schema·예시·`validate_bundle`이 같은 계약이다 | REQ-004,030 |
| TC-181 | 도구가 하나도 없는 노드(`--advertise-without-baseline`)와 기본 노드, 도구를 탐색해 넘긴 노드의 Go·Python·JS 실행 | 기본 노드는 광고하지 않는다. 플래그를 준 노드는 광고하고 모든 도구 작업을 `unsupported`(`tool_not_configured`)로 기록하며 run이 부분 보고서로 끝난다. 탐색 결과를 넘긴 노드는 의존성이 있는 Go 모듈(모듈 캐시)과 Python·JS를 분석하고 `tool_not_configured`가 없다 | REQ-004,030; TC-80 |

## 모델 검토

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-10 | 후보를 반박하는 상위 finally/validation 존재 | 검토 후 기각/범위 축소, 근거 이력 유지 | REQ-009,010,011 |
| TC-11 | 모델이 없는 파일·행·실행 결과를 주장 | 증거 검증 거부, 재현 상태 승격 차단 | REQ-009,010,011; NFR-008,010 |
| TC-12 | 모델 timeout·JSON 오류·permission ask | 제한 재시도 후 partial, 도구 기반 보고서 열람 가능 | REQ-012,013,026; NFR-008,010 |
| TC-16 | 문서 prompt injection이 토큰/권한 상승 요구 | 실행 정책 유지, secret 미노출, 공격 자료로만 취급 | REQ-009,010,011 |
| TC-34 | 원격 OpenCode가 다른 workspace를 봄 | marker/hash 불일치 거부, 다른 개인 경로 fallback 없음 | REQ-026 |
| TC-38 | OpenCode CLI만 설치되고 모델 연결이 확인되지 않은 doctor | `opencode_cli`는 version_only, doctor는 모델·workspace·취소를 항목으로 두지 않고(연결은 `codeye model probe`) 모델을 호출하지 않으며 자동 fallback도 없음 | REQ-026,030 |
| TC-76 | **실모델**. `codeye model probe`가 관리형 OpenCode 프로세스와 승인된 모델로 합성 입력을 보냄 | 작업별 확인값 일치, verified=true, cleanup=confirmed | REQ-026,030; NFR-006,008,010 |
| TC-77 | 설치된 OpenCode CLI에 로컬 합성 응답이 read 도구를 요청(`scripts/test_opencode_policy.py`) | 광고한 도구 0개, 실제 tool_use/read는 error, 파일 내용이 모델로 전달되지 않음. 어댑터 회귀 시험에서 같은 이벤트는 policy_blocked | REQ-026,030; NFR-006,008,010 |
| TC-78 | **실모델**. 실제 요청이 relay 장부에 예약된 뒤 컨텍스트 취소 | OS 프로세스 트리 정리 확인, cancelled. 이미 전송된 공급자 추론은 청구될 수 있어 장부에 사용량 또는 보수적인 예약을 유지 | REQ-026,030; NFR-006,008,010 |
| TC-79 | 세션 검증, 잘린 JSONL, 잘못된 완료 사유·marker, 미설정/미지원 버전, 모델 예산 경합·재시작 | 유효하지 않은 출력 미게시. relay 시험은 합성 서버를 쓴다. 새 장부는 USD 20까지 받고, USD 15 장부는 USD 20으로 재시작해도 상한과 잔액을 유지한다 | REQ-018,026,030; NFR-006,008,010 |
| TC-85 | **실모델**. 문서 없는 코드에서 맥락 → 후보 조사 | 사실과 후보를 받고 소스 근거 ID를 확인한 뒤 저장 | REQ-004,005,009,027 |
| TC-86 | **실모델**. README가 있는 같은 코드, 별도 비밀값 fixture | 허용한 발췌와 참조만 처리하고 비밀값 fixture는 제외 | REQ-004,005,009,027 |
| TC-87 | 최초 50줄에 없는 상위 호출자의 파일 정리와 테스트 | 검증기가 이를 찾아 후보를 기각한다. 합성 응답과 실제 검증기 호출(**실모델**) 두 경로 | REQ-010,011; TC-10 |
| TC-88 | 다른 run·attempt·snapshot, 허위 path·행·blob·근거 ID, 중복 판정, 근거 없는 기각, 모델이 단정한 재현·critical 심각도 | 거부하거나 미확정으로 기록. 이전 기록 형식과 호환 | REQ-011,013,027; TC-11 |
| TC-89 | 형식 복구 성공·재실패, 일부 후보 격리, 취소·권한 거부·timeout·요청 구성 전 실패, 실제 워커의 verifier timeout 주입 | 각 실패를 구분한다. 워커 경로에서 `partial` manifest, 성공한 도구 관찰, `timed_out` 검증기 기록이 남는다 | REQ-012,026; NFR-008,010; TC-12 |
| TC-97 | 문서 부재, 루트와 하위 문서의 충돌, 읽기 예산 초과, 문서 내 권한 변경 요구 | 네 경우를 구분한다. 모델은 제공된 근거 ID만 인용하고 권한·도구 설정은 유지된다. 유효한 모델 산출물만 다음 역할로 넘어간다. | REQ-005,009,011,027 |
| TC-123 | C/C++·Python·JS/TS·Rust 단일 언어 스냅샷과 다언어 스냅샷, data·vendor·중첩 산출물 디렉터리, build 파일만 있는 생태계, 비 Go 파일의 비밀값 탐지와 쓸 수 없는 탐지, 기존 Go fixture | 조사는 생태계별 round-robin 발췌를, 해석은 소스 위치의 줄 창을 받고 verify가 실행된다. 기록은 경로로 정한 종류로 decode되고 같은 신원의 두 취득에서 요청 바이트가 같다. 제외 디렉터리와 범위 밖 생태계 manifest는 입력이 아니고, 그렇게 빠진 범위 안 소스는 `source_in_excluded_directories`로 남는다. 중첩 index 파일은 진입점이 아니다. 실제 worker와 기본 보고서 registry가 Python 기록을 받는다. 비밀값 선별은 새 발췌에도 적용되고 쓸 수 없는 탐지면 요청이 없다. Go 요청은 변경 전 캡처와 같다. 실모델 품질 근거가 아니다. | REQ-004,009,027; TC-100,118 |
| TC-128 | 검토 지침 2.0.0. 거부된 출력 뒤 JSON 아닌 복구 응답, JSON 아닌 응답 연속 두 번, 두 번 거부, verify에서 관찰 ID가 섞인 항목·관찰 ID만으로 지지한 항목·유효 근거 옆의 지어낸 ID, 후속 호출 timeout, 3회 호출 기록의 보고서 경로 | JSON 아닌 응답은 복구와 별도로 한 번 재시도하고 앞 오류 코드를 유지한다(단계당 최대 3회). 연속 두 번이나 두 번 거부면 2회에서 실패하고 격리한다. 요청의 관찰 ID만 빼고도 판정 규칙을 통과하는 항목만 받아들이고 `evidence_id_dropped`를 남긴다. 지어낸 ID는 거부한다. 후속 호출 실패는 수락한 판정을 유지하고 나머지를 격리한다. 3회 호출 기록이 보고서 검증과 두 schema를 통과한다. 합성 입력만 쓴다. | REQ-011,013; NFR-008 |
| TC-129 | 검토 지침 하나(`prompts/review`). 에이전트·워커·서비스 설정에서 검토를 켠 노드, 기본 회귀 집합(`fixtures/regression/`)의 실모델 실행(**실모델**) | 모든 노드가 같은 기본 역할과 같은 조건의 계약 후속 검토를 계획한다. 회귀 실행은 [평가](EVALUATION.md)의 사전 기준으로 판정하고 차단 조건이 없어야 한다. 품질 비교에는 별도 측정이 필요하다. | REQ-010,027; NFR-007,010; TC-100 |
| TC-267 | 구현 이름과 다른 언어로 쓴 문서 계약과 질문, 깊은 구현·테스트 위치, 비밀값 선별·부차 경로·PR 범위, 위치 생략·허위 ID·중복·미제공 식별자 | 실제 경로·줄·이름을 위치 후보로 제공하며 일반 코드 발췌 공간을 먼저 확보한다. 범위 밖·보류 파일은 고르지 않는다. 유효한 선택만 스냅샷에 묶어 저장하고 후속 읽기에서 범위·줄·해시를 다시 확인한다. 선택이 없거나 잘못됐으면 일반 발견과 대조를 수락하고 기존 기록 형식을 유지한다. 위치 ID는 근거나 코드 노출로 세지 않는다(`TestInvestigateKeepsFindingsWhenProbeIsInvalid`, `TestNavigationFitsAfterExistingExcerpts`, `TestContractProbeRereadsExactLocationsWithinScopeAndChanges`, `TestContractProbeRecordExample`). | REQ-009,010,011,027; NFR-006,008 |
| TC-268 | 수락된 전체 조사의 미확인 계약, 새 구현과 후속 모듈 발췌, 선언·주석·테스트만 인용, 문서 충돌·digest 불일치·모델 미설정, 첫 응답 오류와 남은 호출 0·1·2회 | 후속 작업을 기존 검토 뒤에 두고 선택과 새 구현 근거가 있을 때만 요청한다. 판정의 계약·범위·문서 근거를 보존하고 실제 호출과 입력을 기록한다. 모듈 기록은 digest가 맞을 때만 다시 읽는다. 문서가 명시한 이름에는 범위 안의 유일한 구현을 찾는다. 잘못된 응답은 남은 예산으로 한 번 수리할 수 있다. 원래 판정은 후속 실패·모델 미실행·미확인에서 유지된다(`TestContractFollowup`으로 시작하는 시험, `validate_bundle.py`). | REQ-010,011,013,018,027; NFR-006,008 |

TC-268은 전체 조사 한 번에서 같은 파일 뒤쪽의 서로 다른 구현과 여러 계약에 공통인 구현을 묶는다. 근거 단서가 실제 발췌 ID를 가리키고, 같은 원문을 한 번만 보내며, 일반 발췌 자리와 요청 상한을 유지하는지 확인한다. 보류 파일·범위 밖 파일·주석만의 이름은 계약 단서에서 제외한다. 합성 모델 응답으로 일치·불일치·미확인 계약과 각 인용 위치가 실제 보고서 빌더를 거쳐 별도로 게시되는지 검사한다. 후속 모델 호출을 추가하지 않는다(`TestWholeInvestigationAllocatesEvidenceAcrossContractComparisons`). REQ-009,010,011,013,018,027과 NFR-006,008에 연결한다.

TC-268의 Go 호출자 시험은 선언이 창 밖에 있는 함수의 직접 호출과 그 결과가 흐르는 분기·반환, 그리고 선택한 창의 조건에서 직접 호출한 구현을 확인한다. 지역 함수값·타입과 다른 패키지의 같은 이름, 중복 선언, 로그용 호출, 이미 읽은 구현·호출, 생성물·테스트·보류 파일, PR 변경 범위 밖 호출을 함께 검사한다. `TestContractFollowupSendsGoCallerAsCitableEvidence`는 합성 응답으로 앞선 판정과 스냅샷 근거 연결을 검증한다. `TestContractFollowupUsesPromisedTermFromAcceptedSliceStrength`는 계약 원문·수락된 강점·실행문에서 찾은 정확한 단서와 모듈 기록 연결을 검사하고, 재사용할 새 줄이 없거나 digest가 다르면 발췌를 보내지 않는지 확인한다.

TC-268의 짧은 파일 시험은 위치 선택이나 검증된 리터럴로 찾은 새 실행문과 앞선 발췌가 같은 파일에 있을 때 함수 선언부터 공개 호출 결과까지 한 창에서 확인한다. 앞선 인용 ID와 모듈 기록 연결은 유지하고, 별도 근거가 없는 중복 발췌는 생략한다. 전체가 이미 알려졌거나 선택 범위가 주석만 포함하는 경우, 파일 종류·범위·선별·줄·바이트 한도에 걸리는 경우는 확장 대상에서 제외한다(`TestContractFollowupUsesWholeShortFileWithOriginalEvidence`, `TestContractFollowupSkipsIneligibleWholeFile`, `TestContractFollowupWholeFileRetainsExactSliceLineage`).

TC-79의 확인값 시험은 OpenCode 완료 이벤트의 합성 스트림으로 한 번의 불일치 후 복구, 연속 불일치, 호출 예산 소진, 로컬 작업공간 준비 실패, 공급자 실패와 취소를 확인한다. 같은 요청을 새 작업공간과 새 확인값으로 다시 보내며, 실패한 응답은 버리고 사유와 사용량을 보존한다. verify는 수락한 판정과 보류 후보의 형식 복구 요청을 유지한다. 계약 후속 검토는 두 번의 호출 상한을 지킨다(`TestContractWorkspaceProofRetry`, `TestVerifyWorkspaceProofRetryKeepsPending`). REQ-011,013,018,026과 NFR-006,008,010에 연결한다.

TC-09·TC-10은 스냅샷에 묶인 C/C++ 진단과 별도 파일의 조건 위치를 네 창 한도 안에서 함께 보내고 가설이 두 근거와 원래 관찰을 인용하는지 확인한다. 관련 메모 원문, 위치 미확인, 범위 밖, 선별 보류, 작업 사본 변경 또는 이전 형식의 관련 내용은 입력에서 제외한다(`TestInterpretLinksVerifiedCppConditionWithinOrdinaryBudget`).

TC-79의 버전 확인 시험은 합성 실행기로 `--version`과 모델 요청의 제한시간·실패 사유를 구분한다. 버전 확인은 30초와 설정값 중 짧은 시간을 적용한다. 확인이 시간 초과되면 모델 요청으로 넘어가지 않으며 `opencode_version_timeout`을 기록한다. 요청 자체의 시간 초과는 `model_timeout`이다. 호출자 취소와 정리 확인 실패는 각각 원래 사유를 유지한다(`TestReviewSeparatesVersionProbeAndModelTimeouts`, `TestReviewVersionProbeDeadlineKeepsCallerCancellationSeparate`, `TestModelOutcomeMapping`).

TC-270은 README 링크와 번역·기여 문서가 있는 제품, 여러 모듈 README와 문서 사이트가 있는 모노레포, 관리 문서만 있는 프로젝트에서 탐색·발췌 한도를 시험한다. 대표 README와 연결된 안내, 시작·구성·지역 참조 문서를 우선 선택한다. 범위 밖 README와 외부·코드 예제·모호한 링크는 순위를 바꾸지 않는다. 관리 문서만 있어도 검토하며, 남은 목록과 상속·적용 경로, 읽은 바이트와 발췌 제한을 보존한다(`TestDiscoveryKeepsProductGuidanceWithinDocumentBudgets`, `TestReviewPrioritizesProductGuidesUnderInputLimits`, `TestReviewStillUsesAdministrativeOnlyGuidance`). REQ-004,005,011,027과 NFR-006,008에 연결한다.

TC-270의 긴 문서 시험은 첫 발췌와 뒤쪽 API 절 사이의 본문, 전체가 기존 예산에 들어가는 170줄짜리 안내, 여러 긴 안내의 발췌 순서, 개수나 바이트 상한 초과, 운영 절과 다른 문서가 발췌 자리를 모두 쓰는 경우를 다룬다. 기존 발췌를 유지하고 남은 자리에 추가한 본문의 원래 줄과 근거 ID가 요청과 보고서에서 일치하는지 확인한다. 실제 요청에서 본문이 빠졌으면 `document_excerpt_only`를 기록한다(`TestDocumentContinuationReachesReport`, `TestDocumentCoverage`로 시작하는 시험).

TC-270의 JS/TS 옵션 시험은 긴 README 뒤쪽에 명시한 API와 기본값을 구현의 분기 및 동일 입력의 기본·반대 조건 단언에 연결한다. 다른 import, 모호한 이름, 변경 가능한 옵션, 결과를 확인하지 않는 콜백과 Promise에서는 구조적으로 연결된 단언을 보내지 않는다. 두 조건을 포함한 요청과 계약 대조는 `TestOverviewPrioritizesDocumentedJavaScriptDefaultAndOverride`에서 확인한다.

TC-270의 기본 읽기 한도 시험은 64 KiB를 넘는 대표 README와 연결된 짧은 안내를 탐색하고, 두 문서의 발췌를 기존 요청 상한 안에서 검토에 보낸다. 64 KiB를 넘는 설계 문서 하나를 경로로 지정해도 필요한 발췌를 보내며 무관한 문서는 빠진다. 문서당 512 KiB와 합계 4 MiB는 로컬 읽기 상한이다. 512 KiB를 넘은 문서는 `document_too_large`로 남으며 별도 지정한 예산은 계속 적용한다(`TestReviewUsesLongPrimaryReadmeUnderDefaultBudget`, `TestReviewUsesExplicitLongEngineeringDocument`, `TestDocumentsBudget`).

TC-270은 여러 모듈의 부가 목록으로 요청 한도가 부족한 프로젝트에서도 대표 README, 긴 제품 요구사항의 뒤쪽 사용자 흐름과 별도 안내의 원문을 함께 보내는지 확인한다. 지역 안내의 적용 디렉터리와 발췌 줄을 보존하고, 상한을 넘는 파일의 사유와 발췌·부가 목록의 제한을 기록한다. 문서 탐색 예산과 원래 모듈 연결은 유지한다(`TestDocumentRequestPreservesDistinctProductEvidenceUnderMetadataPressure`). 예제와 합성 입력이 많을 때는 대표 README에서 링크한 예제와 남은 제품 본문을 미연결 합성 입력보다 먼저 보낸다. 부차 경로만 범위에 든 경우에도 실제 안내를 보낸다(`TestDocumentSelectionFavorsProductTextOverUnlinkedFixtures`).

TC-271은 여러 모듈의 Go HTTP 서비스에서 문서에 나온 변경과 이후 조회에 해당하는 경로를 함께 고른다. 문서 요청에 원문과 제한된 경로 선택지만 넣고, 선택을 수락한 뒤 전체 조사에 실제 처리기와 등록부를 보낸다. 거짓 경로 ID와 미선택은 문서 결과를 보존한다. 다른 attempt의 기록, 범위 밖·보류·부차 소스와 모호한 등록을 쓰지 않는다. 아직 읽지 않은 구현이 있는 경우 기존 후속 검토에서 실제 근거를 다시 읽는다. 생성기 13의 보고서는 계속 읽고 새 경로 선택 decoder는 생성기 14부터 쓴다(`TestWorkflowSelectionsReachReview`, `TestWorkflowContractFollowupReadsSelectedImplementation`, `TestWorkflowReviewDecoderAndOlderBuilder`, 경로 탐색 시험, `validate_bundle.py`). REQ-005,009,010,011,018,027과 NFR-006,008에 연결한다.

TC-271은 긴 HTTP 헬퍼의 뒤쪽에서 응답과 앞선 읽기·오류 처리를 같은 한도 내 발췌에 담는지 확인한다. 짧은 헬퍼, 앞쪽에 이미 응답이 있는 헬퍼, 응답 후보가 여러 개이거나 다른 응답 객체·패키지를 사용하거나 바이트 한도를 넘는 헬퍼도 검증한다. 고정한 소스에서 같은 발췌 ID와 줄 범위를 다시 읽을 수 있어야 한다(`TestGoHTTPDecisionWindowsReadResponseAndItsPrecedingStatements`).

TC-271의 보충 요청 시험은 유효한 첫 선택, 보충 응답의 유효 선택·빈 선택·허위 ID·거절·시간 초과·작업공간 오류, 코어 호출 예산이 남지 않은 실행을 포함한다. 최초 문서 응답을 보존하고, 실제 보충 호출만 일반 문서 응답과 구분해 장부에 센다(`TestSupplementalWorkflowChoices`).

TC-271은 같은 HTTP 메서드와 패턴을 서로 다른 처리기로 등록한 경우의 선택지도 검증한다. 실제 파일과 함수에서 처리기 이름을 뽑아 같은 패턴을 구분하고, 보류 파일과 비밀값 양식에 맞는 이름은 보내지 않는다. 이름 자체나 범위 밖 경로 ID로는 선택할 수 없다. 요청 여유가 작을 때는 확인한 경로와 문서 발췌를 유지하고 리터럴이 없는 패턴에 먼저 이름을 붙인다. 두 선택 요청의 형식 지침에는 수락될 수 있는 예시 답이 없다(`TestWorkflowCuesDistinguishIdenticalPatterns`, `TestWorkflowCuesAreBoundedAndOptional`, `TestWorkflowChoiceInstructionsContainNoEligibleAnswer`).

TC-271은 문서 검토에서 고른 경로만 후속 구현 탐색 대상으로 삼고, 반환 호출이나 확인한 수신자를 따라 저장·조회 결과를 소스에 연결한다. 직접 확인한 하나와 두 개의 호출, 드라이버별 SQL, 모호하거나 범위를 벗어난 호출, 근거 일부만 요청에 들어간 경우를 확인한다. 호출과 구현을 함께 보냈을 때만 실행하지 않은 조건부 소스 근거라고 표시한다(`TestGoHTTPWorkflowOutcomeLinksReturnedStoreOperations`, `TestGoHTTPWorkflowOutcomeRequiresAnExactBoundedChain`, `TestGoHTTPWorkflowOutcomeReplaysWithScopedConditionalEvidence`, `TestGoHTTPWorkflowOutcomeKeepsTwoBoundedStepsIntoVersionedSQL`).

TC-271의 HTTP 테스트 발췌 시험은 리터럴 요청과 짧은 요청 생성 함수, 경로 변수, 변경 후 조회, 상태·본문 기대값을 대조한다. 테스트 원문의 스냅샷과 줄 범위를 확인한다. 다른 메서드·경로, recorder 재사용, 요청 경로 재할당, 단순 로그, 생성·보류·범위 밖·미변경 테스트와 여러 등록에 맞는 요청은 연결하지 않는다(`TestGoHTTPWorkflowUsesOneReadAfterChangeTestWindow`, `TestGoHTTPWorkflowTestRequiresMatchingExecutedRequestAndAssertion`, `TestGoHTTPWorkflowTestMatchesOnlyConcretePathShape`).

TC-271은 관련 조회·변경 처리기와 테스트를 보낸 전체 조사에서 모델이 조회만 인용한 경우도 확인한다. 선택 경로와 일치하는 최신 문서 기록 및 실제 전송한 세 발췌는 별도 맥락으로 남고 모델의 미확인 판정과 인용은 유지한다. 일부 발췌가 요청에서 빠졌거나 경로 선택·적용 범위·근거 종류·스냅샷이 맞지 않으면 이 묶음을 저장하거나 게시하지 않는다(`TestAcceptedWorkflowReadingRetainsSubmittedContextWithoutModelCitations`, `validate_bundle.py`).

TC-270의 목록 시험은 첫 발췌 바로 다음 줄에 마지막 항목이 있는 Markdown 순서 목록과 글머리 목록, 중첩·코드 블록, 남은 발췌 자리나 바이트가 없는 경우를 다룬다. 마지막 항목까지 전달할 수 있으면 연속한 원문 줄을 보내고, 실제 요청에 목록 일부만 남으면 `incomplete_list`를 붙인다(`TestDocumentRequestPreservesListBoundary`, `TestDocumentListBoundaryReflectsFittedWindows`).

TC-267의 `TestNavigationReachesNestedTestAssertions`는 한 디렉터리에 읽기 상한보다 많은 테스트 파일이 있어도 중첩 디렉터리에 읽기 차례를 준다. 테스트 본문에 있는 단언으로 위치를 고르고 준비 함수에만 단언이 있으면 일반 위치를 유지한다. 요청 한도로 빠진 `L` 별칭과 그 정식 ID는 모두 거부하며 나머지 별칭은 같은 위치에 묶인다. 선택 누락·`null`과 유효하지 않은 객체의 차이는 검토 원시 기록에만 남는다.

TC-97의 Python API 문서 시험은 Markdown 지시문이 참조하는 모듈과 공개 메서드, 루트와 `src/` 경로 중복, 코드 블록 속 예제, 비리터럴 표현, 범위 밖 또는 비밀값이 있는 소스를 다룬다. 문서의 예제 원문과 적용 범위를 유지하고 구현 본문·pytest의 동일 입력 기대값을 별도 근거로 보낸다. 인용한 docstring과 겹치는 코드만으로는 구현 대조 판정을 수락하지 않는다. 결과와 근거 출처는 코드 포함·미포함 보고서에서 검증한다.

TC-97의 공개 CLI 문서 검색은 Go, Java, Python, JS/TS, Rust, C/C++의 명시 등록과 정상 안내를 점검한다. 긴 문서 끝에 이름이 있거나 적용 문서가 읽기 한도를 넘거나 소스의 파서와 등록이 모호하면 검색 결과를 내지 않는다. 실제 `docs.discover`가 반환한 선언 위치와 문서 목록을 보고서에서 확인하고 코드 미포함 내보내기는 이름·기본값을 생략한다. REQ-004,005,012,013에 연결한다(`TestDiscoveryFindsExplicitCLIOptionsAndCheckedDefaults`, `TestDiscoveryNeverClaimsIncompleteOrAmbiguousDocumentationGap`, `TestDiscoverySkipsPrivateAndConditionalCLIOptions`, `TestDocumentationGapPublishes`).

TC-268의 Java 문서 대조는 Javadoc을 가진 공개 메서드에서 같은 클래스의 호출을 읽고 오버로드를 구분한다. 전체 조사와 후속 요청에서 맞는 호출·구현 줄을 받으며 보조 메서드만 인용하면 확정 판정을 수락하지 않는다. 상속·수신자·가변 인자로 구현을 확정할 수 없는 예를 함께 검사한다. REQ-005,010,011,027에 연결한다(`TestJavaLocalCallResolutionRespectsReceiverAndOverloads`, `TestJavaDocumentContractReachesSameClassImplementation`).

TC-97의 패키지 안내 시험은 별도 문서 사이트의 npm 호출 예제와 실행 코드를 대조한다. 발췌 경계에서 나뉜 짧은 코드 블록은 가져오기와 호출이 함께 들어간 원문 줄을 보낸다. 호출 이름 없는 계약, 별도 범위의 질문, 잘못된 blob 해시, 다른 로컬 패키지와의 모호성, 생성 파일과 범위 밖 또는 보류된 소스로 만든 참조는 거부한다. 코드 포함·미포함 보고서에는 동일한 문서 및 구현 위치가 남고, 코드 미포함 JSON에서 패키지와 호출 이름을 생략한다. REQ-005,010,011,012,027과 연결한다.

TC-97의 범위 복구 시험은 이름 있는 호출의 예제와 같은 페이지에 바로 이어진 설명을 함께 인용하는 계약과 질문을 수락한다. 다른 페이지의 범위를 넓히거나 호출 이름 없이 패키지 범위를 쓰면 해당 항목과 실제 인용, 유효한 경로를 기존 한 번의 복구 요청에 담는다. 복구 뒤에도 근거가 부족한 범위만 제외하되 유효한 계약·질문이 남을 때 누락을 문서 분석 공백으로 표시한다. 유효한 항목이 없거나 다른 검증이 실패하면 전체 응답을 거부한다.

TC-268은 일부 구현만 확인한 응답을 미확인으로 유지하는지, 부분 일치의 준수·위반 근거가 같은 파일의 서로 다른 줄 범위에서도 각각 연결되는지 확인한다. 근거를 하나만 인용하거나 같은 조건을 반복하거나 이미 읽은 줄만 참조하면 판정을 바꾸지 않는다. Rust 테스트 전용 블록 중간과 JS·TS 템플릿 문자열 중간에서 시작하는 발췌를 구현으로 인용한 응답을 거부하고, 같은 파일의 실행 코드는 수락한다.

TC-97의 요청 한도 시험은 여러 모듈이 긴 경로의 안내 문서를 상속해 부가 목록만으로 한도를 넘는 경우를 다룬다. 부가 목록을 먼저 줄여 문서 발췌를 담은 요청을 한 번 보내고 요청과 기록의 한계 코드를 확인한다. 탐색의 모듈·문서 연결은 전부 보존한다.

TC-97의 인용 시험은 전달 순서의 `D1`, `D2`와 형식 복구 시의 같은 대응을 확인한다. 미제공 참조와 짧은 참조·스냅샷 근거 ID가 섞인 중복 인용을 거부한다. 계약, 충돌 양쪽, 질문, 지침 메모, 문서 품질의 유효한 인용은 원래 스냅샷 근거 ID로 저장해 후속 조사로 보낸다.

TC-267의 위치 시험은 같은 이름이 앞선 조사에 들어간 설정 창과 새 분기 실행문에 모두 있는 경우를 포함한다. 한 파일의 서로 다른 분기 두 곳과 관련 테스트의 순서, 앞선 읽기 자료로만 접근할 수 있는 구현의 선별, 모든 구현 줄이 이미 제공된 경우, 일반 구조의 모호성, 보류 파일과 PR 변경 밖 구현의 제외를 검사한다. 테스트 위치만 고른 응답에서도 일반 발견과 계약 대조를 수락한다(`TestNavigationRanksFreshContractDecisionsBeforeTestSupport`, `TestNavigationOmitsAmbiguousStructuralCode`, `TestInvestigateKeepsFindingsWhenProbeIsInvalid`).

모듈 단위 검토 분할([모델 검토](REVIEW.md#모듈-단위-검토-분할)):

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-160 | 지원 언어 소스 없이 문서·manifest만 있는 스냅샷, 모델 입력이 될 파일이 없는 스냅샷, 범위 밖 파일, gitleaks 미설정 | 모델 입력 파일(지원 언어 소스·안내 문서·모듈 manifest)이 범위에 있으면 gitleaks를 계획하고 아니면 계획하지 않는다. 미설정이면 `unsupported`(`tool_not_configured`)로 공백이 보인다. 비밀값 작업만 있는 보고서도 `static_analysis_not_planned`다. | REQ-009; NFR-008 |
| TC-161 | 무작위 트리와 항목 순서, 400파일 한도 사례, `fixtures/modules/slices/input` | 적격 파일마다 overview 접두·slice·계획 안 함 중 정확히 한 곳이다. slice는 범위 산출물의 상한(최소 예산에서 8개·12,000바이트)·모듈 8개 이하이고 96개를 넘지 않으며, 순회 순서로 너비가 깊이보다 먼저다. 항목 순서가 바뀌어도 계획이 같다. coverage fixture의 계획은 입력 밖의 기대값(`expected-plan.json`)과 같다. | REQ-009,027 |
| TC-162 | 0~4,000바이트의 무작위 파일(JSON이 escape하는 문자·제어 문자·다바이트 문자) | 파일 상한 `min(2400, 6 × size + 2)`는 실제 JSON 발췌 바이트보다 작지 않다. | REQ-009 |
| TC-163 | 회귀 집합과 기존 fixture처럼 모든 적격 파일이 overview에 들어가는 저장소 | `review.scope`를 계획하고, 파일의 모든 줄을 조사했다면 추가 모델 요청 없이 남는 slot을 `no_review_scope`로 끝낸다. 일반 역할의 요청 바이트는 고정한다(`TestGoReviewInputsUnchanged`, `TestReviewExampleRecords`). | REQ-009,027; NFR-007 |
| TC-164 | 기록 0.3과 범위 산출물 0.1: slice 없는 0.3, slice 있는 0.2, 범위 밖 key, 모듈 9개, interpret 단계·후보, 0.2 기록의 slice upstream, 두 곳에 있는 파일, 정렬되지 않은 `offered`, `offered`와 다른 `covered_by_overview`, 모듈 수 불일치, 목록 밖 선별 상태, 제한 코드 없는 `not_screened` 산출물, 상한 208 | Go decoder와 JSON schema·`validate_bundle.py`가 모두 거부한다. 0.2 기록의 바이트는 그대로다. 예시는 coverage fixture로 다시 만든 바이트와 같다. | REQ-011,027 |
| TC-165 | 상한 15·99·207과 한 번에 수락되는 응답, 형식 복구를 요구하는 응답 | 계획 키는 상한과 무관하다. 같은 slot의 조사와 확인에 쓸 두 호출이 남으면 시작하고 조사 복구는 확인에 쓸 한 호출을 남긴다. 거절된 slot과 짝 verify는 호출·기록 없이 `budget_exhausted`다. 형식 복구에 상한을 다 쓰면 아직 수락하지 못한 범위를 성공으로 세지 않는다(`TestSliceAdmissionKeepsTheCeiling`). | REQ-012,018,027; NFR-007,008 |
| TC-166 | 비밀값 작업 없음·미실행·실패·완료(보고된 파일이 slice에 있음), 판단 순서 | 선별 작업이 없거나 미실행이면 `review.scope`와 slice가 실행되고 범위 산출물은 `not_screened`, 범위와 slice 기록은 `model_input_not_secret_screened`를 싣는다. 선별 결과를 쓸 수 없으면(실패·부분 결과) 미선별과 같이 실행되고 `not_screened`이며, 읽을 수 있는 결과가 보고한 파일은 계속 `withheld`다. 보고된 파일은 범위 산출물에서 `withheld`이고 어떤 요청에도 없다. 파일이 남지 않은 slice는 상한이 다해도 `no_review_scope`다. 승인 없음 → 범위 산출물 없음(`dependency_failed`) 순서이고, 미선별과 쓸 수 없는 선별은 관문이 아니다. | REQ-009; NFR-008 |
| TC-287 | 요청 예산 최소·기본·최대, 파일 전체와 선언 단위, 줄 범위 인용, 감싸는 선언, 이전 schema 기록 | 한 규칙이 모든 상한을 정하고 최소 예산은 이전 고정 요청과 같다. 계획의 작업 키는 예산과 무관하다. 단위는 Go·Java·Python·Rust·C 선언 경계에서 나뉜다. 발췌 안의 인용은 같은 blob의 결정적 ID로 좁혀지고 범위 밖·형식 오류·범위 없는 긴 인용은 거부되어 형식 복구로 간다(복구 뒤 처리는 TC-288). 다른 발췌 안의 창은 참조로 간다. verify는 인용을 감싸는 선언을 받는다. verify가 만든 범위가 앞 단계 발췌와 같은 ID이면 앞 단계 항목 그대로 보내고 기록해 보고서가 verify를 제외하지 않는다(파일 단위인 선언, slice 단위인 선언, slice 머리인 선언, 강점 범위인 선언, 다시 보내지 않은 창으로 좁힌 인용). 담은 단위 없이 남는 강점 범위는 보내지 않는다. slice는 첫 단위와 같은 머리를 다시 붙이지 않는다. 0.2·0.3 기록은 바이트 그대로 읽히고 0.7 필드는 거부된다. 최소·기본·최대 예산과 최소 요청에 기본 출력을 짝지은 예산의 최악 입력이 요청 한도를 지킨다. CRLF 파일은 LF 파일과 같은 경계에서 나뉘고 JSX 태그는 정규식으로 읽지 않는다. OpenCode가 relay 응답을 호출 제한시간만큼 기다리고, relay는 대기 중에 연결을 끊은 요청을 예약·전달하지 않는다(`TestBudgetLimits`, `TestPlanKeysDoNotDependOnBudget`, `TestDeclarationCuts*`, `TestPythonCutsIgnoreCRLFBlankLines`, `TestHeaderEndContinuedImports`, `TestBraceCuts*`, `TestUnitsWholeFileAndLineFallback`, `TestCitationNarrowsToSnapshotRange`, `TestCiterResolvesKnownRangeToUpstreamEntry`, `TestRequestExcerptsReferenceContainedWindows`, `TestVerifyReadsEnclosingDeclaration`, `TestVerifyDeclarationThatIsAnUpstreamUnit`, `TestSliceVerifyDeclarationThatIsAUnit`, `TestVerifyDeclarationThatIsAStrengthRange`, `TestVerifyNarrowedToAnUpstreamWindowItDidNotResend`, `TestVerifyLeavesAStrengthRangeWithoutItsUnit`, `TestSliceHeaderThatIsTheFirstUnit`, `TestSliceVerifyDeclarationThatIsTheHeader`, `TestEarlierSchemaRecordsStayReadable`, `TestReviewRequestSizeWorstCase`, `TestRelayProfileUnchanged`, `TestProviderProfile`, `test_queued_request_of_a_closed_client_is_not_reserved_or_forwarded`) | REQ-009,010,011,027; NFR-007 |
| TC-288 | 여섯 역할과 slice의 형식 오류(범위 없는 긴 인용, 80줄 초과·틀린 범위, 문서·manifest 근거에 붙인 범위, 반복·초과 근거, 목록 한도, 서술·실패 과정·근거 누락, 같은 문서를 양쪽에 둔 충돌)와 무결성 오류(발췌 밖 범위, 요청에 없던 ID)를 첫 응답과 복구 응답에서 두 번 냄, 복구를 보낼 수 없거나 뒤 호출이 timeout·호출 상한으로 끝남, 후보 하나가 격리된 전체·slice verify, dry run 가짜 upstream의 형식·무결성 오류 주입 | 모든 출력 오류 코드가 무결성·형식으로 등록되고 [출력 오류 분류](REVIEW.md#출력-오류-분류)의 표와 같으며, 시험이 읽는 기록의 `output_errors`는 모두 등록된 코드다. 복구 요청은 항목·필드·인용한 발췌 ID와 코드를 담는다. 형식 오류는 복구 뒤 항목 안에서 정리되거나(verify는 후보의 범위 또는 발췌 전체, 강점·대조·계약 후속 검토는 발췌 전체, 반복 제거, 한도까지) 그 항목만 빠지고 단계는 성공한다. 기록에는 `review_output_degraded`와 정리 한계가, 정리한 호출에는 원래 코드가 남는다. 무결성 오류는 이전처럼 거부·격리된다. 일부 후보만 판정한 verify는 `failed`(`review_output_incomplete`)로 남고, 보고서는 수락한 판정을 발견에 싣고 격리한 후보만 `unreviewed`로 보인다. `ai_review`는 `performed`, 그 task 결과는 `accepted_incomplete`, 보고서는 `partial`이고 한계 `review_verification_incomplete`가 Markdown에서 문장으로 읽힌다. 이전 생성기 번호의 `accepted_incomplete`는 검증이 거부한다. 범위 없는 후보를 뺀 보고서는 `no_actionable_findings`가 아니고 코드 분석 공백 `review_candidate_omitted`를 싣는다 (`TestCiterDegrade`, `TestOutputCodesMatchTheReviewTable`, `TestVerifyFormFaultsRepairedThenResolved`, `TestVerifyFormFaultResolvedWhenTheRepairCannotArrive`, `TestRoleFormFaultResolvedWhenTheRepairCannotArrive`, `TestDocumentFormFaultsResolvedAfterTheRepair`, `TestCandidateFormFaultsResolvedAfterTheRepair`, `TestReviewEnvironmentOutputRules`, `TestContractFollowupPreservesPriorOnBudgetOrInvalidOutput`, `TestComparisonOmittedOnlyOnceTheRepairIsSpent`, `TestReportPublishesVerdictsOfAnIncompleteVerify`, `TestReportPublishesVerdictsOfAnIncompleteSliceVerify`, `TestDryRunUpstreamFaultInjection`) | REQ-010,011,012,013,027; NFR-007 |
| TC-293 | 여러 파일의 go vet·Staticcheck 진단 20건(`fixtures/modules/triage/input`), 무해 묶음, 검증이 반증하는 문제 묶음, 같은 줄의 두 도구 진단, 해석 후보가 다룬 진단 | 진단이 규칙·단위별 묶음으로 slot에 담겨 분류된다. 보고서의 수는 관찰에서 나오고 분류·해석·분류하지 않음의 합이 대상 진단 수다. 무해 묶음은 도구 finding과 이유·인용을 함께 남긴다. 지지된 문제는 조치 카드이고 반증된 문제는 아니다. 두 도구의 묶음은 한 묶음이지만 도구 finding은 둘이다. 코드 미포함 내보내기는 모델 이유를 생략한다. 분류·검증 기록은 예시(`examples/review/triage`)와 같고 `validate_bundle.py`가 schema·blob·묶음·후보·상류 digest를 묶고 음성 사례를 거부한다([도구 진단 분류](REVIEW.md#도구-진단-분류)). | REQ-009,010,011,012,013 |
| TC-294 | 가장 작은 호출 상한에서 전체 역할이 호출을 많이 씀 | 두 호출이 남은 첫 slot만 분류하고 다음 slot과 짝 검증은 호출 없이 `budget_exhausted`다. 분류하지 못한 진단은 분류하지 않음으로 센다. | REQ-012,013 |
| TC-295 | 가장 작은 출력 예산(요청당 묶음 2개) | 계획된 두 slot이 묶음 4개를 싣고 나머지는 `diagnostics_outside_triage_slots`로 남으며 분류로 세지 않는다. | REQ-012,013 |
| TC-296 | 분류 답의 형식 오류(없는 분류, 빠진 묶음)와 무결성 오류(요청에 없는 묶음)를 복구 뒤 고치거나 다시 냄 | 복구 요청은 남은 묶음과 코드만 담는다. 고치면 모든 묶음이 분류된다. 다시 내면 첫 답에서 분류한 묶음은 남고 틀린 묶음은 분류하지 않으며 `diagnostic_groups_not_triaged`가 남는다. task는 성공이다. | REQ-010,011,027 |
| TC-297 | dry run 가짜 upstream의 분류·검증 답, `--shape-errors 1`, `--integrity-errors 1` | 기본 답은 문제·무해·판단 불가를 모두 내고 모든 대상 진단이 분류 또는 해석으로 세진다. 형식 오류는 복구 뒤 인용을 정리하거나 그 묶음만 빼고, 무결성 오류는 그 묶음을 거부한다. 기록의 출력 코드는 모두 등록된 코드다. | REQ-010,011,027 |
| TC-298 | 호출 상한 20, 파일·진단이 많은 프로젝트(진단 수백 건, 심각도 섞임): slice가 상한에 다 들어가는 크기와 들어가지 않는 크기 | 다 들어가면 분류 몫은 0이고 모든 검토 가능 줄을 보낸 뒤 분류가 남은 호출을 쓴다. 다 들어가지 않으면 몫은 한 쌍이고 slice는 그만큼만 양보하며, 첫 분류 slot이 심각도가 가장 높은 묶음(과 그 코드를 함께 보이는 낮은 묶음)을 분류하고 뒤 slot은 `budget_exhausted`다. 상한을 넘지 않는다([호출 비용](REVIEW.md#호출-비용)). | REQ-012,013 |
| TC-299 | 분류 답의 경계: 복구 답이 형식 오류 묶음을 빠뜨림, 같은 묶음의 답 두 개, 다른 묶음 발췌 인용, JSON이 아닌(잘린) 출력, 첫 호출 뒤 취소, 64개를 넘는 두 도구 묶음, 진단 64개 묶음 여러 개의 검증(가장 작은 요청 예산) | 복구 요청은 남은 묶음과 그 발췌만 싣고 빠진 묶음은 `triage_item_omitted`다. 중복은 `triage_group_duplicate`, 다른 묶음 인용은 복구 뒤 자기 묶음 인용만 남는다. 잘린 출력의 재시도는 묶음 절반만 묻고 slot은 성공한다. 취소된 slot은 호출과 미분류 묶음을 담은 실패 기록을 남긴다. 나뉜 조각은 도구가 둘 이상일 때만 `cross_tool`이고 기록 검사가 이를 확인한다. 검증 요청은 후보마다 진단 8개만 싣고(`verification_observations_trimmed`) 기록은 모두 가진다. | REQ-010,011,012,027 |
| TC-300 | 미검토 `high` 도구 finding 다섯과 지지된 `medium`·재현된 `low` 발견, 확인 전 분류 문제; 표 이전 생성기 기준 보고서와의 비교; 원시 등급만 다른 같은 위치의 진단 | PR 댓글·요약 우선순위·보조 경로 목록은 검증 상태가 먼저다. 표가 다른 두 보고서의 같은 도구 finding은 `persisting`이고 차이는 `tool:<analyzer>:severity_table` 하나다. 원시 등급만 다른 진단은 한 finding이 높은 기본 심각도를 쓴다. 도구 진단 접힘 제목은 판정한 진단과 판단 보류를 따로 센다. | REQ-007,013 |
| TC-301 | 시험·예제·벤치마크·생성물·외부 코드 경로의 도구 진단과 배포 코드의 같은 규칙 진단(go vet·Staticcheck 합성 문서, 계열마다); 경로 종류 표(여러 언어의 시험 이름, 알려진 뿌리, 소스 트리 안의 `test`·`demo`·`gen`·`build`·`fixtures`가 배포 코드로 남는 경우, builder 23의 이전 규칙; Go와 Python이 같은 표 파일을 읽는다); 낮춘 사유의 코드 미포함 내보내기; builder 22 보고서의 `path_class`; 경로 규칙 버전만 다른 두 보고서의 비교 | 계열의 "배포 코드가 아닌 경로" 등급으로 낮춘 finding은 그 등급과 고정 사유 문장을 갖고 배포 코드의 finding은 그대로다. 표에 없는 규칙은 경로와 상관없이 `low`이고 원시 등급은 바뀌지 않는다. 모든 계열의 낮춘 등급은 `info` 이상, 표 등급 이하이고 낮춘 사유는 `Reasons()`에 있다. 관찰은 위치 경로로 정한 `path_class`를 싣고 다른 값, 위치 없는 관찰의 값, builder 22의 값은 보고서가 거부한다. 도구 행은 `<규칙 팩>/<표 버전>+paths.2`(builder 23 보고서는 `+paths.1`)을 적고 경로 규칙 버전만 다른 두 보고서의 같은 도구 finding은 `persisting`이며 차이는 `tool:<analyzer>:severity_table` 하나다. 같은 표·규칙의 심각도 차이는 변경이다(`TestPathClass`, `TestPathClassSharedTable`, `TestLegacyPathClassIsTheEarlierRule`, `TestDefaultSeverityOnPathsThatAreNotShipped`, `TestToolFindingOnATestPathIsLowered`, `TestPathRuleDifferenceIsToolContextNotAFindingChange`, `validate_bundle.py`). | REQ-007,013 |
| TC-302 | 시험 경로 묶음이 진단도 많고 경로도 앞서는 계획(가장 작은 출력 예산: 요청당 묶음 2개, slot 2개)에 배포 코드의 묶음 셋; 같은 규칙이 배포 코드에서는 `high`이고 다른 경로에서는 `low`인 경우와 두 경로 모두 `low`인 경우; 호출 상한 20에서 시험 파일 40개의 진단 120건과 배포 코드 진단 8건을 실제 slot·기록·보고서 생성기로 실행 | 배포 코드의 묶음이 모두 담기고 첫 묶음은 배포 코드다. 담지 못한 묶음은 `diagnostics_outside_triage_slots`로 남는다. 묶음의 `default_severity`는 보고서가 주는 값이다(배포 코드가 아닌 경로는 낮춘 값이고 `path_class`가 붙는다). 모든 배포 코드의 묶음이 그 밖의 경로의 묶음보다 먼저이고 배포 코드 안의 순서는 이전과 같다. 실제 실행에서는 첫 요청이 배포 코드의 묶음으로 시작하고 배포 코드의 진단은 모두 분류되며, slot에 들지 못한 시험 파일 진단은 분류하지 않음과 한계 코드로 남는다. 시험 경로 finding은 낮춘 심각도와 사유를, 배포 코드 finding은 이전 값을 갖고 종합의 `diagnostics_not_shipped`와 접힘의 줄이 관찰의 수와 같다. 이전 순서로는 이 시험이 실패한다(`TestTriagePacksShippedCodeBeforeOtherPaths`, `TestTriageOrderWithinShippedCodeIsUnchanged`, `TestTriageAtATightCeilingClassifiesShippedCodeFirst`). | REQ-010,012,013 |
| TC-303 | 분류 결과별 구간마다 배포 코드가 아닌 경로의 진단이 섞인 도구 진단 접힘(Markdown·HTML·웹), 구간의 수와 종합 수, builder 22 보고서, 좁은 화면(390 px)과 넓은 화면(1280 px)의 긴 값 | "경로별: 시험·예제 등 배포 코드가 아닌 경로 N건, 배포 코드 M건"이 접힘과 진단이 있는 구간마다 한 줄 나오고 수는 관찰의 `path_class`에서 센다. 종합의 `diagnostics_not_shipped`가 같은 수다. builder 22 보고서는 이 줄 없이 그려지고(builder 22의 저장 예시를 이전 checkout과 이 checkout의 렌더러로 그려 Markdown 바이트가 같고 HTML은 내장 CSS의 규칙 하나만 다르다) 웹 파서는 builder 23 이전·위치 없음·알 수 없는 종류의 `path_class`를 거부한다. 보고서 e2e의 가로 넘침 시험이 이 줄을 포함해 두 너비 모두 통과한다(`TestTriageFoldSplitsDiagnosticsByPath`, `TestOverviewCountsDiagnosticsOffShippedCode`, 웹 단위 시험, `report.spec.ts`). | REQ-009,013 |
| TC-307 | 요청의 경로 종류: 시험·예제 경로의 코드·시험 발췌와 배포 코드의 발췌를 보내는 조사·검증 요청; 저장한 기록과 요청을 다시 읽은 발췌 | 배포 코드가 아닌 경로의 발췌에만 `path_class`가 붙고 기록의 근거에는 없다. 요청을 읽어 들인 발췌는 워커가 가진 발췌와 같다(`TestPathClassIsOnlyInRequests`). | REQ-009,010 |
| TC-308 | 시험 코드만 인용한 조사 후보, 시험을 보조 근거로 인용한 배포 코드 후보, 검증이 실패한 실행, 위조한 표지, 배포 코드가 없는 범위(시험만 바꾼 PR, 예제만, 시험 안으로 좁힌 범위), 섞인 범위(scripted caller, 전체와 slice, 기록·보고서 생성기) | 시험 코드만 인용한 후보는 검증 요청에 없고 검증 기록에 `candidate_not_shipped` 하나뿐인 `unreviewed` 판정으로 남으며 task는 실패하지 않는다. 보고서는 그 후보를 `unreviewed`·한계 `candidate_not_shipped`인 발견으로 남겨 예제·시험 경로 항목 접힘(웹·Markdown·HTML, 코드 미포함 내보내기 포함)에 고정 문구로 싣고 `code_review.candidates_not_shipped`와 종합의 수가 같다. 조치 항목·미확정 후보·우선순위·PR 댓글 목록에 들지 않고 모든 후보가 이 경우인 실행의 status와 판정은 후보가 없는 실행과 같다. 배포 코드의 줄도 인용한 후보는 검증된다. 보낼 후보가 없으면 호출 없이 끝난다. 배포 코드 소스가 없는 범위는 후보를 가리지 않는다. 생성기 24 이전 보고서는 이 수를 가질 수 없다(`TestScopeWithoutShippedCodeIsTheSubject`, `TestShippedSourceOfAPullRequestJob`, `TestShippedSourceIn`, `TestNotShippedMatchesHonoursTheScope`, `TestCandidatesAboutNonShippedPathsAreSetAsideWhenVerifyFails`, `TestForgedNotShippedMarkerIsRejected`, `validate_bundle.py`의 표지 검사와 위조 기록 음성 시험, `TestCandidatesAboutNonShippedPathsAreNotVerified`, `TestNothingToVerifyWhenAllCandidatesAreAboutNonShippedPaths`, `TestDryRunUpstreamRaisesACandidateAboutATestPath`, `TestTriageShippedGroupsPrecedeOtherPathsOfHigherSeverity`, `TestReviewExampleRecords`, `TestReportSlicesExample`, 웹 단위 시험, `TestChangedItemsLeaveOutCandidatesNotSentToVerification`, `report.spec.ts`의 가로 넘침 시험). | REQ-010,011,013 |
| TC-309 | 분류 응답에서 판단 불가 답: `missing`이 없거나 틀림, 같은 요청의 Go 단위에 있는 정의를 없다고 씀(복구가 가리킨 발췌를 인용해 답함/복구 뒤에도 같은 답), 요청에 없는 정의, 다른 종류; 가짜 upstream의 같은 모양(`--shape-errors`) | 형식 오류(`triage_missing_invalid`, `triage_unclear_input_present`)는 복구 한 번 뒤 항목에서 처리한다. 복구 요청은 정의를 담은 발췌 ID를 `items`로 가리키고 그 발췌를 싣고, 그 묶음은 그 발췌를 인용할 수 있다. 복구 뒤에도 모순이면 그 답이 `unclear`로 남고 한계 `triage_unclear_definition_in_request`가 붙으며 아무것도 빠지지 않는다. 틀린 형식의 `missing`(문자열 등)은 그 항목의 형식 오류다. 선언이 여러 단위로 나뉜 정의, 빌드 조건이 붙은 파일, 다른 package는 정의로 세지 않는다. `missing`만 틀렸으면 이유만으로 받고 `triage_missing_unstated`를 남긴다(`TestUnclearNamingAPresentDefinitionIsRepairedWithAPointer`, `TestUnclearThatContradictsTheRequestStandsAfterTheRepair`, `TestDefinitionIndexIsExact`, `TestOptionalAnswerFieldsOfTheWrongTypeAreFaultsOfTheEntry`, `TestUnclearNamingAnAbsentDefinitionStands`, `TestDryRunUpstreamTriage`). | REQ-010,011,012 |
| TC-310 | 한 묶음을 부분마다 분류하는 답: 위치가 맞는 두 부분(하나는 문제), 위치가 겹침, 어느 부분도 가리키지 않은 위치; 전체 답과 부분 답이 섞임; 가짜 upstream의 부분 답 | 부분마다 묶음이 하나씩 저장되고(관찰·`cross_tool`·후보가 그 부분의 것) 기록 검사가 통과하며 보고서의 진단 수가 맞는다. 겹침·범위 밖·형식이 틀린 `positions`는 `triage_diagnostics_invalid`이고 복구 뒤에는 유효한 부분이 남고 나머지 위치는 분류하지 않는다. 빠진 위치도 분류하지 않은 묶음(`triage_answer_missing`)이며 부분이 계약의 개수 이하면 유효한 부분을 잃지 않는다. 계약 문자열의 부분 수가 검사하는 수와 같다. 문제 부분 둘의 발견은 서로 다른 지문을 가진다. 이전 형식의 답(`positions` 없음)은 그대로 유효하다(`TestValidPartsKeepWhatIsValid`, `TestContractStatesThePartsLimit`, `TestProblemPartsOfOneGroupHaveDistinctFingerprints`, `TestAGroupAnsweredPartByPartIsStoredAsOneGroupPerPart`, `TestPartPositionsAreValidated`, `TestTriageRequestNamesPathClassAndReportSeverity`, `TestDryRunUpstreamTriage`). | REQ-010,011,012 |
| TC-311 | 쓰지 않은 값 계열의 묶음: 요청의 `rule_families`, 구체적 입력과 결과를 적은 문제 답과 설계상 무해한 답(scripted caller), 가짜 upstream의 같은 계열 묶음 | 묶음의 계열 이름이 표의 계열과 같고 두 답이 복구 없이 받아들여진다. 지침 문구의 효과는 측정하지 않았다(`TestUnusedValueFamilyAnswersAreAccepted`, `TestDryRunUpstreamTriage`). | REQ-010,012 |
| TC-167 | coverage fixture, 여섯 언어 최악 사례, 상한 207 | 수락된 overview에 들어간 줄을 slice에서 반복하지 않고 같은 파일의 나머지 줄을 보낸다. 공통 검토 지침과 범위 산출물의 digest로 slice 기록을 묶으며, 위치 선택 지침은 후보가 있는 overview에만 붙는다. 모든 요청은 요청 바이트 이하이고 적용 언어의 지침을 싣는다. 파일 첫 줄에서 시작하지 않는 단위는 그 파일의 머리와 함께 간다(`TestSlicesContinueWithoutRepeatingOverviewLines`, `TestSliceRequestSizeWorstCaseAllLanguages`). | REQ-009,027 |
| TC-168 | 진행 단계, 실제 worker(상한 15, slice 요청 중 취소), 노드 프로토콜과 PostgreSQL | `no_review_scope`는 단계를 낮추지 않고 `budget_exhausted`는 `code_review`를 `partial`로 남긴다(이전 worker가 남긴 `secret_screen_failed`도 같다). 실제 worker는 15회 이하로 끝나고 보고서가 만들어지며 AI 검토는 수행이다. 취소는 정리를 확인하고 이후 slice 요청이 없다. 서버는 slice key와 queued·running에서의 `budget_exhausted`를 받고, 계획 revision은 1이며, slice 실행 중 취소로 run이 `cancelled`가 된다. | REQ-012,018; NFR-006 |
| TC-169 | 호출 상한 15의 coverage fixture, 구버전 장부, 잘못된 장부·slice 기록, 미선별과 계획 한도 | `examples/report-slices.json`에서 39개 파일의 조사 입력을 요청하지만 호출 14회 뒤 일부 줄이 남는다. 파일별 요청·수락 줄 수와 남은 줄·이유, 모듈과 후보 검증 상태를 대조한다. 구버전은 원래 집계 의미로 읽고, 검증기는 잘못된 참조와 줄 합계를 거부한다. 조회·코드 제외 내보내기에서도 범위를 유지한다(`TestReportSlicesExample`, `TestReportSlicesUnscreened`, `TestReportSlicesNotPlanned`, `TestLedgerValidation`). | REQ-012,013,027 |

TC-167·169는 67개 모듈의 실제 함수 원문이 적격 모듈마다 요청에 포함되고, 후보가 없는 검토에서 후보 검증을 0으로 세는지 확인한다. 12개 모듈의 67개 파일 시험은 파일 끝의 후보와 근거 범위, 한도를 넘은 긴 줄 다음의 소스, 기각된 요청을 다시 배정하는 경우도 확인한다. 합성 응답을 사용하는 전송·집계 시험이다(`TestCoverageVisitsSixtySevenModulesWithoutInventingCandidateVerification`, `TestReportLineCoverageAcrossModules`, `TestCoverageRecordsRejectedRequestAndReschedulesItsLines`).

TC-165·167·169는 확장 범위와 고정한 스냅샷을 재시도하면서 노드의 호출 상한을 207회에서 600회로 바꿔도 같은 작업 키를 쓰는지 확인한다. 일반 한도에서 남은 파일 끝 원문이 후속 조사 96개 이후 수락되고, 일반 계획은 모델 없이도 기존 작업 수 상한을 지킨다(`TestExtendedCoverageReadsPastOrdinarySlotLimitOnRetry`, REQ-018,027, NFR-007). 12개 코드 파일의 기존 일반 계획에 기록된 64쌍의 작업 키는 호출 상한이 더 높은 노드에서도 그대로 재생성한다(`TestOrdinaryCoverageKeepsPinnedRetryTasks`).
| TC-190 | 비밀값 작업이 실행됐지만 결과를 쓸 수 없음(실패 envelope, 탐지 하나를 담은 부분 결과), 두 검토 팩 | 검토를 건너뛰지 않는다. 모델 입력은 기본 읽기 필터만 거치고 기록은 `model_input_not_secret_screened`를 싣는다. 부분 결과가 보고한 파일은 어떤 요청에도 없고(`secret_files_withheld`) 나머지 파일은 보낸다. 보고서(JSON·Markdown·웹)의 모델 입력 선별은 `not_screened`이고 쓸 수 없던 작업과 사유를 적는다(`TestReportSlicesSecretScreenUnusable`, `TestPartialSecretScanStillWithholdsFlaggedFiles`, `internal/report/testdata/report-screen-unusable.json`) | REQ-009,031; NFR-006,008; TC-118,166 |

`TestReportLineCoverageAcrossModules`는 12개 Go 모듈·67개 파일을 상한 22에서 워커와 보고서 생성기로 검증한다. 같은 합성 입력과 미리 정한 응답으로 바꾸기 전은 17회 호출 기록에 43개 파일의 발췌와 파일 41개의 전 줄이 있었고, 24개는 호출 상한 때문에 보내지 못했다. 현재 시험은 19회에 67개 파일의 발췌가 수락되며 그중 66개는 전 줄이 수락된다. 110줄 파일의 끝까지 보내고, 입력 한도를 넘는 한 줄은 해당 줄에 이유를 기록한 뒤 다음 줄을 보낸다. 제출 범위와 보고서 집계를 검증한 합성 시험이며 실제 모델 응답과 결함 탐지 품질은 평가하지 않았다.

검토 쌍의 동시 실행([호출 상한과 승인](REVIEW.md#호출-상한과-승인), A64):

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-312 | 실제 slice·분류·`review.contract` 실행기와 보고서 생성기, 응답을 내용으로만 정하고 시드로 정한 지연 뒤에 답하는 scripted caller. 병렬도 1·2·4·8과 시드 셋을 계획 순서의 순차 실행과 비교한다. 경우는 호출이 넉넉함(상한 99), 중간에 상한 도달(상한 20, 분류 몫 한 쌍), 상한 근처에서 형식 복구·재시도가 다툼, 한 쌍의 timeout, 한 호출의 계수 쓰기 실패, 진행 중 취소, 후보가 없어 호출 없이 끝나는 slice 검증과 분류 slot이 JSON이 아닌 첫 답 뒤 묶음 절반을 다시 묻는 재시도(`TestPairsVerifyWithoutCallsAndTriageRetries`)다. 결정 규칙 단위 시험도 있다(`TestPairs*`, `TestLedgerDecidesInPlanOrder`). task 상한을 1로 낮춰 역할이 상한을 넘는 호출을 하게 만든 실행과, 일정이 있을 때와 없을 때 상한 다음 호출을 확인한다(`TestPairsTaskCallBoundIsEnforced`, `TestTaskCallBoundRefusesTheNextCall`) | task 상태·사유, 검토 기록·범위 산출물·분류 몫 파일, task별 호출 수, 보고서 본문이 순차 실행과 바이트 단위로 같다. 호출 수는 상한 이하이고, 계획 순서에서 각 task 앞의 호출 수도 순차 실행과 같다. 상한 근처에서는 판단이 앞 단위를 기다린다. 한 쌍의 timeout과 계수 실패는 그 task만 끝낸다. 호출 없이 끝난 검증도 그 단위를 끝내 뒤 단위의 판단을 막지 않는다. 취소하면 상한을 넘지 않고, 실패 없이 `cancelled`로 끝나며, 남는 goroutine이 없다. 앞 단위가 끝나지 않았어도 결과가 정해지면 기다리지 않고, 읽을 수 없는 계수는 오류다. 상한을 넘는 호출은 `review_call_cap_exceeded`로 거절되고 계수되지 않는다. 모든 실행에서 작업별 계수가 상한 이하이며, 결과는 병렬도와 관계없이 같다. `-race`로도 실행한다 | REQ-012,013,018; NFR-008,010 |
| TC-313 | 실제 worker와 노드 프로토콜(상한 33), hello의 병렬도 1·4. 프로세스 그룹을 남기는 모의 OpenCode에 slice 조사 요청 네 개가 걸린 상태에서 취소(`TestReviewSlicesRunConcurrentlyThroughTheWorker`, `TestReviewSlicesCancelledInFlightThroughTheWorker`). 병렬도가 없는 hello, relay를 쓰는 hello, 서비스 설정의 relay를 고른 attempt, 범위 밖 값(에이전트 플래그, hello, 서비스 설정)(`TestReviewParallelFromHello`, `TestServiceRelayRunsReviewPairsOneAtATime`). PostgreSQL에서는 한 attempt의 검토 task 16개가 상태를 동시에 바꾸는 중에 run을 취소하는 일을 12번 반복한다(`TestConcurrentReviewTaskStatusAndCancel`) | 병렬도 4에서는 두 요청 이상이 함께 실행되고, 1에서는 하나씩 실행된다. 두 경우의 task 결과가 같고 보고서가 검증된다. 취소하면 attempt는 `cancelled`로 끝나고 정리는 `confirmed`다. 시작된 모든 요청의 프로세스 그룹이 사라지고 상한을 넘지 않는다. 병렬도가 없는 hello, relay를 쓰는 hello, 서비스 설정에서 relay를 고른 attempt는 1로 실행한다. 0·17(플래그)과 -1·17(hello·설정)은 거부한다. PostgreSQL에서는 모든 상태 변경이 커밋되거나, 취소 뒤의 시작처럼 자기 이유로 거절된다. 취소도 커밋된다. attempt는 `cancelled`로 끝나고 열린 task가 남지 않는다. 데이터베이스의 교착 수는 늘지 않는다 | REQ-018,022; NFR-009 |
| TC-314 | 여러 `code_review` task가 함께 실행 중이고 뒤 slice가 먼저 끝난 진행 상태(두 가지 순서), 가짜 upstream의 동시 응답과 시드 지연(`TestConcurrentReviewTasksInCodeReviewStage`, `scripts/test_fake_model_upstream.py`) | 단계는 `running`이다. 끝난 수, 시작 시각(가장 이른 task), 가중 추정은 끝난 순서와 관계없이 같다. 모두 끝나면 하나씩 실행한 것처럼 정리된다. 가짜 upstream은 요청을 함께 처리하고(지연 0.4초 넷이 1.2초 안에 끝남), 동시 처리 최대 수를 센다. 지연은 시드와 검토 입력으로만 정한다 | REQ-016,017,018 |

검토 우선순위와 요약 보기([검토 우선순위](REVIEW.md#검토-우선순위), [내보내기 상세도](REPORT.md#내보내기-상세도), A45):

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-220 | 부차 경로 조각(대소문자, `tests/data`, Go 파일의 `build`·`dist`, 파일 이름), 생성 파일 표지, 도구 진단의 심각도·수, 실패한 필수 분석, 진입점·소스·테스트, 큰 모듈과 작은 모듈, overview 순회 | 주요가 부차보다, 같은 분류 안에서 진단 있는 파일·실패 분석 모듈·진입점·소스·테스트 순이다. 지표·억제 진단은 세지 않는다. slot은 한 분류·한 순위이고 디렉터리를 되도록 나누지 않으며, 큰 모듈이 먼저 더 받되 두 번째 주요 모듈도 받는다. 부차 파일이 없으면 overview 순서와 요청 바이트가 이전과 같다(`TestSecondaryRuleMatch`, `TestGeneratedHeaderAndSeverity`, `TestPackSlotsPriority`, `TestGoReviewInputsUnchanged`) | REQ-009,027; NFR-007 |
| TC-221 | 무작위 3,000개 트리와 생성 파일, 분석기 진단, 상한 96 | 정해진 slot 안에서 각 모듈에 대표 소스와 나머지 파일·줄을 배정한다. 상한 밖의 줄은 `review_slice_limit`를 남긴다. 채우지 않은 slot은 `no_review_scope`다(`TestSlotSlackCoversRunTimeOrder`, `TestReviewSlicesThroughTheWorker`). | REQ-009,027 |
| TC-222 | 범위 산출물 0.2와 0.1: 분류 누락·모르는 분류·부차인데 이유 없음, 주요 뒤에 오지 않은 부차, slot의 분류, 모듈 부차 수 불일치, slot 97, 상한 208, 0.1 산출물 | Go decoder·JSON schema·`validate_bundle.py`가 어긴 산출물을 거부하고 0.1은 그대로 읽는다(`TestScopeArtifactValidation`, `TestScopeArtifactPlannedSchema`, `TestSliceTaskKeys`) | REQ-011,027 |
| TC-223 | builder 8 장부: 예시(`examples/report-slices.json`), 분류 누락·모르는 분류, builder 7의 분류, 주요 파일이 있는 부차 모듈, 보낸 수를 넘는 부차 보냄, 거절 slot 없는 주요 상한 초과 | 일곱 수의 합이 대상 수다. 어긴 장부와 builder 7 리비전의 분류는 거부된다. 부차 파일이 있으면 종합에 주요·부차 수와 먼저 볼 항목 `review_primary_not_sent`, 공백 `review_scope_secondary_not_reached`가 있다. 장부 표는 분류와 부차·예산 밖 열을 보이고 행의 합이 맞는다(`TestLedgerValidation`, `TestLedgerRowsShowSecondaryFiles`) | REQ-012,027 |
| TC-224 | 요약 보기와 `detail`: 세 예시 보고서, 코드 미포함 projection, `detail=summary·full·all`, JSON의 `detail`, 실제 자체 분석 보고서(`CODEYE_REAL_REPORT`, 수동) | 종합 다음에 환경·문서·정적 분석·지표·비밀정보·코드 분석·공백이 이어지고 형식 오류(`%!`)가 없으며, 모든 표의 행 너비가 머리와 같고, HTML의 문서 안 링크마다 대상이 있다. 규칙 묶음의 수와 위치·"외 N곳"의 합이 진단 수다. 코드 미포함이면 도구 원문이 없다. 전체 보기는 요약에서 접은 진단과 발견의 개별 행을 표시한다. API는 `detail=all`을 400으로 거부하고 JSON은 바뀌지 않으며 전체 내보내기 파일 이름에 `-full`이 붙는다. 종합의 제품 코드 근거와 검토 범위를 같은 실행의 상세에서 확인한다(`TestSummaryLayout`, `TestSummaryStaticGroupsAddUp`, `TestSummaryCodeFreeKeepsMessagesOut`, `TestReportAPIFormatsAndACL`, `TestRealReportRenders`) | REQ-012; NFR-006 |
| TC-225 | 웹 보고서 패널: 예시 보고서, slice 예시, 범위 산출물 예시의 부차 규칙 | 절이 가치 순서이고 종합 문장이 있다. 규칙 묶음의 합이 진단 수이며 위치는 5곳과 나머지 수다. slot은 상태 구간으로, 공백은 묶음으로 접히고 하나도 잃지 않는다. 전체 목록은 `summary`로 열 때만 그려진다. 화면의 부차 규칙이 범위 산출물 예시의 규칙과 같다. 호출 안내는 예시 장부에서 상한 20(10 + 2 × 2 + 6)을 말하고 207을 넘으면 최댓값으로도 모자란다고 말한다. 내보내기에 `detail=full` 링크가 있다(`web/src/reportGroups.test.tsx`, `web/src/ReportSections.test.tsx`, `web/src/ReportPanel.test.tsx`) | REQ-012; NFR-008 |
| TC-226 | 이 저장소 파일럿을 가짜 upstream으로 상한 45·99에서 변경 전후 실행, 회귀 집합 dry run(수동, CI 밖) | 회귀 집합의 요청 dump가 변경 전과 바이트 단위로 같다. 파일럿은 주요 파일을 먼저 보내고 fixtures 모듈에는 slot을 쓰지 않는다. 결과는 [변경 이력](../CHANGELOG.md)에 있다. 경로 검증이며 검토 품질의 근거가 아니다 | REQ-009,027; NFR-007 |

역할·입력·복구 규칙은 [모델 검토](REVIEW.md)에 있다.

모델 설정([모델 검토](REVIEW.md#모델-설정)):

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-182 | 모델 ID(`openai/…`, `vendor/model` 중첩, 공백·`..`·대문자 provider), relay 프로필, 키 파일 provider, 키 파일 형식(CODEYE·OpenCode `auth.json`의 api·oauth 항목, 다른 provider, 권한 0644, 여러 줄 키, 모르는 필드, 없는 파일), 호출 사이에 바뀐 키 | `PROVIDER/MODEL`만 받고 `codeye-gateway/*`는 relay에만 쓴다. relay의 `OPENCODE_CONFIG_CONTENT`는 바이트가 이전과 같다. 키는 호출마다 다시 읽고, 쓸 수 없는 키 파일이면 요청 없이 `unsupported`(`model_credential_unavailable`)다. 키는 프로필에만 있고 기록된 명령·표준 출력에서 가려진다(`TestProviderKeyOnlyInProfile`, `TestCredentialReadAtCallTime`). | REQ-026,030; NFR-006 |
| TC-183 | `codeye start`의 모델: 키 환경변수(`OPENAI_API_KEY` 등) 하나, 같은 키 재기록, 상태 키 파일·`agent.model.credential_file`·OpenCode `auth.json`의 순서, 이름 없는 설정·이름만 있는 설정·OpenCode 설정의 `model`, 키 없는 provider, OpenCode 없음, `model_review: false`인 root | 키 파일은 0600(디렉터리 0700)이고 같은 키면 다시 쓰지 않는다. 기본 모델은 `openai/gpt-6.2-sol`이다. 에이전트 인자에는 키 파일 경로만 있고, `model_review: false`가 아닌 root만 `--model-source-root`를 받는다. 모델을 정하지 못하면 모델 없이 시작한다. probe 기록에 키가 없다(`TestModelSecretFromEnvironment`, `TestModelResolutionOrder`, `TestInstanceModelFromSecret`). | REQ-026; NFR-006 |
| TC-184 | 실제 OpenCode와 가짜 upstream(OpenAI provider에 `base_url`만 바꿈)으로 `codeye start` → `codeye model probe` → 로컬 Go 프로젝트 run. 같은 키 파일을 401 upstream으로 바꾼 run, `--version`만 1.99.0인 wrapper로 재시작한 run(`scripts/test_postgres.py --instance-model`) | probe가 provider·모델·버전과 성공을 출력하고 검토 단계가 수락돼 `ai_review: performed`다. 401이면 probe가 `FAILED at request: model_auth_failed`이고 run은 정적 분석을 마치며 보고서(JSON·Markdown)가 미수행과 사유를 적는다. wrapper run은 검토를 수행하고 한계 `opencode_version_untested`를 싣는다. 키가 키 파일 밖의 상태 파일·로그·산출물·보고서·DB dump·CLI 출력·모든 프로세스 argv에 없다. 가짜 upstream 결과는 경로 검증이며 검토 품질의 근거가 아니다. | REQ-026; NFR-006,008 |
| TC-272 | 관리형 서비스의 OpenCode 웹 설정. 관리자·일반 계정, JSONC와 인증 파일, 모델 미설정·설정 변경 후 새 분석 | 관리자만 저장·연결 확인 가능. 저장은 모델 호출 없이 끝나고 연결 확인과 새 분석에 선택한 모델과 바이너리가 쓰인다. 실행 중인 서비스를 재시작할 필요가 없으며 키 값은 응답·보고서에 없다. | REQ-026,030 |
| TC-185 | `POST /projects`의 `local_path`(symlink·끝 `/`, 가장 긴 root, root 자체, root 밖, 없는 폴더, 상대 경로, `:`가 든 이름, 상태 디렉터리를 품은 홈과 그 안의 폴더), root와 상대 경로로 같은 데이터 폴더, 같은 폴더·저장소(대소문자·`.git`) 재등록, 브랜치 없는 Git, `analysis` 누락 필드, 상태 디렉터리 없는 controller의 `local_path`, `GET /setup`(기록 전후, 관리자·다른 사용자), `PUT /projects/{id}/analysis` | 경로는 root·상대 경로로 바뀐다. 오류는 `FOLDER_NOT_READABLE`·`FOLDER_NOT_FOUND`·`FOLDER_NAME_UNSUPPORTED`·`FOLDER_HOLDS_CODEYE_DATA`(어느 형식이든, 메시지에 두 폴더와 하위 폴더 안내)이고 `FOLDER_NOT_READABLE`은 `details.readable_folders`를 싣는다. 같은 소스는 `open_existing`일 때 200과 같은 프로젝트다. 분석 기본값은 로컬 둘 다 켬, Git 빌드 스크립트 끔이다. 설정 변경은 새 revision이고 소스를 유지한다. setup에 키가 없다(`TestFolderPathsAndSetup`, 격리 시험의 `projects.analysis.put`·`setup.get` 행) | REQ-001,004,015; NFR-006 |
| TC-186 | `POST /runs/{id}/retry`: 노드가 스냅샷을 가진 failed run, 스냅샷 없는 cancelled run, 끝나지 않은 run, viewer, 분석 선택을 바꾼 뒤의 failed run. 노드: 데이터 폴더를 품은 root 상대 경로, 기본 브랜치 해석, 인증·연결 실패 분류, 프로젝트 분석 선택을 따르는 노드와 따르지 않는 노드 | 같은 run(`same_run`)·새 run(`new_run`, `Location`)·409 `RUN_NOT_FINISHED`·403이다. 설정이 바뀐 뒤에는 새 run이다. 노드는 `source_holds_codeye_data`로 확보하지 않고, 빈 브랜치는 원격 HEAD 브랜치를 manifest에 남기며, 인증 요구는 `source_auth_required`, 연결 실패는 `source_unreachable`이다. 정책 노드는 job의 선택만, 그 밖의 노드는 root 허가만 쓴다(`TestRetryRunRoute`, `TestRetryAfterAnalysisChange`, `TestCheckProtected`, `TestGitEmptyBranchUsesTheDefaultBranch`, `TestGitFailureClassification`, `TestProjectAnalysisPolicyFollowsTheProject`) | REQ-005,015,035 |
| TC-187 | 관리형 `codeye up`(시험 빌드, `CODEYE_TEST_GIT_MIRROR`의 file:// 미러): 홈 아래 폴더, `https://github.com/o/r`(브랜치 없음), 홈 폴더 자체의 등록(`scripts/test_postgres.py --instance`) | 폴더는 root `home` 아래로 등록되고 Git 주소는 기본 브랜치의 커밋으로 고정된 보고서(go vet 발견 포함)를 낸다. 두 프로젝트의 분석 선택이 기본값이다. 홈 폴더는 422 `FOLDER_HOLDS_CODEYE_DATA`이고 메시지가 그 폴더를 적는다. `GET /setup`이 git과 도구를 보인다 | REQ-001,004,015 |
| TC-188 | TC-187 설치에서 Chromium(`web/e2e/start.spec.ts`): 도구 화면, 폴더 경로 입력 → 분석, Git 주소 입력 → 분석, cargo가 있으면 Rust crate가 든 저장소의 "켜고 다시 시도", 없는 저장소 → 실패 → 다시 시도. 웹 단위 시험(`StartScreen.test.tsx`, `quickStart.test.ts`) | 한 번 로그인한 화면에서 입력과 분석 버튼만으로 실행 화면으로 가고 게시된 보고서가 저절로 열린다. "더 나은 결과를 얻으려면"에 모델 키 안내, 빌드 스크립트 안내와 버튼이 있고 누르면 선택이 켜지고 새 실행의 보고서에서 그 안내가 사라진다. 실패 실행은 이유와 다시 시도 버튼을 보이고 다시 시도가 새 실행을 따른다. 도구 화면은 찾은 도구 표·모델·Git·폴더를 보인다. 실제 GitHub 저장소로 같은 화면을 확인한 것은 수동 결과다 | REQ-001,013,015; NFR-006 |

## 보고서·화면

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-13 | 원시 도구 수와 finding 수가 다름 | 요약/상세/export 통계 일치, 두 숫자 구분 | REQ-012,013 |
| TC-15 | 근거 코드에 HTML·secret·외부 이미지 | escape/redact, 외부 fetch 없음, export 정책 적용 | REQ-012,013,031; NFR-006,007 |
| TC-80 | 도구 관찰, task별 실행·stdout | 스냅샷 근거와 hash 바인딩 | REQ-008,011; TC-08 |
| TC-81 | 필수 도구·AI 결과가 누락된 보고서 | 상태·품질 판정이 누락을 반영 | REQ-012,013; TC-12 |
| TC-82 | JSON·Markdown·HTML 내보내기 | 집계 일치와 결정적인 본문 | REQ-013,031; TC-13 |
| TC-83 | 실DB·서버·제품 워커 게시 | fencing·재시작·거부·취소에서 올바른 게시 | REQ-022,035; NFR-003 |
| TC-84 | 보고서 조회 | ACL·revision·코드 미포함 기본값과 export escaping | REQ-031; TC-02 |
| TC-90 | 같은 run·attempt·snapshot에 속하는 세 역할 | 세 역할을 연결하고 다른 후보나 선행 호출의 검증 결과를 배제 | REQ-009,010,011 |
| TC-91 | 반증된 후보 | 조건·위치·반대 근거를 유지하고, 반증과 미검증 후보를 조치 대상 통계에서 제외 | REQ-010,011,013 |
| TC-92 | 요약에서 영역·개별 근거 열기 | 같은 revision의 JSON·Markdown·HTML 내려받기 | REQ-012,013 |
| TC-95 | 게시된 리비전을 실제 Chromium에서 열기 | 요약·영역·근거 표시, 세 가지 형식의 내보내기와 수치 대조 | REQ-003,009,010,011,012,013,026; NFR-007,008 |
| TC-98 | 두 프로젝트의 서로 다른 단계, 한 프로젝트의 복수 run, 실행 이력이 없는 프로젝트 | 목록과 상세가 같은 대표 run·단계·상태를 표시한다. 병렬 단계, 대기 사유와 과거 보고서가 구별된다. 권한 없는 프로젝트는 노출되지 않는다. 텍스트만으로도 상태를 식별할 수 있다. | REQ-002,016; NFR-005 |
| TC-130 | builder 5 보고서의 분석 종류별 절: 관찰을 빼거나 두 절에 넣음, 발견을 정적 분석과 코드 분석 양쪽에 넣거나 뺌, 지어낸 지표 최댓값·절 수·공백 수·종합 블록, 블록 일부만 있음, 모르는 단계 상태 | 모든 관찰이 정적 분석·지표·비밀정보 중 한 곳에, 모든 발견이 정적 분석·코드 분석 중 한 곳에 한 번 들어간다. 두 절의 발견 집계 합이 `summary`와 같다. 어긋나면 `ValidateReport`가 저장·조회 모두에서 거부하고, `validate_bundle`과 웹 해석기도 거부한다. 단계 상태는 진행 화면의 단계 상태이며 단계 기록이 없으면 `not_recorded`다. | REQ-012,013 |
| TC-131 | 절 도입 전후의 같은 사실, builder 4·12와 현재 builder의 비교, builder 1 예시(`report-go-vet.json`) | 기존 필드(`observations`, `findings`, `coverage`, `summary`, `tasks`, `limitations`)가 바이트 단위로 같다(절 도입 때 report 패키지 시험 입력 84개의 builder 4 출력과 한 번 대조했다. 상시 시험은 아래 golden과 비교다). builder 4·12의 결과를 현재 결과와 비교하면 버전 차이를 기록하고 같은 발견의 비교 결과를 유지한다. builder 12에서 저장한 후속 대조는 기존 근거 ID와 함께 읽는다. builder 1 예시는 그대로 검증되고 Markdown·HTML이 이전 출력과 바이트 단위로 같으며 웹은 평면 배치로 보인다. | REQ-012,028; TC-82 |
| TC-132 | 문서 계약·충돌·질문과 환경 메모에 코드 같은 문장과 비밀값 같은 문장을 심은 기록, 코드 미포함 projection | projection은 모델 문장을 생략 문구로 바꾸고 버전 제약이 아닌 요구 문자열을 지운다. 코드 미포함 보고서에 문장이 남거나, 어느 보고서든 비밀값 같은 문장이 있으면 검증이 거부한다. 수락된 역할 기록의 문장만 오고 절 근거 목록에 없는 근거 ID는 거부한다. | REQ-012,013,031; NFR-006 |
| TC-133 | 모델 검토 없는 실행(정책 생략·미계획)과 실행하지 않은 분석기 | 환경·문서·코드 분석 절의 모델 상태가 `not_performed`와 사유 코드이고 모델 문장이 없다. 실행하지 않은 도구 행은 "실행하지 않음"으로 보이며 어느 형식에서도 0건으로 읽히지 않는다. 판정과 상태 규칙은 바뀌지 않는다. | REQ-012,013; TC-81 |
| TC-134 | 예시 보고서와 실제 게시 보고서의 세 형식과 웹 화면(실제 Chromium 포함) | 종합이 먼저 오고 분석 종류별 절이 순서대로 하나씩 있다. 절별 수가 JSON 종합 블록, Markdown 표, HTML `data-count`, 웹 화면에서 같다. 웹 절은 키보드로 열 수 있고 종합의 버튼이 해당 절을 연다. 상태는 글자와 기호로 보인다. 모델 문장에는 "모델 해석" 표시가 붙는다. | REQ-012,013; NFR-005; TC-95 |
| TC-135 | 근거 있는 문서 품질·코드 강점·문서 계약 대조, 스냅샷 줄 발췌, 발췌에 해당하는 비밀값 파일, 코드 미포함 projection | 문서 대조는 적용 범위와 문서 근거에 묶이고 일치·부분 일치·불일치에는 구현 근거가 있다. 근거 없는 계약은 미확인이다. 저장한 발췌는 스냅샷 원문과 일치하고 자격증명 패턴이나 탐지 경로의 원문을 담지 않는다. 코드 미포함 보고서는 발췌를 싣지 않는다. 문서에 등장하는 기능을 구현한 늦은 줄과 관련 테스트를 선택해도 인용 위치, 검토 장부, 소스 비공개 처리를 유지한다. | REQ-005,009,011,012,027,031; TC-15 |

TC-135는 문서 인용 개수 초과에 대한 필드별 형식 복구와 조사 범위가 8개 파일을 넘을 때도 뒤쪽 계약의 메서드 본문·관련 테스트 단언을 함께 보내는 경우를 포함한다(`TestDocumentEvidenceRepairReportsTheInvalidField`, `TestDocumentedMemberEffectsReachOverview`).

TC-135는 인용한 예제와 수락한 계약에 정확히 나온 Go 공개 이름을 체크인된 생성 래퍼와 같은 패키지의 수기 구현에 연결한다. 늦은 반환 분기와 관련 테스트가 전체 조사에 나오고 발췌 한도와 `generated_file` 분류를 지키며 장부는 열린 고유 파일을 한 번 센다. 불일치한 이름, 메서드·지역 변수, 인자 변형, 다른 패키지, 중복 정의, 생성된 구현, 비밀값 선별·범위 제외·부차 디렉터리·PR에는 이 호출 연결을 추가하지 않는다(`TestDocumentedGoWrappersReachImplementationAndKeepScopeLedger`, `TestDocumentedGoWrappersRequireExactLocalUnambiguousSource`).

TC-135는 문서와 계약에서 같은 Go CLI 명령을 찾아 QA 스크립트와 시험 입력이 있어도 `cmd`의 실제 인자 전달·분기·호출 대상과 관련 테스트를 고르는지 확인한다. 문서의 다른 인자, 다른 인자 위치, 모호한 분기·패키지, 생성 파일, 보류 파일, 제한된 범위와 PR에서는 이 연결을 만들지 않는다(`TestDocumentedGoCommandPrefersRuntimeRoute`, `TestDocumentedGoCommandRejectsUnverifiedRoutes`). TC-134는 웹과 Markdown·HTML 요약에서 예제와 시험에서만 찾은 항목의 위치와 검토 상태를 유지하고 해당 발견에 출처를 표시하는 경우를 포함한다(`TestSummaryExportsLabelExampleAndTestFindings`, `reportGroups.test.tsx`).

TC-135는 `cmd/<명령>/README`에 인용된 단독·옵션 명령에서 `main`과 여러 번 호출한 한 구현의 전체 줄을 고르는지도 검사한다. 다른 위치의 README, 비슷한 이름, 여러 구현 또는 함수값을 통한 호출도 확인한다(`TestDocumentedStandaloneGoCommandOffersWholeMainAndUniqueCallee`, `TestDocumentedStandaloneGoCommandRequiresTheLocalGuideAndDirectCallee`).

TC-135는 계약에 연결해 선택한 짧은 구현이 앞쪽의 반환 분기까지 포함할 때 넓어진 근거 ID로 대조를 수락하는지도 확인한다. 다른 후보와 테스트를 함께 보내고, 같은 파일의 다른 창이나 직접 호출 연결이 있거나 전체 코드·요청 한도를 넘으면 기존 발췌를 보낸다(`TestOverviewIncludesCompleteDocumentedShortRuntime`, `TestOverviewKeepsExactWitnessesWhenWideningWouldDisplaceInput`).

TC-135·TC-287의 문서 계약 대조 시험은 거부 조건마다 고정 코드가 `output_errors`에, 대조 번호·필드와 함께 복구 요청에 남는지 확인한다. 형식 복구 뒤에도 틀린 대조는 그 대조만 빠지고 후보·강점·다른 대조는 남는다. 보고서는 그 계약을 미확인으로, 문서 절의 분석 공백을 `contract_comparison_omitted`로 보인다. 복구 전의 거부, 복구를 후보 오류에 쓴 응답, 복구 뒤에도 범위가 없는 후보(그 후보만 빠지고 판정은 보류), 가장 작은 요청에 맞춘 복구 목록을 함께 시험한다. Javadoc·docstring·rustdoc로 문서화된 API는 문서를 함께 담은 파일 단위 안에서 본문 줄 범위를 인용해 첫 호출에 수락된다. 기록과 코드 포함·미포함 보고서는 그 범위를 가리킨다. 같은 단위의 두 범위, 범위를 인용한 대조를 받은 verify와 계약 후속 검토도 확인한다. dry run 가짜 upstream의 문서 계약과 대조 응답이 같은 검사를 통과하고 범위 없는 긴 단위 인용이 그 대조만 빠지는지도 확인한다(`TestComparisonRejectionCodes`, `TestComparisonSliceAndAPIBodyCodes`, `TestComparisonOfInSourceDocumentKeepsCitedLines`, `TestComparisonTwoRangesOfOneUnit`, `TestComparisonOmittedOnlyOnceTheRepairIsSpent`, `TestComparisonRepairFitsTheSmallestRequest`, `TestVerifyDeclarationThatIsAComparisonRange`, `TestContractFollowupReadsARangedPriorComparison`, `TestDryRunUpstreamComparisons`).

TC-135는 문서에서 이름을 확인한 Go 쿼리 옵션의 분기, 같은 값을 넘긴 호출, 다른 패키지에 있는 결과 변경과 반환, JSON 태그를 같은 조사 요청에서 확인한다. 서로 반대인 구현에 대한 합성 응답의 일치·불일치를 보존한다. 범위 밖 모듈 정보, 모호한 구현과 가려지거나 재대입된 값은 직접 호출 근거를 만들지 않는다(`TestDocumentedGoQueryOptionReachesDecisiveProjection`, `TestDocumentedGoQueryOptionRequiresUnambiguousScopedBindings`). 직렬화한 값을 그대로 응답에 쓰는 줄을 요청에 넣고, 변경·교체·폐기한 값의 전달은 연결하지 않는다(`TestDocumentedGoQueryOptionRequiresProjectedDelivery`). TC-268은 수락된 모듈 기록에서 일반 발췌 80개 뒤의 선택 경로를 먼저 다시 읽어 코드와 산출물 digest를 보존하는지 확인한다(`TestContractFollowupPrioritizesSelectedLateSliceBeforeOrdinaryReadLimit`).

TC-135는 수락한 계약과 지역 안내가 같은 Python 공개 함수 호출을 인용할 때 기존 구현·테스트와 함께 여러 줄 기본값 및 반환 타입의 실제 선언을 보내는지 확인한다. 파일·줄·바이트 한도와 스냅샷 근거 ID를 보존한다. 문서에 호출이 없거나 중복·가려진 이름·동적 바인딩·지역 함수·보류·범위 밖·PR 비변경·테스트 경로이면 추가 발췌가 생기지 않는다(`TestDocumentedPythonCallSuppliesCompleteScopedSignature`, `TestPythonSignatureRejectsAmbiguousOrNonRuntimeText`, `TestPythonSignatureKeepsReviewScopeAndCitation`).

TC-135는 기본값이 서로 다른 두 Go 설정에 실제 생략 판단을 보내고 구조체 선언, 주석, 유효성 검사와 부차 경로의 유사 예제를 기본값 근거로 고르지 않는지 확인한다(`TestDocumentedGoDefaultsReadActualDecisions`). TC-167의 이후 줄 검토에서는 package 선언이 없는 발췌를 인용해도 스냅샷에서 실제 함수를 찾아 호출자와 테스트를 verify 요청에 싣는지 확인한다(`TestVerifyLateGoSliceReceivesCallersAndTests`).

TC-135의 C 함수군 시험은 인용된 안내에 코드로 표시한 접미사와 실제 C 호출 예시가 있을 때 유일한 기본 함수와 변형 함수, 둘이 공유하는 내부 함수의 조건 분기를 함께 보낸다. 선언·주석만 있는 예시, 함수 형태·호출 대상 불일치, 모호한 정의, 같은 값·변경된 인자, 조건부 구현, 발췌 한도 초과, 보류·PR 미변경 구현은 기존 코드 선택을 사용한다(`TestDocumentedCApiFamilySuppliesBothWrappersAndModeBranch`).

TC-92·TC-98의 공유 실행 링크 시험은 게시 보고서를 불러온 뒤 제목과 종합 시작으로 한 번 이동하는지 확인한다. 일반 프로젝트 선택, 실행 중 상태, 조회 오류, 늦은 응답 전에 사용자가 고른 화면이나 양식 초점, 숨겨진 브라우저 탭, URL fragment 보존을 함께 검사한다(`ProjectsView.test.tsx`).
모델 요청의 멤버 연결은 두 발췌의 정확한 ID를 담는다. 요청 축소로 한 발췌가 빠지거나 계약 범위가 한쪽을 제외하면 연결을 생략한다. 연결을 받은 모델이 미확인으로 응답하면 기록도 미확인이다(`TestMemberTestHintFollowsRequestTrimming`, `TestMemberTestHintSelectionRespectsContractScope`).

TC-135에서 1~24줄을 인용한 모델 발췌를 열면 13줄의 입력 처리와 22줄의 응답을 포함해 고정 스냅샷의 24줄을 읽을 수 있다. 바이트 또는 행 상한에 닿으면 실제 마지막 줄을 표시한다. 같은 시작 줄의 인용 두 건에 각각의 끝 줄을 적용하고 도구 진단의 9줄 문맥과 이전 발췌를 읽을 수 있어야 한다. 비밀값이 인용 범위 밖에 있는 파일과 코드 미포함 내보내기에는 원문을 싣지 않는다.
긴 사용 안내의 후반 발췌를 여러 API 제목에서 나눌 때 원래 줄 범위·바이트와 다른 문서의 첫 발췌를 보존하며, 해당 제목의 개별 ID를 인용한 보고서가 같은 위치를 가리키는지도 확인한다(`TestDocumentSectionSplitsPreserveRangeAndBudget`, `TestDocumentContinuationReachesReport`).

TC-134·TC-135에서 종합의 같은 파일과 주제에 속하는 확인된 동작과 후보는 각자의 줄 범위로 이동한다. 후보는 기록된 확인 방법을 보이고 발견을 확정한 상태로 바꾸지 않는다. 같은 호출과 정확히 같은 코드 줄을 인용한 일치 대조에서만 이름 있는 옵션의 조건을 함께 보인다. Git URL과 긴 NFS 경로가 표지에서 빠짐없이 보이며 사본 경로는 나타나지 않는다. 분석 대상 경로, 종합의 상태, 상세 근거의 파일·줄 범위와 발췌 조작은 390px·1280px Chromium에서도 확인한다. 코드 미포함 상태에서는 서술 생략을 밝히고 구조화된 상태와 위치를 유지한다.

TC-134의 웹 보고서 검색은 분리된 한글 자모와 영문 기호·규칙·경로를 찾고, 다른 탭이나 접힌 목록에 있는 원래 대조·발견의 제목에 초점을 둔다. 현재 판정이나 목록 필터에 가려진 발견을 선택하면 그 항목을 펼치고 필터와 판정을 유지한다. 코드 미포함·이전 배치에서도 위치와 규칙을 찾고 숨긴 원문, 도구 원시 메시지, 이전 실행의 비교 자료와 내부 ID는 결과에 나오지 않는다. 검색 결과 없음과 Esc 초점 복귀, 리비전 전환, 390px의 검색 입력과 결과 너비를 확인한다(`ReportSearch.test.tsx`, REQ-012·031, NFR-005).

TC-134의 프로젝트 사용 흐름은 네 개의 미확인 설명 뒤에 저장된 반환식이 있는 다섯 번째 대조를 앞에 표시한다. 일반적인 사용법 제목이 없는 README에서 동작을 설명한 경우도 포함한다. 가져오기만 인용한 항목, 배포 설명, 동률의 저장 순서, 불일치·부분 일치·일치의 우선순위, 우선 판단과 중복을 뺀 뒤 네 건을 선택하는지 확인한다. 짧은 인용이 일부일 때 같은 웹·HTML 카드에서 저장된 발췌를 한 번에 펼치고 범위가 같으면 중복을 생략한다. 다시 렌더링해도 순서가 같으며 저장된 대조와 코드 미포함 집계·위치는 그대로다(`TestProjectBehaviorsRankStoredComparisonsBeforeTakingFour`, `TestProjectBehaviorsRetainDocumentAndConditionalResponse`, `reportBehaviors.test.tsx`).

미확인 대조를 종합의 첫 판단으로 보여 줄 때 같은 우선순위에서는 저장된 문서 질문과 적용 범위·문서 인용·구체어가 모두 일치하고 후보가 하나일 때 그 대조를 먼저 보여 준다. 모호한 질문, 문장 없는 이전 보고서 및 코드 미포함에서는 저장 순서를 유지한다. 선택된 미확인 대조는 설명서와 코드 두 행에 실제 동작과 기존 이유 또는 추가 관찰을 표시한다. 웹·HTML·Markdown에서 링크가 같은 대조에 연결되며 코드 인용과 상세의 상태·집계를 보존한다. 짝을 이룬 백틱 안의 내용은 인라인 코드로 보이고 짝 없는 백틱과 태그처럼 보이는 문장은 평문으로 표시된다. 표지에서는 각각의 라벨로 시작·종료 시각과 작성 시각 대체 표시를 확인하고, 실제 고정 커밋과 시작·종료를 기록한 경우의 소요 시간만 표시되는지 확인한다.

TC-134·TC-135에서 제품 문서 대조가 모두 미확인이고 주요 구현 근거가 있으면 코드에서 확인한 동작과 적용 범위를 먼저 보여 준다. 상태 수는 시험 범위를 포함한 저장된 대조 전체에서 세며 구현 대조 미확인을 구체적으로 표시한다. 설명서 예시와 선두 강점을 고를 때는 제품 범위를 확인한다. 코드 미포함 내보내기에는 상태 수와 이동 위치가 남고 설명서 예시와 코드 서술은 생략된다(`TestProjectOpeningLeadsWithProductEvidenceWhenComparisonsAreUnknown`, `ReportSections.test.tsx`).

TC-134에서 지지·판단 보류 판정의 종합에 첫 서술형 한계를 표시한다. 지지된 발견은 한계 다음에 저장된 권고를 조건부 조치로 표시하고, 판단 보류 발견은 확인 방법을 표시한다. 사유 코드만 있는 경우 기존 판단을 유지한다. 게시한 판정과 집계를 보존하고 코드 미포함 내보내기에서 한계 서술을 생략한다(`TestProjectDecisionShowsRecordedLimitationBeforeAction`, `ReportSections.test.tsx`).

TC-134에서 종합의 강점 제목·효과를 함께 읽을 수 있는지 확인한다. 효과가 제목을 반복하면 한 번만 표시한다. 관련 강점의 공통 코드 범위와 미확인 조건이 함께 있는 경우의 별도 인용 두 건을 확인한다(`TestStrengthOpeningKeepsSharedEvidenceWithoutRepeatingItsTitle`, `TestRelatedConfirmedControlAndPendingBehaviorHaveSeparateRanges`, `ReportSections.test.tsx`).

TC-134·TC-135는 종합의 선택된 대조에 정확한 호출·입력·설정·반환값이 연결될 때 같은 저장 인용에서 읽은 설명서 예시 하나를 웹·HTML·Markdown에서 표시하는지 확인한다. 다른 호출, 옵션 누락과 중복, 서로 다른 제목이나 코드 블록 사이에 떨어진 결과, 서로 모순되는 결과, 잘린 발췌, 가린 값은 표시하지 않는다. HTML 태그처럼 보이는 값은 코드 글자로 읽히고 코드 미포함 보고서에는 예시가 없다. 웹 집계의 "도구가 보고한 항목"과 "소스 위치를 확인하지 못한 항목"은 게시된 수를 쓴다.

HTML 내보내기에서 발견 카드의 긴 발췌를 열어도 390px·1280px에서 페이지 너비가 늘어나지 않아야 한다. 좌우로 스크롤해 발췌의 마지막 글자를 읽을 수 있는지 확인한다(`web/e2e/report.spec.ts`).

TC-269는 한 미확인 문서 계약의 추가 대조 기록을 게시할 때 적용한다. 같은 스냅샷·시도·문서·범위와 수락된 선행 기록에 새로운 구현 근거가 연결된 경우에만 판정과 이유를 갱신한다. 응답 실패·예산 소진·정책 제외·선행 불일치·테스트만 인용·구버전 기록은 원래 판정을 유지한다. 새 구현 근거를 인용한 미확인 대조는 원래 이유·근거·상태·집계를 유지하면서 추가 관찰을 표시한다. 같은 파일에서 이전 호출부와 새 구현을 함께 인용한 응답은 두 위치를 순서대로 표시한다. 처음 조사에서 읽은 원문이나 위치 후보만 인용하거나 테스트에만 새 줄이 있는 경우 추가 관찰을 싣지 않는다. 실제 보낸 요청·고유 파일은 통계에 남고 테스트 실행은 미실행으로 남는다. 웹·Markdown·HTML에서 동작과 연결된 문서·코드 근거, 같은 호출과 줄로 입증한 별도 설정 문서, 독립적인 중대한 조치 한 건을 확인한다. 코드 미포함 상태에서 구조화된 판정·위치만 남는지, 390px·1280px Chromium에서 종합에 가로 넘침이 없는지도 확인한다. REQ-010,011,012,013,027,031과 연결한다.

보고서 계약은 [보고서와 화면](REPORT.md)에 있다.

TC-134·TC-135의 문서 대조에서 같은 동작의 조회·변경 처리기와 테스트 단언을 함께 읽은 경우, 해당 위치를 종합과 대조 상세에서 연다. 웹·Markdown·HTML에서 문서 대조의 판정과 이유, 테스트 실행 상태는 기록된 값을 쓰며, 코드 미포함 내보내기는 세 위치와 용도를 남긴다. 구버전, 수락되지 않은 조사, 누락되거나 종류가 다른 근거는 해당 위치를 게시할 수 없다. `TestRelatedWorkflowReportKeepsFindingsAndTestStatus`와 `reportBehaviors.test.tsx`에서 확인한다.

TC-134·TC-135는 미확인 동작 여럿과 문서 품질이 같은 파일의 동일·포함 발췌를 인용하는 경우를 포함한다. 문서별로 한 원문 발췌를 열고, 줄 위치와 여러 파일을 함께 인용한 동작의 상세에 이동한다. 코드 미포함 내보내기는 위치·판정·근거 종류를 유지한다. 코드 분석에서 근거로 지지하거나 재현한 항목의 심각도 순서, 판단 보류와 기각 상태, 모듈 단위의 접힌 구현 관찰을 확인한다.

## 이력·비교

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-14 | 이전 finding이 이번 도구 실패로 안 보임 | resolved가 아니라 absent_unverified | REQ-028,029 |
| TC-104 | 각 분석기 팩. 이전 run에 있던 도구 finding이 사라짐: 규칙 선택·규칙 팩 버전·저장소 설정 파일 변경, 규칙 문맥이 없는 이전 builder 보고서, 소스 안 억제 주석 추가, 억제를 보고하지 않는 도구 | 양쪽 규칙 문맥이 알려지고 같을 때만 `resolved_observed`다. 그 밖은 `absent_unverified`와 `rule_context_unknown`·`rule_context_changed`·`suppressed_in_source`·`source_suppressions_hidden`이다. 억제된 진단은 관찰로 남고 finding이 아니다. go vet 문맥은 합성되어 이전 builder와도 해결이 유지된다. 코드 제외 export에는 설정 경로·digest가 없다. | REQ-028; TC-14,31 |

TC-104는 혼합 프로젝트에서 기존 TS 검사에 관리 JS가 추가되거나 48개 파일 상한으로 건너뛴 경우도 확인한다. 설정이 같은 TS 검사에서 사라진 TS 발견은 `resolved_observed`이고 새 TS 발견은 이전 검사 가능 여부를 그대로 반영한다. 검사하지 않은 JS와 설정이 바뀐 TS는 미확인으로 남는다. 이전 보고서와 처리 경로가 일부만 기록된 행의 문맥도 확인한다.

TC-104는 구문 검사와 관리 JS가 중복 진단을 내어 목록에서 관리 JS 발견만 사라지는 경우를 포함한다. 이전과 같은 활성 원시 진단이 수락된 행에 남으면 `absent_unverified`와 `active_observation_remains`가 표시된다. 다른 규칙의 관찰이나 수락되지 않은 결과는 이 사유를 만들지 않는다.

사람의 판정(disposition)과 권한은 TC-31·TC-49의 권한 조건과 함께 시험한다.

## 예약·진행·SSE

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-25 | 같은 예약 중복, 실행 중 여러 occurrence, 서버 정지 | 유일성·coalesce_latest·latest_once 적용 | REQ-023; NFR-002,003 |
| TC-26 | browser 종료/reconnect와 plan 추가 task | 실행 유지, cursor 복원, revision과 새 진행 분모 표시 | REQ-016,017,034; NFR-004,005 |
| TC-99 | 도구 실패와 성공 혼합, 정책 제외, 취소, 새 attempt 후 옛 이벤트 재전송, 연결 단절, 단계 정보가 없는 이전 기록, SSE 순번 누락, 계획 분모 변경 | 일부 수행·미실행·복구를 구분한다. 새로고침 뒤 같은 상태가 복원되고 오래된 epoch가 덮어쓰지 않는다. SSE 순번이 끊기면 서버가 `state.resync`(`gap`)로 연결을 닫고 클라이언트는 GET 뒤 다시 연결한다. 계획 확정 뒤 task가 추가되면 계획 revision이 오르고 진행 분모가 바뀐다. | REQ-013,016,017,022; NFR-003,004 |

## 평가

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-33 | schema/지침/모델/규칙 변경 | revision/digest·holdout 회귀 결과 기록 | REQ-027; NFR-008,010 |
| TC-93 | 정답·확인 코드를 소스 외부에 둔 개발 5쌍과 보류 5쌍(`fixtures/regression/`) | 별도 테스트로 수정 전후 동작 확인 | REQ-003,009,010,011,012,013,026; NFR-007,008 |
| TC-94 | **실모델**. 공개 고정본과 합성 사례, 별도 Git 입력 | 취득 SHA, 보고서·원본 digest 및 위치 대조 | REQ-003,009,010,011,012,013,026; NFR-007,008 |
| TC-100 | 지침 개정 시. 검토 지침 하나를 고치고 기본 회귀 집합(`fixtures/regression/`)을 사전 기준으로 한 번 실행한다(A33, A52) | 역할별 digest·호출 내역과 평가 분모를 보존한다. Q08·Q10의 계약 밖 입력, 수정 전 결함, 호출자가 정리하는 반례를 분리 채점한다. 문서 계약을 바꾸는 지침은 독립적으로 판정할 수 있는 조건과 결과를 내는지, 결정 경로가 빠진 계약을 미확인으로 남기는지 함께 본다. 허위 근거·모델 재현 단정·통계 오염은 개정의 병합을 막는다. | REQ-010,011,013,027; NFR-007,008,010 |
| TC-120 | 정답 차단: 정답 목록 생성, relay `--forbidden-phrases`, 줄바꿈으로 나뉜 문구·camelCase 토큰·대소문자가 다른 부분 문자열을 심은 요청, 입력·지침에 정상으로 나오는 표현, 목록 없이 시작한 relay | 목록은 어떤 사례·변형·README 입력에도 없는 라벨 문구·고유 토큰만 담는다. 일치 요청은 예약·전달·차감 없이 403 `blocked_answer_leak`이고 장부에는 항목 ID·역할 표지만 남는다. 정상 표현은 통과한다. 형식이 틀린 목록이면 relay가 시작하지 않는다. 선택 옵션 `--allow-model-echo`는 relay가 돌려준 답에 나온 ID만 예외로 기록하고, runner는 그 ID가 같은 사례의 모델 출력에 없으면 `answer_echo_unverified`로 뺀다. runner는 relay가 이 목록을 현재 corpus digest로 읽지 않았으면 review batch를 시작하지 않고, 차단된 사례는 `invalid`로 지표에서 뺀다. 심은 정답이 막히지 않으면 `planted_leak_not_blocked`다. | REQ-013,027; NFR-010 |
| TC-121 | 평가 실행·예산: `tools`·`review` 조건, 층별 표본과 seed, 반복, holdout 등록부, 상한 추정이 남은 예산·요청 수를 넘는 batch, batch 중간의 relay 예산 거절, 시험용 upstream | 사례마다 실제 controller·agent·분석기 경로로 실행하고 manifest에 팩·역할 digest, corpus digest, 모델, OS, 도구 버전·플래그, 예산, 시각, seed를 남긴다. 같은 seed는 같은 표본이다. 이전 다른 digest의 실제 실행이 있으면 `holdout_seen_by_prior_revision`을 표시한다. 시작 전 거절은 사유를 남기고 아무 사례도 실행하지 않는다. 중간 거절은 그 사례를 무효로, 남은 변형을 `not_run`으로 기록하고 멈춘다. 시험용 upstream은 loopback URL만 받고 키를 읽지 않으며 실제 장부와 섞이지 않는다. | REQ-006,013,018,027; NFR-010 |
| TC-122 | 채점: 위치 pre-fill, 판정 양식, 미판정·판정 충돌, 판정된 단위의 지표와 Wilson 95% 구간, 언어·범주·detection 분해, 조건 비교, 반복 일치도, 보고서 읽기 답 없음·있음 | 자동 위치 대조는 판정이 아니며 구조 지표로만 보고한다. 비율의 분모는 판정된 단위이고 미판정 수를 함께 낸다. 충돌한 판정은 미판정이다. 비용·시간과 정확도를 한 점수로 합치지 않는다. 표시된 batch나 dry run이 섞인 비교는 `independent: false`다. 근거 유효성은 새로 만든 입력과 대조한다. 검토 결과는 task 상태로 분류하고, 비밀값 선별 실패(`secret_screen_failed`)는 모델 실패와 따로 센다. 코드 노출은 보고서의 파일 수가 아니라 검토 기록의 근거 종류로 센다. 보고서 읽기는 사람의 답이 없으면 `not_measured`이고 모델로 독자를 흉내 내지 않는다. | REQ-011,012,013,027; NFR-010 |

| TC-283 | 프로젝트 규모 결함 집합(`fixtures/project-defects/`): 네트워크 없는 `validate`·`digest --split holdout`, 첫 50줄 안의 양성 위치·diff와 겹치지 않는 위치·두 번째 파일 없는 `needs_cross_file`·비허용 라이선스·범위 줄 수 밖·종류 불일치, 두 분할에 같은 저장소, 네트워크가 필요한 `verify`·`materialize` | 오프라인 검사가 메타데이터·패치·diff만으로 구조·라벨 위치·분할 규칙을 확인하고 holdout digest가 봉인과 같다. 잘못된 사례는 사유와 함께 거부한다. `verify`는 pin·부모 커밋·패치·diff 적용·트리 digest·범위 줄 수·앵커·라이선스 파일·라벨 누설을 확인하고, `--run-checks`로 사례 검사가 before 실패·after 통과인지 작업 사본에서 실행한다. 만든 입력에는 `.git`·사례 ID·변형 이름·라벨이 없다. 캐시에 커밋이 없고 네트워크가 없으면 이유와 함께 실패한다. 정답 목록은 relay가 그대로 읽는다. | REQ-027; NFR-010 |
| TC-284 | runner·채점의 프로젝트 규모 지원: `--corpus`·`--split`(필수)·`--cache`, 사례 범위의 run scope, `--build-root`, 이력 없는 큰 범위 변형의 계획, 줄 범위 위치 지표, 블라인드 판정 묶음과 되돌리기 | 분할 없이는 시작하지 않는다. 변형마다 범위 경로가 `scope.kind=paths`로 간다. manifest가 build root와 runner checkout을 따로 남기고 지침 digest는 build root에서 읽는다. 이력이 없는 변형의 상한은 호출 상한이다. 위치 지표는 모델 finding과 도구 진단, before와 after를 나누고 심볼 대조 없이 줄 범위만 쓴다. 묶음에는 사례 ID·변형·키·run·snapshot·fingerprint ID·라벨이 없고, 되돌린 판정은 기존 판정 형식으로 `score`가 읽는다. 시험(`test_quality_eval.py`)은 모델·네트워크 없이 돈다. 품질 근거가 아니다. | REQ-011,013,027; NFR-010 |

평가 방법과 보류 데이터 규칙은 [평가](EVALUATION.md)에 있다.

## 운영

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-30 | 폐쇄망 배포팩. 폐쇄망에서 없는 패키지·모델·외부 CDN 요청 | 외부 fallback 없음, 명확한 부분 실패, UI는 로컬 자산 사용. **부분 실행**: TC-240~245가 사설 CA·로컬 미러·network namespace로 확인한다. 실제 폐쇄망·RHEL 8.10·회사 CA와 미러에서는 실행하지 않았다 | REQ-030 |
| TC-32 | **한 머신 규모**(실제 데이터 규모의 목표 시간은 미측정). DB+artifact backup 복원 | 근거·ACL·예약 중복·lease 재조정 정상. 실행 중 온라인 백업을 복원하면 그 run이 `recovery_required`이고 정리 확인 뒤 새 run이 끝나는 것은 TC-255 D7이 실행한다 | NFR-002,003,006,007 |
| TC-248 | `codeye cleanup`: 기본 dry run, `--apply`, 1시간 미만 기준, 끝나지 않은 run, 원시가 아닌 역할, 다른 행이 같은 내용을 참조, 열린 journal attempt의 작업 사본, symlink, 은퇴한 에이전트 디렉터리 | dry run은 아무것도 바꾸지 않는다. 적용하면 끝난 run의 오래된 `stdout`·`stderr`·`raw_output`·`other` 내용에 `content_removed_at`을 적고 감사 `storage.retention_applied` 한 건을 쓴 뒤 참조가 남지 않은 파일만 지운다. 보고서·관찰·manifest·감사 행은 그대로이고 run 화면에 "원시 증거가 삭제됨"이 보인다. 백업·산출물 상한은 지운 내용을 세지 않는다(`TestRetentionMarksOldRawArtifactsOfFinishedRuns`, `retention_test.go`, `TestCleanupNodeOnlyDryRunThenApply`) | REQ-018; NFR-006,007 |
| TC-249 | `codeye service print·install·uninstall`(throwaway 이름, 임시 설정, WSL2 사용자 systemd) | unit은 절대 경로·명시적 PATH·oneshot·`RemainAfterExit`·정지 시간을 담고 systemd 특수 문자를 escape한다. install은 enable만 하고, `systemctl --user start`가 `codeye start`를, stop이 `codeye stop`을 실행해 상태가 `running`·`stopped`로 바뀐다. journal에 로그인 토큰이 없다. uninstall은 설정 없이 CODEYE가 쓴 unit만 지운다(`TestServiceUnitText`, `TestServiceUninstallRemovesOnlyItsUnit`, 수동 실행). 재부팅·로그아웃 뒤 유지는 시험하지 않았다 | NFR-003 |
| TC-255 | 운영 훈련(`scripts/ops_drill.py`): D1 모델 endpoint HTTP 500, D2 노드 offline, D3 DB 짧은 중단·lease보다 긴 중단, D4 도구 버전 변경·사라짐, D5 작업 중 max workers 축소, D6 산출물 상한, D7 실행 중 백업·복원, D8 권한 회수, D9 보존 정리(`codeye start`의 서비스) | 훈련마다 한 일·관찰·통과 여부·시간을 표로 남긴다. 기대: 정적 결과와 모델 검토 미수행 사유(D1), 운영자 CLI 정리 확인과 이어 실행(D2), 중단 중 503·복구 뒤 작업 계속 또는 정리 보고로 `failed`와 정합성 검사 0건(D3), 비교의 버전 차이 행과 `resolved_observed` 0(D4), drain만 하고 취소 없음(D5), 새 작업만 대기(D6), 같은 보고서와 정리 확인 뒤 새 run(D7), 즉시 접근 차단(D8), dry run은 아무것도 바꾸지 않고 적용 뒤 같은 보고서·감사 한 건·"원시 증거가 삭제됨"(D9). 결과는 [운영](OPERATIONS.md#장애-대응-runbook)과 [변경 이력](../CHANGELOG.md)에 있다 | REQ-018,021,022,028; NFR-002,003,006 |
| TC-124 | 저장 영역(snapshot·workspace·cache·logs·artifacts) 상한에 도달한 노드·설치, 실행 중 attempt, 상한 없는 heartbeat | 새 claim만 거절하고(204) 대기 사유는 `storage_quota_exceeded`다. 실행 중 attempt와 확정 산출물·보고서는 그대로다. 자동 삭제는 없다. 상한이 없으면 heartbeat 필드를 보내지 않는다. 측정값과 적용 근거(노드 heartbeat·DB 집계)를 구분해 표시한다. | REQ-018; NFR-007 |
| TC-125 | `codeye start`·`stop`·`status`: 실행 중 작업의 drain, drain 시간 초과, stale 기록·재사용 pid·기록 없는 에이전트·포트 점유, 설정 오류 | drain은 실행 작업을 끝낸 뒤 추가 claim 없이 종료한다. 시간 초과는 취소하고 정리를 확인한다. 불확실하면 `recovery_required`다. 기록과 시작 시각이 다른 프로세스에는 신호를 보내지 않는다. 상태 파일은 0600·0700이고 DB URL과 장기 secret이 없다. | REQ-021,022; NFR-006 |
| TC-126 | `codeye doctor --config`: 상한 초과 영역, 백업 없음·오래됨·읽을 수 없음, 불일치 상태, 진행 중 start/stop | 선택 필드 `operations`만 더하고 기존 check는 같다. schema가 상태와 필드 조합을 강제한다. | REQ-018; NFR-006 |
| TC-127 | 개인 사용 경로: 시작, 프로젝트 추가, Go 분석, 정지, 백업, 새 DB·산출물 디렉터리로 복원, 복원본 시작, 보고서·근거 확인 | 모든 단계가 실제 바이너리로 끝난다. 복원본의 보고서·근거 hash·사람 판정이 원본과 같다. 안내서의 명령은 모두 실행한 것이다. | REQ-012; NFR-003 |
| TC-170 | 설정 파일도 DB도 없는 상태에서 `codeye up --no-open <Go 폴더>`(PostgreSQL 프로그램은 PATH 탐색), 환경에 `CODEYE_DATABASE_URL`·`PGHOST`·`PGPASSWORD` | `schema_version`만 담은 0600 설정 파일이 생긴다. 상태 디렉터리에 cluster가 초기화되고(비밀번호 파일 0600) 127.0.0.1의 55432 이상 포트에서 뜬다. schema가 만들어지고 프로젝트가 등록되고 run이 보고서와 함께 끝난다. 출력에 환경 변수를 무시했다는 알림과 `#launch=…&project=…&run=…` 링크가 있고 DB URL·셸의 비밀번호는 없다. `status`의 `database.postgres`는 `running`이다. | REQ-001,004; NFR-006 |
| TC-171 | TC-170이 출력한 링크를 Chromium으로 연다 | 한 번에 로그인되고 주소에 토큰이 남지 않으며 `project`만 남는다. 그 프로젝트와 현재 실행이 열려 있고 보고서를 연다. 세션 만료는 6일 뒤보다 늦다(7일). 웹 단위 시험: 깊은 링크는 canonical UUID인 `project`·`run`만 남기고 나머지는 지운다. | REQ-001; NFR-006 |
| TC-172 | 서비스 실행 중 기존 root 밖의 읽을 수 있는 폴더를 `up PATH`에 입력 | 설정 파일 변경이나 노드 재등록 없이 두 번째 폴더를 분석한다. 보호 데이터 폴더와 겹치면 거부한다 | REQ-001,002,019 |
| TC-173 | `codeye stop` 뒤 `codeye up`, 정지 상태의 `codeye backup`(설정에 `backup_dir` 없음), 새 `state_dir`로의 `codeye restore` | stop의 `database`는 `stopped`이고 그 cluster의 PostgreSQL 프로세스가 없다. 다시 up하면 초기화·사용자 생성 없이 같은 프로젝트가 보인다. 백업은 `<state_dir>-backups` 아래에 세트를 만들고 띄운 PostgreSQL을 다시 멈춘다. 비밀번호는 `postgres/password` 밖의 상태 파일에 없다. 복원은 새 상태 디렉터리에 cluster를 만들고 복원하며 그 뒤 up이 새 노드로 시작한다(복원은 수동 실행으로 확인, smoke에는 없다). | NFR-003,006 |
| TC-174 | PostgreSQL 프로그램 디렉터리: 16·17·13 설치, 프로그램이 빠진 디렉터리, 명시한 틀린 경로, 아무것도 없음, 주 버전 16 데이터 디렉터리와 17 프로그램, `PG_VERSION` 없는 비어 있지 않은 디렉터리 | 명시가 없으면 가장 높은 주 버전(14 이상), 데이터 디렉터리가 있으면 그 버전을 고른다. 플래그가 설정보다 먼저다. 명시한 경로가 조건을 채우지 않으면 다른 곳을 찾지 않고 그 경로와 이유를 말한다. 없으면 설치 방법·`postgres_bin_dir`·`external`을 알리는 오류다. 다른 주 버전 데이터 디렉터리와 정체 모를 디렉터리는 거부한다. | REQ-004; NFR-006 |
| TC-175 | `{"schema_version":"0.1"}`만 있는 설정, `up`의 설정 편집(필드 순서·기본값·잘못된 결과), 기본 포트 7331을 다른 프로그램이 쓰는 상태, 설정에 적은 포트를 다른 프로그램이 쓰는 상태 | 기본값은 관리형 DB, `$XDG_STATE_HOME/codeye`(없으면 `~/.local/state/codeye`), root 없음이다. 편집은 다른 필드와 순서를 보존하고 기본값을 쓰지 않으며, 검증을 통과하지 못하면 파일을 바꾸지 않는다. 기본 포트가 쓰이면 7332 이후 빈 포트로 띄우고 `stop`·`status`·`open`이 그 주소를 쓴다. 적은 포트가 쓰이면 시작하지 않는다(포트 이동은 수동 실행으로 확인). | REQ-001; NFR-006 |
| TC-240 | `offline_pack.py build`를 두 번, `verify`에 바뀐·빠진·더해진 파일, ELF 프로그램 헤더(interpreter·dynamic·없음), `INSTALL.md`의 안내서·변경 이력 링크 | 같은 commit에서 묶음 바이트가 같다(이 머신 sha256 일치). 묶음은 정적 실행 파일·`web/dist`·문서·`CHANGELOG.md`·설정 예시·`INSTALL.md`·`THIRD_PARTY_NOTICES/`·`MANIFEST.json`·`SHA256SUMS`를 담는다. 설치 문서의 링크는 묶음 안에서 열리고 `sha256sum -c`가 통과한다. verify는 세 변경을 모두 보고한다. 동적 실행 파일은 묶지 않는다(`scripts/test_offline_pack.py`, 수동 build) | REQ-030; NFR-007 |
| TC-241 | 웹 산출물의 외부 글꼴·CDN script·CSS `@import`·`url()`·`fetch`·원격 source map, 식별자·문구 주소, 빌드된 `web/dist` | 외부 load마다 실패하고 식별자·문구 주소는 통과한다. 빌드된 화면은 통과한다(CI 웹 작업의 `check-web`, `scripts/test_offline_pack.py`) | REQ-030 |
| TC-242 | `offline: true`·`up --offline`: git_hosts 없음, Go proxy 없음·캐시 있음·없음·`off`·사내 proxy, npm·pip·cargo 미러 없음, `base_url` 없는 키 파일, 환경변수 키, 키 없는 모델, relay | 설정에 저장되고 에이전트가 `--offline`을 받는다. git_hosts는 빈 목록이다. Go는 운영자 캐시만 읽거나 사내 proxy만 쓰고 그 밖은 `offline_no_registry_configured`, GOSUMDB는 `off`다. 다른 생태계는 받지 않는다. 모델은 `base_url`이나 relay만 쓰고 환경변수 키는 키 파일을 바꾸지 않는다(`TestOfflineConfiguration`, `TestEditFileSetsOffline`, `TestOfflineModelNeedsAnEndpoint`, `TestInstanceAgentArgvOffline`, `TestInstanceOfflineExampleParses`, `TestSetupInstanceModelOfflineKeepsKeyFile`, `TestOfflineGo*`, `TestOfflineJSAndPythonAndRustNeedARegistry`, `TestOfflineNeedsAConfiguredEndpoint`, `TestManagedOpenCodePinsTheNpmRegistry`). GitHub.com은 오프라인에서 요청하지 않으며 설정한 사내 GHES는 인증정보와 사내 CA로 연결한다(`TestOfflinePullRequestHost`, `TestPullRequestRunsOverHTTP`, `TestOfflineGHESPullRequestUsesConfiguredPlatform`) | REQ-030; NFR-006 |
| TC-243 | `ca_bundle`: 상대 경로, 인증서 없는 파일, 사설 CA HTTPS 미러에 go·git·npm·pnpm·yarn·pip·uv·OpenCode를 CA 변수 있이·없이 | 설정·에이전트가 잘못된 파일을 거부한다. 다섯 변수가 같은 파일을 가리킨다. 변수가 있으면 성공하고 없으면 인증서 검증 오류다. OpenCode는 `NODE_EXTRA_CA_CERTS`만, `SSL_CERT_FILE`만으로도 성공한다. git은 CA 변수와 GitHub header를 함께 환경으로 받고 token은 argv에 없다(`TestCheckCABundle`, `TestCAEnvNamesOneBundleForEveryTool`, `TestGitCommandsTrustTheConfiguredCABundle`, `TestGitGetsTheCABundleAndTheTokenTogether`, `scripts/offline_smoke.py`). cargo는 실행하지 않았다 | REQ-030; NFR-006 |
| TC-244 | `doctor --config`의 `offline`: 오프라인 켬·끔, 미러·사용자 go env·npm 설정·기본값, CA 상태, PostgreSQL 프로그램, 언어별 빠진 도구. `tools --checklist` | 접속할 곳마다 출처와 받지 않는 이유가 있고 schema(if/then)가 조합을 강제한다. 점검표는 모든 도구에 옮기는 방법과 시험한 계열을 적는다(`TestOfflineReadiness`, `TestChecklistCoversEveryTool`, `validate_bundle.py`의 doctor 음성 사례) | REQ-030 |
| TC-245 | 묶음을 빈 디렉터리에 풀고 그 실행 파일만으로, 새 HOME·설정·상태, network namespace(loopback만)와 connect·DNS 기록: 모듈 캐시에 있는 Go 프로젝트, 없는 프로젝트, 사설 CA 미러·가짜 모델 upstream(TLS)으로 Go·Python·JS 프로젝트와 Git 주소, doctor·점검표·웹 화면 | 캐시 프로젝트는 `prepared`, 없는 프로젝트는 `offline_no_registry_configured`다. 미러 단계는 미러에서 받고 모델 검토가 사내 endpoint로 `performed`다. loopback 밖 connect와 DNS 조회가 없다. 웹 화면은 묶음의 `index.html`이다(`scripts/offline_smoke.py`, 수동·CI 밖, Linux) | REQ-030; NFR-001,006 |

`codeye up`·`start`로 띄운 서비스의 운영 계약은 [운영](OPERATIONS.md)에 있다. `up`·`start`·`stop`·`status`·백업·복원 시험은 Linux 전용이다. 브라우저 자동 열기(`xdg-open`·`wslview`·`explorer.exe`)는 시험하지 않았다(열기 프로그램 선택 순서만 단위 시험). 장애 훈련과 권한 철회는 한 호스트의 TC-255이 실행한다. NFS 지연, 실제 데이터 규모의 백업·복원 목표 시간, 회사 SSO·GitHub App 권한 철회, 품질 baseline은 TC가 없으며 [로드맵](ROADMAP.md)의 운영 수용 작업에 남아 있다.

## 연동

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-27 | **설계(미구현)**: GitHub App 연결 상태. webhook이 없지만 repository readable | 수동/예약 사용 가능, PR 기능만 연결 미완료 | REQ-024,025 |
| TC-28 | 로컬 가짜 GitHub API와 PostgreSQL로 저장소 웹훅, 잘못된 HMAC, 저장소 불일치, 중복·충돌, 권한 철회, 다른 head, 포크와 댓글 명령 시험 | 같은 배달과 같은 PR head는 중복 실행 없이 처리한다. 다른 본문을 같은 배달 ID로 보내면 409다. 봇·인용·코드·읽기 전용 사용자의 명령과 포크 PR은 실행되지 않는다. 허용된 댓글은 설정한 프로젝트 계정으로 고정 PR 실행을 만든다. 실제 GitHub.com·GHES 웹훅은 미실행(`TestGitHubWebhookDelivery`, `TestWebhookMentionIgnoresQuotesAndCode`) | REQ-024,025 |
| TC-29 | **일부**(token 경로의 결과 댓글은 TC-228·235, App 경로는 설계): PR 결과 댓글. PR head 변경 후 이전 결과 완료·댓글 API 실패 | stale 표기, 최신 댓글 미덮어쓰기, outbox만 재시도 | REQ-024,025 |
| TC-35 | 문서·코드·서비스 설정의 기능 검토 | 기존 CI 연결 및 초기 CVE 기능이 없음 | REQ-032,033 |

TC-27의 수동·예약 분석은 승인된 HTTPS 호스트의 Git 입력으로 동작한다. 플랫폼 계정의 PR·댓글은 TC-227~236·TC-258에서 확인한다. [GitHub 연결](GITHUB.md)

GitHub token, pull request, 결과 댓글([GitHub 연결](GITHUB.md), [pull request 보고서](REPORT.md#pull-request-보고서), A46):

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-227 | 가짜 GitHub API와 Git 미러, `github.token_env`에 지정한 플랫폼 환경변수, `codeye up --comment …/pull/7`(fork head, 삭제 파일 하나, 변경 줄의 go vet 진단과 기존 코드의 진단) | `up`이 플랫폼 계정와 고정한 head·base·fork를 출력하고 선택을 기억한다. run의 `pull_request`와 보고서 `inputs.commit_sha`가 고정 head다. 보고서 절은 삭제 파일 1, 검토 대상 1, 줄 기준이고 진단을 변경된 줄과 기존 코드로 나눈다. Markdown 요약이 pull request와 "같은 깊이로 검토하지 않았을 수 있다"를 말한다. 댓글 하나가 게시되고 `GET /setup`의 `github_connections`에는 계정 상태만 있다(`scripts/instance_smoke.py`) | REQ-024 |
| TC-228 | TC-227 뒤 새 push(ref 이동), 같은 주소로 다시 `up` | 새 head의 새 run, 같은 댓글 하나가 새 head로 고쳐진다. 비교 기준은 이전 run이다(`scripts/instance_smoke.py`) | REQ-024 |
| TC-229 | `--dry-run-comment`, 그 뒤 상태 디렉터리 전체 | GitHub 쓰기 요청 0, 댓글 미리 보기 출력, 기억한 선택 유지. 모든 GitHub 요청에 token이 있고, token은 `secrets/github-platform.json`(0600) 하나에만 있다(`scripts/instance_smoke.py`) | REQ-024; NFR-006 |
| TC-230 | 명시적 호스트별 환경변수, 기본 `GH_TOKEN`과 `GITHUB_TOKEN`, 오프라인 GitHub.com | 이름을 지정한 해당 호스트만 인증정보를 읽으며 개인 설정을 승격하지 않는다(`TestSetupGitHubUsesExplicitPlatformSource`, `TestGitHubConfigField`) | REQ-024; NFR-006 |
| TC-231 | API client: page 넘김, fork·파일 상태, 짧은 목록, 오류 종류(401·403·404·429·5xx), 다른 origin의 다음 page, redirect, patch의 변경 줄 | 고정값이 정확하고 잘린 목록을 표시한다. token은 API host 밖으로 가지 않는다(`TestResolvePaginatesAndKeepsTheForkAndTheFileStatuses`, `TestResolveMarksAShortListingTruncated`, `TestErrorKinds`, `TestTokenStaysOnTheAPIHost`, `TestChangedLines`) | REQ-024; NFR-006 |
| TC-232 | `refs/pull/N/head` 가져오기, ref가 움직인 뒤 고정 SHA, 고정 없음·브랜치와 함께, token 전달 | 고정 SHA를 확보하고 ref가 움직여도 그 SHA다. token은 git 환경변수의 header로만 가고 argv·URL·작업 사본에 없다(`TestGitFetchesThePullRequestHead`, `TestGitPullRequestThatMovedFetchesThePinnedCommit`, `TestGitPullRequestNeedsAPinnedHeadAndNoBranch`, `TestGitTokenTravelsInTheEnvironmentOnly`, `TestReadHostToken`) | REQ-024; NFR-006 |
| TC-233 | HTTP: 상태 디렉터리 없는 controller, run 보기·미리 보기, 브랜치 run, 기억·명시 댓글, GitHub 404·429·403·401, 번호 0, 다른 host, 예약, 격리 표의 새 경로 | 정한 코드로 거절하고 미리 보기는 쓰지 않는다. 비구성원은 404다(`TestPullRequestRunsOverHTTP`, 격리 표 `runs.pull_request_comment`) | REQ-024,025 |
| TC-234 | 저장: 고정 값 한 번 쓰기, job의 pull request, outbox 대기·lease·끝·다시 대기, 새 run 게시 뒤 이전 run | 고정 값은 바뀌지 않고 lease는 한 번에 하나다. 더 늦은 run이 게시했으면 이전 run은 `superseded`다(`TestPullRequestRunPinAndJob`, `TestPullRequestCommentOutbox`) | REQ-024 |
| TC-235 | 게시: 댓글 하나 유지, push 뒤 갱신, DB와 marker의 더 새 version, 남이 붙인 marker, 실패 종류와 재시도, 본문의 mention·참조·문자 참조·URL·code | 하나만 남고 되돌아가지 않는다. 남의 댓글은 고치지 않는다. 본문에 살아 있는 mention·참조·링크·코드가 없다(`TestPosterKeepsOneCommentAndNeverGoesBack`, `TestPosterFailuresAreRecorded`, `TestPublicationMarkerRoundTrip`, `TestRenderedTextHasNoLiveMentionOrReference`, `TestRenderedCommentWithNothingInTheChanges`) | REQ-024; NFR-006 |
| TC-236 | 검토 순서: 변경 파일·같은 폴더·나머지, 삭제 파일, 범위 산출물 label, 검토 역할 끝까지, pull request가 아닌 run의 계획, 보고서 절의 분류와 예시 | 변경 파일이 overview와 첫 slot에 먼저 오고 pull request가 아닌 run은 이전과 같다. 보고서 예시(`examples/report-pull-request.json`)가 schema를 지킨다(`TestPullRequestChangedFilesFirst`, `TestPullRequestRunReviewsChangedFilesFirst`, `TestScopePullRequestLabels`, `TestPullRequestSectionClassifiesTheChanges`, `TestPullRequestSectionCountsSentFilesOnlyWithModelCalls`, `validate_bundle.py`) | REQ-009,012,024 |

## 여러 사용자의 한 서비스

`codeye up` 하나가 다른 머신의 사용자에게도 서비스한다. 플랫폼 소스 접근과 사용자별 프로젝트 권한을 아래 시험에서 확인한다. [사용 안내서](GUIDE.md#다른-사람-초대)

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-256 | 다른 머신에 서비스: 비 loopback listen을 TLS(인증서·키·https public origin)로, `--plain-http`로, 둘 다 없이. 평문 public face의 Host·Origin·cookie, 로컬 listener(기본 listen 포트 + 1)의 Host, 로컬에서 만든 세션을 public face에 보냄, 로그인 링크, `0.0.0.0`의 TLS | 둘 다 없으면 시작하지 않는다. 평문은 경고 한 줄과 함께 시작하고 public origin의 Host·Origin만 받으며 cookie는 Secure가 아니다. 로컬 listener는 loopback Host만 받고 그 세션은 public face에서 쓰이지 않는다. 로그인 링크와 "이 서비스 주소"는 public origin이다(`TestServeOtherMachines`, `TestPlainHTTPServiceAndLocalListener`, `TestTLSServerOnWildcardAddress`, `TestListenRules`) | REQ-002; NFR-006 |
| TC-257 | 플랫폼 GitHub 인증정보와 이전 버전의 사용자별 토큰 파일, API 응답·로그·감사 detail, 권한 모드 | 이전 토큰은 쓰이지 않고 플랫폼 토큰만 서비스 파일(0600)에 있다. 응답·로그·감사 detail에 값이 없다(`TestSharedServiceForOtherUsers`, `scripts/instance_smoke.py`) | REQ-024; NFR-006 |
| TC-258 | 두 사용자와 runner 구성원이 비공개 PR을 분석·미리 보기, 댓글 게시, 호스트별 인증정보 | 허가된 요청은 같은 플랫폼 계정을 쓰고 댓글은 호스트별 플랫폼 계정으로 게시한다(`TestSharedServiceForOtherUsers`, `TestPosterPostsWithHostPlatformToken`) | REQ-024,025; NFR-006 |
| TC-259 | Linux 계정이 읽을 수 있는 서버 폴더 등록·재사용, 없는 폴더, CODEYE 데이터 폴더, 노드 배정과 ACL | 별도 폴더 허가 없이 등록하고 기존 경로를 다시 선택해도 같은 프로젝트를 쓴다. 보호 폴더는 거부하고 배정은 프로젝트 실행 권한을 다시 확인한다(`TestFolderPathsAndSetup`, `TestClaimRechecksActorPermission`) | REQ-001~003; NFR-006 |
| TC-260 | 관리자 스위치 "다른 사용자도 모델 검토 사용"·"다른 사용자의 저장소 빌드 스크립트 실행 허용": 기본값, 바꾸기, 같은 값, 관리자의 run, 다른 사용자의 run(관리자 프로젝트를 구성원이 시작한 경우 포함), `GET /setup` | 기본은 모델 켬·빌드 스크립트 끔이다. offer는 run을 시작한 사용자 기준이며 관리자의 run은 영향이 없다. 스위치가 끈 것만 `model_review_not_offered`·`repository_code_not_offered`로 보고서에 남고 project가 고르지 않은 것은 남지 않는다. setup은 사용자마다 `available`과 이유를 답하고 관리자가 아니면 도구 안내가 없다. 같은 값은 감사 행이 없다(`TestSharedServiceForOtherUsers`, `TestEveryoneRootsOfferAndControllerNode`, `TestOfferControllerSourcesAndRequesterToken`, `quickStart.test.ts`) | REQ-002,004,026 |
| TC-261 | 프로젝트·관리·보고서 API와 PR 댓글 미리 보기 | 관리 API는 관리자만 쓸 수 있다. 프로젝트 권한은 역할에 따라 적용한다. 권한을 통과한 미리 보기는 플랫폼 자격증명을 쓴다(`TestRouteIsolationAndAuditTable`, `TestRouteGatesForEveryRoute`) | REQ-002,031; NFR-006 |
| TC-262 | `codeye up`이 띄운 노드(`--controller-sources`): 웹에서 더한 root와 Git host, 따로 띄운 노드 | up의 노드는 등록에 없던 root의 경로와 승인 host를 job에서 받고 project 선택을 offer 안에서 따른다. 따로 띄운 노드는 job에서 아무것도 받지 않고 운영자 플래그가 상한이다(`TestOfferControllerSourcesAndRequesterToken`, `TestEveryoneRootsOfferAndControllerNode`, `TestInstanceAgentArgv`, `TestInstanceAgentArgvOffline`) | REQ-002,018,019 |
| TC-263 | 한 웹 서비스의 두 사용자(`scripts/web_service_smoke.py`, Chromium), HTTP와 자체 서명 TLS, 이메일 가입, 서버 폴더와 비공개 PR | 신규 계정이 서비스 계정의 읽을 수 있는 폴더를 분석하고 같은 호스트의 플랫폼 계정으로 비공개 PR을 시작한다. 보호된 폴더는 거부한다 | REQ-002,024; NFR-005,006 |
| TC-264 | 사람이 실제로 쓰는 주소: `0.0.0.0`의 평문 HTTP를 `--public-origin` 없이, 이 머신의 인터페이스 주소·VPN 주소·호스트 이름·search domain 이름·loopback 주소들·대괄호 IPv6·기본 포트를 뺀 Host, 다른 포트·접두사만 같은 이름·남의 이름, loopback으로만 듣는 서비스에 쓴 호스트 이름·인터페이스 주소·다른 loopback 주소, 다른 받는 주소의 Origin, loopback과 VPN 주소에서 만든 로그인 링크, 받지 않는 Host의 화면과 JSON, 그 안내의 명령 실행 | loopback이 아닌 주소로 들으면 플래그 없이 이 머신의 이름·주소는 모두 받고 나머지는 403이다. loopback으로만 들으면 loopback 이름·듣는 주소·설정한 origin만 받는다. 받는 주소로 로그인과 POST가 되고 다른 받는 주소의 Origin은 `ORIGIN_REJECTED`다. 링크는 관리자가 연 주소를 쓰고 loopback이면 알려 줄 주소다. 403은 쓴 주소(모양이 맞을 때만, HTML escape), 받는 주소, `codeye up --public-origin …`을 말하고 그 명령 뒤 controller만 다시 떠서 그 주소를 받는다. `up`과 관리 화면은 받는 주소를 묶어 보이고 `0.0.0.0`·bridge 주소는 보이지 않는다(`TestHostAcceptance`, `TestHostRejectionTellsWhatWorks`, `TestPlainHTTPServiceAndLocalListener`, `internal/hostaddr`, `TestControllerListenArgs`, `TestEditFileServeAndPublicOrigins`, `scripts/web_service_smoke.py` wildcard) | REQ-001,002; NFR-006 |
| TC-265 | 명령줄 도움말: `codeye`, `codeye --help`·`-h`·`help`, `codeye up --help`, start·stop·status·open·tools·doctor의 `-h`·`--help`, `codeye user --help`, `codeye user create --help`, 다른 인자·모르는 플래그와 함께 쓴 `--help`, 없는 명령의 `--help` | 모두 0으로 끝난다. 전체 도움말은 자주 쓰는 명령(up, up --listen, open, status, stop, tools, doctor)을 운영 명령과 따로 띄운 controller·노드의 관리 명령보다 먼저 보이고 명령마다 한 줄 설명을 단다. 명령의 도움말은 `Usage:`와 그 명령의 줄만 보인다(`user`는 user 명령 모두). 없는 명령이면 전체 도움말이다. 명령과 플래그는 바뀌지 않는다(`TestCommandHelpExitsZero`, `TestHelpPutsEverydayCommandsFirst`, `TestUsageEntriesMatchTheirLines`) | REQ-001 |

## 요구사항 추적표

`scripts/validate_bundle.py`는 이 표의 `REQ-001,004,005` 형식(쉼표, 공백 없음)에서 ID를 읽어 요구사항 문서의 모든 REQ·NFR이 연결됐는지 검사한다. 여러 번호는 이 형식으로만 적는다.

| 요구 | 주요 문서 | 수용 테스트 |
|---|---|---|
| REQ-001,004,005 | ARCHITECTURE, REVIEW, REPORT, OPERATIONS | TC-01,05,96,97,265 |
| REQ-002,015 | ARCHITECTURE, EXECUTION, SECURITY | TC-02,04,17,266, TC-256~264 |
| REQ-003 | ARCHITECTURE | TC-03,04 |
| REQ-006,007,008 | ARCHITECTURE, ANALYZERS | TC-06,07,08 |
| REQ-009,010,011 | REQUIREMENTS, REVIEW, ARCHITECTURE | TC-09,10,11,16 |
| REQ-012,013 | ARCHITECTURE, REPORT | TC-12,13,15,130,134 |
| REQ-014 | ARCHITECTURE, EXECUTION | TC-17,18,189,191 |
| REQ-016,017,034 | API, REPORT | TC-26,98,99 |
| REQ-018,019 | ARCHITECTURE, EXECUTION | TC-21,24 |
| REQ-020,021 | EXECUTION | TC-19,20 |
| REQ-022,035 | ARCHITECTURE, EXECUTION, API | TC-18,21,22,23 |
| REQ-023 | EXECUTION | TC-25 |
| REQ-024,025 | GITHUB | TC-27,28,29, TC-227~236, TC-257,258 |
| REQ-026 | REVIEW | TC-12,34,76,170,171,172,182,183,184,272 |
| REQ-027 | REVIEW, EVALUATION | TC-33,97,100,283,284 |
| REQ-028,029 | ARCHITECTURE, REPORT | TC-14,31 |
| REQ-030 | SECURITY, OPERATIONS | TC-30,170,240~245 |
| REQ-031 | REPORT, SECURITY | TC-15,31,266 |
| REQ-032,033 | REQUIREMENTS | TC-35 |
| NFR-001,009 | EXECUTION, OPERATIONS | TC-17,18,23 |
| NFR-002,003 | EXECUTION, API, OPERATIONS | TC-21,23,25,32 |
| NFR-004,005 | REPORT | TC-26, [UI 성능](#고위험-시나리오) |
| NFR-006,007 | ARCHITECTURE, SECURITY | TC-02,15,22,31,32,170,171,172 |
| NFR-008,010 | REVIEW, EVALUATION | TC-11,12,24,33,172 |

주요 문서 열의 이름은 `docs/` 아래 같은 이름의 `.md` 파일이다. 이 표는 요구별 대표 시험이며, 나머지 연결은 영역별 표의 요구 열에 있다.

## 고위험 시나리오

**Lease partition:** A가 실행 중인 상태에서 controller 통신을 끊는다. agent가 마지막 갱신 뒤 안전 여유 안에 새 task를 멈추는지 확인한다. 종료 불확실한 test를 자동 중복 실행하지 않는지 확인한다. A의 이전 epoch 완료 요청은 거부되고 새 attempt 결과는 유지돼야 한다. 한 호스트에서는 로컬 TCP 프록시 차단·SIGSTOP·SIGKILL로 TC-251·TC-252이 실행한다. 여러 물리 머신에서의 시험은 다중 노드 검증 작업에 남아 있다.

**취소:** worker→shell→test→자식 프로세스 구조를 만든다. 정상 종료, 응답 없는 자식, agent crash, PID 재사용을 각각 시험한다. 종료된 process group/Job의 결과와 cleanup 상태를 검증하며 로그만 보고 성공 처리하지 않는다.

**소스 일관성:** 파일이 추가/삭제/수정되는 중 복사한다. 정상 안정 사본, 재시도 후 성공, source_unstable, 외부 링크 차단을 각각 확인한다. NFS 단순 복사가 원자적이라는 문구가 UI에 없어야 한다.

**UI 성능:** 큰 로그/다수 findings fixture에서 목록 페이지·검색·필터·키보드 이동을 시험한다. 정상 연결의 이벤트 반영 지연 목표를 측정하고 백엔드 처리 지연과 UI 지연을 구분한다. 결과는 OS·브라우저·데이터 크기와 함께 기록한다. 이 측정은 아직 하지 않았다(NFR-004는 미측정 목표).

## 시험 실행 방법

### Go 시험

```sh
go test ./...
```

- 단위 시험은 외부 서비스 없이 실행된다. PostgreSQL이 필요한 저장·HTTP 통합 시험은 `CODEYE_TEST_DATABASE_URL`이 없으면 건너뛰며(skip, 실행 안 함) 통과로 세지 않는다. 격리된 시험용 DB URL을 직접 주거나 아래 `scripts/test_postgres.py`를 쓴다.
- Go toolchain은 시험 바이너리를 만든 것(`GOROOT/bin/go`)을 PATH 검색 없이 쓴다. `CODEYE_TEST_GO`로 바꿀 수 있다. `GOTOOLCHAIN=local`로 실행한다.
- 실도구 시험은 도구 경로 환경변수가 있을 때만 실행한다. 없으면 skip하고, 같은 판정 경로는 캡처한 실제 출력을 재생하는 시험이 검사한다.

| 변수 | 쓰는 시험 |
|---|---|
| `CODEYE_TEST_STATICCHECK`, `CODEYE_TEST_GOMODCACHE` | Staticcheck 실도구, 운영자 모듈 캐시 |
| `CODEYE_TEST_CPPCHECK`, `CODEYE_TEST_CLANG_TIDY` | C/C++ 팩 |
| `CODEYE_TEST_RUFF`, `CODEYE_TEST_MYPY`, `CODEYE_TEST_PYRIGHT`, `CODEYE_TEST_BANDIT` | Python 팩 |
| `CODEYE_TEST_NODE`, `CODEYE_TEST_ESLINT`, `CODEYE_TEST_TSC` | 공유 node toolchain probe, JS/TS 팩, pyright·jscpd |
| `CODEYE_TEST_CARGO`, `CODEYE_TEST_CLIPPY`, `CODEYE_TEST_TOOLPATH` | Rust 팩(`CODEYE_TEST_TOOLPATH`에 링커 `cc`) |
| `CODEYE_TEST_JAVA`, `CODEYE_TEST_PMD`, `CODEYE_TEST_SPOTBUGS` | 설치한 Java 실행 파일과 분석기 JAR의 절대 경로(TC-277) |
| `CODEYE_TEST_JAVA_EXTERNAL_JAR` | 실제 Commons Lang 3.17.0 JAR로 Maven·Gradle 정적 클래스패스 연동 시험(TC-277) |
| `CODEYE_TEST_JAVA_CLASSPATH_CATALOG`, `CODEYE_TEST_COMMONS_TEXT`, `CODEYE_TEST_COMMONS_CSV` | 운영 노드 분석 경로의 공개 프로젝트 시험(TC-277). 카탈로그와 선택한 Apache Commons 1.13.0 체크아웃의 절대 경로를 지정한다. 카탈로그의 로컬 미러에 두 프로젝트가 선언한 컴파일 의존성을 준비한다. |
| `CODEYE_TEST_LIZARD`, `CODEYE_TEST_JSCPD`, `CODEYE_TEST_GITLEAKS` | 구조·중복·비밀값 팩 |
| `CODEYE_TEST_PG_BIN` | 백업·복원 시험의 `pg_dump` 위치(없으면 PATH) |
| `CODEYE_TEST_DEPS_NETWORK=1` | 공개 Go proxy와 PyPI에서 실제로 받는 의존성 시험(TC-199, TC-218). 네트워크가 필요하다 |
| `CODEYE_TEST_PYTHON` | 받은 트리를 읽는 실제 mypy 시험의 interpreter(TC-215) |
| `CODEYE_TEST_OPENCODE_BIN`, `CODEYE_TEST_REVIEW_RELAY`, `CODEYE_TEST_REVIEW_MODEL`, `CODEYE_TEST_REVIEW_LEDGER`, `CODEYE_TEST_OPENCODE_MODEL` | 실모델 시험(`TestLive*`). 비용이 들며 명시적으로 하나씩 실행한다([모델 검토](REVIEW.md)) |

### 웹 시험

```sh
npm --prefix web ci
npm --prefix web test        # vitest 단위 시험
npm --prefix web run build   # tsc 검사와 정적 자산 빌드
```

Playwright 종단 시험(`web/e2e/`)은 실제 서버가 필요하므로 단독으로 돌리지 않고 `scripts/test_postgres.py --web`이 실행한다. 빌드된 `web/dist`와 `npx playwright install chromium`(web 디렉터리에서)이 필요하다.

### 문서·계약 검사

```sh
python3 -m pip install -r scripts/requirements.txt
python3 scripts/validate_bundle.py
```

문서 링크, JSON schema와 예시, 예시 계획의 DAG, 보고서 수치·근거 범위 참조, 지침 파일 참조, 요구사항 추적표를 검사한다. 보조 검사이며 제품 시험이 아니다.

### 통합 시험

```sh
python3 scripts/test_postgres.py --web --nodes --scheduler --instance --web-service
```

- 필요: PATH의 `initdb`·`postgres`·`psql`·`go`, `scripts/requirements.txt`의 Python 패키지(jsonschema 등). `--web`은 위 웹 빌드와 Chromium도 필요하다. 기존 DB 서비스를 쓰지 않고 loopback의 빈 포트에 임시 PostgreSQL을 만들고 끝나면 지운다.
- 기본 단계: `CODEYE_TEST_DATABASE_URL`을 준 `go test -race -count=1 ./...`, CLI 빌드, `codeye migrate` 두 번, `codeye doctor` schema 검증과 credential 비노출 확인.
- `--web`: 실제 `codeye serve`에 대한 브라우저 smoke(Playwright·Chromium).
- `--nodes`: 서로 다른 노드 신원 두 개의 Linux Native 수용 시험. `CODEYE_TEST_STATICCHECK`·`CODEYE_TEST_NODE`·`CODEYE_TEST_RUFF` 등 도구 변수가 있으면 실제 도구로도 실행하고, 없으면 해당 smoke를 SKIP으로 출력한다.
- `--scheduler`: 별도 DB에서 실제 controller·autoscaler와 `-tags codeye_fixture`로 빌드한 agent·worker(TC-71~75). fixture handler는 시험용이며 분석 결과가 아니다.
- `--instance`: `codeye start`/`status`/`stop`, 백업·복원, 복원본 재시작(TC-125, TC-127, TC-32), 관리형 PostgreSQL의 `codeye up` 경로(TC-170~173, 웹 빌드가 있으면 TC-171의 Chromium 확인 포함), 경로·Git 주소 입력(TC-187, 웹 빌드가 있으면 TC-188의 Chromium 확인 포함, cargo가 있으면 빌드 스크립트 안내까지). 관리형 cluster는 PATH의 PostgreSQL 프로그램으로 만든다. Linux 전용이며 모델 호출·다운로드가 없다.

- `--instance-model`: `codeye start`의 모델 설정과 웹에서 저장한 연결로 다시 분석하기(TC-184, TC-272). 실제 OpenCode(`~/.opencode/bin/opencode` 또는 `CODEYE_TEST_OPENCODE_BIN`)와 가짜 upstream을 쓰며 유료 호출이 없다. `CODEYE_SMOKE_REAL_MODEL_CREDENTIAL`에 0600 키 파일(`base_url` 없음)을 주면 그 키로 첫 단계(probe와 run 하나, 호출 상한 15)만 실행한다(**실모델**, 비용 발생, relay 장부를 거치지 않으므로 비용은 따로 기록한다).
- `--web-service`: 여러 사용자의 한 서비스(TC-263). `codeye up`을 loopback 별칭(`127.0.0.2`)의 `--plain-http`와 자체 서명 TLS로 한 번씩 띄우고 Chromium 두 context(관리자와 다른 사용자)로 시작 화면·폴더 허용·GitHub 연결·관리 화면을 확인한다. 세 번째로 `--listen 0.0.0.0:PORT --plain-http`만으로 띄워 듣는 주소가 아닌 `127.0.0.2`로 로그인과 POST, 받지 않는 Host의 안내와 그 명령을 확인한다(TC-264). 가짜 GitHub(`scripts/fake_github.py`)를 쓰며 웹 빌드·Chromium·`openssl`이 필요하다. Linux 전용이다.
- `--multinode`: 별도 DB에서 한 호스트의 노드 신원 8개로 능력 배정·대기·노드 사망·분할·drain·에이전트 갱신(TC-250~254). 실제 go vet과 시험용 handler를 쓰고 `git`이 필요하다. ruff가 PATH에 없으면 "go 없는 노드"는 `--advertise-without-baseline`으로 띄운다. Linux 전용이며 이 호스트에서 약 2분이다.
- `--ops-drill`: 운영 훈련 D1~D9(TC-255). 자기 PostgreSQL cluster를 띄워 그것만 멈춘다(`pg_ctl` 필요). D1은 OpenCode가 없으면, D4는 ruff가 없으면 `NOT RUN`으로 기록한다. 이 호스트에서 약 9분(그중 D1이 4분여)이라 CI에서는 실행하지 않는다.
- `--smokes-only`: Go 시험을 건너뛰고 고른 smoke만 다시 돌린다. 이때 마지막 줄은 저장 통합 시험을 실행했다고 주장하지 않는다.
- `--pilot`: **실모델** 파일럿. 실제 모델 요청을 보낸다. 고정 OpenCode와 승인된 relay가 필요하다. `CODEYE_PILOT_RELAY_FILE`에 relay descriptor, `CODEYE_PILOT_RESULTS`에 새 비공개 결과 디렉터리를 지정한다. `CODEYE_PILOT_CASES`(예: `Q01-before,pilot`)로 대상을 고른다. 보관된 결과는 `python3 scripts/regression_pilot.py --audit-results "$CODEYE_PILOT_RESULTS"`로 다시 대조한다. 이 대조는 위치·줄 범위·blob SHA의 참조 검사이며 주장이 옳다는 보증이 아니다. Git 시험에 기록된 commit 객체가 로컬에 있어야 한다.

### 그 밖의 보조 검사

```sh
python3 fixtures/regression/corpus.py --check                       # 회귀 집합의 결함 전후 계약 시험(모델 없음)
python3 -m unittest discover -s fixtures/cleanup -p 'test_*.py' -v  # 합성 결함 전후
python3 -m unittest discover -s scripts -p test_model_budget_relay.py -v
python3 -m unittest discover -s scripts -p test_quality_eval.py -v
python3 -m unittest discover -s scripts -p test_offline_pack.py -v  # 배포팩 재현·검증·웹 외부 주소(TC-240·241)
python3 -m unittest discover -s scripts -p test_stage_labels.py -v  # 내용·경로의 단계·팩 세대 표시 검사와 허용 목록
```

배포팩 전체 경로(TC-245, Linux, 수동):

```sh
python3 scripts/offline_pack.py build
python3 scripts/offline_smoke.py --pack .codeye/pack/codeye-VERSION-linux-amd64.tar.gz --postgres-bin-dir DIR
```

필요: `unshare`(비특권 user namespace), `strace`, `ip`, `openssl`, `git`, node와 npm, go와 그 모듈 캐시의 `github.com/google/uuid@v1.6.0`·`github.com/spf13/pflag@v1.0.6`(미러 내용을 이 캐시에서 복사한다), PostgreSQL 프로그램. OpenCode·pnpm·yarn·uv가 있으면 그 CA 확인도 한다. 이 머신의 결과는 [변경 이력](../CHANGELOG.md)에 있다. 모델 호출·외부 다운로드가 없다. `test_postgres.py`에 넣지 않았다.

### CI가 실행하는 것

[`.github/workflows/verify.yml`](../.github/workflows/verify.yml)은 PR과 main push에서 네 작업을 실행한다. 모델 credential·모델 호출·배포가 없고 저장소 권한은 read-only다(A13).

| 작업 | 실행 내용 |
|---|---|
| Linux / PostgreSQL / contracts (ubuntu-24.04) | Go 1.27.4·Python 3.14.4·Node 24.18.0. 웹 빌드, Playwright Chromium 설치, `gofmt`·`go vet`, `scripts/test_postgres.py --nodes --web --scheduler --instance --web-service`, scripts 단위 시험(배포팩 `test_offline_pack.py`, 표시 검사 `test_stage_labels.py` 포함), `validate_bundle.py`, cleanup fixture 시험, `fixtures/regression/corpus.py --check`, CLI 빌드 |
| Linux / several nodes on one host (ubuntu-24.04) | `scripts/test_postgres.py --smokes-only --multinode`(TC-250~254). 개발 호스트에서 약 2분이라 Linux 작업의 25분 예산과 나누어 따로 돈다. 러너에는 ruff가 없어 "go 없는 노드"를 `--advertise-without-baseline`으로 띄운다. IPv6 loopback이 없으면 IPv4 loopback(`127.0.0.1`·`127.0.0.2`)으로 바꿔 실행한다. 호스팅 러너에서도 독립 CI 작업으로 실행한다 |
| Windows / Go unit tests (windows-2025) | `go test -count=1 ./...`, `go vet`, CLI 빌드만. DB 통합·smoke·실도구 시험은 없다 |
| Web / tests and build | `npm ci`, `npm test`, `npm run build`, 빌드한 화면의 외부 주소 검사(`offline_pack.py check-web web/dist`) |

CI 러너에는 go vet 말고 분석 도구가 없어 실도구 시험은 skip되고 재생 시험만 돈다. Windows CI의 단위 시험은 Windows 11의 NativeExecutor·DB·전체 플랫폼 수용 시험을 대신하지 않는다.

### 공개 배포판 설치와 업그레이드

TC-170~173의 첫 실행·재시작과 TC-240·245의 배포팩 검증은 실제 릴리스 파일에서도 확인한다.

1. 공개 릴리스에서 각 CPU용 압축 파일과 같은 이름의 `.sha256`을 새 디렉터리에 내려받는다. `sha256sum -c <파일>.sha256`으로 전송을 확인하고 `python3 scripts/offline_pack.py verify <파일>.tar.gz`로 내부 목록과 파일 해시를 검증한다.
2. 해당 CPU에서 압축을 풀고 `./codeye version --json`의 버전·커밋을 릴리스 태그와 대조한다. 새 환경에서 `./codeye up`으로 시작해 브라우저 접속, 프로젝트 분석, 게시된 보고서 조회를 확인한다. 에뮬레이터에서 확인한 범위와 실제 호스트에서 확인한 범위를 구분해 기록한다.
3. 이전 공개 배포판으로 프로젝트와 보고서를 만든 뒤 [배포판 업그레이드](OPERATIONS.md#배포판-업그레이드)를 따른다. 기존 보고서의 같은 리비전을 같은 옵션으로 JSON 내보냈을 때 바이트가 같은지 비교하고, 같은 프로젝트에서 새 분석과 HTML 내보내기가 되는지 확인한다.

## 보조 검증과 제품 시험의 차이

`scripts/validate_bundle.py`는 문서 링크·schema·예시·계획 DAG·보고서 수치·범위 참조를 검사한다. `fixtures/cleanup/test_cleanup.py`는 합성 결함 전후를 검사하고, `fixtures/regression/corpus.py --check`는 회귀 집합 사례의 계약 시험을 검사한다. 이들의 성공을 TC 전체 통과나 실제 분석 성능 확보로 보고하지 않는다. scripted caller나 fixture handler의 결과는 플랫폼 경로의 근거이지 지침 품질이나 분석 정확도의 근거가 아니다. 코드 시험 결과와 제품 분석 정확도 평가는 별도 결과로 보고한다([평가](EVALUATION.md)).
