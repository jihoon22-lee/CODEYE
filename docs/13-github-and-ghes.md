# 13. GitHub.com·GHES 연결

## 1. 범위

Git 저장소 읽기, 브랜치 선택, 선택적인 PR/댓글 트리거와 결과 댓글만 다룬다. GitHub Actions·다른 CI의 결과·workflow·status check를 수집하거나 생성하지 않는다. GitHub App은 Git 연동 인증 수단이지 CI 연계가 아니다.

provider는 host, API base URL, clone policy, CA bundle, App identity, credential reference를 갖는다. GitHub.com과 각 GHES는 별도 연결/인증정보를 사용한다. GHES 버전과 허용 API를 실제 연결 진단에서 확인하고 지원 계약을 고정한다.

## 2. 역할

운영자는 App 등록·최소 권한·이벤트·webhook URL·secret·private key를 설정한다. 저장소 이용자 또는 관리자는 App을 설치하거나 대상 저장소를 추가한다. 플랫폼은 필요한 작업을 안내하고 연결 상태를 검사하며 Git 권한을 자동 수정하지 않는다. 조직 승인 정책을 우회하지 않는다.

App 방식에서는 App webhook을 사용한다. 별도 봇 계정 초대와 저장소별 webhook을 무조건 함께 요구하지 않는다. App을 사용할 수 없는 배포의 서비스 계정 방식은 별도 구성 가이드를 제공한다.

## 3. 연결 상태

repository_readable, branch_resolvable, webhook_verified, pr_readable, comment_writable, report_url_reachable을 각각 표시한다. webhook이 없어도 읽기가 가능하면 수동·예약 분석을 제공한다. 공개 저장소 read-only 사용에 불필요한 쓰기 토큰을 요구하지 않는다. 집에서 외부 수신 주소를 마련하지 않아도 기본 기능은 동작한다.

## 4. 권한과 자격증명

저장소 내용 읽기, PR 읽기, 결과를 게시할 댓글 경로에 필요한 쓰기 권한만 요청한다. issue_comment와 PR 대화 댓글 API의 권한 차이는 해당 GHES 계약으로 확인한다. Checks/Actions 쓰기 권한은 초기 기능에 불필요하다.

App private key와 게시 credential은 관리 서버가 보관한다. 소스 확보가 필요한 노드에는 필요한 저장소·기간으로 제한된 읽기 credential만 전달하고 checkout 이후 제거한다. 분석 프로세스에 댓글·설정 변경 권한을 주지 않는다. 폐쇄망에서 다른 호스트 인증으로 fallback하지 않는다.

## 5. webhook 처리

1. raw body size와 요청 시간 제한 적용.
2. 설정한 secret으로 HMAC 서명을 constant-time 검증.
3. provider+delivery ID로 중복 확인; 동일 ID 다른 payload digest는 거부·감사.
4. event/action, installation, repository, 프로젝트 등록 상태 검증.
5. durable delivery와 작업 의도를 저장한 뒤 신속히 2xx 응답.
6. 별도 처리기가 provider API로 PR·권한·SHA를 확인하고 run 생성.

응답 목표는 2초 이내이며 분석을 완료할 때까지 연결을 유지하지 않는다. 큐 저장 실패 시 성공 응답으로 유실시키지 않는다. secret rotation에는 제한된 이전 secret 검증 기간을 둘 수 있다. webhook 수신만으로 Native 실행을 승인하지 않는다.

## 6. 이벤트와 명령

pull_request의 opened/reopened/synchronize/ready_for_review 등 설정된 action을 처리한다. draft는 기본 제외하되 사용자가 선택할 수 있다. issue_comment는 payload가 PR에 연결됐는지 확인한다. inline review comment는 별도 이벤트이며 초기 댓글 명령은 대화 탭에 한정한다.

명령 문법 초안은 `@codeye analyze`, `@codeye analyze deep`, `@codeye analyze tests`다. 실제 App/봇 login을 설정에서 사용한다. quoted text·코드 블록 안의 명령을 실행하지 않고 봇 자기 댓글을 무시한다. tests는 승인된 프로젝트 정책 안에서만 활성화한다. 댓글 작성자의 저장소 권한과 CODEYE 실행 권한을 모두 확인한다.

외부 fork 또는 신뢰하지 않는 기여자의 PR은 Native 실행을 자동 승인하지 않는다. 기본은 정책상 승인 대기 또는 제한된 읽기 분석이다. 이것을 이유로 신뢰된 자기 프로젝트의 Native 기능까지 비활성화하지 않는다.

## 7. 입력과 결과 게시

PR number, base SHA, head SHA, repository ID를 고정한다. 변경 범위를 중심으로 보되 필요한 호출부·인터페이스는 전체 snapshot에서 읽는다. 관련 경로를 읽었다고 전체 저장소를 동일 깊이로 검토했다고 하지 않는다.

댓글 하나를 marker로 찾아 갱신한다. body에는 SHA·실행 상태·주요 발견·검사 공백·보고서 URL을 포함하고 원시 소스/secret은 붙이지 않는다. 키는 provider/repo/PR/analysis-kind이며 publication version으로 오래된 완료가 새 SHA 결과를 덮지 못하게 한다. 새 커밋이 생겼으면 이전 결과임을 명시한다.

## 8. 실패

rate limit/일시 오류는 제한된 backoff, 권한 철회는 연결 경고와 신규 분석 중지, 댓글 실패는 outbox 재시도다. report URL에 접속할 수 없는 사용자에게 공개 링크로 권한을 우회시키지 않는다. 닫힌 PR의 진행 작업은 정책에 따라 취소/완료하되 상태와 이유를 기록한다.
