# 로드맵

다음에 할 일과 순서, 각 작업의 완료 조건을 정한다. 끝난 작업은 이 문서에서 지우고 변경 기록에 적는다. 아래는 소유자가 승인한 계획이며 완료된 작업이 아니다. 범위와 착수는 소유자가 정한다. 이미 끝난 작업의 이력은 [변경 기록](../CHANGELOG.md), 지금 제공하는 기능은 [요구사항](REQUIREMENTS.md)의 현재 제공 범위에 있다.

## 진행 원칙

- 작업마다 구현·검증·CI 통과·병합까지 끝낸 뒤 다음 작업을 시작한다. 구현 설명만 있고 실제 시험 결과가 없으면 완료로 처리하지 않는다.
- 코드 시험 결과와 제품 분석 정확도 평가는 별도 결과로 보고한다. 경고 수나 모델 호출 수의 증가를 품질 향상으로 발표하지 않는다([평가](EVALUATION.md)).
- 한 머신에서 별도 노드 신원 둘로 한 시험은 여러 물리 머신·NFS·network partition 보증을 대신하지 않는다. 개발에 쓰지 않은 OS(Windows 11, RHEL 8.10)의 시험은 미실행으로 명시한다.
- '다섯 언어 지원'은 탐색·기본·정밀·실행 검증 수준을 나누어 표시한다. 공통 탐색만 된 언어를 정밀 지원이라고 하지 않는다.
- 기존 CI 통합과 의존성 CVE 검사를 어떤 작업에도 끼워 넣지 않는다. 제품 범위 변경은 소유자 결정이 필요하다.
- API·schema를 바꾸는 PR은 호환 가능한 응답·예시·검증 스크립트·migration 필요 여부를 함께 검토한다([시험](TESTING.md), [결정 기록](DECISIONS.md)의 변경 절차).
- 지침 개정은 기본 팩 하나에 하고 기본 회귀 집합(`fixtures/regression/`)으로 검증한다. 보류 평가 자료를 읽지 않는 규칙은 [평가](EVALUATION.md)를 따른다.

## 순서

정리 작업을 먼저 하고, 그다음 여러 사용자·GitHub 연동·분산 운영 작업을 한다. CODEYE는 사내 전용이 아니므로 개인 사용과 GitHub.com 연동을 실제로 쓸 수 있는 수준까지 만든다.

| 작업 | 선행 |
|---|---|
| 리팩토링 | 없음 |
| 보고서 재구성 | 리팩토링 |
| 검토 범위 확대 | 리팩토링 |
| 다중 사용자·권한 | 정리 작업 전체 |
| GitHub 연결 | 다중 사용자·권한 |
| PR·댓글 분석 | GitHub 연결 |
| 다중 노드 검증 | 다중 사용자·권한. GitHub 연결·PR·댓글 분석과 병행 |
| 폐쇄망 배포팩 | 다중 노드 검증 |
| 운영 수용 | PR·댓글 분석, 폐쇄망 배포팩 |

## 정리 작업

### 리팩토링

분석기 팩이 반복하는 처리(도구 경로·버전 probe, 관리 설정 생성, 위치 인자 처리, 작업 사본 밖 결과 제거, 규칙 문맥 기록, 실행 결과 판정)를 공유 helper로 모으고, 지나치게 큰 파일(예: `internal/agent/agent.go`, `cmd/codeye/personal.go`, `internal/report/build.go`, `internal/worker/analysis/cpp/compiledb.go`, `scripts/validate_bundle.py`)을 책임별로 나눈다. 동작은 바꾸지 않는다.

완료 조건:
- 같은 입력에서 보고서 JSON·Markdown·HTML과 모델 요청(프롬프트) 바이트가 변경 전과 같다. `TestLegacyPackIsPinned`와 Go 요청 캡처 비교 같은 기존 고정 시험이 그대로 통과하고, 각 분석기 팩의 재생 시험 산출물(envelope·보고서)도 바이트 비교한다.
- 공개 API·schema·CLI 플래그·DB migration이 바뀌지 않는다.
- Linux 전체 시험(`scripts/test_postgres.py --web --nodes --scheduler --personal`), Windows Go 단위 시험, 웹 시험이 통과한다.

### 보고서 재구성

보고서를 분석 종류별 절로 다시 구성한다.

