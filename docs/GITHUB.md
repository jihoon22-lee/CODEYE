# GitHub.com·GHES 연결

GitHub.com은 소유자가 실제로 쓸 일급 대상이다. GitHub Enterprise Server(GHES)는 같은 계약을 따르지만 실제 인스턴스에서 시험하지 않았다.

| 기능 | 상태 |
|---|---|
| 승인한 HTTPS 호스트의 Git 브랜치 가져오기와 commit SHA 고정 | 구현. 공개 GitHub 저장소에서 실행해 확인했다. GHES 호스트는 실제 인스턴스로 시험하지 않았다 |
| 개인 모드의 Git 주소(`codeye up https://github.com/OWNER/REPO`, 화면의 입력란) | 구현. 브랜치를 비우면 기본 브랜치다. 공개 GitHub 저장소로 확인했다 |
| provider 등록(`codeye provider add`) | 구현. 종류·호스트·선택 API URL을 저장할 뿐 API로 연결을 확인하지 않는다 |
| GitHub App 설치, 연결 상태 진단, 권한 진단 | 설계 계약(미구현) |
| webhook 수신, PR 이벤트와 댓글 명령 | 설계 계약(미구현) |
| PR 분석(base/head SHA 고정), 결과 댓글 게시 | 설계 계약(미구현) |
| 비공개 저장소 credential 전달, 사내 CA·proxy, submodule/LFS | 미구현. 조용히 사용자 credential이나 인터넷 대체 경로로 우회하지 않는다 |

이 문서의 나머지는 설계 계약이다. "현재 동작하는 것"을 제외한 절은 코드에 없다. 외부에서 들어오는 webhook 주소가 없어도 읽기 가능한 저장소의 수동·예약 분석 같은 기본 기능은 동작한다. 집에서 외부 수신 주소를 마련하지 않아도 된다.

## 현재 동작하는 것

**개인 모드**에서는 준비가 필요 없다. `codeye up`이 git을 찾아 에이전트에 넘기고, 설정의 `git_hosts`(기본 `["github.com"]`)마다 provider를 등록한다(`github.com`은 `github`, 그 밖은 `ghes`). 그다음 주소만 준다.

```sh
codeye up https://github.com/OWNER/REPO                # 기본 브랜치
codeye up https://github.com/OWNER/REPO/tree/release   # 그 브랜치
codeye up github.com/OWNER/REPO                        # https://를 붙인다
```

화면의 "폴더 경로 또는 Git 주소" 입력란에 같은 주소를 붙여넣어도 된다. 같은 저장소를 다시 주면 그 프로젝트에 새 분석을 요청한다. Git 저장소의 프로젝트는 소스를 모델에 보내고(기본 켬), 저장소의 빌드 스크립트·`cargo check`처럼 저장소 코드를 실행하는 분석은 기본으로 하지 않는다. 그 분석이 빠진 보고서의 "더 나은 결과를 얻으려면"에서 "저장소의 빌드 스크립트 실행"을 켜고 다시 시도한다. 비공개 저장소는 아직 지원하지 않는다. 인증을 요구하는 주소는 소스 확보가 "저장소를 인증 없이 읽을 수 없음"(`source_auth_required`)으로 실패한다.

**서버 모드**에서는 다음 조건으로 동작한다.

1. provider를 등록한다. `github`은 호스트가 `github.com`이어야 하고, `ghes`는 호스트 이름과 선택 `--api-url https://HOST/api/v3`를 받는다. API URL은 저장만 하고 아직 쓰지 않는다.

   ```sh
   codeye provider add --kind github --host github.com
   ```

2. 에이전트에 Git 실행 파일과 허용 호스트를 준다([운영](OPERATIONS.md)). 에이전트는 `--git`과 `--git-host`가 모두 있을 때만 `source:git:<host>` 능력을 알린다.

   ```sh
   codeye agent --controller URL --state-dir DIR --git /abs/git --git-host github.com ...
   ```

