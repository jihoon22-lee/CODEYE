# 시험과 요구사항 추적

수용 시험(TC)의 정의, 요구사항과 시험의 연결, 시험을 실행하는 방법을 정한다. TC 번호와 뜻은 바꾸지 않는다. 요구사항 ID의 뜻은 [요구사항](REQUIREMENTS.md)에 있다.

## 시험을 읽는 방법

- 각 TC는 제품 경로에서 확인할 조건과 결과다. 보조 검사(문서 validator, fixture 검사)의 통과를 TC 통과로 보고하지 않는다([보조 검증과 제품 시험의 차이](#보조-검증과-제품-시험의-차이)).
- **설계(미구현)** 표시는 해당 기능이 없어 아직 실행할 수 없는 시험이다. **일부** 표시는 개인 모드에서 일부 조건만 실행할 수 있다는 뜻이며 괄호 안에 남은 부분을 적는다.
- **실모델** 표시는 승인된 모델 연결이 있어야 하는 시험이다. CI는 모델을 호출하지 않는다.
- 표시가 없다는 것은 다른 문서에서 미구현으로 확인되지 않았다는 뜻일 뿐이며, 자동 시험이 있거나 모든 조건을 실행했다는 보장이 아니다. 또 일부 환경에서는 실행하지 않았다. 실기 검증은 Linux(WSL2)에서 했고, Windows는 CI의 Go 단위 시험까지, RHEL 8.10과 여러 물리 머신은 실행하지 않았다.
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
| TC-02 | **일부**(여러 사용자 계정 사이의 격리는 설계(미구현)). OS 계정은 읽지만 웹 사용자는 권한 없는 경로 | 등록·목록·보고서·SSE 모두 접근 거부 | REQ-002,015; NFR-006,007 |
| TC-31 | **일부**(여러 사용자의 권한 철회·삭제는 설계(미구현)). 권한 철회/삭제·code-free export | source/report/SSE 차단, export 내용과 metadata 민감값 제거 | REQ-028,029,031; NFR-006,007 |
| TC-46 | loopback 강제, Host/Origin/CSRF 거부 | 요청 거부 | NFR-006 |
| TC-47 | 일회용 launch token, 동시 교환·비활성화, 세션 만료/폐기 | 한 번만 교환되고 만료·폐기 뒤 거부 | REQ-002; NFR-006 |
| TC-48 | 설명 없는 local/Git 선언 등록, root grant | 등록 성공, 소스 미검증 표시 | REQ-001,002,005 |
| TC-49 | 비구성원·viewer·철회된 구성원의 접근 | 비구성원 404, viewer 403, 철회 후 접근 거부 | REQ-002 |
| TC-50 | 멱등 요청·충돌·DB 실패·서버 재접속 | 멱등 202, 충돌 409, DB 실패 503, 재접속 후 상태 보존 | REQ-022; NFR-002,003 |
| TC-51 | readiness 조회, serve의 구/신/변조 schema | readiness는 읽기 전용, 맞지 않는 schema 거부 | NFR-003 |
| TC-52 | 정적 파일 경계·CSP·비밀값 비노출·실브라우저 요청/취소 | 경계 밖 파일 없음, CSP 적용, 비밀값 없음, 브라우저에서 요청·취소 동작 | NFR-005,006 |

개인 모드에서 TC-02·TC-31의 권한 부분은 owner·프로젝트 구성원 ACL과 source root grant로 시험한다. 코드 제외 내보내기의 식별 정보 제거는 구현돼 있다.

## 계정·권한·감사

여러 사용자 사이의 격리는 아래 시험이 실제 HTTP와 PostgreSQL로 확인한다. 웹 화면은 아직 없으므로 화면 시험은 없다.

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-140 | 라우터의 모든 경로(`Server.routes`에서 목록을 만든다)를 관리자(비구성원), 외부인, viewer, runner, manager, 소유자로 요청한다. 노드 경로는 사용자 세션으로 요청한다 | 사례가 없는 경로가 있으면 실패한다. 비구성원은 관리자도 404, 역할 부족 403, 관리자 경로는 관리자만. 거절된 요청은 감사 행이 없다. 받아들인 상태 변경(과 내보내기)은 그 요청 ID의 감사 행이 경로가 선언한 action 중 하나다. 상태를 바꾸는 사용자 경로는 모두 감사 action을 선언한다. 노드 경로는 사용자 세션으로 401. 같은 경로 목록으로: 세션 없음 401(인증 경로 전체), 다른 Origin·CSRF 없음 403이고 감사 행 없음(상태 변경 경로 전체), 비밀번호 변경 대기 세션 403(허용 경로 밖 전체) | REQ-002,031; NFR-006 |
| TC-141 | 비밀번호 로그인: 맞는 비밀번호, 틀린 비밀번호, 없는 사용자, 빈 이름, 섞인 본문, 다른 Origin, 연속 실패, 잠긴 뒤 맞는 비밀번호, 없는 이름의 연속 실패 | 201과 계정 필드. 틀림·없음·빈 이름은 같은 401과 같은 문구. 섞인 본문 400, 다른 Origin 403. 한도를 넘으면 429와 `Retry-After`(= `details.retry_after_seconds`), 맞는 비밀번호도 429. 없는 이름도 같은 방식으로 잠긴다. 로그에 비밀번호가 없다 | REQ-002; NFR-006 |
| TC-142 | 관리자가 만든 계정의 첫 로그인과 비밀번호 설정, 변경 대기 세션의 다른 경로, 현재 비밀번호 없음·틀림·CSRF 없음, 다른 세션, 관리자 재설정·자기 재설정·비관리자 재설정, 비활성화, 마지막 관리자 | 변경 대기 세션은 403 `PASSWORD_CHANGE_REQUIRED`. 첫 비밀번호는 현재 비밀번호 없이 정한다. 변경은 다른 세션을 끝내고 이 세션은 유지한다. 약한 새 비밀번호는 현재 비밀번호보다 먼저 422. 재설정은 모든 세션과 이전 링크를 끝내고 새 링크만 남긴다. 비활성화는 세션을 끝낸다. 마지막 관리자는 내릴 수 없다(409). 감사 detail·로그에 비밀번호가 없다 | REQ-002; NFR-006 |
| TC-143 | 프로젝트 목록·상세의 역할 필드, 비구성원의 목록, 프로젝트 감사 기록 | `role`·`can_run`·`can_manage`가 역할과 맞다. 관리자·외부인의 목록에 그 프로젝트와 run이 없다. 프로젝트 감사 기록은 그 프로젝트 행만 담고 `project` 필터를 받지 않는다 | REQ-002 |
| TC-144 | JSON·Markdown·HTML 내보내기(코드 포함·제외)를 관리자·외부인·viewer로, 다른 프로젝트의 기준 run, 감사 행을 쓸 수 없는 DB | 비구성원은 여섯 경우 모두 404이고 본문·감사 행이 없다. viewer의 내보내기마다 형식과 코드 포함 여부가 감사 detail에 남고 발견 문구는 없다. 다른 프로젝트의 기준 run은 두 프로젝트의 소유자에게도 404. 감사 행을 쓰지 못하면 내보내기 본문을 보내지 않고 run·구성원 변경도 커밋되지 않는다(503) | REQ-028,031; NFR-006 |
| TC-145 | 열린 이벤트 스트림 중 구성원 제거(API), 사용자 비활성화(관리자 API), 비밀번호 재설정, 변경 대기 세션의 스트림 | 다음 poll에서 `closed`(`access_revoked`, `unauthenticated`, `unauthenticated`)로 끝난다. 변경 대기 세션은 스트림을 열지 못한다(403) | REQ-002,016; NFR-006 |
| TC-146 | 수신 규칙: listen 형식, TLS 없는 비 loopback 주소, TLS 일부만, http·path·사용자 정보가 있는 public origin, group이 읽을 수 있는 키, Serve에 넘긴 평문 wildcard listener, 자체 서명 인증서의 TLS 서버 | 비 loopback은 TLS 없이 시작하지 않고 평문 listener도 거부한다. TLS 서버는 HSTS를 보내고 public origin 밖의 Host를 403, http Origin을 403으로 거부한다. cookie는 `__Host-codeye_session`(Secure, HttpOnly, Domain 없음)이고 평문 이름 cookie는 401. 로그인 링크는 public origin을 쓴다. 같은 포트의 평문 요청은 API를 받지 못한다 | NFR-006 |
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
| TC-155 | 계약 검사: 감사 action·행위자·대상 목록이 migration(0010·0011)·저장 계층·OpenAPI에서 같음, `examples/accounts.json`, 허용 detail key 이름 | `scripts/validate_bundle.py`가 대조하고 목록 차이·비밀번호 key·모르는 action·hash 필드 같은 부정 사례를 거부한다. 보조 검사이며 TC-140~TC-154를 대신하지 않는다 | NFR-006 |

TC-02·TC-31의 "여러 사용자" 부분은 TC-140·TC-144·TC-145가 실행한다. 웹 화면에서 역할에 따라 버튼을 숨기는 시험은 화면이 생길 때 더한다.

## 소스 확보

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-03 | 큐 대기 중 브랜치 변경, 확보 후 다시 변경 | 확보 시 SHA를 사용하고 후속 변경 미반영; 재시도도 동일 SHA | REQ-003 |
| TC-04 | local 복사 중 파일 변경·symlink root 탈출 | 변경 재시도/불안정 실패, 외부 링크 미추적, 원본 미수정 | REQ-002,003,015 |
| TC-53 | 승인 root 원자적 경계·symlink/FIFO 교체·credential 제외·원본 아래 data root | 경계 유지, credential 제외, 원본 아래 data root 거부 | REQ-002,015; TC-04 |
| TC-54 | 변경 감지 재시도·불안정 실패·manifest 무결성·독립 workspace·retained retry | 안정 사본 또는 `source_unstable`, manifest 무결, retained retry는 원본 재확보 없음 | REQ-003,015; TC-03,04 |
| TC-55 | 실제 Git 확보 | SHA 고정, filter 미실행, LFS/submodule 공백 표시, host/전송 제한, soft fetch quota | REQ-003,024,030 |

## 실행기

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-17 | **일부**(빌드·기존 테스트 실행은 설계(미구현)). Native에서 도구/build/test 실행 | 원본 hash 불변, 작업별 환경·자원·로그 독립 | REQ-002,014,015; NFR-001,009 |
| TC-18 | 손자 프로세스를 만든 작업 취소/timeout | 그룹 종료 확인, 불확실하면 recovery_required | REQ-014,022,035; NFR-001,009 |
| TC-56 | literal argv·최소 env·원본/snapshot/cwd 경계 | 경계 유지, 비밀값과 잘린 출력 마스킹 | REQ-014,015; NFR-006 |
| TC-57 | timeout/cancel 후 자식·손자 | OS 종료 확인, 잔류/탈출·관측 실패는 uncertain | REQ-022,035; TC-18 |
| TC-58 | Windows suspended 생성→Job 연결→resume, breakaway | breakaway 거부, 미지원과 enforced limit 구분 | NFR-001,009 |

프로세스 그룹/Job은 OS 권한 샌드박스가 아니다.

## 노드·워커·스케줄러

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-19 | min0/max2, 작업 유입 후 유휴 | 0→최대2→0, agent/controller는 살아 있음 | REQ-020,021 |
| TC-20 | busy3 상태에서 max1로 축소 | 작업 강제 종료 없이 drain, 새 작업 제한 | REQ-020,021 |
| TC-21 | 두 agent가 같은 run을 동시에 claim | 한 활성 epoch만 소유, 노드/전역 예산 중복 차감 없음 | REQ-018,019,022,035; NFR-002,003 |
| TC-22 | **일부**(여러 물리 머신의 실제 network partition은 미실행). node partition 후 오래된 complete 도착 | lease fence 409, 최신 보고서 미덮어쓰기 | REQ-022,035; NFR-006,007 |
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

노드 수용 시험(TC-59~65)은 전용 빌드의 시험용 handler로 프로토콜과 OS 동작을 검사한다. 한 머신에서 별도 노드 신원 둘로 실행하며 여러 물리 머신·NFS·network partition 보증을 대신하지 않는다.

## 분석기

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
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
| TC-105 | C/C++. 다른 머신의 compile DB: 원래 루트·build 디렉터리 경로, 상대 경로, compiler wrapper, 같은 파일의 두 항목, 스냅샷 밖·헤더·MSVC·nvcc 항목, plugin·응답 파일·driver 설정 flag, 값에 넣은 `@file`, 옵션처럼 보이는 파일 이름(`-x.c`, `--help.cpp`), compile DB 없음 | 경로는 추정한 원래 루트 기준으로 작업 사본에 매핑하고 항목의 compiler는 실행하지 않는다. 제외 항목과 버린 flag는 사유별 수로 남는다. 허용 목록 밖 flag, `@` 원소, 코드 적재 옵션은 CODEYE compile DB에 없다. 도구가 받는 위치 인자는 모두 절대 경로다. compile DB가 없으면 clang-tidy는 `unsupported`(`compile_db_missing`)이고 cppcheck 축소 모드는 제약으로 표시되며 정밀 분석 완료가 아니다. | REQ-002,006,007,015 |
| TC-106 | C/C++. 생성 헤더(configure가 만드는 `config.h`)가 없음·있음, 한 batch의 두 단위 실패, 단위별 batch, 헤더만 있는 범위, 결함 없는 단위 | 찾지 못한 헤더는 결함이 아니며 그 단위는 부분·실패 범위다. 작업은 `missing_include_files`로 실패하고 다른 단위의 관찰은 남는다. clang-tidy의 누적 오류 수로 실패 단위를 가른다. 처리 파일은 성공한 작업의 번역 단위만이며 헤더를 주장하지 않는다. | REQ-006,007,008,013 |
| TC-107 | C/C++. cppcheck·clang-tidy 정규화: 종료 코드와 출력 불일치, 잘린·손상 출력, 버전 계열, 저장소 `.clang-tidy`·cwd의 cppcheck 설정·clang driver 설정 파일, NOLINT와 `cppcheck-suppress`, 두 엔진의 같은 줄 보고, 작업 사본 밖 파일을 include하는 소스 | 성공은 종료 코드·출력·단위 처리가 모두 맞을 때만이다. 저장소 도구 설정은 결과를 바꾸지 못한다. 규칙 문맥은 compile DB digest를 기록하고 cppcheck 억제는 `disabled`, clang-tidy NOLINT는 `honored`다. 기준 분석기와 같은 구성만 보고한 clang-tidy 검사는 선택하지 않고 남은 같은 줄 보고는 도구별 finding이다. 밖 파일의 내용은 저장 stream·envelope·보고서에 없다. | REQ-007,008,009,013,028; TC-102,104 |
| TC-108 | Python 스냅샷: ruff·mypy·pyright·bandit 설정·미설정, 하위 모듈, 선언된·없는 Python 버전, 구문 오류, 도구 실패·잘림·버전 미인식 | ruff는 필수 기준이고 나머지는 추가 깊이다. 모듈마다 task를 계획하고 `pack-gap.python`은 없다. 대상 버전과 출처(`requires_python`·`python_requires`·`python_version_file`·`codeye_default`)를 명시 전달·기록한다. 구문 오류는 tool error이며 task는 실패다. 처리 파일은 도구 자신의 기록으로 확인한 성공 task에만 센다. 두 타입 검사기 동시 실행은 `--python-both-type-checkers` 없이는 agent 사용법 오류다. | REQ-007,008,012,013,015; TC-101 |
| TC-109 | Python. 저장소 설정(ruff.toml·mypy plugins·pyrightconfig·.bandit)과 소스 안 억제(`# noqa` 계열·`# nosec`·`# type: ignore`·`# pyright: ignore`), 작업 공간 밖을 가리키는 설정 값, 옵션처럼 보이는 파일 이름 | 네 도구 모두 CODEYE 관리 설정으로 저장소 설정을 대신하고(`overridden`) 저장소 코드를 실행하지 않는다. ruff·bandit 억제는 두 실행 차이로 `reported`, mypy·pyright는 `honored`다. 밖을 가리키는 설정의 canary는 stream·envelope·보고서에 없고 밖에 아무것도 쓰지 않는다. 수집 stream에 소스 줄이 없다. 위치 인자는 `--`·`./`로 옵션이 되지 않는다. | REQ-002,015,028; TC-103,104 |
| TC-110 | Python. 설치되지 않은 의존성 import·stub 누락, 두 엔진이 같은 구성을 보고 | import·stub 공백은 관찰이 아니라 `import_unresolved`·`stub_missing`·`import_source_missing` 단위와 제약이다. 후속 오류를 결함으로 보이지 않는다. ruff와 타입 검사기·bandit의 같은 구성 검사는 한쪽에서 끈다(측정한 제외 목록). | REQ-007,008,009,013; TC-102 |
| TC-111 | JS/TS. 루트·TS·JS 모듈의 모노레포, tsconfig 체인(JSONC·상위 extends·plugin 항목·solution 참조), 실행되면 표식을 남기는 저장소 `eslint.config.js`, 쓰기·호스트 경로 tsconfig 옵션, option처럼 보이는 파일 이름 | 모듈마다 기준선 eslint(Required)·tsc·type-aware 작업이 계획된다. 저장소 설정·plugin은 실행되지 않고 원본·스냅샷·작업 사본이 바뀌지 않는다. 체인은 경로·SHA-256으로 기록된다. 쓰기 옵션과 스냅샷 밖 경로는 실행 전에 `skipped_policy`로 끝나고 옵션 이름만 남는다. 위치 인자는 option으로 해석되지 않는다. | REQ-007,008,012,015; TC-101,103 |
| TC-112 | JS/TS. node_modules 없는 스냅샷의 TS 모듈(해석되지 않는 import·그 결과의 implicit any·무관해 보이는 타입 오류) | 기준선은 성공한다. tsc·type-aware 작업은 `failed`(`dependency_unavailable`)이고 공백은 dependency tool error, 나머지 진단은 withheld로 남는다. 관찰·처리 파일로 세지 않는다. | REQ-007,008,013; TC-08 |
| TC-113 | JS/TS. eslint-disable 계열·inline 규칙 설정·`eslint-suppressions.json`·`@ts-expect-error` | eslint는 관리 실행에서 모든 계열이 꺼져 진단이 모두 관찰되고 `source_suppressions: disabled`다. tsc는 숨긴 진단을 보고할 수 없어 `honored`이고 `unreported_suppression_rules`는 비어 있다. | REQ-013; TC-104 |
| TC-114 | Rust. cargo check와 clippy 결과 판정: 경고만(exit 0), 컴파일 오류·deny 수준 lint·빌드 스크립트 실패(exit 101), `--all-targets`의 중복 진단, clippy가 함께 출력하는 rustc 진단, 비ASCII 줄의 열 | 종료 코드와 마지막 `build-finished`가 일치하고 레코드를 모두 읽었을 때만 성공이다. 경고는 `success_with_findings`, 컴파일 오류는 도구 오류(`package_errors`), deny lint는 관찰을 남기고 `lints_denied`로 실패한다(처리 파일 없음). clippy 작업은 `clippy::` 진단만 남기고 lint 수준을 warn으로 낮춰 같은 구성이 두 작업에 중복되지 않는다. 같은 진단은 하나로 합치고 열은 바이트 열이다. 잘림·손상·알 수 없는 레코드는 성공이 아니다. | REQ-007,008,012,013 |
| TC-115 | Rust. 모듈·workspace·범위·build script: 가상 workspace, 독립 crate, 멤버가 아닌 중첩 crate, 경로 범위, build script와 proc macro가 있는 workspace, Cargo.lock·registry 의존성 없음, 링커 없음 | 가장 바깥 Cargo.toml마다 한 작업(`--workspace`)이고 키·범위·필수 여부가 계획에서 정해진다. 멤버가 아닌 소스는 `files_unlisted`와 `rust_files_not_compiled`로 보인다. build script·proc macro 실행은 `build_script_executed`·`proc_macro_executed`로 남는다. 의존성·잠금 파일·링커 부족은 내려받거나 생성하지 않고 구성 오류 사유(`dependency_unavailable`·`lockfile_missing`·`linker_unavailable`)로 끝난다. | REQ-007,008,012,013; TC-68 |
| TC-116 | Rust. 저장소 코드 신뢰와 toolchain 무결성: 허가 없는 소스(도구 설정·미설정), 허가 있는 소스, rustup proxy와 `rust-toolchain.toml`, 저장소 `.cargo/config`의 target·rustc wrapper, 작업 사본을 고쳐 쓰는 build script, 소스 안 `#[allow]`, unsafe·panic review lint | 허가가 없으면 기준 분석기도 `unsupported`(`repository_code_not_allowed`)이고 version probe 말고는 프로세스가 시작되지 않으며 단계·보고서가 부분이다. 허가가 있어도 운영자가 지정한 실제 toolchain만 쓰고 proxy는 거부한다. target·wrapper는 환경으로 고정하고 CARGO_HOME·target은 scratch에 둔다. 작업 사본이 바뀌면 `workspace_drift`로 실패한다. `#[allow]`가 숨긴 진단은 보고되지 않으므로 소스 안 억제는 `honored`이고 사라진 finding은 해결되지 않는다. unsafe·panic review lint는 clippy 작업이 켠다. | REQ-002,015,028; TC-103,104 |
| TC-117 | 구조·중복 지표: 임계값을 넘는 함수 복잡도(CCN 17)와 10줄 이상 중복 블록, 임계값 아래 값, `vendor`·`testdata`·숨김 디렉터리의 같은 코드, 지표만 있는 성공, 위치를 확인하지 못한 지표, 지표를 인용한 finding | 지표는 `metric`을 가진 관찰로만 남고 finding·결함 수·조치 수·모델 입력이 되지 않는다. 지표만 있으면 `success_without_findings`이고 요약에는 `metric_observations`로 따로 센다. 제외 디렉터리는 `excluded_data_directory`로 센다. 위치를 확인하지 못한 지표는 미해결 관찰로 세어 판정을 `inconclusive`로 둔다. 지표를 인용한 finding이 있는 보고서는 무효다. 복잡도는 lizard, 중복은 jscpd만 잰다. | REQ-007,008,012,013; TC-09 |
| TC-118 | 비밀값 탐지: 생성한 자격증명을 소스·설정·이진 파일에 두고, 인라인 `gitleaks:allow`, 저장소 `.gitleaks.toml` allowlist, `.gitleaksignore`(정상·사본 밖 항목), 기본 allowlist 파일, 같은 파일의 같은 규칙 두 번, 모델 입력 선별의 세 경우 | 탐지는 고정 문구의 미검토 낮음 보안 finding이고 값·일부·hash는 어디에도 남지 않는다. entropy와 열은 원시 stdout에만 있고 관찰·보고서에는 없다. 식별은 규칙·경로·고정 문구다. 인라인 allow와 저장소 `.gitleaks.toml`은 효과가 없고 `.gitleaksignore`는 digest와 함께 따른다. 사본 밖을 가리키는 항목은 값을 기록하지 않고 작업을 거부한다. 비밀값 작업이 실행되지 않으면 모델 입력은 거르지 않되 제약을 남긴다. 실행했지만 쓸 수 없으면 검토를 건너뛴다. 성공하면 탐지된 파일을 모델 입력에서 뺀다. ruff S105·bandit B105와 합치지 않는다. | REQ-008,011,013,015,031; NFR-006 |
| TC-119 | 적대적 저장소와 비밀값 부재 종단: `--help`·`-x` 이름의 경로, 작업 사본 밖 canary를 가리키는 링크, 사본 밖 결과, 실제 세 도구를 쓰는 워커·agent·controller·PostgreSQL 실행 | 저장소 경로는 옵션으로 읽히지 않고 링크는 따라가지 않는다(`workspace_link_refused`). 사본 밖 결과는 버리고 작업은 실패한다. 생성 비밀값과 canary는 stream 산출물, envelope, 보고서, 코드 포함·제외 JSON·HTML·Markdown, 로그, DB 행, 노드 scratch 어디에도 없다. | REQ-008,015,031; NFR-006 |

분석기 계약과 도구별 규칙은 [분석기](ANALYZERS.md)에 있다. 실도구 시험은 도구 경로 환경변수가 있을 때만 실행하고, CI에서는 캡처한 실제 출력을 재생하는 시험이 같은 판정 경로를 검사한다([시험 실행 방법](#시험-실행-방법)).

## 도구 탐지

도구를 찾는 규칙은 [도구 찾기](ANALYZERS.md#도구-찾기)다. 시험은 임시 디렉터리에 만든 가짜 도구 트리(버전 명령에 답하는 셸 스크립트)로 하므로 이 머신에 무엇이 설치되어 있는지와 무관하다. 실제 머신 시험은 `CODEYE_TEST_REAL_TOOLS=all`(또는 쉼표로 나눈 도구 이름)을 줄 때만 돈다.

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-176 | 모든 도구를 문서가 정한 위치에 둔 가짜 머신: PATH, `~/.local/bin`, `~/.opencode/bin`, CODEYE 도구 접두사(`bin`, `cpp/bin`, `go/bin`), `node_modules`의 진입 스크립트(도구 접두사와 node 설치 옆), rustup 기본 toolchain과 PATH의 rustup proxy, PATH의 npm shim | 18개 도구가 각 위치에서 경로·버전·`source`와 함께 `found`다. cargo·clippy는 proxy가 아닌 toolchain 디렉터리, JS 도구는 shim이 아닌 진입 스크립트다. 빈 머신은 모든 도구가 `not_found`이고 추가 방법이 있으며 쓸 수 있다고 세지 않는다 | REQ-004,030 |
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
| TC-79 | 세션 검증, 잘린 JSONL, 잘못된 완료 사유·marker, 미설정/미지원 버전, 모델 예산 경합·재시작 | 유효하지 않은 출력 미게시. relay 시험은 합성 서버를 쓴다 | REQ-026,030; NFR-006,008,010 |
| TC-85 | **실모델**. 문서 없는 코드에서 맥락 → 후보 조사 | 사실과 후보를 받고 소스 근거 ID를 확인한 뒤 저장 | REQ-004,005,009,027 |
| TC-86 | **실모델**. README가 있는 같은 코드, 별도 비밀값 fixture | 허용한 발췌와 참조만 처리하고 비밀값 fixture는 제외 | REQ-004,005,009,027 |
| TC-87 | 최초 50줄에 없는 상위 호출자의 파일 정리와 테스트 | 검증기가 이를 찾아 후보를 기각한다. 합성 응답과 실제 검증기 호출(**실모델**) 두 경로 | REQ-010,011; TC-10 |
| TC-88 | 다른 run·attempt·snapshot, 허위 path·행·blob·근거 ID, 중복 판정, 근거 없는 기각, 모델이 단정한 재현·critical 심각도 | 거부하거나 미확정으로 기록. 이전 기록 형식과 호환 | REQ-011,013,027; TC-11 |
| TC-89 | 형식 복구 성공·재실패, 일부 후보 격리, 취소·권한 거부·timeout·요청 구성 전 실패, 실제 워커의 verifier timeout 주입 | 각 실패를 구분한다. 워커 경로에서 `partial` manifest, 성공한 도구 관찰, `timed_out` 검증기 기록이 남는다 | REQ-012,026; NFR-008,010; TC-12 |
| TC-97 | 문서 부재, 루트와 하위 문서의 충돌, 읽기 예산 초과, 문서 내 권한 변경 요구 | 네 경우를 구분한다. 모델은 제공된 근거 ID만 인용하고 권한·도구 설정은 유지된다. 유효한 모델 산출물만 다음 역할로 넘어간다. | REQ-005,009,011,027 |
| TC-123 | C/C++·Python·JS/TS·Rust 단일 언어 스냅샷과 다언어 스냅샷, data·vendor·중첩 산출물 디렉터리, build 파일만 있는 생태계, 비 Go 파일의 비밀값 탐지와 쓸 수 없는 탐지, 기존 Go fixture | review-v1 코퍼스는 진입점·문서·소스·manifest·테스트 순으로 그 언어 코드를 담는다. review-r1 조사는 생태계별 round-robin 발췌를, 해석은 소스 위치의 줄 창을 받고 verify가 실행된다. 기록은 경로로 정한 종류로 decode되고 같은 신원의 두 취득에서 요청 바이트가 같다. 제외 디렉터리와 범위 밖 생태계 manifest는 입력이 아니고, 그렇게 빠진 범위 안 소스는 `source_in_excluded_directories`로 남는다. 중첩 index 파일은 진입점이 아니다. 실제 worker와 기본 보고서 registry가 Python 기록을 받는다. 비밀값 선별은 새 발췌에도 적용되고 쓸 수 없는 탐지면 요청이 없다. Go 요청은 변경 전 캡처와 같다. 실모델 품질 근거가 아니다. | REQ-004,009,027; TC-100,118 |
| TC-128 | review-r1 2.0.0. 거부된 출력 뒤 JSON 아닌 복구 응답, JSON 아닌 응답 연속 두 번, 두 번 거부, verify에서 관찰 ID가 섞인 항목·관찰 ID만으로 지지한 항목·유효 근거 옆의 지어낸 ID, 후속 호출 timeout, 3회 호출 기록의 보고서 경로 | JSON 아닌 응답은 복구와 별도로 한 번 재시도하고 앞 오류 코드를 유지한다(단계당 최대 3회). 연속 두 번이나 두 번 거부면 2회에서 실패하고 격리한다. 요청의 관찰 ID만 빼고도 판정 규칙을 통과하는 항목만 받아들이고 `evidence_id_dropped`를 남긴다. 지어낸 ID는 거부한다. 후속 호출 실패는 수락한 판정을 유지하고 나머지를 격리한다. 3회 호출 기록이 보고서 검증과 두 schema를 통과한다. 합성 입력만 쓴다. | REQ-011,013; NFR-008 |
| TC-129 | review-r1 2.0.0 기본 팩. 팩 미지정·`review-v1`·모르는 팩의 에이전트·워커·개인 설정, 기본 회귀 집합(`fixtures/regression/`)의 실모델 실행(**실모델**) | 미지정이면 r1의 다섯 역할을 계획하고 `review-v1`은 세 역할, 모르는 팩은 사용 오류다. v1 요청 바이트는 그대로다. 회귀 실행은 [평가](EVALUATION.md)의 사전 기준으로 판정하고 차단 조건이 없어야 한다. 품질 우월의 근거가 아니다. | REQ-010,027; NFR-007,010; TC-100 |

모듈 단위 검토 분할([모델 검토](REVIEW.md#모듈-단위-검토-분할)):

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-160 | 지원 언어 소스 없이 문서·manifest만 있는 스냅샷, 모델 입력이 될 파일이 없는 스냅샷, 범위 밖 파일, gitleaks 미설정 | 모델 입력 파일(지원 언어 소스·안내 문서·모듈 manifest)이 범위에 있으면 gitleaks를 계획하고 아니면 계획하지 않는다. 미설정이면 `unsupported`(`tool_not_configured`)로 공백이 보인다. 비밀값 작업만 있는 보고서도 `static_analysis_not_planned`다. | REQ-009; NFR-008 |
| TC-161 | 무작위 트리와 항목 순서, 400파일 한도 사례, `fixtures/modules/slices/input` | 적격 파일마다 overview 접두·slice·계획 안 함 중 정확히 한 곳이다. slice는 8개·12,000바이트(상한 기준)·모듈 8개 이하이고 32개를 넘지 않으며, 순회 순서로 너비가 깊이보다 먼저다. 항목 순서가 바뀌어도 계획이 같다. coverage fixture의 계획은 입력 밖의 기대값(`expected-plan.json`)과 같다. | REQ-009,027 |
| TC-162 | 0~4,000바이트의 무작위 파일(JSON이 escape하는 문자·제어 문자·다바이트 문자) | 파일 상한 `min(2400, 6 × size + 2)`는 실제 JSON 발췌 바이트보다 작지 않다. | REQ-009 |
| TC-163 | 회귀 집합과 기존 fixture처럼 모든 적격 파일이 overview에 들어가는 저장소 | slice와 `review.scope`를 계획하지 않는다. 계획·요청 바이트·기록이 이전과 같다(`TestGoReviewInputsUnchanged`의 요청 hash 줄, `TestRolePackExampleRecords`, `TestLegacyPackIsPinned`). | REQ-009,027; NFR-007 |
| TC-164 | 기록 0.3과 범위 산출물 0.1: slice 없는 0.3, slice 있는 0.2, 범위 밖 key, 모듈 9개, interpret 단계·후보, 0.2 기록의 slice upstream, 두 곳에 있는 파일, 정렬되지 않은 `offered`, `offered`와 다른 `covered_by_overview`, 모듈 수 불일치, 목록 밖 선별 상태, 제한 코드 없는 `not_screened` 산출물, 상한 208 | Go decoder와 JSON schema·`validate_bundle.py`가 모두 거부한다. 0.2 기록의 바이트는 그대로다. 예시는 coverage fixture로 다시 만든 바이트와 같다. | REQ-011,027 |
| TC-165 | 상한 15·99(기본)·207, task마다 1회와 3회 호출하는 가짜 upstream | 계획 key는 상한과 무관하다. attempt의 호출은 상한 이하다. 승인된 slice는 계획의 앞부분이고 각자 남은 호출이 6 이상일 때 승인됐다. 거절된 slice와 짝 verify는 호출·기록 없이 `budget_exhausted`(`review_budget_exhausted`)다. 1회 호출이면 최악 사례 사전 승인보다 많은 slice가 실행된다. | REQ-012,027; NFR-007 |
| TC-166 | 비밀값 작업 없음·미실행·실패·완료(보고된 파일이 slice에 있음), 판단 순서 | 선별 작업이 없거나 미실행이면 `review.scope`와 slice가 실행되고 범위 산출물은 `not_screened`, 범위와 slice 기록은 `model_input_not_secret_screened`를 싣는다. 선별이 실패하면 전체 역할처럼 `review.scope`와 모든 slice가 `secret_screen_failed`이고 요청과 범위 산출물이 없다. 보고된 파일은 범위 산출물에서 `withheld`이고 어떤 요청에도 없다. 파일이 남지 않은 slice는 상한이 다해도 `no_review_scope`다. 승인 없음 → 선별 실패 → 범위 산출물 없음(`dependency_failed`) 순서이고, 미선별은 관문이 아니다. | REQ-009; NFR-008 |
| TC-167 | coverage fixture와 다섯 언어 최악 사례, 상한 207 | 어떤 코드·테스트 파일도 두 investigate 요청에 가지 않는다. slice 요청의 지침은 overview와 바이트가 같다. slice 기록은 0.3이고 범위 산출물에 묶인다. run이 보낸 파일 수가 overview보다 많다. 다섯 언어 최악 사례의 모든 slice 요청이 40,000바이트 안이고 다섯 언어 지침을 싣는다. | REQ-009,027 |
| TC-168 | 진행 단계, 실제 worker(상한 15, slice 요청 중 취소), 노드 프로토콜과 PostgreSQL | `no_review_scope`는 단계를 낮추지 않고 `budget_exhausted`·`secret_screen_failed`는 `code_review`를 `partial`로 남긴다. 실제 worker는 15회 이하로 끝나고 보고서가 만들어지며 AI 검토는 수행이다. 취소는 정리를 확인하고 이후 slice 요청이 없다. 서버는 slice key와 queued·running에서의 `budget_exhausted`를 받고, 계획 revision은 1이며, slice 실행 중 취소로 run이 `cancelled`가 된다. | REQ-012,018; NFR-006 |
| TC-169 | 코드 분석 절의 모듈별 검토 장부: coverage fixture를 상한 15로 실행한 보고서(`examples/report-slices.json`), 장부·slice 발견·호출 `task_key`를 하나씩 어긴 보고서, builder 5 리비전, 비밀정보 검사가 없는 기본 상한 실행(`internal/report/testdata/report-unscreened.json`), 범위 계획 밖 파일이 생기는 200개 Go 파일 저장소 | slice 기록과 범위 산출물을 거부 없이 읽는다. slice 발견은 `review_sNNN_<후보>`로 코드 분석 분할에 들어간다. 모듈 행의 여섯 수 합이 대상 파일 수이고, 보낸 파일 합은 `llm_exposed_files` 이하이며 overview보다 많다. `calls_used`는 실제 호출 수(10)이고 상한 이하다. 상한으로 거절된 slice의 파일이 `files_budget_exhausted`와 공백 `review_scope_budget_exhausted`로 보인다. Go 검증·JSON schema·`validate_bundle.py`·웹 파서가 어긴 보고서를 거부하고, builder 5 리비전은 장부 없이 유효하다. 코드 미포함 projection은 장부를 바꾸지 않는다. Markdown·HTML·웹 화면이 같은 수를 보인다. 검사가 없어도 범위 산출물이 수락되고 모든 모듈 행과 slice 발견이 보이며, `model_input`은 `not_screened`이고 공백 `model_input_not_screened`가 보인다. 계획 밖 파일은 `files_not_planned`와 공백 `review_scope_not_planned`로 보인다. | REQ-012,027 |

역할·입력·복구 규칙은 [모델 검토](REVIEW.md)에 있다.

모델 설정([모델 검토](REVIEW.md#모델-설정)):

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-182 | 모델 ID(`openai/…`, `vendor/model` 중첩, 공백·`..`·대문자 provider), relay 프로필, 키 파일 provider, 키 파일 형식(CODEYE·OpenCode `auth.json`의 api·oauth 항목, 다른 provider, 권한 0644, 여러 줄 키, 모르는 필드, 없는 파일), 호출 사이에 바뀐 키 | `PROVIDER/MODEL`만 받고 `codeye-gateway/*`는 relay에만 쓴다. relay의 `OPENCODE_CONFIG_CONTENT`는 바이트가 이전과 같다. 키는 호출마다 다시 읽고, 쓸 수 없는 키 파일이면 요청 없이 `unsupported`(`model_credential_unavailable`)다. 키는 프로필에만 있고 기록된 명령·표준 출력에서 가려진다(`TestProviderKeyOnlyInProfile`, `TestCredentialReadAtCallTime`). | REQ-026,030; NFR-006 |
| TC-183 | 개인 모드: 키 환경변수(`OPENAI_API_KEY` 등) 하나, 같은 키 재기록, 상태 키 파일·`agent.model.credential_file`·OpenCode `auth.json`의 순서, 이름 없는 설정·이름만 있는 설정·OpenCode 설정의 `model`, 키 없는 provider, OpenCode 없음, `model_review: false`인 root | 키 파일은 0600(디렉터리 0700)이고 같은 키면 다시 쓰지 않는다. 기본 모델은 `openai/gpt-6.2-sol`이다. 에이전트 인자에는 키 파일 경로만 있고, `model_review: false`가 아닌 root만 `--model-source-root`를 받는다. 모델을 정하지 못하면 모델 없이 시작한다. probe 기록에 키가 없다(`TestModelSecretFromEnvironment`, `TestModelResolutionOrder`, `TestPersonalModelFromSecret`). | REQ-026; NFR-006 |
| TC-184 | 실제 OpenCode와 가짜 upstream(OpenAI provider에 `base_url`만 바꿈)으로 개인 모드 start → `codeye model probe` → 로컬 Go 프로젝트 run. 같은 키 파일을 401 upstream으로 바꾼 run, `--version`만 1.99.0인 wrapper로 재시작한 run(`scripts/test_postgres.py --personal-model`) | probe가 provider·모델·버전과 성공을 출력하고 검토 단계가 수락돼 `ai_review: performed`다. 401이면 probe가 `FAILED at request: model_auth_failed`이고 run은 정적 분석을 마치며 보고서(JSON·Markdown)가 미수행과 사유를 적는다. wrapper run은 검토를 수행하고 한계 `opencode_version_untested`를 싣는다. 키가 키 파일 밖의 상태 파일·로그·산출물·보고서·DB dump·CLI 출력·모든 프로세스 argv에 없다. 가짜 upstream 결과는 경로 검증이며 검토 품질의 근거가 아니다. | REQ-026; NFR-006,008 |

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
| TC-131 | 절 도입 전후의 같은 사실: 기존 필드, builder 4 기준과 builder 5 현재 비교, builder 1 예시(`report-go-vet.json`) | 기존 필드(`observations`, `findings`, `coverage`, `summary`, `tasks`, `limitations`)가 바이트 단위로 같다(절 도입 때 report 패키지 시험 입력 84개의 builder 4 출력과 한 번 대조했다. 상시 시험은 아래 golden과 비교다). builder 차이 외에는 4-4 비교와 같은 결과다. builder 1 예시는 그대로 검증되고 Markdown·HTML이 이전 출력과 바이트 단위로 같으며 웹은 평면 배치로 보인다. | REQ-012,028; TC-82 |
| TC-132 | 문서 계약·충돌·질문과 환경 메모에 코드 같은 문장과 비밀값 같은 문장을 심은 기록, 코드 미포함 projection | projection은 모델 문장을 생략 문구로 바꾸고 버전 제약이 아닌 요구 문자열을 지운다. 코드 미포함 보고서에 문장이 남거나, 어느 보고서든 비밀값 같은 문장이 있으면 검증이 거부한다. 수락된 역할 기록의 문장만 오고 절 근거 목록에 없는 근거 ID는 거부한다. | REQ-012,013,031; NFR-006 |
| TC-133 | 모델 검토 없는 실행(정책 생략·미계획)과 실행하지 않은 분석기 | 환경·문서·코드 분석 절의 모델 상태가 `not_performed`와 사유 코드이고 모델 문장이 없다. 실행하지 않은 도구 행은 "실행하지 않음"으로 보이며 어느 형식에서도 0건으로 읽히지 않는다. 판정과 상태 규칙은 바뀌지 않는다. | REQ-012,013; TC-81 |
| TC-134 | 예시 보고서와 실제 게시 보고서의 세 형식과 웹 화면(실제 Chromium 포함) | 종합이 먼저 오고 분석 종류별 절이 순서대로 하나씩 있다. 절별 수가 JSON 종합 블록, Markdown 표, HTML `data-count`, 웹 화면에서 같다. 웹 절은 키보드로 열 수 있고 종합의 버튼이 해당 절을 연다. 상태는 글자와 기호로 보인다. 모델 문장에는 "모델 해석" 표시가 붙는다. | REQ-012,013; NFR-005; TC-95 |

보고서 계약은 [보고서와 화면](REPORT.md)에 있다.

## 이력·비교

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-14 | 이전 finding이 이번 도구 실패로 안 보임 | resolved가 아니라 absent_unverified | REQ-028,029 |
| TC-104 | 각 분석기 팩. 이전 run에 있던 도구 finding이 사라짐: 규칙 선택·규칙 팩 버전·저장소 설정 파일 변경, 규칙 문맥이 없는 이전 builder 보고서, 소스 안 억제 주석 추가, 억제를 보고하지 않는 도구 | 양쪽 규칙 문맥이 알려지고 같을 때만 `resolved_observed`다. 그 밖은 `absent_unverified`와 `rule_context_unknown`·`rule_context_changed`·`suppressed_in_source`·`source_suppressions_hidden`이다. 억제된 진단은 관찰로 남고 finding이 아니다. go vet 문맥은 합성되어 이전 builder와도 해결이 유지된다. 코드 제외 export에는 설정 경로·digest가 없다. | REQ-028; TC-14,31 |

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
| TC-100 | 지침 교체 시. 동일 스냅샷·모델·예산에서 활성 팩과 후보 팩을 평가. 기본 팩 하나를 개정할 때는 기본 회귀 집합(`fixtures/regression/`)을 사전 기준으로 실행하고, 새 팩 id로 기본값을 바꿀 때만 두 팩을 비교한다(A33) | 역할별 digest·호출 내역과 평가 분모를 보존한다. Q08·Q10의 계약 밖 입력, 수정 전 결함, 호출자가 정리하는 반례를 분리 채점한다. 허위 근거·모델 재현 단정·통계 오염은 기본 팩 승격을 막는다. | REQ-010,011,013,027; NFR-007,008,010 |
| TC-120 | 정답 차단: 정답 목록 생성, relay `--forbidden-phrases`, 줄바꿈으로 나뉜 문구·camelCase 토큰·대소문자가 다른 부분 문자열을 심은 요청, 입력·지침에 정상으로 나오는 표현, 목록 없이 시작한 relay | 목록은 어떤 사례·변형·README 입력에도 없는 라벨 문구·고유 토큰만 담는다. 일치 요청은 예약·전달·차감 없이 403 `blocked_answer_leak`이고 장부에는 항목 ID·역할 표지만 남는다. 정상 표현은 통과한다. 형식이 틀린 목록이면 relay가 시작하지 않는다. 선택 옵션 `--allow-model-echo`는 relay가 돌려준 답에 나온 ID만 예외로 기록하고, runner는 그 ID가 같은 사례의 모델 출력에 없으면 `answer_echo_unverified`로 뺀다. runner는 relay가 이 목록을 현재 corpus digest로 읽지 않았으면 review batch를 시작하지 않고, 차단된 사례는 `invalid`로 지표에서 뺀다. 심은 정답이 막히지 않으면 `planted_leak_not_blocked`다. | REQ-013,027; NFR-010 |
| TC-121 | 평가 실행·예산: `tools`·`review` 조건, 층별 표본과 seed, 반복, holdout 등록부, 상한 추정이 남은 예산·요청 수를 넘는 batch, batch 중간의 relay 예산 거절, 시험용 upstream | 사례마다 실제 controller·agent·분석기 경로로 실행하고 manifest에 팩·역할 digest, corpus digest, 모델, OS, 도구 버전·플래그, 예산, 시각, seed를 남긴다. 같은 seed는 같은 표본이다. 이전 다른 digest의 실제 실행이 있으면 `holdout_seen_by_prior_revision`을 표시한다. 시작 전 거절은 사유를 남기고 아무 사례도 실행하지 않는다. 중간 거절은 그 사례를 무효로, 남은 변형을 `not_run`으로 기록하고 멈춘다. 시험용 upstream은 loopback URL만 받고 키를 읽지 않으며 실제 장부와 섞이지 않는다. | REQ-006,013,018,027; NFR-010 |
| TC-122 | 채점: 위치 pre-fill, 판정 양식, 미판정·판정 충돌, 판정된 단위의 지표와 Wilson 95% 구간, 언어·범주·detection 분해, 조건 비교, 반복 일치도, 보고서 읽기 답 없음·있음 | 자동 위치 대조는 판정이 아니며 구조 지표로만 보고한다. 비율의 분모는 판정된 단위이고 미판정 수를 함께 낸다. 충돌한 판정은 미판정이다. 비용·시간과 정확도를 한 점수로 합치지 않는다. 표시된 batch나 dry run이 섞인 비교는 `independent: false`다. 근거 유효성은 새로 만든 입력과 대조한다. 검토 결과는 task 상태로 분류하고, 비밀값 선별 실패(`secret_screen_failed`)는 모델 실패와 따로 센다. 코드 노출은 보고서의 파일 수가 아니라 검토 기록의 근거 종류로 센다. 보고서 읽기는 사람의 답이 없으면 `not_measured`이고 모델로 독자를 흉내 내지 않는다. | REQ-011,012,013,027; NFR-010 |

평가 방법과 보류 데이터 규칙은 [평가](EVALUATION.md)에 있다.

## 운영

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-30 | **설계(미구현)**: 폐쇄망 배포팩. 폐쇄망에서 없는 패키지·모델·외부 CDN 요청 | 외부 fallback 없음, 명확한 부분 실패, UI는 로컬 자산 사용 | REQ-030 |
| TC-32 | DB+artifact backup 복원 | 근거·ACL·예약 중복·lease 재조정 정상 | NFR-002,003,006,007 |
| TC-124 | 저장 영역(snapshot·workspace·cache·logs·artifacts) 상한에 도달한 노드·설치, 실행 중 attempt, 상한 없는 heartbeat | 새 claim만 거절하고(204) 대기 사유는 `storage_quota_exceeded`다. 실행 중 attempt와 확정 산출물·보고서는 그대로다. 자동 삭제는 없다. 상한이 없으면 heartbeat 필드를 보내지 않는다. 측정값과 적용 근거(노드 heartbeat·DB 집계)를 구분해 표시한다. | REQ-018; NFR-007 |
| TC-125 | `codeye start`·`stop`·`status`: 실행 중 작업의 drain, drain 시간 초과, stale 기록·재사용 pid·기록 없는 에이전트·포트 점유, 설정 오류 | drain은 실행 작업을 끝낸 뒤 추가 claim 없이 종료한다. 시간 초과는 취소하고 정리를 확인한다. 불확실하면 `recovery_required`다. 기록과 시작 시각이 다른 프로세스에는 신호를 보내지 않는다. 상태 파일은 0600·0700이고 DB URL과 장기 secret이 없다. | REQ-021,022; NFR-006 |
| TC-126 | `codeye doctor --config`: 상한 초과 영역, 백업 없음·오래됨·읽을 수 없음, 불일치 상태, 진행 중 start/stop | 선택 필드 `operations`만 더하고 기존 check는 같다. schema가 상태와 필드 조합을 강제한다. | REQ-018; NFR-006 |
| TC-127 | 개인 사용 경로: 시작, 프로젝트 추가, Go 분석, 정지, 백업, 새 DB·산출물 디렉터리로 복원, 복원본 시작, 보고서·근거 확인 | 모든 단계가 실제 바이너리로 끝난다. 복원본의 보고서·근거 hash·사람 판정이 원본과 같다. 안내서의 명령은 모두 실행한 것이다. | REQ-012; NFR-003 |
| TC-170 | 설정 파일도 DB도 없는 상태에서 `codeye up --no-open <Go 폴더>`(PostgreSQL 프로그램은 PATH 탐색), 환경에 `CODEYE_DATABASE_URL`·`PGHOST`·`PGPASSWORD` | `schema_version`과 `source_roots`만 담은 0600 설정 파일이 생긴다. 상태 디렉터리에 cluster가 초기화되고(비밀번호 파일 0600) 127.0.0.1의 55432 이상 포트에서 뜬다. schema가 만들어지고 프로젝트가 등록되고 run이 보고서와 함께 끝난다. 출력에 환경 변수를 무시했다는 알림과 `#launch=…&project=…&run=…` 링크가 있고 DB URL·셸의 비밀번호는 없다. `status`의 `database.postgres`는 `running`이다. | REQ-001,004; NFR-006 |
| TC-171 | TC-170이 출력한 링크를 Chromium으로 연다 | 한 번에 로그인되고 주소에 토큰이 남지 않으며 `project`만 남는다. 그 프로젝트와 현재 실행이 열려 있고 보고서를 연다. 세션 만료는 6일 뒤보다 늦다(7일). 웹 단위 시험: 깊은 링크는 canonical UUID인 `project`·`run`만 남기고 나머지는 지운다. | REQ-001; NFR-006 |
| TC-172 | 실행 중에 다른 폴더로 `codeye up`, 설정의 root 아래 폴더, 상태 디렉터리를 포함하는 폴더, Git URL | 새 폴더는 root로 설정에 더해지고 에이전트가 drain 뒤 다시 등록되며 그 run도 보고서로 끝난다. root 아래 폴더는 그 root와 상대 경로를 쓰고 설정을 바꾸지 않는다. 데이터 디렉터리를 포함하는 폴더는 거부한다. Git URL은 아무것도 쓰거나 띄우지 않고 clone 안내와 종료 코드 2다. | REQ-001,015 |
| TC-173 | `codeye stop` 뒤 `codeye up`, 정지 상태의 `codeye backup`(설정에 `backup_dir` 없음), 새 `state_dir`로의 `codeye restore` | stop의 `database`는 `stopped`이고 그 cluster의 PostgreSQL 프로세스가 없다. 다시 up하면 초기화·사용자 생성 없이 같은 프로젝트가 보인다. 백업은 `<state_dir>-backups` 아래에 세트를 만들고 띄운 PostgreSQL을 다시 멈춘다. 비밀번호는 `postgres/password` 밖의 상태 파일에 없다. 복원은 새 상태 디렉터리에 cluster를 만들고 복원하며 그 뒤 up이 새 노드로 시작한다(복원은 수동 실행으로 확인, smoke에는 없다). | NFR-003,006 |
| TC-174 | PostgreSQL 프로그램 디렉터리: 16·17·13 설치, 프로그램이 빠진 디렉터리, 명시한 틀린 경로, 아무것도 없음, 주 버전 16 데이터 디렉터리와 17 프로그램, `PG_VERSION` 없는 비어 있지 않은 디렉터리 | 명시가 없으면 가장 높은 주 버전(14 이상), 데이터 디렉터리가 있으면 그 버전을 고른다. 플래그가 설정보다 먼저다. 명시한 경로가 조건을 채우지 않으면 다른 곳을 찾지 않고 그 경로와 이유를 말한다. 없으면 설치 방법·`postgres_bin_dir`·`external`을 알리는 오류다. 다른 주 버전 데이터 디렉터리와 정체 모를 디렉터리는 거부한다. | REQ-004; NFR-006 |
| TC-175 | `{"schema_version":"0.1"}`만 있는 설정, `up`의 설정 편집(필드 순서·기본값·잘못된 결과), 기본 포트 7331을 다른 프로그램이 쓰는 상태, 설정에 적은 포트를 다른 프로그램이 쓰는 상태 | 기본값은 관리형 DB, `$XDG_STATE_HOME/codeye`(없으면 `~/.local/state/codeye`), root 없음이다. 편집은 다른 필드와 순서를 보존하고 기본값을 쓰지 않으며, 검증을 통과하지 못하면 파일을 바꾸지 않는다. 기본 포트가 쓰이면 7332 이후 빈 포트로 띄우고 `stop`·`status`·`open`이 그 주소를 쓴다. 적은 포트가 쓰이면 시작하지 않는다(포트 이동은 수동 실행으로 확인). | REQ-001; NFR-006 |

개인 모드 운영 계약은 [운영](OPERATIONS.md)에 있다. `up`·`start`·`stop`·`status`·백업·복원 시험은 Linux 전용이다. 브라우저 자동 열기(`xdg-open`·`wslview`·`explorer.exe`)는 시험하지 않았다(열기 프로그램 선택 순서만 단위 시험). 여러 사용자 운영의 수용(장애 훈련, 권한 철회, 품질 baseline)은 아직 TC가 없으며 [로드맵](ROADMAP.md)의 운영 수용 작업에서 정한다.

## 연동

| TC | 조건 | 기대 결과 | 요구 |
|---|---|---|---|
| TC-27 | **설계(미구현)**: GitHub App 연결 상태. webhook이 없지만 repository readable | 수동/예약 사용 가능, PR 기능만 연결 미완료 | REQ-024,025 |
| TC-28 | **설계(미구현)**: webhook·댓글 명령. 잘못된 HMAC·중복 delivery·무권한 mention | 요청 차단/멱등 처리, 승인된 사용자만 실행 | REQ-024,025 |
| TC-29 | **설계(미구현)**: PR 결과 댓글. PR head 변경 후 이전 결과 완료·댓글 API 실패 | stale 표기, 최신 댓글 미덮어쓰기, outbox만 재시도 | REQ-024,025 |
| TC-35 | 문서·코드·서비스 설정의 기능 검토 | 기존 CI 연결 및 초기 CVE 기능이 없음 | REQ-032,033 |

TC-27의 수동·예약 분석 부분은 승인된 HTTPS 호스트의 Git 입력으로 지금도 동작한다. 연결 상태 표시와 PR 기능은 없다. 연결 계약은 [GitHub 연결](GITHUB.md)에 있다.

## 요구사항 추적표

`scripts/validate_bundle.py`는 이 표의 `REQ-001,004,005` 형식(쉼표, 공백 없음)에서 ID를 읽어 요구사항 문서의 모든 REQ·NFR이 연결됐는지 검사한다. 여러 번호는 이 형식으로만 적는다.

| 요구 | 주요 문서 | 수용 테스트 |
|---|---|---|
| REQ-001,004,005 | ARCHITECTURE, REVIEW, REPORT | TC-01,05,96,97 |
| REQ-002,015 | ARCHITECTURE, EXECUTION, SECURITY | TC-02,04,17 |
| REQ-003 | ARCHITECTURE | TC-03,04 |
| REQ-006,007,008 | ARCHITECTURE, ANALYZERS | TC-06,07,08 |
| REQ-009,010,011 | REQUIREMENTS, REVIEW, ARCHITECTURE | TC-09,10,11,16 |
| REQ-012,013 | ARCHITECTURE, REPORT | TC-12,13,15,130,134 |
| REQ-014 | ARCHITECTURE, EXECUTION | TC-17,18 |
| REQ-016,017,034 | API, REPORT | TC-26,98,99 |
| REQ-018,019 | ARCHITECTURE, EXECUTION | TC-21,24 |
| REQ-020,021 | EXECUTION | TC-19,20 |
| REQ-022,035 | ARCHITECTURE, EXECUTION, API | TC-18,21,22,23 |
| REQ-023 | EXECUTION | TC-25 |
| REQ-024,025 | GITHUB | TC-27,28,29 |
| REQ-026 | REVIEW | TC-12,34,170,171,172 |
| REQ-027 | REVIEW, EVALUATION | TC-33,97,100 |
| REQ-028,029 | ARCHITECTURE, REPORT | TC-14,31 |
| REQ-030 | SECURITY, OPERATIONS | TC-30,170 |
| REQ-031 | REPORT, SECURITY | TC-15,31 |
| REQ-032,033 | REQUIREMENTS | TC-35 |
| NFR-001,009 | EXECUTION, OPERATIONS | TC-17,18,23 |
| NFR-002,003 | EXECUTION, API, OPERATIONS | TC-21,23,25,32 |
| NFR-004,005 | REPORT | TC-26, [UI 성능](#고위험-시나리오) |
| NFR-006,007 | ARCHITECTURE, SECURITY | TC-02,15,22,31,32,170,171,172 |
| NFR-008,010 | REVIEW, EVALUATION | TC-11,12,24,33,172 |

주요 문서 열의 이름은 `docs/` 아래 같은 이름의 `.md` 파일이다. 이 표는 요구별 대표 시험이며, 나머지 연결은 영역별 표의 요구 열에 있다.

## 고위험 시나리오

**Lease partition:** A가 실행 중인 상태에서 controller 통신을 끊는다. agent가 마지막 갱신 뒤 안전 여유 안에 새 task를 멈추는지 확인한다. 종료 불확실한 test를 자동 중복 실행하지 않는지 확인한다. A의 이전 epoch 완료 요청은 거부되고 새 attempt 결과는 유지돼야 한다. 여러 물리 머신에서의 시험은 다중 노드 검증 작업에 속한다.

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
| `CODEYE_TEST_LIZARD`, `CODEYE_TEST_JSCPD`, `CODEYE_TEST_GITLEAKS` | 구조·중복·비밀값 팩 |
| `CODEYE_TEST_PG_BIN` | 백업·복원 시험의 `pg_dump` 위치(없으면 PATH) |
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
python3 scripts/test_postgres.py --web --nodes --scheduler --personal
```

- 필요: PATH의 `initdb`·`postgres`·`psql`·`go`, `scripts/requirements.txt`의 Python 패키지(jsonschema 등). `--web`은 위 웹 빌드와 Chromium도 필요하다. 기존 DB 서비스를 쓰지 않고 loopback의 빈 포트에 임시 PostgreSQL을 만들고 끝나면 지운다.
- 기본 단계: `CODEYE_TEST_DATABASE_URL`을 준 `go test -race -count=1 ./...`, CLI 빌드, `codeye migrate` 두 번, `codeye doctor` schema 검증과 credential 비노출 확인.
- `--web`: 실제 `codeye serve`에 대한 브라우저 smoke(Playwright·Chromium).
- `--nodes`: 서로 다른 노드 신원 두 개의 Linux Native 수용 시험. `CODEYE_TEST_STATICCHECK`·`CODEYE_TEST_NODE`·`CODEYE_TEST_RUFF` 등 도구 변수가 있으면 실제 도구로도 실행하고, 없으면 해당 smoke를 SKIP으로 출력한다.
- `--scheduler`: 별도 DB에서 실제 controller·autoscaler와 `-tags codeye_fixture`로 빌드한 agent·worker(TC-71~75). fixture handler는 시험용이며 분석 결과가 아니다.
- `--personal`: 개인 모드 start/status/stop, 백업·복원, 복원본 재시작(TC-125, TC-127, TC-32), 관리형 PostgreSQL의 `codeye up` 경로(TC-170~173, 웹 빌드가 있으면 TC-171의 Chromium 확인 포함). 관리형 cluster는 PATH의 PostgreSQL 프로그램으로 만든다. Linux 전용이며 모델 호출·다운로드가 없다.

- `--personal`: 개인 모드 start/status/stop, 백업·복원, 복원본 재시작(TC-125, TC-127, TC-32). Linux 전용이며 모델 호출·다운로드가 없다.
- `--personal-model`: 개인 모드 모델 설정(TC-184). 실제 OpenCode(`~/.opencode/bin/opencode` 또는 `CODEYE_TEST_OPENCODE_BIN`)와 가짜 upstream을 쓰며 유료 호출이 없다. `CODEYE_SMOKE_REAL_MODEL_CREDENTIAL`에 0600 키 파일(`base_url` 없음)을 주면 그 키로 첫 단계(probe와 run 하나, 호출 상한 15)만 실행한다(**실모델**, 비용 발생, relay 장부를 거치지 않으므로 비용은 따로 기록한다).
- `--smokes-only`: Go 시험을 건너뛰고 고른 smoke만 다시 돌린다. 이때 마지막 줄은 저장 통합 시험을 실행했다고 주장하지 않는다.
- `--pilot`: **실모델** 파일럿. 실제 모델 요청을 보낸다. 고정 OpenCode와 승인된 relay가 필요하다. `CODEYE_PILOT_RELAY_FILE`에 relay descriptor, `CODEYE_PILOT_RESULTS`에 새 비공개 결과 디렉터리를 지정한다. `CODEYE_PILOT_CASES`(예: `Q01-before,pilot`)로 대상을, `CODEYE_PILOT_REVIEW_PACK`으로 검토 팩을 고른다(기본 `review-r1`). 보관된 결과는 `python3 scripts/regression_pilot.py --audit-results "$CODEYE_PILOT_RESULTS"`로 다시 대조한다. 이 대조는 위치·줄 범위·blob SHA의 참조 검사이며 주장이 옳다는 보증이 아니다. Git 시험에 기록된 commit 객체가 로컬에 있어야 한다.

### 그 밖의 보조 검사

```sh
python3 fixtures/regression/corpus.py --check                       # 회귀 집합의 결함 전후 계약 시험(모델 없음)
python3 -m unittest discover -s fixtures/cleanup -p 'test_*.py' -v  # 합성 결함 전후
python3 -m unittest discover -s scripts -p test_model_budget_relay.py -v
python3 -m unittest discover -s scripts -p test_quality_eval.py -v
```

### CI가 실행하는 것

[`.github/workflows/verify.yml`](../.github/workflows/verify.yml)은 PR과 main push에서 세 작업을 실행한다. 모델 credential·모델 호출·배포가 없고 저장소 권한은 read-only다(A13).

| 작업 | 실행 내용 |
|---|---|
| Linux / PostgreSQL / contracts (ubuntu-24.04) | Go 1.27.4·Python 3.14.4·Node 24.18.0. 웹 빌드, Playwright Chromium 설치, `gofmt`·`go vet`, `scripts/test_postgres.py --nodes --web --scheduler --personal`, scripts 단위 시험, `validate_bundle.py`, cleanup fixture 시험, `fixtures/regression/corpus.py --check`, CLI 빌드 |
| Windows / Go unit tests (windows-2025) | `go test -count=1 ./...`, `go vet`, CLI 빌드만. DB 통합·smoke·실도구 시험은 없다 |
| Web / tests and build | `npm ci`, `npm test`, `npm run build` |

CI 러너에는 go vet 말고 분석 도구가 없어 실도구 시험은 skip되고 재생 시험만 돈다. Windows CI의 단위 시험은 Windows 11의 NativeExecutor·DB·전체 플랫폼 수용 시험을 대신하지 않는다.

## 보조 검증과 제품 시험의 차이

`scripts/validate_bundle.py`는 문서 링크·schema·예시·계획 DAG·보고서 수치·범위 참조를 검사한다. `fixtures/cleanup/test_cleanup.py`는 합성 결함 전후를 검사하고, `fixtures/regression/corpus.py --check`는 회귀 집합 사례의 계약 시험을 검사한다. 이들의 성공을 TC 전체 통과나 실제 분석 성능 확보로 보고하지 않는다. scripted caller나 fixture handler의 결과는 플랫폼 경로의 근거이지 지침 품질이나 분석 정확도의 근거가 아니다. 코드 시험 결과와 제품 분석 정확도 평가는 별도 결과로 보고한다([평가](EVALUATION.md)).