| 절 | 내용 |
|---|---|
| 종합 | 판정, 주요 발견, 분석 공백 요약, 숫자 |
| 환경 분석 | 모듈·런타임·toolchain 요구와 노드 관측, 불일치 |
| 문서 분석 | 찾은 문서, 없음·충돌·불완전, 문서와 코드의 충돌 |
| 정적 분석 | 언어·도구별 관찰과 finding, 도구 실패·미설정·허가 없음 |
| 구조·중복 지표 | 지표 관찰(결함 수와 분리) |
| 비밀정보 | 탐지 위치와 모델 입력 선별 결과(값 없음) |
| 코드 분석 | 모델 검토의 후보·검증 결과·근거 |
| 분석 공백 | 실행하지 않았거나 실패한 검사와 이유 |
| 이전 실행과 비교 | 새로 생김·관찰로 해결·확인 안 됨(absent≠fixed)·사람 판정 |

완료 조건:
- 모든 숫자는 지금처럼 구조화 데이터에서 계산한다. 모델 서술이 숫자·상태를 바꾸지 않는다.
- JSON·Markdown·HTML과 화면의 같은 revision 숫자가 일치한다(보고서 수치 검사 통과).
- 보고서 schema·builder 버전 변경과 이전 revision 읽기 호환을 [결정 기록](DECISIONS.md)에 남기고, 이전 보고서의 비교 규칙이 바뀌지 않는다.
- 코드 제외 내보내기의 제거 규칙이 새 절에도 적용된다.
- 실제 Chromium에서 각 절을 열고 내보내기와 대조한다.

### 검토 범위 확대

지금 모델 검토는 한 run에서 적은 수의 파일만 본다. `review-r1` 조사 역할은 코드 파일 최대 8개·12,000바이트(`roleMaxCodeFiles`·`roleMaxCodeBytes`, [`internal/worker/review/rolepack_inputs.go`](../internal/worker/review/rolepack_inputs.go)), `review-v1` 코퍼스는 최대 6개(`maxFiles`, [`internal/worker/review/context.go`](../internal/worker/review/context.go))이고, 요청마다 40,000바이트 한도(`PromptBudget`, [`internal/worker/review/rolepack.go`](../internal/worker/review/rolepack.go))가 있다. 모듈 단위로 나누어 검토해 모델이 저장소의 더 많은 부분을 보게 한다.

완료 조건:
- 모듈마다 검토 task를 계획하고, 각 task의 입력 범위(모듈·파일·바이트)와 잘린 범위를 기록한다.
- 보고서와 범위 원장이 검토한 모듈·파일과 검토하지 않은 범위를 구분한다. 모델에 보낸 파일 수를 검증 범위라고 계산하지 않는다.
- 호출 수·비용 상한을 실행 전에 정하고 초과하면 남은 모듈을 미검토로 남긴다.
- 기본 회귀 집합으로 회귀를 확인하고, 품질 향상 주장은 새 보류 사례로만 한다.

## 여러 사용자·GitHub 연동·분산 운영

### 다중 사용자·권한

관리자가 관리하는 로컬 계정과 GitHub 로그인을 지원한다. 회사 SSO는 확장 지점만 두고 구현하지 않는다. 역할은 운영자·프로젝트 관리자·실행자·열람자다([요구사항](REQUIREMENTS.md)의 사용자 역할).

완료 조건:
- 프로젝트·source root·run·artifact·finding·export·SSE의 사용자 간 격리 시험. 버튼 숨김이 아닌 API의 ACL로 거부한다(TC-02, TC-31의 다중 사용자 부분).
- 권한 철회가 진행 중인 SSE와 다음 요청에 바로 적용된다.
- 감사 기록: 등록·설정 변경·실행·취소·내보내기·판정 변경, 노드 등록·토큰 회전, 실행 정책 변경, 오래된 epoch 거부, 민감 export([보안](SECURITY.md)).
- 원격 공개 시 인증·TLS를 필수로 검사하고 무인증 `0.0.0.0` 실행을 기본값으로 두지 않는다.
- 서버 실행 계정의 NFS 권한이 웹 사용자에게 자동 승계되지 않는다.

개발 머신에서 검증할 수 없는 것: 회사 SSO 연동.

### GitHub 연결

실제 GitHub.com App으로 연결한다. 이벤트는 webhook 수신과, 외부에서 들어올 주소가 없는 호스트를 위한 polling 방식 둘로 받는다. GHES는 같은 계약을 따르지만 시험하지 않은 상태로 둔다. 계약은 [GitHub 연결](GITHUB.md)에 있다.