3. 프로젝트의 소스를 Git으로 등록한다. 형식은 승인된 provider의 `https://<provider host>/<owner>/<repo>[.git]`와 선택 branch(check-ref-format 부분집합, 비우면 기본 브랜치)다. 등록은 형식과 권한만 검사하고 서버는 네트워크에 접근하지 않는다. 분석 요청 뒤 워커가 가져온다.

동작 규칙:

- 운영자가 승인한 HTTPS host와 branch만 받아 실제 fetch 시점의 commit SHA를 고정한다. branch가 비어 있으면 fetch 직전에 `git ls-remote --symref … HEAD`로 기본 브랜치를 정하고 manifest의 `source_branch`에 남긴다. 이후 브랜치가 움직여도 이미 확보한 snapshot은 바뀌지 않는다.
- URL의 userinfo·query·fragment, SSH, `git://`, 알려지지 않은 host, redirect는 허용하지 않는다.
- 임시 bare 저장소에서 fetch → rev-parse → ls-tree → cat-file을 argv로 실행한다. snapshot과 workspace에 `.git`을 넣지 않고 checkout, hooks, smudge/clean filter, export-ignore를 쓰지 않는다. Git 사용자·system 설정과 credential helper를 차단한다. 받은 객체는 `transfer.fsckObjects`/`fetch.fsckObjects`로 검사해 변조된 트리를 거부한다.
- manifest에는 요청한 branch(`source_branch`)와 요청 시각(`requested_at`)이 남고 URL·remote 이름·credential은 들어가지 않는다. provider repository ID는 manifest에 아직 없다.
- 확보 전체에 deadline(최대 10분)이 있고, fetch 중 임시 저장소의 쓰기 용량을 감시한다(soft 한도이며 파일시스템 quota가 아니다). 취소·timeout·용량 중지에서 정리 완료를 확정하지 못하면 `recovery_required`와 격리 디렉터리를 남긴다.
- 소스 확보와 snapshot 계약은 [실행](EXECUTION.md)에 있다.

## 범위

Git 저장소 읽기, 브랜치 선택, 선택적인 PR/댓글 트리거와 결과 댓글만 다룬다. GitHub Actions·다른 CI의 결과·workflow·status check를 수집하거나 생성하지 않는다. GitHub App은 Git 연동 인증 수단이지 CI 연계가 아니다.

provider는 host, API base URL, clone policy, CA bundle, App identity, credential reference를 갖는다. GitHub.com과 각 GHES는 별도 연결과 인증정보를 쓴다. GHES 버전과 허용 API를 실제 연결 진단에서 확인하고 지원 계약을 고정한다.

## 역할 (설계 계약)

운영자는 App 등록, 최소 권한, 이벤트, webhook URL, secret, private key를 설정한다. 저장소 이용자 또는 관리자는 App을 설치하거나 대상 저장소를 추가한다. 플랫폼은 필요한 작업을 안내하고 연결 상태를 검사하며 Git 권한을 자동으로 수정하지 않는다. 조직 승인 정책을 우회하지 않는다.

App 방식에서는 App webhook을 쓴다. 별도 봇 계정 초대와 저장소별 webhook을 무조건 함께 요구하지 않는다. App을 쓸 수 없는 배포의 서비스 계정 방식은 별도 구성 가이드를 제공한다.

## 연결 상태 (설계 계약)

`repository_readable`, `branch_resolvable`, `webhook_verified`, `pr_readable`, `comment_writable`, `report_url_reachable`을 각각 표시한다. webhook이 없어도 읽기가 가능하면 수동·예약 분석을 제공한다. 공개 저장소 read-only 사용에 불필요한 쓰기 토큰을 요구하지 않는다.

## 권한과 자격증명 (설계 계약)

