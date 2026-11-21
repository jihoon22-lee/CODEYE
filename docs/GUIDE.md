# CODEYE 사용 안내서

Linux에서 CODEYE 웹 서비스를 시작하고 프로젝트를 분석하는 안내서다. 설치 뒤에는 같은 화면에서 보고서를 보고 구성원을 초대할 수 있다. 설정 필드, 지원 환경, 백업·복원은 [운영](OPERATIONS.md)에 있다. 아래 경로와 이름은 예시다.

## 준비물

| 하려는 일 | 준비물 |
|---|---|
| 웹 서비스 시작 | Linux x86-64 또는 ARM64, 일반 Linux 계정, CODEYE 실행 파일과 웹 화면, PostgreSQL 서버 프로그램 14 이상. 시험한 환경은 [지원 범위](OPERATIONS.md#지원-범위)에 있다 |
| 폴더 분석 | 서비스 Linux 계정이 읽을 수 있는 프로젝트 폴더의 절대 경로. WSL 파일과 서버에 마운트한 NFS 경로도 같은 방식으로 입력한다 |
| Git 저장소 분석 | `git`과 저장소 HTTPS 주소. 비공개 GitHub.com·GHES는 [플랫폼 GitHub 계정](GITHUB.md#플랫폼-github-계정)에 읽기 권한을 준다 |
| 추가 정적 분석 | Go의 `go vet`, Python의 Ruff처럼 프로젝트 언어에 맞는 도구. 설치된 도구는 `./codeye tools`에서 확인한다. [분석기](ANALYZERS.md) |
| 모델 검토 | OpenCode와 사용할 모델. 공급자가 인증을 요구하면 API 키도 준비한다. [모델 설정](#모델-설정) |

Ubuntu는 `sudo apt install postgresql`로 PostgreSQL을 설치할 수 있다. CODEYE는 `initdb`, `pg_ctl`, `postgres`, `pg_dump`, `pg_restore`가 있는 디렉터리를 찾아 전용 데이터베이스를 만든다. 별도 위치는 `./codeye up --postgres-bin-dir /절대/경로/bin`로 지정한다. 기존 데이터베이스 서버를 쓸 때는 [외부 PostgreSQL](OPERATIONS.md#외부-postgresql)을 따른다.

## 첫 시작

CPU에 맞는 배포 파일과 SHA-256 검증 파일을 같은 폴더에 받는다. 배포 파일을 이미 풀었다면 그 안의 `INSTALL.md`를 연다.

- Linux x86-64(amd64) [배포 파일](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-amd64.tar.gz) · [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-amd64.tar.gz.sha256)
- Linux ARM64 [배포 파일](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-arm64.tar.gz) · [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-arm64.tar.gz.sha256)

x86-64에서 시작하는 예다. ARM64는 명령의 `amd64`를 `arm64`로 바꾼다.

```sh
sha256sum -c codeye-v0.1.0-linux-amd64.tar.gz.sha256 &&
  tar -xzf codeye-v0.1.0-linux-amd64.tar.gz &&
  cd codeye-v0.1.0-linux-amd64 &&
  ./codeye up
```

- `up`은 설정 파일과 데이터베이스를 만들고 웹 서버와 로컬 에이전트를 시작한다. 기본 주소는 `http://127.0.0.1:7331`이며 사용 중이면 다음 빈 포트(7340까지)를 고른다. [실행 명령과 설정 경로](OPERATIONS.md#up-open-start-stop-status)
- 첫 로그인은 관리자 계정으로 한다. 브라우저가 열리지 않으면 터미널에 출력된 로그인 링크를 연다. 링크는 10분 안에 한 번 쓸 수 있고, 로그인은 7일 동안 유지된다. 새 링크는 `./codeye open`, 링크만 출력하려면 `./codeye open --no-open`으로 받는다.
- 첫 화면의 **폴더 경로 또는 Git 주소**에 프로젝트를 입력하고 **분석**을 누른다. 분석 사본과 결과가 생길 때까지 화면에서 진행 상황을 볼 수 있다.
- 아래에서 사용하는 `codeye`는 PATH에 둔 실행 파일을 뜻한다. 풀어 놓은 폴더에서 직접 실행할 때는 `./codeye`를 쓴다. 명령 옵션은 `./codeye up --help`처럼 확인한다.

브라우저를 여는 동시에 첫 분석을 요청할 수도 있다.

```sh
./codeye up ~/src/my-project
./codeye up https://github.com/OWNER/REPO
./codeye up https://github.com/OWNER/REPO/tree/release
./codeye up https://github.com/OWNER/REPO/pull/123
```

브랜치를 생략하면 저장소의 기본 브랜치를 쓴다. PR은 당시 head 커밋을 고정하고 변경 파일부터 검토한다. 댓글은 기본으로 꺼져 있다. 게시 없이 확인하려면 `--dry-run-comment` 또는 실행 화면의 **미리 보기**를 쓴다. 게시하려면 `--comment` 또는 프로젝트 **고급** 설정에서 켠다. [결과 댓글](GITHUB.md#결과-댓글)

## 다른 사람 초대

### 다른 머신에서 열 수 있게 하기

기본 주소는 이 머신에서만 열린다. 다른 머신에서 열려면 서비스가 받을 주소와 브라우저에 알려 줄 주소를 정한다. 아래 설정은 저장되므로 다음부터는 `codeye up`으로 시작한다.

```sh
codeye up --listen 0.0.0.0:7443 --tls-cert /etc/codeye/cert.pem --tls-key /etc/codeye/key.pem --public-origin https://codeye.example:7443
```

신뢰하는 LAN·VPN에서는 `codeye up --listen 0.0.0.0:7331 --plain-http`로 평문 HTTP를 선택할 수 있다. 이 경우 로그인 비밀번호와 쿠키도 암호화 없이 전송된다. 명령이 출력한 주소 중 상대의 네트워크에서 닿는 것을 공유한다. [TLS 수신](OPERATIONS.md#tls-수신) · [받는 주소](OPERATIONS.md#받는-주소)

**WSL에서 내 기기만 연결.** WSL 안에서 Tailscale을 실행하고 tailnet 접근 정책으로 접속 기기를 제한했다면 CODEYE는 loopback에 두고 Tailscale Serve로 TCP 포트를 전달할 수 있다. [Tailscale Serve 포트 전달](https://tailscale.com/docs/reference/tailscale-cli/serve)

```sh
tailnet_ip=$(tailscale ip -4) && test -n "$tailnet_ip" &&
  tailscale serve --bg --tcp=7331 tcp://127.0.0.1:7331 &&
  codeye up --listen 127.0.0.1:7331 --plain-http --public-origin "http://${tailnet_ip}:7331"
```

기본 수신으로 돌아가려면 설정 파일에서 `listen`·`tls_cert`·`tls_key`·`public_origins`·`plain_http`를 지우고 `codeye up`으로 반영한다.

### 사용자 더하기

다른 사람에게 **관리** 화면의 서비스 주소를 알려 준다. 그 주소에서 **회원가입**을 눌러 이메일과 비밀번호를 입력하면 바로 로그인된다. 이후에는 같은 이메일과 비밀번호로 들어간다. 이메일의 대소문자와 앞뒤 공백은 무시한다. 이메일 확인 메일은 보내지 않는다.

기존 사용자 이름과 비밀번호 또는 일회용 로그인 링크로도 로그인할 수 있다. 관리자가 일회용 링크로 사용자를 추가하는 방법은 [계정과 관리자 CLI](OPERATIONS.md#계정과-관리자-cli)에 있다. 같은 저장소 주소를 입력해도 각 사용자는 자기 프로젝트와 보고서를 갖는다. 프로젝트를 공유하려면 **구성원**에서 상대방의 이메일이나 기존 사용자 이름을 입력한다.

### 다른 사람이 분석할 수 있는 것

새 사용자도 같은 첫 화면을 본다. 입력란에 GitHub 주소를 붙여넣고 **분석**을 누르면 된다. 무엇을 쓸 수 있는지는 그 사람의 권한과 관리자 설정이 정하고, 쓸 수 없는 것은 첫 화면의 "이 서비스에서 쓸 수 없는 것"이 이유와 함께 보여 준다.

| 할 수 있는 것 | 누가 | 정하는 곳 |
|---|---|---|
| 공개 Git 저장소 분석 | 모두 | 승인된 Git host(기본 github.com, **관리 > Git 공급자**에서 GitHub Enterprise Server host를 더함) |
| 비공개 GitHub.com·GHES 저장소, pull request 결과 댓글 | 프로젝트를 실행할 수 있는 사용자 | 플랫폼 GitHub 계정의 저장소 읽기 권한과 선택적인 댓글 쓰기 권한 |
| 서버 폴더를 경로로 분석 | 로그인한 사용자 | 서비스 Linux 계정이 읽을 수 있는 프로젝트 폴더의 절대 경로 |
| 모델 검토 | 관리자, 그리고 "다른 사용자도 모델 검토 사용"이 켜져 있으면(기본 켬) 모두 | **관리 > 서비스** |
| 저장소의 빌드 스크립트 실행 | 관리자, 그리고 "다른 사용자의 저장소 빌드 스크립트 실행 허용"이 켜져 있으면(기본 끔) 모두 | **관리 > 서비스** |
| 다른 사람의 프로젝트 | 소유자가 구성원으로 더한 사람(보기·실행·관리) | 프로젝트의 **구성원** |

- 요청자는 CODEYE 웹 계정으로 기록된다. Git fetch, PR 확인과 댓글 게시에는 호스트별 플랫폼 계정을 사용한다.
- 서버 폴더를 입력할 때는 서버 Linux 계정에서 읽을 수 있는 절대 경로를 준다. WSL 경로와 서버에 마운트된 NFS 경로는 같은 방식으로 쓴다. 상태·백업 폴더를 담는 경로는 거부한다.
- 관리자 스위치는 관리자가 아닌 사람이 시작한 분석에 적용된다. 관리자가 시작한 분석은 영향을 받지 않는다. 스위치 때문에 빠진 검토는 보고서에 "관리자가 다른 사용자의 모델 검토를 허용하지 않아 …"처럼 그 이유로 나온다.
- 사람마다 남의 프로젝트·보고서·감사 기록은 보이지 않는다(구성원이 아니면 없는 것처럼 404).
- 새 사용자가 만든 프로젝트와 보고서는 그 사용자의 것이다. 기존 프로젝트에 함께 접근하려면 [구성원](API.md#구성원)에 추가한다.

## 설정 파일

기본 파일은 `~/.config/codeye/config.json`이다(`XDG_CONFIG_HOME`을 따름). `codeye up --config PATH`로 다른 설치를 열 수 있다. 수신 주소, 별도 DB, 상태 디렉터리, 분석기 경로를 바꿀 때 수정한다. [서비스 설정 파일](OPERATIONS.md#서비스-설정-파일)

## 프로젝트와 source root

**source root**는 실행 노드의 내부 경로 매핑이다. `codeye up PATH`와 웹 등록은 경로를 해석해 기존 매핑을 재사용하거나 새 매핑을 DB에 기록한다.

설정에 직접 root를 더해도 된다(`{"label": "more", "path": "/home/user/more"}`). 그 뒤 `codeye up`을 하면 root를 등록하고 에이전트를 다시 등록한다. 이전 에이전트 상태는 지우지 않고 이름만 바꿔 둔다.

```text
registered source root "more" (b54bb497-…)
restarting the agent: its configuration changed (running work finishes first, up to 300s)
re-enrolling the local agent: the configured source roots changed (previous state kept in /…/state/agent.retired-…)
```

화면에서는 "분석할 코드"의 **폴더 경로 또는 Git 주소** 입력란에 절대 경로나 저장소 주소를 붙여넣고 **분석**을 누른다. 프로젝트를 등록하고(같은 폴더·저장소면 그 프로젝트를 쓴다) 분석을 요청한 뒤 바로 그 실행 화면으로 간다. 분석이 아직 진행 중이면 새로 요청하지 않고 그 실행을 보여 준다. 접힌 **고급**에는 이름, 브랜치(`…/tree/브랜치` 주소면 채워진다), 아래의 선택이 있다. pull request 주소(`…/pull/N`)를 넣으면 "pull request에 결과 댓글 남기기"도 있다. 실행 없이 먼저 등록하려면 **프로젝트 등록만 하기**를 연다. 등록한 프로젝트는 입력란 아래 "프로젝트 목록"에 카드로 보이고, 카드를 고르면 그 프로젝트의 최근 실행과 보고서가 열린다. 프로젝트 설명이나 요구사항 문서는 필요 없다. CODEYE가 문서·환경·소스를 직접 조사한다.

### 프로젝트별 분석 선택

프로젝트마다 세 가지를 고른다. 등록할 때 고급에서 정하고, 나중에는 보고서의 "더 나은 결과를 얻으려면"에서 켠다.

| 선택 | 뜻 | 로컬 폴더 기본 | Git 저장소 기본 |
|---|---|---|---|
| 소스를 모델에 보냄 | 설정한 검토 모델이 이 프로젝트의 소스를 읽는다. 끄면 정적 분석만 한다 | 켬 | 켬 |
| 저장소의 빌드 스크립트 실행 | build script·`cargo check`·plugin처럼 저장소의 코드를 실행하는 분석기를 돌린다([저장소 코드 실행 허가](ANALYZERS.md)) | 켬 | 끔 |
| 의존성 받기 | 분석 전에 모듈마다 의존성을 레지스트리에서 받아 분석기가 읽게 한다. Go 모듈, Rust crate(빌드 스크립트 실행도 켜야 한다), JS/TS 패키지(npm·pnpm·yarn 1), Python 패키지(wheel만)다. 저장소 코드는 실행하지 않는다([의존성 준비](ARCHITECTURE.md#의존성-준비)) | 켬 | 켬 |

- 내 폴더는 내 코드라 둘 다 켠다. Git 저장소는 누구의 코드든 될 수 있어 빌드 스크립트를 실행하지 않는다. 그래서 Rust 저장소는 처음에 `cargo check`·clippy가 "저장소 코드 실행이 허가되지 않아 실행하지 않음"으로 빠진다. 켜면 그 저장소의 코드가 이 컴퓨터에서 내 권한으로 실행된다.
- 의존성 받기는 "고급"의 "의존성 받기"다. 켜면 분석이 인터넷(Go는 `proxy.golang.org`, Rust는 crates.io, JS/TS는 `registry.npmjs.org`, Python은 PyPI)에 접속하고 처음 받는 것은 시간이 걸린다. 받은 것은 `state_dir/cache/deps`에 남아 다음 분석은 빠르다. 끄면 의존성이 있는 모듈의 go vet·Staticcheck, cargo check·clippy, tsc·type-aware ESLint가 `dependency_unavailable`로 끝나거나 mypy·pyright가 import를 풀지 못할 수 있고, 보고서의 "더 나은 결과를 얻으려면"이 다시 켜기를 권한다. 사용자의 `go env`에 `GOPROXY`·`GOPRIVATE` 등을 정해 두었으면 그 값을 따른다. 비공개 모듈은 공개 proxy에 묻지 않고, 내 Go 모듈 캐시에 이미 있는 것만 쓴다. npm 설정(`npm config get registry`)에 기본값과 다른 레지스트리가 있으면 JS/TS 받기가 그것을 쓰고(인증정보가 든 주소는 쓰지 않는다), 따로 정하려면 `agent.dependencies.npm_registry`다. JS/TS 패키지는 이번 분석의 작업 사본에 설치되고 분석이 끝나면 함께 지워진다. pnpm·yarn은 `codeye tools`가 없다고 하면 안내대로 설치한다. Python은 mypy·pyright가 있을 때 `uv.lock`, `pyproject.toml`, `requirements*.txt`의 패키지를 PyPI(또는 `agent.dependencies.pip_index_url`)에서 wheel로만 받는다. 이 컴퓨터에 uv나 pip이 있는 Python이 있어야 한다(`codeye tools`의 uv·python). 저장소의 `setup.py`는 실행하지 않으므로 wheel이 없는 패키지는 빠지고 보고서에 남는다. 컴퓨터 전체에서 끄려면 설정 `agent.dependencies.enabled: false`다.
- 이 선택을 따르는 것은 `codeye up`이 띄운 에이전트다. 관리자가 아닌 사람이 시작한 분석은 관리자 스위치(**관리 > 서비스**)가 허용하는 것만 한다. 따로 띄운 노드(`codeye agent`)는 운영자의 root별 허가(설정 source root의 `repository_code`·`model_review`, 노드 플래그)를 상한으로 두고 그 안에서 프로젝트 선택을 따른다.

## 사용할 수 있는 도구 확인

```bash
codeye tools
```

이 머신에서 찾은 도구(경로와 버전), 찾지 못한 도구와 추가하는 방법을 표로 보여 준다. 화면의 **도구** 메뉴도 `codeye up`이 마지막으로 찾은 같은 표와 모델과 호스트별 플랫폼 GitHub 연결 상태를 보여 준다(도구 경로는 관리자에게만 보인다). `codeye up`은 시작할 때 같은 탐색을 하므로 도구 경로를 설정에 적지 않아도 된다. 설정의 `agent.tools.<이름>`에 적은 경로는 항상 탐색보다 우선한다. 없는 도구에 해당하는 분석은 보고서에 "미지원"으로 나온다([도구 찾기](ANALYZERS.md#도구-찾기), [운영](OPERATIONS.md#codeye-tools)).

## 분석 실행

위의 입력란으로 시작하거나, 프로젝트를 고르고 "새로 분석"을 누른다. 요청은 대기열에 저장되고 에이전트가 가져가 실행한다. 화면은 실행 상태(기호·글자·색), 진행 막대, 단계(소스 확보, 환경 분석, 문서 검토, 정적 분석, 코드 분석, 보고서 생성)별 상태와 이유를 먼저 보여 준다. 실행 ID·스냅샷·진행률 계산 근거는 "실행 세부 정보"를 펼쳐 본다. 브라우저 탭 제목에도 실행 상태와 진행률이 나온다.

- **일부만 완료(partial)**: 일부 작업을 하지 못했다는 뜻이다. 화면에 이유가 나온다. 예: "이 분석 도구가 설치·설정되지 않아 실행하지 않음", "이 프로젝트는 소스를 모델에 보내지 않도록 설정되어 제외됨".
- **대기 사유**: 시작하지 않은 run에는 이유가 표시된다. 저장 공간 상한에 걸리면 "저장 공간 상한에 도달해 새 작업을 시작하지 않습니다"가 나온다([저장 공간](#저장-공간)).
- 실행을 취소하면 실행 중인 도구 프로세스를 정리하고 정리 결과를 확인한 뒤 취소로 표시한다. 확인하지 못하면 "정리 확인 필요"(`recovery_required`)로 남는다.
- **다시 시도**: 실패·일부 완료·취소된 실행에 "다시 시도" 버튼이 있다. 고정한 입력이 노드에 남아 있는 실패·취소 실행은 같은 입력으로 다시 하고, 그 밖(소스 확보 실패, 도구를 설치하거나 선택을 바꾼 뒤)에는 소스를 다시 받아 새 실행을 시작한다. 화면은 그 실행을 따라간다. `recovery_required`는 다시 시도하지 못한다.
- 소스를 읽지 못한 실행은 이유를 보여 준다. 비공개 저장소의 읽기 권한을 플랫폼 GitHub 계정에 부여했는지와 주소를 확인한다.

## 보고서 읽기

실행이 끝나 보고서가 게시되면 실행 화면에 저절로 열린다. 표지에서 프로젝트명, 등록한 Git URL 또는 로컬·NFS 경로와 분석 상태를 확인한다. 기록이 있으면 시작·종료 시각, 소요 시간과 커밋을 표시한다. 종료 기록이 없으면 작성 시각을 표시한다. 커밋 전체 값은 보고서의 입력과 실행 기록에서 확인할 수 있다. 종합에서 근거가 연결된 판단 하나부터 읽는다. 데스크톱의 옆 탐색과 휴대폰의 위쪽 탐색에서 환경·문서·정적 분석·코드 분석으로 이동한다. 보고서의 구조와 판정 규칙은 [보고서](REPORT.md#분석-종류별-절)에 있다.

- **더 나은 결과를 얻으려면.** 보고서 위에, 분석 공백 중 사용자가 채울 수 있는 것을 짧게 적는다. 없는 도구는 설치 방법(도구 화면과 같은 문구), 모델이 없으면 키 설정, 호출 상한 때문에 못 본 파일이 있으면 `review_max_calls`(장부가 있으면 이번 실행이 쓴 호출 수와, 거절된 slot을 모두 검토하는 데 필요한 상한. slot 상한 96개 밖이라 계획하지 않은 파일은 상한을 올려도 검토되지 않는다고 따로 적는다), Git 저장소의 빌드 스크립트가 꺼져 빠진 분석이 있으면 "켜고 다시 시도" 버튼이다. 버튼은 프로젝트 관리자에게만 보인다. 도구를 설치한 뒤에는 `codeye up`을 다시 하고 "다시 시도"를 누른다.
- **종합.** 사용 설명에 적힌 동작 하나를 보고 그 범위의 구현과 일치하는지, 어떤 조건까지 확인했는지 읽는다. 문서만 확보한 동작은 확인하지 못한 이유와 함께 표시한다. 판정 우선순위가 같은 미확인 동작 중에는 같은 문서 줄과 적용 범위에서 한 동작만 지목하는 문서 질문을 참고한다. “문서 분석에서 보기”는 종합에서 읽은 대조로 이동한다. 주요 코드에서 근거를 확인한 중대한 조치는 별도 한 줄에서 읽을 수 있다. "코드 보기" 또는 "문서 보기"에서 고정 커밋의 줄을 열거나 저장된 발췌를 펼친다. 미확인 상태에서 추가로 확인한 코드가 있으면 종합에서 그 범위를 읽고 문서 분석에서 원래 이유와 각각의 근거를 열 수 있다. 추가 코드로 갱신한 대조는 그렇게 표시되며, 테스트 코드의 단언을 읽은 경우 "테스트 코드 보기"에서 근거를 확인한다. 테스트 실행 여부는 실행 기록에서 확인한다. 전체 수치와 수행 범위는 접힌 집계에 있다.
- **용어 풀이.** 웹이나 HTML 보고서의 종합에서 `Promise`, `JSON 파싱`, HTTP 상태 코드나 줄 범위가 나오면 "용어 풀이"를 펼쳐 그 뜻을 읽을 수 있다. 판단할 근거가 부족한 경우에는 모델 검토를 하지 못한 사유 등 확인된 기록을 함께 표시한다.
- **이전 실행과 비교.** 종합 아래에 화면에서만 보인다([이전 실행과 비교](#이전-실행과-비교)). 내보낸 파일에는 없다.
- **환경 분석.** 모듈, 모듈이 선언한 도구 요구(예: `go 1.22`), 노드에서 관측한 도구 버전, 요구별 판정(충족·불일치·판단 불가)이다.
- **문서 분석.** 찾은 문서와 읽었는지 여부(예산 초과·제외 포함), 모듈별로 적용되는 문서, 같은 범위에 겹치는 안내를 보여 준다. 한 저장소 안에서 별도 문서 사이트가 다른 npm 패키지의 실제 사용 예제를 제공하면 그 예제의 원문과 해당 패키지에서 읽은 구현을 나란히 확인할 수 있다.
- **절 순서.** 환경 분석, 문서 분석, 정적 분석, 구조·중복 지표, 비밀정보, 코드 분석, 분석 공백 순이다. 긴 목록은 묶어 앞의 몇 개만 보이고, 각 절의 "전체 목록"을 펼치면 모든 행이 나온다. 시험 입력·예제·생성 파일 같은 부차 경로(`fixtures`, `testdata`, `examples`, `vendor` 등)의 항목은 주요 경로 뒤에 짧게 보인다.
- **정적 분석.** 언어마다 분석기 표(작업·성공 수, 진단 수, 성공하지 않은 작업)와 규칙 표(규칙마다 진단 수, 심각도, 메시지 한 줄, 위치 앞 5곳과 "외 N곳")다. 실행하지 않은 도구는 "실행하지 않음"으로 나오고 0건이 아니다.
- **구조·중복 지표.** 복잡도·중복 같은 측정값과 임계 초과 위치다. 결함이 아니다.
- **비밀정보.** 비밀값으로 보이는 값을 탐지한 위치와 규칙이다. 값 자체는 어디에도 저장하지 않는다. 모델 입력에서 그 값을 걸렀는지도 나온다.
- **코드 분석(모델 검토).** 모델 역할별 결과, 호출 수, 모델이 본 파일 수, 모델 검토 발견이다. 예제·시험 입력·테스트에서만 근거를 찾은 발견은 "예제·시험 입력 등에서 발견한 항목"에서 위치와 검토 상태를 확인한다. 파일이 많아 모듈 단위로 나눠 검토한 실행에는 **모듈별 검토 장부**가 있다. 모듈마다 검토 대상 코드·테스트 파일 중 모델에 보낸 수, 호출 상한(`agent.model.review_max_calls`) 때문에 보내지 못한 수, 비밀값 탐지로 뺀 수 등을 보여 준다. 모델은 도구 진단이 있는 파일, 필수 분석이 실패한 모듈, 진입점, 그 밖의 소스, 테스트 순으로 주요 경로 파일을 먼저 보고 부차 경로 파일은 마지막에 본다([검토 우선순위](REVIEW.md#검토-우선순위)). 모델에 보낸 범위는 검증된 범위가 아니다. 보내지 못한 파일이 많으면 상한을 올리거나 범위를 좁혀 다시 실행한다. 종합의 코드 분석 줄에도 보낸 파일과 보내지 않은 파일 수가 나온다.
- **모델 해석.** 환경·문서·코드 분석 절에서 "모델 해석"이라고 표시된 상자는 모델이 쓴 문장이다. 근거 위치가 함께 나오며 판정이나 수치의 근거가 아니다. 모델 검토를 하지 않았으면 그 이유가 나온다.
- **분석 공백.** 하지 못했거나 일부만 한 분석과 그 이유를 모은 목록이다. 여기 적힌 범위는 "문제 없음"이 아니다.
- **발견.** 정적 분석과 코드 분석 절의 발견마다 발생 조건, 심각도 근거, 영향, 개선 방향, 확인 방법, 근거가 있다. 접힌 카드에는 제목, 심각도·검증 상태 표지, 첫 위치가 보이고, 펼치면 영향과 개선 방향이 먼저 나온다. 주장과 근거 목록은 카드 안의 "근거 N개와 판단 과정"을 펼친다. 근거는 snapshot의 파일 위치와 hash, 도구 원시 출력 산출물이다.
- **이전 보고서.** 절이 생기기 전에 게시된 보고서는 요약, 분석 범위와 한계, 영역별 발견 사항의 이전 배치로 보인다.
- **HTML 보고서의 긴 발췌.** 발췌 영역 안에서 좌우로 움직여 긴 줄의 끝까지 읽을 수 있다. 키보드에서는 발췌에 초점을 맞춘 뒤 좌우 방향키를 쓴다.
- **사람 판정.** 발견에 "확인함", "위험 수용", "오탐" 같은 판정과 사유를 기록한다. 기록은 감사 이력으로 남고 수정·삭제되지 않는다. 다음 실행의 비교에 반영된다.
- **내보내기.** JSON·Markdown·HTML이다. "코드 포함"을 고르지 않으면 코드 발췌를 빼고 내보낸다. Markdown과 HTML은 화면과 같은 요약 보기이고, 모든 행이 필요하면 "Markdown 전체"·"HTML 전체"(API `detail=full`)를 쓴다. JSON은 언제나 전체다([내보내기 상세도](REPORT.md#내보내기-상세도)).

## 예약 실행

프로젝트 화면의 "예약 실행"에서 정해진 시각마다 같은 분석 요청을 대기열에 넣는다. 예약 등록과 삭제는 프로젝트 관리자만 한다.

- **cron 식**은 5필드(분 시 일 월 요일)다. 예: `0 12 * * 1-5`(평일 12:00), `@daily`, `@weekly`. 초 필드와 `L`·`W`·`#`·`?`는 받지 않는다.
- **시간대**는 IANA 이름(`Asia/Seoul`, `UTC`)만 받는다. `Local`은 거부한다.
- **이전 실행이 끝나지 않았을 때**: 기본은 최신 1건만 대기시키고 더 이전 대기분은 최신 예약으로 대체한다. "이번 예약 건너뛰기"나 "모두 대기열에 넣기(최대 10건)"를 고를 수 있다.
- **CODEYE가 꺼져 있는 동안 지난 예약**: 기본은 멈춘 사이 지난 예약 중 최신 1건만 실행한다. 멈춘 시간만큼 몰아서 실행하지 않는다. "2분보다 늦으면 건너뛰기"를 고를 수 있다. 실행하지 않은 구간은 화면에 "CODEYE가 꺼져 있던 사이 …부터의 예약은 실행하지 않았습니다"로 나온다.
- 예약 실행은 등록한 관리자의 권한으로 요청되고, 실행할 때마다 권한을 다시 확인한다. 입력은 실행이 시작되는 시점에 고정한다.
- 서머타임이 있는 시간대에서 특정 시각(`30 2 * * *`)의 예약은 하루 한 번만 실행된다. 매시·15분 같은 간격 예약은 경과 시간 기준으로 돌고, 시계가 뒤로 가는 날의 반복 시각에도 실행한다.
- 예약을 삭제해도 이미 대기열에 넣은 실행은 취소되지 않는다. 대기 중이던 예약 회차만 취소된다.
- 예약은 `codeye start`로 띄운 controller가 실행 중일 때만 평가된다. 정지해 둔 동안의 예약은 위 규칙으로 처리한다.

## 이전 실행과 비교

보고서 화면은 같은 프로젝트의 이전 실행과 비교한다. 기준은 같은 소스(Git 저장소와 브랜치, 또는 source root와 경로), 같은 프로필과 범위의 가장 최근 완료·부분 완료 보고서다. 비교를 불러오지 못해도 보고서는 그대로 보인다.

| 상태 | 뜻 |
|---|---|
| `new` | 이번에만 있다 |
| `persisting` | 양쪽에 있고 개수·심각도·검증 상태·영역이 같다. 줄 위치만 바뀐 것도 여기에 든다 |
| `changed` | 양쪽에 있고 개수·심각도·검증 상태·영역 중 하나가 다르다 |
| `resolved_observed` | 기준에만 있고, 이번 실행이 같은 소스·범위에서 같은 도구를 성공적으로 실행해 그 파일을 실제로 검사했다. 도구 버전과 규칙이 같고, 소스 안 억제 주석 때문에 사라진 것이 아니다 |
| `absent_unverified` | 기준에만 있으나 위 조건을 확인하지 못했다. 사유가 함께 나온다 |

- **사라진 발견을 해결로 세지 않는다.** 검사가 빠졌거나 실패했거나 범위가 줄었거나 규칙이 바뀌었거나 억제 주석이 생겼으면 `absent_unverified`다. 모델이 후보를 다시 내지 않은 것은 관찰이 아니므로 모델 후보는 `resolved_observed`가 되지 않는다.
- 같은 식별의 발견이 한쪽에 여럿이면 하나씩 짝짓지 않고 묶음 전체의 상태를 `ambiguous`로 표시한다. 경로만 바뀐 항목은 이동 후보로 보여 줄 뿐 자동으로 합치지 않는다.
- **사람 판정의 적용.** 판정은 발견의 식별(fingerprint)마다 최신 기록 하나가 적용된다. 식별이 약하거나 모호하면 자동 적용하지 않고 사람이 그 발견에서 다시 판정해야 한다. 만료 시각이 지났거나 경로 범위 밖이면 `open`으로 돌아간다.
- 위험 수용·오탐·억제로 판정한 발견은 조치 집계에서 빠지고 "숨김 판정"으로 따로 센다. 전체 발견 수와 원시 진단, 비교에는 그대로 남는다.
- 과거 리비전을 열면 그 리비전을 게시할 때의 판정이 보이고, 지금의 판정은 따로 표시한다.

비교와 판정의 계약은 [보고서](REPORT.md)에 있다.

## 모델 설정

모델 검토에는 서버에 설치된 OpenCode와 사용할 모델이 필요하다. 인증을 요구하는 공급자에는 해당 모델의 API 키를 준비한다. 인증이 필요 없는 사내 endpoint도 연결할 수 있다. 모델 설정 전에도 정적 분석을 할 수 있다.

### 관리자 화면에서 연결하기

1. 웹에서 **관리 > OpenCode 연결**을 연다. **도구** 화면의 **OpenCode 연결**에서도 갈 수 있다.
2. 서버에서 실행할 OpenCode의 경로를 입력한다. 기존 `opencode.json` 또는 `opencode.jsonc`를 쓸 때는 그 경로를 지정한다. 절대 경로나 `~/`로 시작하는 경로를 쓸 수 있다. `~/`는 CODEYE 서비스 Linux 계정의 홈이다.
3. 필요한 경우에만 인증 파일의 경로와 `공급자/모델` 이름을 준다. API 키 방식의 OpenCode `auth.json` 또는 CODEYE 모델 키 파일을 사용할 수 있다. OpenCode에서 OAuth로만 로그인했다면 공급자가 지원하는 API 인증정보를 별도로 준비한다. 비운 항목에는 설치의 기본 설정을 적용한다.
4. **설정 저장**을 누른 뒤 **저장한 설정으로 연결 확인**을 누른다. 공급자에 실제 요청 한 번을 보낸다. 결과에 표시된 경로나 모델 인증 상태를 확인하고 필요하면 고친다.

`codeye up` 또는 `codeye start`로 운영하는 설치는 새 분석이 시작될 때 저장한 값을 읽는다. 별도로 실행한 서버와 노드의 모델은 운영자가 각 실행 설정에서 관리한다.

인터넷 없는 머신에서는 사내 endpoint와 인증 경로를 설정하고 [오프라인 모드](OPERATIONS.md#오프라인-모드)의 조건을 따른다.

<details>
<summary>사내 OpenAI 호환 서버용 opencode.json 예시</summary>

기존 설정 파일이 있으면 그 파일을 지정한다. 새로 만들 때는 예시의 주소와 `review-model`을 실제 서버 주소와 모델 ID로 바꾼다. 인증이 필요하면 서비스 계정의 인증 파일에서 같은 공급자 이름(`internal`)을 사용한다.

```json
{
  "model": "internal/review-model",
  "provider": {
    "internal": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {"baseURL": "https://llm.example.test/v1"},
      "models": {"review-model": {"name": "Review model"}}
    }
  }
}
```

</details>

### 터미널에서 연결하기

OpenCode의 기본 탐색 경로는 `~/.opencode/bin/opencode`와 PATH다. 서비스 계정의 OpenCode `auth.json`에 API 키가 있으면 사용할 수 있다. 터미널에서 OpenAI 키를 전달하는 예다.

```bash
export OPENAI_API_KEY=sk-...      # 이미 ~/.bashrc에 있으면 그대로 둔다
codeye up
```

- `codeye up`은 `OPENAI_API_KEY`(또는 `ANTHROPIC_API_KEY`, `OPENCODE_API_KEY`)를 `<state_dir>/secrets/model.json`(본인만 읽을 수 있는 파일)에 저장하고, 선택한 모델로 한 번 시험 호출한 뒤 결과를 보여 준다. 키를 바꾼 뒤 다시 `codeye up`을 하면 새 키로 다시 시험한다.
- 키는 이 파일에만 남는다. 로그·보고서·화면·프로세스 인자에는 나오지 않는다. 키를 지우려면 그 파일을 지운다.
- 모델을 바꾸려면 설정에 `"agent": {"model": {"name": "openai/gpt-6.1-sol"}}`처럼 이름을 적는다.
- 시험 호출이 실패해도 분석은 실행된다. 보고서의 코드 분석 절에 모델 검토를 하지 못한 이유(예: "모델 공급자가 API 키를 거부했다")가 나온다. 언제든 `codeye model probe`로 다시 확인한다. 실제 추론을 한 번 요청하므로 비용이 조금 든다.
- 모델이 설정되면 "소스를 모델에 보냄"이 켜진 프로젝트(기본 켬, 로컬 폴더와 Git 저장소 모두)의 코드를 모델에 보낸다. 빼려면 등록할 때 고급에서 끈다. 비밀값으로 선별된 파일은 보내지 않는다.
- OpenAI 호환 서버(사내 게이트웨이, 로컬 추론 서버)를 쓰려면 `model.json`에 `"base_url": "https://…/v1"`을 더한다. chat completions만 지원하는 서버면 `"openai_compatible": true`도 더한다([검토](REVIEW.md#모델-설정)).

## 모델 검토 설정

모델 설정이 없으면 문서·코드의 모델 검토 단계는 실행하지 않는다. 보고서에는 "모델 검토 미수행"으로 나온다. 다른 모델이나 인터넷 서비스로 자동 전환하지 않는다.

`agent.model`의 모든 항목은 선택이다. 위 [모델 설정](#모델-설정)만으로 충분하다.

```json
"agent": {
  "model": {"name": "openai/gpt-6.2-sol", "opencode": "/home/user/.opencode/bin/opencode",
            "config_file": "/home/user/.config/opencode/opencode.jsonc",
            "credential_file": "/home/user/.config/codeye/model.json", "timeout_seconds": 180,
            "review_max_calls": 99}
}
```

- `opencode`를 비우면 `~/.opencode/bin/opencode`, 없으면 PATH의 `opencode`를 쓴다. `config_file`은 기존 OpenCode JSON/JSONC 설정을 지정할 때 쓴다. `name`은 모델을 덮어쓸 때, `credential_file`은 기존 인증 파일을 다른 곳에 둘 때 적는다. `timeout_seconds`는 모델 호출 한 번의 제한시간이다(기본 180초).
- `relay_file`은 승인된 로컬 relay를 쓸 때만 넣는다. 이 저장소의 `scripts/model_budget_relay.py`는 예산 상한을 강제하는 QA 전용 relay이며 제품 구성요소가 아니다. 사용법은 그 파일의 머리말과 `--help`에 있다.
- `review_max_calls`는 분석 시도 하나가 쓸 모델 호출 수의 상한이다(15~207회, 기본 99). 비용과 검토 범위는 모델, 프로젝트 크기와 재시도 횟수에 따라 달라진다. 실제로 모델에 보낸 범위와 선별하지 못한 정보는 보고서에서 확인한다. [모듈 단위 검토와 호출 한도](REVIEW.md#모듈-단위-검토-분할)
- 모델 연결은 `codeye model probe`(서비스 설정의 모델) 또는 `codeye model probe --executable PATH --model PROVIDER/MODEL [--credential-file PATH | --relay-file PATH]`로 확인한다. 이 명령은 실제 추론을 요청하므로 비용이 들 수 있다. `codeye doctor`는 모델을 호출하지 않는다.

## 저장 공간

상태 디렉터리에 DB, 소스·작업 사본, 로그, 산출물이 쌓인다. `codeye status`에서 사용량을 보고 필요하면 설정의 `quota`로 새 작업에 적용할 상한을 둔다. 오래된 도구 출력과 작업 사본을 지우기 전에는 `codeye cleanup --older-than 30d`로 대상을 확인한다. [저장 영역과 상한](OPERATIONS.md#저장-영역과-상한) · [보존 정리](OPERATIONS.md#보존-정리)

### 로그아웃 뒤에도 실행

Linux의 systemd 사용자 서비스에서 실행한다. `codeye service install`로 등록하고 `systemctl --user start codeye`로 시작한다. 로그인 링크는 `codeye open`으로 받는다. 로그아웃 뒤 유지 조건과 해제 방법은 [사용자 서비스](OPERATIONS.md#사용자-서비스)에 있다.

## 정지

`codeye stop`은 새 분석을 받지 않고 진행 중인 작업의 종료를 기다린다. 시간 안에 끝나지 않으면 취소와 정리 확인을 거친다. 마지막 줄의 `outcome`이 `recovery_required`이면 `codeye status`와 로그를 확인한다. 다음 `codeye up`은 같은 설치의 데이터를 이어서 쓴다. [정지 결과](OPERATIONS.md#stop)

## 백업과 복원

`codeye backup`으로 DB와 확정 산출물을 한 세트로 보관한다. 복원에는 빈 상태 디렉터리와 데이터베이스가 필요하다. 새 설치에서 `codeye restore --config PATH --from DIR`로 복원하고 `codeye up --config PATH`로 시작해 새 로그인 링크를 받는다. [백업과 복원](OPERATIONS.md#백업과-복원)

## 인터넷 없는 머신

연결된 머신에서 배포 파일과 검증 파일을 받아 함께 옮긴다. 묶음 안 `INSTALL.md`의 검증 절차를 마친 뒤 `./codeye up --offline /절대/프로젝트/경로`를 실행한다. 한 번 지정한 오프라인 설정은 저장된다. 필요한 PostgreSQL, 분석 도구, 모델 실행기는 대상과 같은 OS/arch용으로 따로 준비한다. [폐쇄망 배포팩](OPERATIONS.md#폐쇄망-배포팩)

## 업그레이드 (설계 절차, 시험 안 함)

이전 버전에서 올리는 절차 전체는 시험하지 못했다. 각 명령은 개별로 실행해 보았다.

```sh
codeye stop
codeye backup
# 새 codeye 바이너리와 web/dist로 교체
codeye up
```

관리형 PostgreSQL이면 `codeye up`(과 `start`)이 schema를 맞춘다. 데이터가 있으면 먼저 `pg_dump` 사본(`<백업 디렉터리>/codeye-pre-migrate-<UTC>/database.dump`, 빈 DB에 `pg_restore`로 되돌린다)을 쓴다. `database.mode: external`이면 migration은 `codeye migrate`만 하고, start는 schema가 다르면 시작하지 않는다.

## 문제 해결

| 증상(출력) | 원인과 조치 |
|---|---|
| `PostgreSQL binaries not found (searched: …)` | PostgreSQL을 설치한다(Ubuntu: `sudo apt install postgresql`). 다른 위치면 `codeye up --postgres-bin-dir DIR` 또는 `database.postgres_bin_dir`. 이미 있는 서버를 쓰려면 `database.mode: external` |
| `database.postgres_bin_dir …: missing pg_restore` 같은 오류 | 적은 디렉터리에 필요한 프로그램이 없다. 다섯 프로그램이 모두 있는 `bin` 디렉터리를 준다. 적은 경로가 틀리면 다른 곳을 찾지 않는다 |
| `the managed data directory … was created by PostgreSQL 16, found 17 binaries …` | cluster를 만든 주 버전의 프로그램을 `postgres_bin_dir`로 준다. 버전을 올리려면 그 버전으로 백업하고 새 `state_dir`에 복원한다 |
| `PostgreSQL did not start (…); see …/logs/postgres.log` | 그 로그를 본다 |
| `web UI not built - run …` | 실행 파일 옆에 `web/dist`가 없다. 빌드하거나 `web_dir`를 적는다. API는 떠 있다 |
| `note: ignoring PGHOST, … from the environment` | 관리형 모드는 셸의 `PG*`·`CODEYE_DATABASE_URL`을 쓰지 않는다. 정상 |
| `CODEYE_DATABASE_URL is not set (database.mode external)` | 셸에 DB URL이 없다. [설정 파일](#설정-파일) |
| `database unavailable: invalid input`, status의 `database=false` | external: URL 형식이 틀렸거나 환경에 `PGPASSWORD` 같은 `PG*` 변수가 있다. 그 변수를 지운다. managed: PostgreSQL이 멈춰 있다. `codeye up` |
| `database schema is outdated (applied N, this binary expects M)…` | external: 백업한 뒤 `codeye migrate` |
| `database schema is newer than this codeye binary …` | 더 새 바이너리로 만든 DB다. 그 바이너리를 쓴다 |
| `config is not valid: …` | 설정 오류. 메시지가 필드를 알려 준다(예: `agent.toolchain_path entries must be absolute, clean paths`) |
| `CODEYE is already running (controller …)` | 이미 실행 중이다. 종료 코드 0. `codeye up`은 이때 새 로그인 링크를 준다 |
| `127.0.0.1:7331 is in use by another program; using 127.0.0.1:7332` | 다른 프로그램이 기본 포트를 쓴다. 다음 빈 포트를 썼고 `stop`·`status`·`open`이 그 포트를 안다 |
| `the listen address … is already in use by a process codeye start did not record` | 설정에 적은 `listen` 포트를 다른 프로그램이 쓴다. `listen`을 바꾸거나 지우거나 그 프로그램을 멈춘다 |
| `the agent is still finishing its work and takes no new work; …` | 설정이 바뀌어 에이전트를 다시 띄우려 했지만 실행 중인 분석이 drain 시간 안에 끝나지 않았다. 에이전트는 그 분석을 끝내고 종료한다. `codeye status`로 확인한 뒤 같은 명령을 다시 한다 |
| `… contains or is inside the CODEYE data directory …` | 상태·백업 디렉터리를 포함하는 폴더는 분석하지 않는다. 프로젝트 폴더를 준다 |
| `an agent that codeye start did not record holds …/agent/agent.lock …` | 수동으로 띄운 `codeye agent`가 같은 상태 디렉터리를 쓴다. 그 프로세스를 직접 멈춘다 |
| `removed stale controller record (pid … pid_reused)` | 이전 프로세스가 비정상 종료됐다. 기록만 지웠고 신호는 보내지 않았다. 정상 |
| `status`의 `issue: stale_record`, 종료 코드 1 | 위와 같다. `codeye start`가 정리한다 |
| `the agent did not enroll and report a heartbeat in time` | `state_dir/logs/agent.log` 확인. 도구 경로·source root 경로가 맞는지 본다 |
| 보고서 환경 절의 의존성 표에 "의존성 저장소(registry·proxy)에 연결하지 못했다"(`registry_unreachable`) | 인터넷이나 proxy에 닿지 않는다. 네트워크를 확인하거나 `agent.dependencies.go_proxy`(Go)·`agent.dependencies.npm_registry`(JS/TS)에 쓸 수 있는 주소를 적는다. 분석은 받은 것 없이 계속됐다 |
| 의존성 표에 `download_limit_exceeded`·`dependency_budget_exhausted` | 받을 의존성이 크거나 느리다. `agent.dependencies.max_download_bytes`·`timeout_seconds`·`total_timeout_seconds`를 늘린다 |
| 의존성 표에 `private_module_not_fetched` | 비공개 모듈은 공개 proxy에서 받지 않는다. 그 저장소에서 한 번 `go mod download`를 해 내 Go 모듈 캐시에 두면 다음 분석이 쓴다 |
| 분석이 시작되지 않고 저장 공간 상한 문구가 나온다 | [저장 공간](#저장-공간). `codeye status`의 `exceeded=true` 영역을 정리하거나 상한을 늘린다 |
| stop의 `outcome: recovery_required` | [정지](#정지) |
| 실행 화면의 "정리를 확인해야 다시 실행할 수 있습니다"(run `recovery_required`) | 이 실행을 맡은 노드가 사라졌거나 하위 프로세스가 끝났는지 확인하지 못했다. `codeye status`와 작업 관리자에서 그 실행의 프로세스(`codeye worker`와 그 자식)가 없는지 보고, 확인 상자를 체크한 뒤 "정리 확인 후 다시 실행"을 누른다. CLI는 `codeye run recover --id RUN_ID --confirm-cleanup`이다(설정 파일의 DB를 쓴다). 확인은 감사 기록에 남는다 |
| `the target database is not empty; restore only into a new, empty database` | 복원 대상 DB가 비어 있지 않다. 새 DB를 만든다 |
| `the target state_dir already has admin.json; restore needs a fresh state_dir` | 새 `state_dir`를 쓴다 |
| `backup verification failed: …` | 세트가 손상됐거나 완성되지 않았다(manifest 없음). 다른 세트를 쓴다 |
| 로그인 링크가 만료됐거나 이미 썼다 | `codeye open` |
| `codeye start/stop/status manage processes on Linux only …` | Windows·macOS에서는 `codeye up`으로 서비스를 띄우지 못한다(`codeye serve`·`agent`는 따로 실행할 수 있다) |

점검 순서: `codeye status --format json` → `codeye doctor --config PATH` → `state_dir/logs/controller.log`, `agent.log`, `postgres.log`. 로그와 `status`·`stop` 출력에는 DB 비밀번호와 토큰이 나오지 않는다(`up`·`open`이 출력하는 로그인 링크에는 일회용 토큰이 있다). 문의할 때 설정 파일과 로그를 그대로 보내도 연결 정보는 들어 있지 않다. 다만 경로와 프로젝트 이름은 들어 있다. 관리형 DB 비밀번호는 `state_dir/postgres/password`에만 있으니 상태 디렉터리를 통째로 보내지 않는다.