완료 조건:
- 연결 가이드와 최소 권한(내용 읽기, PR 읽기, 결과 댓글 쓰기). Checks/Actions 권한을 요구하지 않는다. 플랫폼이 Git 권한을 자동으로 바꾸지 않는다.
- 연결 상태 `repository_readable`·`branch_resolvable`·`webhook_verified`·`pr_readable`·`comment_writable`·`report_url_reachable`을 각각 진단한다. webhook이 없어도 읽기가 되면 수동·예약 분석이 동작한다(TC-27).
- webhook: 크기·시간 제한, constant-time HMAC 검증, provider+delivery ID dedupe(같은 ID 다른 payload는 거부·감사), durable 저장 뒤 비동기 2xx 응답(목표 2초), 큐 저장 실패를 성공으로 응답하지 않음(TC-28).
- polling(제안, 아직 명세 없음): webhook과 같은 dedupe와 작업 의도 저장을 거치고, 놓친 이벤트를 무제한 따라잡지 않는다. 주기·조회 범위·rate limit 처리는 이 작업에서 명세한다.
- App private key와 게시 credential은 관리 서버만 갖는다. 노드에는 저장소·기간이 제한된 읽기 credential만 주고 checkout 뒤 제거한다. 다른 호스트 인증으로 fallback하지 않는다.

개발 머신에서 검증할 수 없는 것: 실제 GHES 인스턴스(버전·허용 API·댓글 API 권한 차이).

### PR·댓글 분석

PR 이벤트와 권한 있는 사용자의 댓글 명령(`@codeye analyze`, `@codeye analyze deep`, `@codeye analyze tests`)으로 분석하고 결과 댓글을 게시한다.

완료 조건:
- PR number·base SHA·head SHA·repository ID를 고정한다. 변경 범위를 중심으로 보되 필요한 호출부는 전체 snapshot에서 읽고, 관련 경로를 읽었다고 전체 저장소를 같은 깊이로 검토했다고 하지 않는다.
- 댓글 작성자의 저장소 권한과 CODEYE 실행 권한을 모두 확인한다. 인용문·코드 블록 안 명령과 봇 자신의 댓글은 무시한다.
- 외부 fork·신뢰하지 않는 기여자의 PR은 Native 실행을 자동 승인하지 않는다(승인 대기 또는 제한된 읽기 분석).
- marker로 찾은 댓글 하나를 갱신하고, publication version으로 오래된 완료가 새 SHA 결과를 덮지 못하게 한다. 새 커밋이 있으면 이전 결과임을 표시한다(TC-29).
- 댓글 실패는 outbox만 재시도한다. 댓글에 원시 소스·secret을 넣지 않는다.

개발 머신에서 검증할 수 없는 것: GHES의 PR·댓글 API 동작. GitHub.com에서는 시험용 저장소와 App으로 검증한다.

### 다중 노드 검증

다중 사용자·권한 뒤에 GitHub 연결·PR·댓글 분석과 병행한다. 실제 여러 물리 머신에 에이전트를 등록해 분산 동작을 검증한다.

완료 조건:
- 실제 node capability(도구·toolchain·source root)에 따른 배정과 대기 사유.
- 노드마다 다른 NFS 접근 권한·경로 차이의 처리.
- network partition·노드 정지 뒤 lease fence, `recovery_required` 확인, 복구, drain(TC-22의 물리 머신 부분, [시험](TESTING.md)의 고위험 시나리오).
- 에이전트 업데이트 절차: drain → 바이너리 교체 → protocol 확인 → 재등록 없이 복귀.
- private Git credential·CA profile과 노드 간 snapshot 이동(A16의 후속 항목)의 결정.

개발 머신에서 검증할 수 없는 것: 여러 물리 노드, 실제 NFS, 실제 network partition.

### 폐쇄망 배포팩

내부 미러와 CA만 쓰는 배포 묶음을 만든다. 계약은 [운영](OPERATIONS.md)에 있다.

완료 조건:
- 버전·hash가 고정된 서비스 바이너리·웹 자산·DB migration·규칙 팩·도구 manifest. manifest에 OS/arch·빌드 ABI·toolchain·DB·모델 adapter 버전을 적는다.
- 외부 fallback 없음: 미러에 없는 버전은 unavailable로 표시하고 외부 다운로드·telemetry·외부 모델로 해결하지 않는다. CA 검증을 끄지 않는다(TC-30).
- UI는 로컬 자산만 쓴다(외부 CDN 폰트·아이콘·JS·CSS 없음).
- 분석기 catalog, 언어 toolchain, 프로젝트 의존성 mirror를 구분한다.
- RHEL 8 대상의 glibc·시스템 라이브러리 호환을 실제로 확인한다. 최신 빌드 머신에서 컴파일됐다는 사실로 가정하지 않는다.