저장소 내용 읽기, PR 읽기, 결과를 게시할 댓글 경로에 필요한 쓰기 권한만 요청한다. issue_comment와 PR 대화 댓글 API의 권한 차이는 해당 GHES 계약으로 확인한다. Checks/Actions 쓰기 권한은 초기 기능에 불필요하다.

App private key와 게시 credential은 관리 서버가 보관한다. 소스 확보가 필요한 노드에는 필요한 저장소·기간으로 제한된 읽기 credential만 전달하고 checkout 이후 제거한다. 분석 프로세스에 댓글·설정 변경 권한을 주지 않는다. 폐쇄망에서 다른 호스트 인증으로 fallback하지 않는다. 자격증명 일반 규칙은 [보안](SECURITY.md)에 있다.

## webhook 처리 (설계 계약)

1. raw body size와 요청 시간 제한을 적용한다.
2. 설정한 secret으로 HMAC 서명을 constant-time으로 검증한다.
3. provider+delivery ID로 중복을 확인한다. 동일 ID에 다른 payload digest는 거부하고 감사한다.
4. event/action, installation, repository, 프로젝트 등록 상태를 검증한다.
5. durable delivery와 작업 의도를 저장한 뒤 신속히 2xx로 응답한다.
6. 별도 처리기가 provider API로 PR·권한·SHA를 확인하고 run을 만든다.

응답 목표는 2초 이내이며 분석을 완료할 때까지 연결을 유지하지 않는다. 큐 저장에 실패하면 성공 응답으로 유실시키지 않는다. secret rotation에는 제한된 이전 secret 검증 기간을 둘 수 있다. webhook 수신만으로 Native 실행을 승인하지 않는다.

## 이벤트와 명령 (설계 계약)

`pull_request`의 opened/reopened/synchronize/ready_for_review 등 설정된 action을 처리한다. draft는 기본 제외하되 사용자가 선택할 수 있다. `issue_comment`는 payload가 PR에 연결됐는지 확인한다. inline review comment는 별도 이벤트이며 초기 댓글 명령은 대화 탭에 한정한다.

명령 문법 초안은 `@codeye analyze`, `@codeye analyze deep`, `@codeye analyze tests`다. 실제 App/봇 login을 설정에서 쓴다. quoted text·코드 블록 안의 명령을 실행하지 않고 봇 자기 댓글을 무시한다. tests는 승인된 프로젝트 정책 안에서만 활성화한다. 댓글 작성자의 저장소 권한과 CODEYE 실행 권한을 모두 확인한다.

외부 fork 또는 신뢰하지 않는 기여자의 PR은 Native 실행을 자동 승인하지 않는다. 기본은 정책상 승인 대기 또는 제한된 읽기 분석이다. 이것을 이유로 신뢰된 자기 프로젝트의 Native 기능까지 비활성화하지 않는다.

## 입력 고정과 결과 게시 (설계 계약)

PR number, base SHA, head SHA, repository ID를 고정한다. 변경 범위를 중심으로 보되 필요한 호출부·인터페이스는 전체 snapshot에서 읽는다. 관련 경로를 읽었다고 전체 저장소를 동일 깊이로 검토했다고 하지 않는다.

댓글 하나를 marker로 찾아 갱신한다. body에는 SHA·실행 상태·주요 발견·검사 공백·보고서 URL을 포함하고 원시 소스와 secret은 붙이지 않는다. 키는 provider/repo/PR/analysis-kind이며 publication version으로 오래된 완료가 새 SHA 결과를 덮지 못하게 한다. 새 커밋이 생겼으면 이전 결과임을 명시한다.

## 실패 (설계 계약)

- rate limit·일시 오류: 제한된 backoff.
- 권한 철회: 연결 경고와 신규 분석 중지.
- 댓글 게시 실패: outbox 재시도.
- report URL에 접속할 수 없는 사용자에게 공개 링크로 권한을 우회시키지 않는다.
- 닫힌 PR의 진행 작업은 정책에 따라 취소하거나 완료하되 상태와 이유를 기록한다.
