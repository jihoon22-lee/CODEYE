# GitHub.com·GHES 연결

GitHub.com과 GitHub Enterprise Server(GHES)의 저장소·브랜치·pull request를 승인된 HTTPS 호스트에서 읽는다. 수동 분석, 예약 분석, PR 고정, 댓글 미리 보기와 게시가 설치에 설정한 호스트별 플랫폼 GitHub 계정을 쓴다. GHES의 API와 사설 CA 경로는 로컬 HTTPS 시험 서버에서 확인했다. 실제 GHES 버전과 조직 권한 정책은 운영 환경에서 확인한다.

## 저장소 등록

`codeye up`과 `codeye start`는 설정의 `git_hosts`(온라인 기본 `["github.com"]`)에 있는 호스트를 provider로 등록한다. 웹에서는 **폴더 경로 또는 Git 주소** 입력란에 저장소 주소를 넣는다. 비공개 저장소는 저장소 관리자가 플랫폼 계정에 권한을 부여한 뒤 주소를 붙여넣는다.

```sh
codeye up https://github.com/OWNER/REPO                # 기본 브랜치
codeye up https://github.com/OWNER/REPO/tree/release   # 그 브랜치
codeye up github.com/OWNER/REPO                        # https://를 붙인다
codeye up https://github.com/OWNER/REPO/pull/123       # pull request
```

화면의 "폴더 경로 또는 Git 주소" 입력란에 같은 주소를 붙여넣어도 된다. 같은 저장소를 다시 주면 그 프로젝트에 새 분석을 요청한다. Git 저장소의 프로젝트는 소스를 모델에 보내고(기본 켬), 저장소의 빌드 스크립트·`cargo check`처럼 저장소 코드를 실행하는 분석은 기본으로 하지 않는다. 그 분석이 빠진 보고서의 "더 나은 결과를 얻으려면"에서 "저장소의 빌드 스크립트 실행"을 켜고 다시 시도한다.

플랫폼 인증정보가 없으면 익명으로 읽는다. 저장소가 요구하는 인증에 실패하면 `source_auth_required`, 연결에 실패하면 `source_unreachable`이 보고된다. 비공개 저장소에 사용할 플랫폼 계정은 **도구** 화면에서 호스트별로 확인할 수 있다.