개발 머신에서 검증할 수 없는 것: 내부 미러와 CA가 있는 RHEL 8.10.

### 운영 수용

여러 사용자에게 공개하기 전 마지막 작업이다.

완료 조건:
- 장애 훈련: 모델 응답 실패, node offline, DB unavailable, NFS 지연, 도구 버전 변경, max worker 축소, artifact 상한 초과([운영](OPERATIONS.md)의 상황별 대응).
- 실제 데이터 규모에서 백업·복원 시험과 목표 시간 결정.
- 권한 철회 훈련(사용자·노드·GitHub App).
- 품질 baseline과 제한 사항 문서. 측정하지 않은 정확도를 주장하지 않는다.
- 남은 열린 항목의 결정: 철회된 노드의 `recovery_required` attempt 정리 명령, 저장 영역 정리·보존(retention) 정책, 로그아웃 뒤 지속 실행 방식(systemd 등) — [결정 기록](DECISIONS.md)의 A32와 [보안](SECURITY.md)의 보관과 삭제.

개발 머신에서 검증할 수 없는 것: 여러 물리 노드와 NFS가 있는 실제 장애 훈련, 내부 미러·CA가 있는 RHEL 8.10에서의 운영, 회사 SSO를 쓰는 권한 철회. 앞 작업에서 검증하지 못한 항목은 운영 환경에서 시험하고 미실행이면 미실행으로 남긴다.

## 코드 대조에서 확인한 공백

문서를 통합하면서 코드와 대조해 찾은 항목이다. 각 항목을 처리할 작업을 함께 적는다.

| 공백 | 처리할 작업 |
|---|---|
| Git 입력은 모델 검토를 승인할 방법이 없다. 승인은 로컬 source root 단위(`ModelSourceRoots`)뿐이라 Git 저장소 분석은 정적 분석 결과만 나온다. | GitHub 연결 (저장소별 모델 전송 승인) |
| 개인 모드 `codeye start`가 에이전트에 Git 실행 파일·허용 호스트를 넘기지 않는다. Git 프로젝트는 에이전트를 직접 띄워야 한다. | GitHub 연결 |
| 보고서에 환경·문서 탐색 결과가 절로 나오지 않는다. 검증만 하고 버린다. | 보고서 재구성 |
| 모델 검토가 run당 코드 파일 최대 8개만 본다. | 검토 범위 확대 |
| `codeye doctor`는 모델·프로세스 제어 항목이 항상 미검증이라 종료 코드가 늘 1이다. | 리팩토링 뒤 운영 점검 정리 |
| 재시도는 저장 계층에만 있고 API·CLI·화면에서 부를 수 없다. `recovery_required`를 되돌리는 경로도 없다. | 운영 수용 |
| 지원 언어 소스가 없는 저장소(문서·설정만)는 비밀정보 검사가 계획되지 않는다. | 리팩토링 (동작 변경이므로 별도 커밋과 시험) |
| `with-tests`·`focused` 프로필은 API가 받지만 워커가 실행하지 않는다. 시험 상태는 항상 미실행이다. | 후속 선택 작업 |

## 후속 선택 작업

필요할 때 별도 작업으로 만든다. 순서는 정하지 않았다.

- Container 실행기(ContainerExecutor). Native 실행을 대체하지 않는 선택 실행기다.
- 고비용 interfile 분석.
- 추가 OS(Windows의 개인 모드 start/stop 포함).
- 인프라 용량 공급자(CapacityProvider): 클라우드 VM 생성·삭제는 초기 범위 밖이다.
- PDF 내보내기.

## 작업 티켓 형식

각 작업에는 다음을 적는다.

| 항목 | 내용 |
|---|---|
| 목적 | 해결할 문제 |
| 요구 ID | REQ·NFR([요구사항](REQUIREMENTS.md)) |
| 입력/출력 | 받는 것과 만드는 것 |
| 변경 모듈 | 바꿀 패키지·파일 |
| 선행 작업 | 먼저 끝나야 하는 작업 |
| 실패 시나리오 | 다룰 실패와 그때의 상태 |
| API/schema 영향 | 바뀌는 계약과 호환 |
| 수용 테스트 | TC([시험](TESTING.md)) |
| 관측 지표 | 운영에서 볼 값 |
| rollback·마이그레이션 | 되돌리는 방법과 migration |
| 완료 증거 | 실제로 실행한 시험과 결과 |

구현 설명만 있고 실제 시험 결과가 없으면 done으로 처리하지 않는다.
