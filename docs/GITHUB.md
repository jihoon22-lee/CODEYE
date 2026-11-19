# GitHub.com·GHES 연결

GitHub.com은 소유자가 실제로 쓸 일급 대상이다. GitHub Enterprise Server(GHES)는 같은 Git 계약을 따르지만 실제 인스턴스에서 시험하지 않았다. 아래 표의 "구현"은 코드와 시험이 있는 기능이고 "설계 계약"은 코드에 없다.

| 기능 | 상태 |
|---|---|
| 승인한 HTTPS 호스트의 Git 브랜치 가져오기와 commit SHA 고정 | 구현. 공개 GitHub 저장소에서 실행해 확인했다. GHES 호스트는 실제 인스턴스로 시험하지 않았다 |
| 개인 모드의 Git 주소(`codeye up https://github.com/OWNER/REPO`, 화면의 입력란) | 구현. 브랜치를 비우면 기본 브랜치다 |
| 개인 모드의 GitHub token 자동 발견(`GH_TOKEN`, `GITHUB_TOKEN`, `gh auth token`)과 비공개 저장소 가져오기 | 구현(github.com만). [GitHub token](#github-token) |
| 개인 모드의 pull request 분석(`codeye up …/pull/N`, 화면의 입력란) | 구현(github.com만). [pull request 분석](#pull-request-분석) |
| 개인 모드의 결과 댓글 하나(선택, 기본 끔)와 미리 보기 | 구현(github.com만). [결과 댓글](#결과-댓글) |
| provider 등록(`codeye provider add`) | 구현. 종류·호스트·선택 API URL을 저장할 뿐 API로 연결을 확인하지 않는다 |
| 서버 모드의 token, pull request 분석, 댓글 | 없음. 서버 모드는 token을 찾지 않고 pull request 요청을 `PULL_REQUEST_UNSUPPORTED`로 거절한다 |
| GitHub App 설치, webhook 수신, PR 이벤트와 댓글 명령, 연결·권한 진단 | 설계 계약(미구현). [다중 사용자 설치](#다중-사용자-설치-설계-계약) |
| pull request 주기 확인(polling) | 만들지 않는다. 새 push는 사용자가 다시 요청한다 |
| GHES의 token·pull request, 사내 CA·proxy, submodule/LFS | 미구현. 조용히 다른 credential이나 인터넷 대체 경로로 우회하지 않는다 |

외부에서 들어오는 webhook 주소가 없어도 읽기 가능한 저장소의 수동·예약 분석과 pull request 분석은 동작한다. 집에서 외부 수신 주소를 마련하지 않아도 된다.

## 현재 동작하는 것

**개인 모드**에서는 준비가 필요 없다. `codeye up`이 git과 gh를 찾아 에이전트에 넘기고, 설정의 `git_hosts`(기본 `["github.com"]`)마다 provider를 등록한다(`github.com`은 `github`, 그 밖은 `ghes`). 그다음 주소만 준다.

```sh
codeye up https://github.com/OWNER/REPO                # 기본 브랜치
codeye up https://github.com/OWNER/REPO/tree/release   # 그 브랜치
codeye up github.com/OWNER/REPO                        # https://를 붙인다
codeye up https://github.com/OWNER/REPO/pull/123       # pull request
```

화면의 "폴더 경로 또는 Git 주소" 입력란에 같은 주소를 붙여넣어도 된다. 같은 저장소를 다시 주면 그 프로젝트에 새 분석을 요청한다. Git 저장소의 프로젝트는 소스를 모델에 보내고(기본 켬), 저장소의 빌드 스크립트·`cargo check`처럼 저장소 코드를 실행하는 분석은 기본으로 하지 않는다. 그 분석이 빠진 보고서의 "더 나은 결과를 얻으려면"에서 "저장소의 빌드 스크립트 실행"을 켜고 다시 시도한다.

비공개 github.com 저장소는 [GitHub token](#github-token)이 있으면 읽는다. token이 없거나 그 token으로 읽을 수 없는 주소는 소스 확보가 "저장소를 인증 없이 읽을 수 없음"(`source_auth_required`)으로 실패한다. 다른 호스트에는 token을 보내지 않는다.

**서버 모드**에서는 다음 조건으로 공개 저장소를 가져온다. token 발견과 pull request는 없다.

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
- manifest에는 요청한 branch(`source_branch`, pull request snapshot은 대신 `source_ref` `refs/pull/N/head`)와 요청 시각(`requested_at`)이 남고 URL·remote 이름·credential은 들어가지 않는다. provider repository ID는 manifest에 아직 없다.
- 확보 전체에 deadline(최대 10분)이 있고, fetch 중 임시 저장소의 쓰기 용량을 감시한다(soft 한도이며 파일시스템 quota가 아니다). 취소·timeout·용량 중지에서 정리 완료를 확정하지 못하면 `recovery_required`와 격리 디렉터리를 남긴다.
- 소스 확보와 snapshot 계약은 [실행](EXECUTION.md)에 있다.

## GitHub token

개인 모드의 `codeye up`은 실행할 때마다 github.com token을 다음 순서로 찾는다. 따로 설정할 것은 없다.

1. 환경변수 `GH_TOKEN`
2. 환경변수 `GITHUB_TOKEN`
3. `gh auth token --hostname github.com`(toolfind가 찾은 gh. 사용자 환경에서 위 두 변수를 뺀 채 15초 안에 실행한다)

찾은 token은 GitHub API `GET /user`로 확인한다. GitHub이 거부하면(401) 쓰지 않고 이전 파일도 지운다. GitHub에 닿지 못하면 token을 두고 계정은 모른다고 표시한다. 확인한 token은 모델 키처럼 `<state>/secrets/github.json`(0600, 폴더 0700)에 계정·출처와 함께 두고 `up`마다 새로 쓴다. token을 찾지 못하거나 발견을 끄면 파일을 지운다.

```text
github: signed in as octocat on github.com (token from gh) - private repositories and pull requests you can read
github: no token (sign in with `gh auth login` or set GH_TOKEN, then run codeye up again) - public repositories only
```

`GET /api/v1/setup`의 `github`는 token이 있는지(`token`), 계정, 출처(`GH_TOKEN`·`GITHUB_TOKEN`·`gh`)와 이유만 싣는다. token 자체는 설정 파일, 명령 인자, 로그, 보고서, 감사 기록, 데이터베이스 어디에도 들어가지 않는다. 설정에 `"github": {"enabled": false}`를 두면 발견을 끈다(공개 저장소만, pull request는 token 없이 공개 저장소만 확인).

**오프라인 모드**(`"offline": true`, [운영](OPERATIONS.md#오프라인-모드))에서는 GitHub을 쓰지 않는다. 환경변수·gh에서 token을 찾지 않고(`gh auth token`을 실행하지 않는다) 이전에 저장한 `github.json`을 지우며, github.com은 기본 Git 호스트가 아니다. `git_hosts`에 github.com을 적어도 token은 보내지 않는다. pull request 주소는 `codeye up`과 `POST /projects/{id}/runs`가 거부한다(`PULL_REQUEST_UNSUPPORTED`). controller는 GitHub API를 부르지 않는다(pull request 확인·댓글 미리보기·게시 없음). `codeye up` 출력은 `github: off (offline mode) - ...`, `GET /setup`의 `github`는 `enabled: false`, `reason: "offline_mode"`다. 사내 Git 호스트용 credential은 지원하지 않는다(그 호스트는 인증 없이 읽을 수 있어야 한다).

token을 쓰는 곳은 둘이다.

- **Git fetch**: 에이전트는 파일 경로(`--git-credential-file`)만 받고, 워커가 fetch마다 파일을 읽어 github.com 요청에만 HTTP header로 넣는다. header는 git 환경변수(`GIT_CONFIG_COUNT`, `http.https://github.com/.extraHeader`)로 전달하므로 URL, argv, 작업 사본, snapshot에 남지 않는다. `ca_bundle`이 있으면 같은 git 프로세스 환경에 `GIT_SSL_CAINFO`·`SSL_CERT_FILE`도 들어간다([사내 CA](OPERATIONS.md#사내-ca)). 분석기와 모델 프로세스는 이 파일과 값을 받지 않는다.
- **GitHub API**: controller와 `codeye up`이 pull request 확인과 댓글 게시에 쓴다. 요청마다 파일을 다시 읽으므로 token이 바뀌어도 다시 시작할 필요가 없다. redirect를 따라가지 않고, 다음 page 주소가 API와 다른 origin이면 쓰지 않는다.

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

거절은 각각 다른 코드로 돌아온다: github.com 저장소 프로젝트가 아님·서버 모드(`PULL_REQUEST_UNSUPPORTED`), 이 token으로 보이지 않는 pull request(`PULL_REQUEST_NOT_FOUND`), 거부된 token(`GITHUB_TOKEN_REFUSED`), 권한 없음(`GITHUB_FORBIDDEN`), rate limit(`GITHUB_RATE_LIMITED`, 다시 시도 가능), 연결 실패(`GITHUB_UNAVAILABLE`). 예약 run은 pull request를 받지 않는다.

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

## 범위

Git 저장소 읽기, 브랜치 선택, pull request 분석과 결과 댓글만 다룬다. GitHub Actions·다른 CI의 결과·workflow·status check를 수집하거나 생성하지 않는다.

provider는 host, API base URL, clone policy, CA bundle, App identity, credential reference를 갖는다. GitHub.com과 각 GHES는 별도 연결과 인증정보를 쓴다. GHES 버전과 허용 API를 실제 연결 진단에서 확인하고 지원 계약을 고정한다.

## 다중 사용자 설치 (설계 계약)

개인 모드의 token 방식은 한 사람의 컴퓨터에서 그 사람의 권한으로 동작한다. 여러 사람이 쓰는 서버 설치의 계획은 여전히 GitHub App과 webhook이다. 이 절은 코드에 없다. pull request를 주기적으로 확인하는 polling 방식은 만들지 않았다(A46 6).

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
6. 별도 처리기가 provider API로 PR·권한·SHA를 확인하고 run을 만든다(개인 모드의 고정·게시 경로를 그대로 쓴다).

응답 목표는 2초 이내이며 분석을 완료할 때까지 연결을 유지하지 않는다. 큐 저장에 실패하면 성공 응답으로 유실시키지 않는다. webhook 수신만으로 Native 실행을 승인하지 않는다.

### 이벤트와 명령

`pull_request`의 opened/reopened/synchronize/ready_for_review 등 설정된 action을 처리한다. draft는 기본 제외하되 사용자가 선택할 수 있다. `issue_comment`는 payload가 PR에 연결됐는지 확인한다. 명령 문법 초안은 `@codeye analyze`, `@codeye analyze deep`, `@codeye analyze tests`다. quoted text·코드 블록 안의 명령을 실행하지 않고 봇 자기 댓글을 무시한다. 댓글 작성자의 저장소 권한과 CODEYE 실행 권한을 모두 확인한다. 외부 fork 또는 신뢰하지 않는 기여자의 PR은 Native 실행을 자동 승인하지 않는다.

### 실패

- 권한 철회: 연결 경고와 신규 분석 중지.
- report URL에 접속할 수 없는 사용자에게 공개 링크로 권한을 우회시키지 않는다.
- 닫힌 PR의 진행 작업은 정책에 따라 취소하거나 완료하되 상태와 이유를 기록한다.