**따로 띄운 노드**(`codeye agent`, [운영](OPERATIONS.md#서버노드-배포))는 다음 조건으로 저장소를 가져온다.

1. provider를 등록한다. `github`은 호스트가 `github.com`이어야 하고, `ghes`는 호스트 이름과 선택 `--api-url https://HOST/api/v3`를 받는다. GitHub.com은 `https://api.github.com`, GHES는 `https://HOST/api/v3`를 기본으로 쓴다. 지정 URL은 해당 API 호스트의 HTTPS 주소여야 한다.

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
- manifest에는 요청한 branch(`source_branch`, pull request snapshot은 대신 `source_ref` `refs/pull/N/head`)와 요청 시각(`requested_at`)이 남고 URL·remote 이름·credential은 들어가지 않는다. provider repository ID는 manifest에 아직 없다.
- 확보 전체에 deadline(최대 10분)이 있고, fetch 중 임시 저장소의 쓰기 용량을 감시한다(soft 한도이며 파일시스템 quota가 아니다). 취소·timeout·용량 중지에서 정리 완료를 확정하지 못하면 `recovery_required`와 격리 디렉터리를 남긴다.
- 소스 확보와 snapshot 계약은 [실행](EXECUTION.md)에 있다.

## 플랫폼 GitHub 계정

운영자는 `git_hosts`와 `github.token_env`로 인증정보를 사용할 호스트와 환경변수 이름을 지정한다. 다음 예는 GitHub.com과 사내 GHES를 둘 다 쓰는 설치다. 토큰 값은 서비스 시작 명령의 환경변수로 공급한다.

```json
{
  "schema_version": "0.1",
  "git_hosts": ["github.com", "git.corp.example"],
  "github": {
    "token_env": {
      "github.com": "CODEYE_PLATFORM_GITHUB_TOKEN",
      "git.corp.example": "CODEYE_PLATFORM_GHES_TOKEN"
    }
  }
}
```

1. 플랫폼 계정에 대상 저장소 내용과 PR을 읽을 권한을 부여한다. 결과 댓글을 켜려면 같은 계정에 댓글 쓰기 권한도 부여한다.
2. 지정한 환경변수를 주입해 `codeye start` 또는 `codeye up`을 실행한다. 시작 시 호스트별 `GET /user`로 계정을 확인하고 401로 거부된 토큰은 저장하지 않는다. 계정 확인이 연결 실패 등으로 끝나면 설정 여부와 확인 실패 사유를 남긴다.
3. 시작 출력이나 웹의 **도구** 화면에서 호스트와 플랫폼 계정 이름을 확인한다. 토큰을 갱신할 때는 환경변수를 교체하고 다시 시작 명령을 실행한다. `github.enabled: false`이면 설치에 설정한 토큰 사용을 끈다.

서비스는 `<state>/secrets/github-platform.json`(0600)을 사용한다. 토큰 값은 설정 JSON, API 응답, 명령행, DB·백업, 로그, 보고서에 쓰지 않는다. 이전 사용자별 GitHub 토큰 파일과 `gh` 로그인은 읽지 않는다. `GH_TOKEN`과 `GITHUB_TOKEN`도 명시적으로 환경변수 이름에 지정한 경우에만 읽는다. 복원 후에는 시작 환경의 토큰을 다시 공급한다.

수동·예약 실행의 Git fetch는 대상 호스트의 토큰을 Git 자식 프로세스의 호스트 범위 HTTP 헤더로 전달한다. pull request 고정·미리 보기·댓글 게시도 같은 호스트의 플랫폼 토큰을 사용한다. 주소의 리다이렉트나 다른 origin의 다음 페이지는 따르지 않는다. API와 Git 모두 `ca_bundle`로 사내 인증서를 신뢰할 수 있다. 웹 요청자와 프로젝트 권한은 CODEYE에 기록되고 댓글 작성자는 그 호스트의 플랫폼 계정이다.

**오프라인 모드**에서는 `git_hosts`에 명시된 사내 GHES만 대상으로 한다. 이전에 등록된 공개 GitHub 공급자가 있어도 사용할 수 없다. [오프라인 모드](OPERATIONS.md#오프라인-모드)

## pull request 분석

```sh
codeye up https://github.com/OWNER/REPO/pull/123
codeye up https://github.com/OWNER/REPO/pull/123/files   # 브라우저의 탭 주소도 받는다
```

화면의 입력란에 같은 주소를 넣어도 된다. 저장소의 프로젝트가 없으면 등록하고(기본 브랜치 프로젝트와 같은 프로젝트다), 그 프로젝트에 pull request run을 넣는다.

1. **고정**: run을 넣을 때 GitHub API로 pull request의 base/head 브랜치와 SHA, head 저장소(외부 기여자의 fork), 변경 파일과 상태(added, modified, removed, renamed …), patch가 있으면 head 쪽 변경 줄 범위를 확인해 run에 한 번만 쓴다(`runs.pull_request`, 바꿀 수 없다). 변경 파일은 최대 3000개(GitHub의 목록 한도)이며 목록이 잘렸으면 그 사실을 남긴다. 요청 본문(`pull_request: {number, comment?}`)은 그대로 idempotency 비교에 쓴다.
2. **가져오기**: 워커는 base 저장소의 `refs/pull/N/head`를 가져와 고정한 head SHA인지 확인한다. 그 사이 push로 ref가 움직였으면 고정한 SHA를 직접 가져오고, 그것도 안 되면 소스 확보가 불안정 실패로 끝난다. fork의 head도 base 저장소의 ref로 받으므로 fork 저장소에는 접근하지 않는다.
3. **검토 순서**: 변경 파일이 가장 먼저이고(도구 진단이 있는 파일보다 앞), 이어 변경 파일과 같은 폴더의 파일, 그다음 평소 순서다([검토 우선순위](REVIEW.md#검토-우선순위)). 삭제된 파일은 head에 없으므로 검토하지 않는다.
4. **보고서**: 요약이 pull request 번호, base/head SHA, 변경 파일 중 모델에 보낸 수를 말하고, 발견과 도구 진단을 변경된 줄·변경 파일·기존 코드로 나눈다. patch가 없는 파일은 파일 단위로 세고 그 사실을 적는다. 저장소 전체를 같은 깊이로 검토했다고 말하지 않는다([보고서](REPORT.md#pull-request-보고서)).
5. **다시 분석**: 새 push 뒤 같은 주소를 다시 주면 그때의 head로 새 run을 만든다(진행 중인 run이 있어도). 비교 기준은 같은 pull request의 이전 run이다.

거절은 각각 다른 코드로 돌아온다: 승인된 GitHub API를 쓸 수 없음(`PULL_REQUEST_UNSUPPORTED`), 플랫폼 계정(또는 익명)으로 보이지 않는 pull request(`PULL_REQUEST_NOT_FOUND`), 거부된 token(`GITHUB_TOKEN_REFUSED`), 권한 없음(`GITHUB_FORBIDDEN`), rate limit(`GITHUB_RATE_LIMITED`, 다시 시도 가능), 연결 실패(`GITHUB_UNAVAILABLE`). 예약 run은 pull request를 받지 않는다.

외부 fork의 pull request도 같은 정책으로 분석한다. 저장소 코드를 실행하는 분석(빌드 스크립트)은 프로젝트 선택을 따르고 Git 프로젝트에서 기본으로 꺼져 있다.

## 결과 댓글

결과 댓글은 선택이며 기본으로 꺼져 있다.

- 켜기: 화면의 접힌 "고급"에서 "pull request에 결과 댓글 남기기"를 고르거나 `codeye up --comment …/pull/N`. 프로젝트마다 기억한다(`analysis.comment_on_pull_request`). `--comment=false`로 끈다.
- 미리 보기: `codeye up --dry-run-comment …/pull/N`은 아무것도 게시하지 않고 보고서를 기다려(`--comment-wait`, 기본 1시간) 댓글 본문을 출력한다. 기억한 선택은 바꾸지 않는다. 화면에서는 보고서의 pull request 줄에서 "미리 보기"를 누른다(`GET /api/v1/runs/{id}/pull-request-comment`).

동작:

1. 댓글을 켠 run의 보고서가 게시되면 같은 transaction이 게시 대기 행(`pull_request_comments`)을 만든다. controller가 그 행을 lease로 가져와 게시하고 결과를 남긴다.
2. 댓글은 pull request마다 하나다. token 계정이 쓴 댓글 가운데 숨은 marker(`<!-- codeye:pull-request-comment project=… -->`)가 있는 것을 찾아 고치고, 없으면 새로 만든다. 다른 사람이 marker를 붙여 넣은 댓글은 고치지 않는다.
3. 오래된 결과가 새 결과를 덮지 않는다. 같은 pull request의 더 늦게 만든 run이 이미 게시했거나, GitHub의 댓글에 적힌 publication version(`<!-- codeye:publication run=… version=… head=… -->`, run 생성 시각)이 더 크면 `superseded`로 끝낸다.
4. 실패는 run에 기록되고 분석을 실패로 만들지 않는다. rate limit과 연결 실패는 1분부터 두 배씩 최대 30분 간격으로 6번까지 다시 시도한다. 거부된 token, 권한 없음, 사라진 pull request, token 없음은 `failed`와 이유로 끝난다. `GET /api/v1/runs/{id}`의 `pull_request.comment`가 상태(`not_requested`, `waiting_for_report`, `pending`, `posted`, `superseded`, `failed`)를 보여 준다.

본문은 한국어이며 다음만 싣는다: 상태와 판정, 분석한 head SHA(그 뒤 pull request의 head가 바뀌었으면 그 사실), 모델에 보낸 변경 파일 수, 변경 부분의 주요 발견 5개까지(file:line과 영향 한 줄), 변경 부분의 도구 진단(규칙과 위치), 나머지 수(변경 부분·기존 코드), 확인하지 못한 주요 공백. 코드 인용, 도구 원문 메시지, 증거, 비밀정보는 싣지 않는다. 모델이 쓴 제목·영향은 첫 줄만 쓰고 code span과 code block을 지운다. `&`를 먼저 escape해 문자 참조(`&#64;`, `&commat;` 등)가 글자로 보이게 하고, mention(`@name`), issue 참조(`#12`, `GH-12`), URL 자동 링크, HTML을 끊어 아무에게도 알림을 보내지 않고 아무것도 연결하지 않는다. 본문은 60000자를 넘지 않는다. CODEYE 보고서 주소는 싣지 않는다(localhost 주소는 다른 사람에게 쓸모가 없다).

## 저장소 웹훅

GitHub.com 또는 GHES의 저장소 웹훅을 한 CODEYE 프로젝트에 연결할 수 있다. 웹훅을 받는 CODEYE 주소는 GitHub에서 접근할 수 있어야 한다. 수동·예약 분석은 연결 없이 쓸 수 있다.

1. CODEYE에서 저장소 프로젝트를 등록한다. 프로젝트 ID와 그 프로젝트에서 실행할 수 있는 활성 사용자의 ID를 확인한다. 계정 ID는 `codeye user list`에서 확인할 수 있다. 저장소의 숫자 ID는 GitHub REST의 `GET /repos/OWNER/REPO` 응답의 `id`다.
2. 서비스 머신에서 `<state_dir>/secrets/github-webhooks/<project_id>.json`을 생성한다. 파일 권한을 `0600`, 디렉터리를 `0700`으로 두고 최소 32바이트의 임의 secret을 각 프로젝트마다 따로 정한다.

   ```json
   {
     "host": "github.com",
     "repository": "OWNER/REPO",
     "repository_id": 123456,
     "actor_user_id": "d5e1f3cc-3271-4ce4-b7cd-c5d9867eab7a",
     "secret": "REPLACE_WITH_AN_INDIVIDUAL_RANDOM_SECRET",
     "pull_requests": true,
     "mentions": true
   }
   ```

3. 저장소의 **Settings > Webhooks**에서 Payload URL을 `<CODEYE 주소>/api/v1/github/webhooks/<project_id>`, 콘텐츠 유형을 `application/json`, Secret을 설정 파일과 같은 값으로 지정한다. 켠 기능에 맞춰 **Pull requests** 또는 **Issue comments**를 선택한다. 실제 계정에는 PR 조회 권한과, 멘션을 켠 경우 [저장소 권한 조회](https://docs.github.com/en/rest/collaborators/collaborators#get-repository-permissions-for-a-user) 권한이 필요하다.

`pull_requests`를 켜면 열기·다시 열기·새 커밋·검토 준비 이벤트를 처리한다. `mentions`를 켜면 PR 대화의 새 댓글에서 `@플랫폼-GitHub-로그인`을 한 줄에 입력해 실행을 요청할 수 있다. 멘션의 대소문자를 무시하며 인용, 코드, 봇과 플랫폼 계정 자신의 댓글은 건너뛴다. 작성자에게 `write` 또는 `admin` 권한이 있는지 GitHub에 확인한다. CODEYE 실행은 설정의 `actor_user_id`가 요청한 것으로 기록하고, 그 계정의 프로젝트 실행 권한을 큐에 넣기 직전에 다시 확인한다. 분석 결과 댓글은 프로젝트의 `comment_on_pull_request` 설정을 따른다.

CODEYE는 두 경우 모두 PR의 base와 head가 설정한 저장소에 속하는지 API로 확인하고 SHA와 변경 파일을 고정한다. 포크 PR, 닫힌 PR, draft, 다른 SHA로 바뀐 PR 이벤트는 자동 분석을 시작하지 않는다. 이미 요청된 실행은 고정된 SHA를 쓴다.

서명과 저장소를 확인해 PostgreSQL에 기록하면 202로 응답한다. 같은 프로젝트와 배달 ID에 같은 이벤트·본문이 오면 202, 다른 이벤트나 본문이면 409다. 외부 API 호출과 run 생성은 이후에 처리한다. 같은 PR head 이벤트나 같은 댓글로 실행을 중복해서 만들지 않는다. API 연결·rate limit 오류는 최대 5회 시도한다. 처리 상태와 이유는 `github_webhook_deliveries`에 남긴다. secret과 댓글 본문은 저장하지 않는다. 설정 파일을 제거하면 새 전달을 받지 않으며 아직 처리하지 않은 전달도 건너뛴다.

## 범위

Git 저장소 읽기, 브랜치 선택, pull request 분석과 결과 댓글, 저장소 웹훅을 다룬다. GitHub Actions·다른 CI의 결과·workflow·status check를 수집하거나 생성하지 않는다.

provider는 host, API base URL, clone policy, CA bundle, App identity, credential reference를 갖는다. GitHub.com과 각 GHES는 별도 연결과 인증정보를 쓴다. GHES 버전과 허용 API를 실제 연결 진단에서 확인하고 지원 계약을 고정한다.

## 다중 사용자 설치 (설계 계약)

플랫폼 계정 인증과 별도로 조직 단위 GitHub App은 설계 계약이다. pull request를 주기적으로 확인하는 polling은 이 설계 범위에 남아 있다.

### 역할

운영자는 App 등록, 최소 권한, 이벤트, webhook URL, secret, private key를 설정한다. 저장소 이용자 또는 관리자는 App을 설치하거나 대상 저장소를 추가한다. 플랫폼은 필요한 작업을 안내하고 연결 상태를 검사하며 Git 권한을 자동으로 수정하지 않는다. 조직 승인 정책을 우회하지 않는다. App 방식에서는 App webhook을 쓴다. 별도 봇 계정 초대와 저장소별 webhook을 무조건 함께 요구하지 않는다.

### 연결 상태

`repository_readable`, `branch_resolvable`, `webhook_verified`, `pr_readable`, `comment_writable`, `report_url_reachable`을 각각 표시한다. webhook이 없어도 읽기가 가능하면 수동·예약 분석을 제공한다. 공개 저장소 read-only 사용에 불필요한 쓰기 token을 요구하지 않는다.

### 권한과 자격증명

저장소 내용 읽기, PR 읽기, 결과를 게시할 댓글 경로에 필요한 쓰기 권한만 요청한다. Checks/Actions 쓰기 권한은 불필요하다. App private key와 게시 credential은 관리 서버가 보관한다. 소스 확보가 필요한 노드에는 필요한 저장소·기간으로 제한된 읽기 credential만 전달한다. 분석 프로세스에 댓글·설정 변경 권한을 주지 않는다. 폐쇄망에서 다른 호스트 인증으로 fallback하지 않는다. 자격증명 일반 규칙은 [보안](SECURITY.md)에 있다.

### webhook 처리

1. raw body size와 요청 시간 제한을 적용한다.
2. 설정한 secret으로 HMAC 서명을 constant-time으로 검증한다.
3. provider+delivery ID로 중복을 확인한다. 동일 ID에 다른 payload digest는 거부하고 감사한다.
4. event/action, installation, repository, 프로젝트 등록 상태를 검증한다.
5. durable delivery와 작업 의도를 저장한 뒤 신속히 2xx로 응답한다.
6. 별도 처리기가 provider API로 PR·권한·SHA를 확인하고 run을 만든다(지금의 고정·게시 경로를 그대로 쓴다).

응답 목표는 2초 이내이며 분석을 완료할 때까지 연결을 유지하지 않는다. 큐 저장에 실패하면 성공 응답으로 유실시키지 않는다. webhook 수신만으로 Native 실행을 승인하지 않는다.

### 이벤트와 명령

`pull_request`의 opened/reopened/synchronize/ready_for_review 등 설정된 action을 처리한다. draft는 기본 제외하되 사용자가 선택할 수 있다. `issue_comment`는 payload가 PR에 연결됐는지 확인한다. 명령 문법 초안은 `@codeye analyze`, `@codeye analyze deep`, `@codeye analyze tests`다. quoted text·코드 블록 안의 명령을 실행하지 않고 봇 자기 댓글을 무시한다. 댓글 작성자의 저장소 권한과 CODEYE 실행 권한을 모두 확인한다. 외부 fork 또는 신뢰하지 않는 기여자의 PR은 Native 실행을 자동 승인하지 않는다.

### 실패

- 권한 철회: 연결 경고와 신규 분석 중지.
- report URL에 접속할 수 없는 사용자에게 공개 링크로 권한을 우회시키지 않는다.
- 닫힌 PR의 진행 작업은 정책에 따라 취소하거나 완료하되 상태와 이유를 기록한다.
