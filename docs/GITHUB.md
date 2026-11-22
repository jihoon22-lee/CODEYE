# GitHub.com·GHES 연결

승인된 호스트의 공개 저장소와 PR은 익명으로 읽을 수 있다. 비공개 저장소와 그 PR을 읽거나 결과 댓글을 게시할 때는 호스트별 플랫폼 GitHub 계정을 쓴다. CODEYE 웹 계정은 프로젝트의 열람·실행 권한을 정한다.

## 저장소 등록

**폴더 경로 또는 Git 주소**에 HTTPS 저장소·브랜치·PR 주소를 넣고 **분석**을 누른다. `git`이 설치되어 있어야 한다. GitHub.com은 온라인 설치에서 기본으로 승인된다. GHES는 관리자가 **관리 > Git 공급자 > 호스트 추가**에서 승인하거나 서비스 설정 `git_hosts`에 적는다. [서비스 설정](OPERATIONS.md#서비스-설정-파일)

```sh
codeye up https://github.com/OWNER/REPO                # 기본 브랜치
codeye up https://github.com/OWNER/REPO/tree/release   # 지정한 브랜치
codeye up https://github.com/OWNER/REPO/pull/123       # pull request
```

일반 분석은 워커가 저장소를 가져오는 시점에 커밋을 고정한다. 브랜치를 비워 두면 당시의 기본 브랜치를 사용한다. URL에는 사용자 이름·토큰과 같은 인증정보, query, fragment를 넣을 수 없다. 승인된 HTTPS 주소만 사용하고 리다이렉트는 따르지 않는다. 워커는 임시 bare 저장소에서 Git 객체를 읽어 분석 사본을 만들며 `.git`·hooks·Git 필터를 사용하지 않는다. [소스 확보와 스냅샷](ARCHITECTURE.md#git-확보)

별도로 실행한 노드에서는 `codeye provider add --kind github --host github.com`처럼 공급자를 등록하고 `codeye agent`에 `--git /abs/git --git-host github.com`을 준다. GHES 공급자는 `--kind ghes --host HOST`를 쓴다. 기본 API 주소는 GitHub.com에서 `https://api.github.com`, GHES에서 `https://HOST/api/v3`이다. [서버·노드 배포](OPERATIONS.md#서버노드-배포)

## 플랫폼 GitHub 계정

1. 플랫폼 계정에 대상 저장소 내용과 PR의 읽기 권한을 준다. 결과 댓글을 게시하려면 댓글 쓰기 권한도 준다.
2. 서비스 설정의 `git_hosts`에 호스트를, `github.token_env`에 호스트별 토큰을 전달할 환경변수 이름을 적는다. 토큰 값은 해당 환경변수에 공급하고 `codeye up` 또는 `codeye start`를 실행한다. 온라인 설치의 예시다.

   ```json
   {
     "schema_version": "0.1",
     "git_hosts": ["github.com", "git.example.test"],
     "github": {
       "token_env": {
         "github.com": "CODEYE_PLATFORM_GITHUB_TOKEN",
         "git.example.test": "CODEYE_PLATFORM_GHES_TOKEN"
       }
     }
   }
   ```

3. 시작 출력이나 **도구** 화면에서 호스트와 플랫폼 계정 이름을 확인한다. 토큰을 바꿀 때는 시작 환경의 값을 교체하고 다시 시작 명령을 실행한다. 설치의 플랫폼 토큰 사용을 끄려면 `github.enabled: false`를 설정한다.

서비스는 시작 시 호스트별 `GET /user`로 계정을 확인한다. 401로 거부된 토큰은 저장하지 않는다. 인증이 필요한 소스에서 읽기 권한을 확인하지 못하면 `source_auth_required`, 연결에 실패하면 `source_unreachable`이 보고된다. `GH_TOKEN`·`GITHUB_TOKEN`은 설정에 지정한 경우에만 사용하며 `gh` 로그인과 기존 웹 사용자별 토큰은 사용하지 않는다.

토큰은 `<state_dir>/secrets/github-platform.json`(권한 `0600`)에 저장된다. 복원했다면 시작 환경에서 다시 공급한다. Git fetch와 PR API는 대상 호스트의 인증정보를 사용한다. GHES의 사설 CA는 `ca_bundle`로 설정한다. 오프라인 모드는 `git_hosts`에 명시한 사내 GHES만 사용한다. [자격증명](SECURITY.md) · [사내 CA](OPERATIONS.md#사내-ca) · [오프라인 모드](OPERATIONS.md#오프라인-모드)

## pull request 분석

`https://github.com/OWNER/REPO/pull/123`과 `/files`가 붙은 브라우저 주소를 모두 쓸 수 있다. 저장소의 프로젝트에 PR 분석을 요청하며 없으면 프로젝트를 등록한다. 예약은 브랜치 분석에 사용한다.

- 요청할 때 base·head SHA와 변경 파일을 고정한다. PR 목록은 최대 3,000개이며 목록이 잘리면 표시한다. 워커는 base 저장소의 `refs/pull/N/head`에서 고정한 커밋을 확인한다. ref가 움직이면 고정한 SHA를 직접 가져오고 확보할 수 없으면 실패 사유를 남긴다.
- 변경 파일부터 검토한다. 보고서에서 변경된 줄·변경 파일·기존 코드의 발견을 확인한다. patch를 받지 못한 경우에는 파일 단위로 집계한다. [PR 보고서](REPORT.md#pull-request-보고서)
- 새 커밋을 push한 뒤 같은 주소로 다시 분석하면 새 head를 고정한다. 비교 기준은 같은 PR의 이전 실행이다. 수동으로 요청한 포크 PR도 base 저장소의 PR ref를 통해 분석한다. Git 프로젝트의 저장소 코드 실행은 기본으로 꺼져 있다. [프로젝트별 분석 선택](GUIDE.md#프로젝트별-분석-선택)

읽을 수 없는 PR과 토큰 거부, 권한 부족, rate limit, API 연결 실패는 요청 결과의 사유에서 확인한다. [PR API](API.md#pull-request-run)

## 결과 댓글

- **켜기**는 PR 주소를 입력한 뒤 **고급 > pull request에 결과 댓글 남기기**를 선택하거나 `codeye up --comment …/pull/N`을 실행한다. 프로젝트별로 기억하며 기본은 꺼져 있다. `--comment=false`로 끈다.
- **미리 보기**는 보고서의 PR 줄에서 **미리 보기**를 누르거나 `codeye up --dry-run-comment …/pull/N`을 실행한다. CLI는 보고서를 기다려 본문을 터미널에 출력한다(`--comment-wait` 기본 1시간). 미리 보기는 게시나 프로젝트 설정 변경 없이 확인하는 경로다.

CODEYE는 보고서가 게시된 뒤 댓글을 보낸다. PR별로 플랫폼 계정이 작성한 CODEYE 댓글 하나를 갱신하고, 이전 분석이 더 최근 댓글을 덮지 않게 한다. 댓글 실패는 별도 상태와 사유로 기록되며 게시된 보고서는 유지된다. 연결·rate limit 오류는 1분부터 최대 30분 간격까지 지연을 늘려 최대 6회 재시도한다. 댓글 상태는 `GET /api/v1/runs/{id}`의 `pull_request.comment`에서도 확인할 수 있다.

댓글에는 분석한 head SHA, 변경 부분의 주요 발견과 도구 진단, 빠진 분석 범위가 담긴다. 코드 원문·도구 원문·비밀값·보고서 주소는 생략하며 멘션과 자동 링크는 글자로 표시한다.

## 저장소 웹훅

저장소 웹훅은 한 CODEYE 프로젝트에 연결한다. GitHub에서 수신 주소에 접근할 수 있어야 한다. 수동·예약 분석에는 웹훅이 필요 없다.

1. CODEYE 프로젝트를 등록하고 프로젝트 ID와 실행 권한이 있는 활성 사용자 ID를 확인한다. `codeye user list`에서 사용자 ID를, GitHub REST `GET /repos/OWNER/REPO`의 `id`에서 숫자 저장소 ID를 확인한다.
2. `<state_dir>/secrets/github-webhooks/<project_id>.json`을 만든다(권한 `0600`, 디렉터리 `0700`). 프로젝트마다 32바이트 이상의 별도 임의 secret을 쓴다.

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

3. 저장소의 **Settings > Webhooks**에서 Payload URL을 `<CODEYE 주소>/api/v1/github/webhooks/<project_id>`, 콘텐츠 유형을 `application/json`, Secret을 설정 파일과 같은 값으로 지정한다. `pull_requests`에는 **Pull requests**, `mentions`에는 **Issue comments** 이벤트를 선택한다.

`pull_requests`는 열기·다시 열기·새 커밋·검토 준비 이벤트를 처리한다. `mentions`는 PR의 새 댓글에서 한 줄로 입력한 `@플랫폼-GitHub-로그인`을 처리한다. 인용·코드·봇·플랫폼 계정 자신의 댓글을 건너뛰고 작성자의 `write`·`admin` 권한을 확인한다. 이를 위해 플랫폼 계정은 PR 읽기와 [저장소 권한 조회](https://docs.github.com/en/rest/collaborators/collaborators#get-repository-permissions-for-a-user)가 가능해야 한다. CODEYE에서 실행을 요청한 계정으로 `actor_user_id`를 기록하고 대기열에 넣기 직전에 그 권한을 다시 확인한다. 자동 분석은 닫힌 PR·draft·포크 PR·다른 SHA로 바뀐 이벤트를 건너뛴다.

서명과 저장소를 확인해 전달을 기록하면 202를 반환한다. 같은 배달 ID의 같은 내용은 202, 다른 내용은 409다. 전달을 처리할 때 같은 PR head나 댓글로 중복 실행을 만들지 않으며 연결·rate limit 오류는 최대 5회 시도한다. 처리 상태는 `github_webhook_deliveries`에서 확인한다. 설정 파일을 제거하면 새 전달을 받지 않고 아직 처리하지 않은 전달도 건너뛴다. [웹훅 API](API.md#github-저장소-웹훅)

## 범위

현재 경로는 저장소 읽기, 브랜치·PR 분석, 댓글과 저장소 웹훅이다. CI 결과 수집과 workflow·status check 생성은 제품 범위 밖이다. GHES API와 사설 CA는 로컬 HTTPS 시험 서버에서 확인했다. 실제 GitHub.com·GHES의 웹훅 배달과 실제 GHES 인스턴스는 운영 환경 검증이 남아 있다. [연동 시험](TESTING.md#연동)

## 다중 사용자 설치 (설계 계약)

조직 단위 GitHub App 연동과 polling은 설계 계약이다. [로드맵](ROADMAP.md#github-연결)

### 역할

운영자가 App과 webhook을 설정하고 저장소 관리자가 대상 저장소에 App을 설치한다. 플랫폼은 연결 상태를 안내하며 저장소 권한은 GitHub에서 관리한다.

### 연결 상태

저장소·브랜치 읽기, PR 조회, 댓글 쓰기, webhook 확인과 보고서 주소 접근을 개별 진단한다. 저장소를 읽을 수 있으면 수동·예약 분석을 제공한다.

### 권한과 자격증명

저장소 내용·PR 읽기와 선택적인 댓글 쓰기 권한을 요청한다. App 키와 게시 인증정보는 관리 서버가 보관하고 워커에는 저장소와 기간을 제한한 읽기 인증정보를 준다. [보안](SECURITY.md)

### webhook 처리

서명·installation·repository·전달 ID를 검증한 뒤 의도를 저장하고 응답한다. 응답 목표는 2초 이내다. 후속 처리기가 권한과 SHA를 확인해 PR 분석을 요청한다.

### 이벤트와 명령

설정한 PR 이벤트와 권한 있는 사용자의 PR 댓글 명령을 처리한다. 초안에는 `@codeye analyze`, `@codeye analyze deep`, `@codeye analyze tests`가 있다.

### 실패

권한을 철회하면 연결 경고를 표시하고 새 분석을 중지한다. 진행 중인 닫힌 PR의 상태와 종료 사유는 기록하며 보고서 권한은 CODEYE 프로젝트에서 확인한다.
